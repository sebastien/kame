#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-07 unified runner shared-session execution"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/session"
mkdir -p "$work/cwd"
cat >"$work/values.km" <<'KM'
name = "Ada"
(cat "ignored" name)
KM
cat >"$work/functions.km" <<'KM'
(greet WHO) = (cat "Hello " WHO)
KM
cat >"$work/process.kash" <<'KASH'
printf "%s\n" @(greet name) | cat
KASH
cat >"$work/definitions.kash" <<'KASH'
unused = $(touch should-not-exist)
KASH
cat >"$work/rules.kmk" <<'KMK'
build :
	printf rule
KMK
cat >"$work/arguments.kmk" <<'KMK'
leaf :
	printf leaf
build : @((first @*))
	printf "rule%s" @((first @*))
KMK
cat >"$work/cwd/Makefile.kmk" <<'KMK'
name = "Ada"
(report value) = (out value)
build :
	printf rule
KMK

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local expected="$1"; shift
		local status=0
		"${command[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		printf '%s' "$expected" >"$work/expected"
		if [ "$status" = 0 ] && cmp -s "$work/out" "$work/expected"; then test-ok "$backend: $*"; else test-fail "$backend: $* (status $status)"; cat "$work/err"; fi
	}
	reject() {
		local code="$1"; shift
		local status=0
		"${command[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects before effects: $code"; else test-fail "$backend expected $code (status $status)"; cat "$work/err"; fi
	}
	test-step "$backend shared values, entries, forward references and final result"
	check '"Hello Ada"' do run "$work/functions.km" -c '(greet name)' -c 'name = "Ada"'
	check '"Ada"' do run "$work/values.km" name
	check 'Hello Ada"Hello Ada"' do run -c '(out "Hello ")' -c '(out "Ada")' -c '"Hello Ada"'
	# The output effects above precede the one final implicit value.
	check 'firstsecond42' do run -c '(out "first")' -c '(out "second")' -l expr -c '42'
	check '["one two" "--help"]' do run -l expr -c '@*' -- 'one two' --help
	check ':nil' do run -l expr -c ':nil'
	check '' do run -c ''
	check '' do run "$work/definitions.kash"
	check '"Ada"' "$work/values.km" name
	check '"Hello Ada"' -c 'name = "Ada"' -c '(cat "Hello " name)'
	printf 'printf stdin' | "${command[@]}" -l kash - >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = stdin ]; then test-ok "$backend explicit stdin source"; else test-fail "$backend stdin source"; fi

	test-step "$backend Kash streams, capture isolation and fragment replay"
	check $'Hello Ada\n"done"' do run --allow-run "$work/functions.km" "$work/process.kash" -c 'name = "Ada"' -c '"done"'
	check $'Hello Ada\n' --allow-run "$work/functions.km" -c 'name = "Ada"' "$work/process.kash"
	check 'AB"C"' do run -l kash -c 'printf A; printf B' -l km -c '"C"'
	check 'once"later"' do run -l kash -c 'printf once' -l km -c '$(printf later)'
	check '"nested"' do run --allow-run -c 'capture = $(printf nested)' -c 'capture'
	check '' do run -l kash -c 'unused = $(touch should-not-exist)'
	check 'abc' do run --capture-limit 1 -l kash -c 'printf abc | cat'
	"${command[@]}" do run -l kash -c 'printf "\\377"' >"$work/binary" 2>"$work/err"
	printf '\377' >"$work/expected"
	if cmp -s "$work/binary" "$work/expected"; then test-ok "$backend streams non-UTF8 without capture decoding"; else test-fail "$backend decoded statement stdout"; fi

	test-step "$backend preflight and invocation policy"
	reject PARSE_ERR do run -c '(out "must-not-print")' -l expr -c '['
	reject DEF_INVALID do run -c '(out "must-not-print")' -c 'name = 1' -c 'name = 2'
	reject FS_ERR do run -c '(out "must-not-print")' "$work/missing.kash"
	reject TGT_NO_RULE do run -c '(out "must-not-print")' "$work/values.km" missing
	reject CAP_DENIED do run -c 'name = "Ada"' "$work/process.kash" "$work/functions.km"
	reject CAP_DENIED do run -c '$(printf denied)'
	reject CAP_DENIED do run -c 'name = "Ada"' "$work/rules.kmk" build
	reject CAPTURE_LIMIT do run --allow-run --capture-limit 1 -l expr -c '$(printf abc)'
	reject RECIPE_FAIL do run -l expr --allow-run -c '"must-not-print"' -l kash -c '/bin/sh -c "exit 7"'
	reject PARSE_ERR do run -l expr -c '(cat "a"' -c '"b")'
	check '"Hello Ada"' do run -C "$work/cwd" "$work/functions.km" -c 'name = "Ada"' -c '(greet name)'
	test-step "$backend rule and value composition"
	check 'rule"Ada"' do run "$work/rules.kmk" build -c 'name = "Ada"' -c 'name'
	check 'leafruleleaf"done"' do run "$work/arguments.kmk" build -c '"done"' -- leaf
	test-step "$backend discovered build plus inline work"
	check 'ruleAda"Ada"' -C "$work/cwd" build -c '(report name)'
	check 'rulefirstAda"Ada"' -C "$work/cwd" build -c '(out "first")' -c '(report name)'
	reject PARSE_ERR -C "$work/cwd" build -c '['
	reject DEF_INVALID -C "$work/cwd" build -c 'name = "duplicate"'
	test-step "$backend removed commands give migration help"
	for removed in expr kash; do
		status=0
		"${command[@]}" do "$removed" -c '42' >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 2 ] && grep -q CMD_UNKNOWN "$work/err" && grep -q 'use kame do run' "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend migration for do $removed"; else test-fail "$backend migration for do $removed"; fi
	done
done
test-end
