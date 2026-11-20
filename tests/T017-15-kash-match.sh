#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-15 Kash match and lexical captures"
test-step "build artifacts"
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
cli_build
work="$TMPDIR/match"
mkdir -p "$work"
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$wasm_wrapper"); fi
	check() {
		local expected="$1" source="$2"; shift 2
		local status=0
		"${command[@]}" do run -C "$work" "$@" -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend match"; else test-fail "$backend match (status $status)"; cat "$work/err"; fi
	}
	reject() {
		local code="$1" source="$2"; shift 2
		local status=0
		"${command[@]}" do run -C "$work" "$@" -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err"; then test-ok "$backend preserves $code"; else test-fail "$backend wrong match failure"; cat "$work/err"; fi
	}
	test-step "$backend uses Kame patterns and first-match selection"
	check first $'match (nop "same")\n\t"same" =>\n\t\tprintf first\n\t"same" =>\n\t\tprintf forbidden'
	check fallback $'match (nop "other")\n\t"same" =>\n\t\tprintf forbidden\n\telse =>\n\t\tprintf fallback'
	check after $'match (nop "other")\n\t"same" =>\n\t\tprintf forbidden\nprintf after'
	check 'mainouter' $'def name = "outer"\nmatch ./src/main.c\n\t./src/{name:*}.c =>\n\t\tprintf "%s" $name\nprintf "%s" $name'
	check 'héllo' $'match (nop "héllo.txt")\n\t"{name:*}.txt" =>\n\t\tprintf "%s" $name'
	check literal $'match (nop "*.txt")\n\t"*.txt" =>\n\t\tprintf literal'
	check branch $'match $(printf subject)\n\t"subject" =>\n\t\tprintf branch'
	check bound $'match "bound.c"\n\t"{name:*}.c" =>\n\t\tprintf "%s" $name'
	check 'subjectbody' $'match (nop (out! "subject") "x")\n\t"x" =>\n\t\tprintf "%s" $(printf body)'
	check waiting $'def subject () = $(printf waiting.c)\nmatch (subject ())\n\t"{name:*}.c" =>\n\t\tprintf "%s" $name'
	test-step "$backend captures bind lazy definitions, functions and nested controls"
	check main $'match ./main.c\n\t./{name:*}.c =>\n\t\tdef value = $(printf "%s" $name)\n\t\tdef unused = $(touch forbidden)\n\t\tdef f x = str name x\n\t\tif (nop true)\n\t\t\tprintf "%s" $value'
	check 'main.c' $'match (nop "main.c")\n\t"{name:*}.c" =>\n\t\tdef f x = str name x\n\t\tprintf "%s" (f ".c")'
	check 'outerinnerouter' $'match (nop "outer")\n\t"{name:*}" =>\n\t\tprintf "%s" $name\n\t\tmatch (nop "inner")\n\t\t\t"{name:*}" =>\n\t\t\t\tprintf "%s" $name\n\t\tprintf "%s" $name'
	check done $'match $(/bin/sh -c "printf x >> attempts; printf subject")\n\t"subject" =>\n\t\tprintf "%s" $(printf done)'
	if [ "$(cat "$work/attempts")" = x ] && [ ! -e "$work/forbidden" ]; then test-ok "$backend subject runs once and unused definitions stay lazy"; else test-fail "$backend subject replay or eager definition"; fi
	rm "$work/attempts"
	test-step "$backend retains type, capture conflict and capability boundaries"
	check number $'match 1\n\t"x" =>\n\t\tprintf forbidden\n\t1 =>\n\t\tprintf number'
	reject PARSE_ERR $'match (nop "x")\n\t[1] =>\n\t\tprintf forbidden'
	reject DEF_INVALID $'match (nop "x")\n\t"{name:*}" =>\n\t\tdef name = "conflict"\n\t\tprintf forbidden'
	reject DEF_INVALID $'match (nop "x")\n\t"{name:*}" =>\n\t\tdef name x = x\n\t\tprintf forbidden'
	reject REF_MISSING $'match (nop "x")\n\t"{name:*}" =>\n\t\tprintf yes\nprintf "%s" $name'
	reject RECIPE_FAIL $'match $(false)\n\telse =>\n\t\tprintf forbidden'
	reject CAP_DENIED $'match $(printf subject)\n\telse =>\n\t\tprintf forbidden' --allow-run=/bin/true
	test-step "$backend rejects malformed match structure"
	for text in $'"x" =>\n\tprintf no' $'match (nop "x")\n"x" =>\n\tprintf no' $'match (nop "x")\n\t"x" =>\n\tprintf no' $'match (nop "x")\n\t"x" "y" =>\n\t\tprintf no' $'match (nop "x")\n\telse =>\n\t\tprintf no\n\t"x" =>\n\t\tprintf no' $'match (nop "x"); printf no\n\telse =>\n\t\tprintf no'; do reject PARSE_ERR "$text"; done
	test-step "$backend JSON has no unselected effects"
	"${command[@]}" do run --json -l kash -c $'match (nop "selected.c")\n\t"{name:*}.c" =>\n\t\tprintf "%s" $name\n\telse =>\n\t\tprintf forbidden' >"$work/$backend.jsonl" 2>"$work/err"
	if [ ! -s "$work/err" ] && jq -e -s 'all(.schema == 1 and .type != "diagnostic")' "$work/$backend.jsonl" >/dev/null && ! grep -q forbidden "$work/$backend.jsonl"; then test-ok "$backend selected-arm JSON"; else test-fail "$backend match JSON"; fi
done
test-step "portable match events, AST and canonical formatting"
for backend in native wasm; do jq -cS 'del(.node,.request,.generation,.attempt,.runtimeMS,.elapsedMS)' "$work/$backend.jsonl" >"$work/$backend.norm"; done
if cmp -s "$work/native.norm" "$work/wasm.norm"; then test-ok "match event parity"; else test-fail "match events differ"; diff -u "$work/native.norm" "$work/wasm.norm" || true; fi
printf '%s\n' 'if (nop true)' '  match $(printf main.c)' '    # arm comment' '    "{name:*}.c" =>' '      let local = name' '      printf "%s" $local' '    else =>' '      printf no' >"$work/source.kash"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" -l kash "$work/source.kash" >"$work/native"
	node "$wasm_wrapper" do "$operation" -l kash "$work/source.kash" >"$work/wasm"
	if cmp -s "$work/native" "$work/wasm"; then test-ok "$operation parity"; else test-fail "$operation parity"; fi
done
"$CLI_BIN" do fmt -l kash "$work/native" >"$work/again"
if cmp -s "$work/native" "$work/again"; then test-ok "match formatting is idempotent"; else test-fail "formatting changed arm structure"; fi
test-end
