#!/usr/bin/env bash
# Spec: docs/spec/041-pattern-predicates-and-substitution.md
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T041-01 patterns predicates and substitution"
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null

for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	for sample in \
		'(match? ./{*}.{*} "./foo.txt")|:true' \
		'(match? ./{*}.{*} "./foo")|:false' \
		'(match? ./{*}.{*} "./.txt")|:false' \
		'((match? {name:~[a-z]+}) "demo")|:true' \
		'(map (match? {name:*}-{name:*}) ["a-a" "a-b"])|[:true :false]' \
		'(filter-out (match? ./{*}.{*}) (map (sub ./journal/{name:*} ./{name}) ["./journal/foo" "./journal/bar.txt"]))|["./foo"]' \
		'(filter (match? ./{*}.{*}) ["./foo" "./foo.txt"])|["./foo.txt"]' \
		'(sub ./journal/{name:*} missing "./other/foo")|"./other/foo"' \
		'(replace ./journal/{name:*} missing "./other/foo")|"./other/foo"' \
		'(sub {name:*}.c {name}.o ["a.c" "b.h"])|["a.o" "b.h"]' \
		'(sub "a" "b" "banana")|"bbnbnb"' \
		'(sub {*}.c "x" [])|[]' \
		'(sub {*}.c "x" :nil)|:nil' \
		'(regex-replace {name:~[a-z]+} "x" "42")|:nil'; do
		"${runner[@]}" do run --lang expr -c "${sample%|*}" >"$TMPDIR/value" 2>"$TMPDIR/error"
		if [ "$(cat "$TMPDIR/value")" = "${sample##*|}" ] && [ ! -s "$TMPDIR/error" ]; then test-ok "$backend ${sample%|*}"; else test-fail "$backend ${sample%|*}: $(cat "$TMPDIR/value")"; fi
	done
	for expression in '(match? {*} :nil)' '(match? {*} ["a"])' '((match? {*}) 42)' '(match? "a" "a")' '(match? {name})' '(match? (pattern "plain"))'; do
		status=0
		"${runner[@]}" do run --lang expr -c "$expression" >"$TMPDIR/value" 2>"$TMPDIR/error" || status=$?
		if [ "$status" = 1 ] && grep -Eq 'EXPR_INVALID|PAT_INVALID' "$TMPDIR/error"; then test-ok "$backend rejects $expression"; else test-fail "$backend accepted $expression"; fi
	done
done
test-end
