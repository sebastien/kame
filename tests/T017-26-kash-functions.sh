#!/usr/bin/env bash
# Specs: docs/spec/{004-language,005-evaluation,007-library,017-kash}.md
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-26 native Kash functions and explicit value construction"
cli_build
if [ -n "${KAME_WASM:-}" ]; then
	wasm_wrapper="$CLI_ROOT/src/js/kame.js"
else
	make -C "$CLI_ROOT" dist-wasm >/dev/null
	wasm_wrapper="$CLI_ROOT/dist/kame.js"
fi
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	invoke() { "${runner[@]}" do run -C "$work" --allow-run --allow-read --allow-write "$@"; }
	test-step "$backend list spread and line framing"
	invoke -l expr -c '(prefix => [...prefix, "two words", "", ...[], ...[[1], nil]]) ["printf", "<%s>"]' >"$work/out"
	if [ "$(cat "$work/out")" = '["printf" "<%s>" "two words" "" [1] nil]' ]; then test-ok "$backend one-level spread"; else test-fail "$backend spread values"; cat "$work/out"; fi
	invoke -l kash -c '(out! (json (map line ["", "plain", " x \n", "a\n\n", "a\r\n", "a\r", "a\nb"])))' >"$work/out"
	if [ "$(cat "$work/out")" = '["","plain"," x ","a\n","a\r","a\r","a\nb"]' ]; then test-ok "$backend one terminal LF"; else test-fail "$backend line framing"; cat "$work/out"; fi

	test-step "$backend fresh functions, lexical parameters, nested return and cleanup"
	cat >"$work/functions.kash" <<'KASH'
def label = "fresh"
def nested status
	defer printf "inner-cleanup\n"
	return status
def probe status rest...
	defer printf "outer-cleanup=%s\n" $status
	let marker = $?(printf "%s\n" $label).value
	printf "%s/%s/%s\n" (line marker) (count rest) (count args)
	if status == 0
		match (nop "yes")
			"yes" =>
				return 0
	(nested 2)
	for item in [1, 2]
		if item == 1
			return status
	printf unreachable
let first = probe 75 "two words" ""
let second = probe 0
printf "values=%s,%s\n" $first $second
def enclosing ()
	def local value
		return value
	(local 3)
	return 0
(enclosing ())
KASH
	invoke -l kash "$work/functions.kash" >"$work/out" 2>"$work/err"
	printf 'fresh/2/3\ninner-cleanup\nouter-cleanup=75\nfresh/0/1\nouter-cleanup=0\nvalues=75,0\n' >"$work/expected"
	if cmp -s "$work/out" "$work/expected" && [ ! -s "$work/err" ]; then test-ok "$backend functions and call-local finalization"; else test-fail "$backend functions replayed or failed to unwind"; cat "$work/out" "$work/err"; fi
	"${runner[@]}" do fmt -l kash "$work/functions.kash" >"$work/formatted.kash"
	"${runner[@]}" do fmt -l kash "$work/formatted.kash" >"$work/again.kash"
	if cmp -s "$work/formatted.kash" "$work/again.kash"; then test-ok "$backend function formatting idempotent"; else test-fail "$backend function formatting changed nesting"; fi
	invoke -l kash "$work/formatted.kash" >"$work/formatted-out" 2>"$work/err"
	if cmp -s "$work/formatted-out" "$work/expected"; then test-ok "$backend formatted functions execute identically"; else test-fail "$backend formatted function semantics"; fi
	"${runner[@]}" do parse --json -l kash "$work/functions.kash" >"$work/ast"
	if grep -q '"kash-function"' "$work/ast" && grep -q '"return"' "$work/ast"; then test-ok "$backend native AST nodes"; else test-fail "$backend missing function AST"; fi

	test-step "$backend typed integers and rejected status modifiers"
	for number in -1 1 75 256; do
		printf 'def value ()\n\tdefer printf "cleanup\\n"\n\treturn %s\nlet result = value ()\nprintf "%%s" $result\n' "$number" >"$work/value.kash"
		status=0
		invoke -l kash "$work/value.kash" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$(printf 'cleanup\n%s' "$number")" ] && [ ! -s "$work/err" ]; then test-ok "$backend returns integer $number"; else test-fail "$backend integer return"; cat "$work/out" "$work/err"; fi
	done
	for modifier in '?' '&' '>> ./forbidden' '| cat'; do
		printf 'def invalid ()\n\treturn 75 %s\n(invalid ())\n' "$modifier" >"$work/invalid.kash"
		status=0
		invoke -l kash "$work/invalid.kash" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q PARSE_ERR "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects return modifier $modifier"; else test-fail "$backend accepted return modifier"; fi
	done

	test-step "$backend return joins async work and cannot hide cleanup failures"
	cat >"$work/join.kash" <<'KASH'
def background ()
	printf "joined\n" > ./background &
	return 0
(background ())
cat ./background
def cleanup-fails ()
	defer false
	return 75
(cleanup-fails ())
printf unreachable
KASH
	status=0
	invoke -l kash "$work/join.kash" >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" != 0 ] && [ "$(cat "$work/out")" = joined ]; then test-ok "$backend joins children and propagates cleanup failure"; else test-fail "$backend async/cleanup ownership"; cat "$work/out" "$work/err"; fi
done
test-end
