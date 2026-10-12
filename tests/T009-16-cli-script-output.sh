#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Script output framing and literal assignments
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T009-16 script output and assignment classification"
cd "$CLI_ROOT"
cli_build
make dist-wasm >/dev/null
work="$TMPDIR/framing"
mkdir -p "$work"
cat >"$work/data.km" <<'KM'
(out "{\"ok\":true}\n")
(err "progress\n")
[ignored: 42]
KM
cat >"$work/default.km" <<'KM'
default = (out "default\n")
KM
cat >"$work/config.kmk" <<'KMK'
SDK_PATH = "original"
default :
	@(out SDK_PATH)
deploy {path} :
	@(out path)
KMK
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local expected="$1"; shift
		local status=0
		"${command[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		printf '%s' "$expected" >"$work/expected"
		if [ "$status" = 0 ] && cmp -s "$work/out" "$work/expected"; then test-ok "$backend: $*"; else test-fail "$backend: $* (status $status)"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend exact machine-readable stdout, explicit display and operands"
	check $'{"ok":true}\n' "$work/data.km"
	if [ "$(cat "$work/err")" = progress ] && jq -e '.ok == true' "$work/out" >/dev/null; then test-ok "$backend JSON data and stderr stay separate"; else test-fail "$backend stdout framing"; fi
	check $'{"ok":true}\n[ignored: 42]' do run --print-result "$work/data.km"
	check '' do run -c '42'
	check '' do run -c ':nil'
	check '' do run -c '""'
	check '42' do run --print-result -c '42'
	check '7' do run --lang expr -c '7' --lang km -c '42'
	check 'raw42' do run --print-result -c '(out "raw")' -c '42'
	check 'raw42' do run --lang expr -c '(nop (out "raw") 42)'
	check ':nil' do run --lang expr -c ':nil'
	check $'default\n' "$work/default.km"
	check $'default\n"default\\n"' --print-result "$work/default.km"
	check $'default\n"default\\n"' "$work/default.km" default
	check '["one two" "--help" "x.km"]' do run --lang expr -c 'args' -- 'one two' --help x.km
	check '["one two","--help"]' do run -c '(out (json args))' -- 'one two' --help
	check '[]' do run --lang expr -c 'args'
	check '"one two"' do run --lang expr -c 'args.0' -- 'one two'
	status=0
	"${command[@]}" do run --print-result -c '42' -c '(fail "broken")' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && [ ! -s "$work/out" ]; then test-ok "$backend failure prevents final result"; else test-fail "$backend fabricated a success result"; fi

	test-step "$backend assignments remain literal before and after rule sources"
	for suffix in km kmk kash ksh; do
		value="/missing/sdk.$suffix"
		check "$value" do run "$work/config.kmk" "SDK_PATH=$value"
		check "$value" "SDK_PATH=$value" "$work/config.kmk"
		check "$value" "$work/config.kmk" default "SDK_PATH=$value"
		check "$value" do run "$work/config.kmk" deploy "path=$value"
		check "$value" do run "$work/config.kmk" "SDK_PATH=$value" -c '42'
	done
	check '@(missing)=$(printf injected).km' do run "$work/config.kmk" 'SDK_PATH=@(missing)=$(printf injected).km'
	check '(fail "not an expression")' do run "$work/config.kmk" 'SDK_PATH=(fail "not an expression")'
	check '' do run "$work/config.kmk" 'SDK_PATH='

	test-step "$backend JSON Lines obey the same display policy"
	for display in silent explicit; do
		options=()
		if [ "$display" = explicit ]; then options=(--print-result); fi
		"${command[@]}" do run --json "${options[@]}" "$work/data.km" >"$work/events" 2>"$work/err"
		if jq -e -s --arg display "$display" 'all(.[]; .schema == 1) and any(.[]; .type == "stdout" and .data == "{\"ok\":true}\n") and any(.[]; .type == "stderr" and .data == "progress\n") and ((any(.[]; .type == "target-value")) == ($display == "explicit"))' "$work/events" >/dev/null && [ ! -s "$work/err" ]; then test-ok "$backend $display JSON framing"; else test-fail "$backend $display JSON framing"; cat "$work/events" "$work/err"; fi
	done
done
test-end
