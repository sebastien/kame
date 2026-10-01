#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-15 Kash match and lexical captures"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/match"
mkdir -p "$work"
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
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
	check first $'match @("same")\n\tcase "same"\n\t\tprintf first\n\tcase "same"\n\t\tprintf forbidden'
	check fallback $'match @("other")\n\tcase "same"\n\t\tprintf forbidden\n\telse\n\t\tprintf fallback'
	check after $'match @("other")\n\tcase "same"\n\t\tprintf forbidden\nprintf after'
	check 'mainouter' $'name = "outer"\nmatch @("./src/main.c")\n\tcase ./src/{name:*}.c\n\t\tprintf "%s" $name\nprintf "%s" $name'
	check 'héllo' $'match @("héllo.txt")\n\tcase "{name:*}.txt"\n\t\tprintf "%s" $name'
	check literal $'match @("*.txt")\n\tcase "*.txt"\n\t\tprintf literal'
	check branch $'match $(printf subject)\n\tcase "subject"\n\t\tprintf branch'
	check bound $'pattern = "{name:*}.c"\nmatch @("bound.c")\n\tcase pattern\n\t\tprintf "%s" $name'
	check 'subjectbody' $'match @(let [ignored (out "subject")] "x")\n\tcase "x"\n\t\tprintf "%s" $(printf body)'
	check waiting $'match @("waiting.c")\n\tcase (let [ignored $(printf ready)] "{name:*}.c")\n\t\tprintf "%s" $name'
	test-step "$backend captures bind lazy definitions, functions and nested controls"
	check main $'match @("./main.c")\n\tcase ./{name:*}.c\n\t\tvalue = $(printf "%s" $name)\n\t\tunused = $(touch forbidden)\n\t\t(f X) = @(cat name X)\n\t\tif @(:true)\n\t\t\tprintf "%s" $value'
	check 'main.c' $'match @("main.c")\n\tcase "{name:*}.c"\n\t\t(f X) = @(cat name X)\n\t\tprintf "%s" @(f ".c")'
	check 'outerinnerouter' $'match @("outer")\n\tcase "{name:*}"\n\t\tprintf "%s" $name\n\t\tmatch @("inner")\n\t\t\tcase "{name:*}"\n\t\t\t\tprintf "%s" $name\n\t\tprintf "%s" $name'
	check done $'match $(/bin/sh -c "printf x >> attempts; printf subject")\n\tcase "subject"\n\t\tprintf "%s" $(printf done)'
	if [ "$(cat "$work/attempts")" = x ] && [ ! -e "$work/forbidden" ]; then test-ok "$backend subject runs once and unused definitions stay lazy"; else test-fail "$backend subject replay or eager definition"; fi
	rm "$work/attempts"
	test-step "$backend retains type, capture conflict and capability boundaries"
	reject EXPR_INVALID $'match @(1)\n\tcase "x"\n\t\tprintf forbidden'
	reject EXPR_INVALID $'match @("x")\n\tcase [1]\n\t\tprintf forbidden'
	reject DEF_INVALID $'match @("x")\n\tcase "{name:*}"\n\t\tname = "conflict"\n\t\tprintf forbidden'
	reject DEF_INVALID $'match @("x")\n\tcase "{name:*}"\n\t\t(name X) = X\n\t\tprintf forbidden'
	reject REF_MISSING $'match @("x")\n\tcase "{name:*}"\n\t\tprintf yes\nprintf "%s" $name'
	reject RECIPE_FAIL $'match $(false)\n\telse\n\t\tprintf forbidden'
	reject CAP_DENIED $'match $(printf subject)\n\telse\n\t\tprintf forbidden' --allow-run=/bin/true
	test-step "$backend rejects malformed match structure"
	for text in $'match "x"\n\telse\n\t\tprintf no' $'case "x"\n\tprintf no' $'match @("x")\ncase "x"\n\tprintf no' $'match @("x")\n\tcase "x"\n\tprintf no' $'match @("x")\n\tcase "x" "y"\n\t\tprintf no' $'match @("x")\n\telse\n\t\tprintf no\n\tcase "x"\n\t\tprintf no' $'match @("x"); printf no\n\telse\n\t\tprintf no'; do reject PARSE_ERR "$text"; done
	test-step "$backend JSON has no unselected effects"
	"${command[@]}" do run --json -l kash -c $'match @("selected.c")\n\tcase "{name:*}.c"\n\t\tprintf "%s" $name\n\telse\n\t\tprintf forbidden' >"$work/$backend.jsonl" 2>"$work/err"
	if [ ! -s "$work/err" ] && jq -e -s 'all(.schema == 1 and .type != "diagnostic")' "$work/$backend.jsonl" >/dev/null && ! grep -q forbidden "$work/$backend.jsonl"; then test-ok "$backend selected-arm JSON"; else test-fail "$backend match JSON"; fi
done
test-step "portable match events, AST and canonical formatting"
for backend in native wasm; do jq -cS 'del(.node,.request,.generation,.attempt)' "$work/$backend.jsonl" >"$work/$backend.norm"; done
if cmp -s "$work/native.norm" "$work/wasm.norm"; then test-ok "match event parity"; else test-fail "match events differ"; diff -u "$work/native.norm" "$work/wasm.norm" || true; fi
printf '%s\n' 'if @(:true)' '  match $(printf main.c)' '    # arm comment' '    case "{name:*}.c"' '      local = name' '      printf "%s" $local' '    else' '      printf no' >"$work/source.kash"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" -l kash "$work/source.kash" >"$work/native"
	node "$CLI_ROOT/dist/kame.js" do "$operation" -l kash "$work/source.kash" >"$work/wasm"
	if cmp -s "$work/native" "$work/wasm"; then test-ok "$operation parity"; else test-fail "$operation parity"; fi
done
"$CLI_BIN" do fmt -l kash "$work/native" >"$work/again"
if cmp -s "$work/native" "$work/again"; then test-ok "match formatting is idempotent"; else test-fail "formatting changed arm structure"; fi
test-end
