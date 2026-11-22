#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-08 runner file includes"
test-step "build artifacts"
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
cli_build
work="$TMPDIR/includes"
mkdir -p "$work/nested" "$work/cwd"
cat >"$work/main.km" <<'KM'
parts = ["界"]
include ./nested/child.km
parts += ["after"]
default = nop (out! (join "" parts)) (str name "!")
KM
cat >"$work/nested/child.km" <<'KM'
include ./values.km
parts += ["child"]
KM
printf 'name = "Ada"\n' >"$work/nested/values.km"
cat >"$work/rules.km" <<'KM'
include ./nested/rules.km
task build : child
	printf main
KM
cat >"$work/nested/rules.km" <<'KM'
task child
	printf child
KM
cat >"$work/cycle.km" <<'KM'
default = out! "must-not-run"
include ./nested/cycle.km
KM
printf 'include ../cycle.km\n' >"$work/nested/cycle.km"
printf 'include ./absent.km\n' >"$work/missing.km"
printf 'include ./nested/invalid.km\n' >"$work/invalid.km"
printf 'name = [\n' >"$work/nested/invalid.km"
printf 'include ./empty.km\ninclude ./empty.km\ndefault = 42\n' >"$work/repeated.km"
: >"$work/empty.km"
printf 'include %s\ndefault = name\n' "$work/nested/values.km" >"$work/absolute.km"
printf 'include ./values.km\ndefault = out! name\n' >"$work/nested/<command-file>.km"
printf 'include ./values.km\ndefault = out! name\n' >"$work/nested/<stdin>"
cat >"$work/conditional.km" <<'KM'
if false
	include ./absent.km
else
	if true
		include? ./absent.km
		include ./nested/values.km
default = name
KM

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$wasm_wrapper"); fi
	check() {
		local expected="$1"; shift
		local status=0
		"${command[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend: $*"; else test-fail "$backend include result (status $status)"; cat "$work/err"; fi
	}
	reject() {
		local code="$1"; shift
		local status=0
		"${command[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code before effects"; else test-fail "$backend include rejection (status $status)"; cat "$work/err"; fi
	}
	test-step "$backend includes preserve scope, order, byte offsets and entry selection"
	check '界childafter' do run "$work/main.km"
	check '"Ada"' do run "$work/main.km" name
	check '界childafter"Ada!"' do run --print-result -C "$work/cwd" "$work/main.km"
	check '42' do run --print-result "$work/repeated.km"
	check '"Ada"' do run --print-result "$work/absolute.km"
	check '"Ada"' do run --print-result -C "$work/cwd" "$work/conditional.km"
	check 'childmain' do run -C "$work" "$work/rules.km" build -c 'name = "Ada"' -c 'name'
	check 'childmain' do run -C "$work" "$work/rules.km" build
	# A physical file's spelling must not classify it as an inline/stdin source.
	original_command=("${command[@]}")
	command=(env -C "$work/nested" "${command[@]}")
	check 'Ada' do run './<command-file>.km'
	check 'Ada' do run --lang km './<stdin>'
	command=("${original_command[@]}")
	reject DEP_CYCLE do run "$work/cycle.km"
	reject FS_ERR do run -c '(out! "must-not-run")' "$work/missing.km"
	reject PARSE_ERR do run -c '(out! "must-not-run")' "$work/invalid.km"
done
test-end
