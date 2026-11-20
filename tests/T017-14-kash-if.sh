#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-14 Kash conditional blocks"
test-step "build artifacts"
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
cli_build
work="$TMPDIR/control"
mkdir -p "$work"
printf 'task noop\n\ttrue\n' >"$work/policy.km"
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$wasm_wrapper"); fi
	check() {
		local expected="$1" source="$2"; shift 2
		local status=0
		"${command[@]}" do run -C "$work" "$@" -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend conditional"; else test-fail "$backend conditional (status $status)"; cat "$work/err"; fi
	}
	reject() {
		local code="$1" source="$2"
		local status=0
		"${command[@]}" do run -C "$work" -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err"; then test-ok "$backend preserves $code"; else test-fail "$backend rejected conditional incorrectly"; cat "$work/err"; fi
	}
	test-step "$backend selects commands and Kame truth once"
	check yes $'if true\n\tprintf yes\nelse\n\tprintf no'
	check elif $'if false\n\tprintf no\nelif (nop true)\n\tprintf elif\nelse\n\tprintf no'
	check empty $'if (nop "")\n\tprintf empty\nelse\n\tprintf no'
	check zero $'if (nop 0)\n\tprintf zero'
	check else $'if (nop nil)\n\tprintf no\nelif (nop false)\n\tprintf no\nelse\n\tprintf else'
	check after $'if $?(false | cat).ok\n\tprintf no\nprintf after'
	check nested $'if (nop true)\n  if false\n    printf no\n  else\n    printf nested\nelse\n  printf no'
	check 'liveyes' $'let outcome = $?(/bin/sh -c "printf live; printf warning >&2; exit 0")\nif outcome.ok\n\tprintf "%s" $outcome.value yes'
	if [ "$(cat "$work/err")" = warning ]; then test-ok "$backend forwards condition stderr"; else test-fail "$backend condition stderr"; fi
	test-step "$backend branch definitions are lazy, lexical and forward-visible"
	check innerouter $'def name = "outer"\nif true\n\tdef unused = $(touch forbidden)\n\tprintf "%s" $name\n\tdef name = "inner"\nprintf "%s" $name'
	check ab $'if true\n\tdef f x = $(printf "%s" $x)\n\tprintf "%s" (f "a") (f "b")'
	check outerinner $'if true\n\tdef parent = "outer"\n\tif true\n\t\tdef child = "inner"\n\t\tprintf "%s" $parent $child'
	check fallback $'if true\n\tdef broken = missing\n\tlet value = broken ?? "fallback"\n\tprintf "%s" $value'
	check '' $'if false\n\tprintf $(touch forbidden)\n\tdef bad = (first 1)'
	check 'firstsecond' $'if true\n\tprintf first\n\tprintf "%s" $(printf second)'
	check 'conditionbody' $'if (nop (out! "condition") true)\n\tprintf "%s" $(printf body)'
	check 'localvalueaftervalue' $'if true\n\tdef name = nop (out! "local") "value"\n\tprintf "%s" $name $(printf after)\n\tprintf "%s" $name'
	check 'valueafterfunction' $'if true\n\tdef f x = nop (out! "function") x\n\tprintf "%s" (f "value") $(printf after)'
	check done $'if $?(/bin/sh -c "printf x >> attempts").ok\n\tprintf "%s" $(printf done)'
	if [ "$(cat "$work/attempts")" = x ] && [ ! -e "$work/forbidden" ]; then test-ok "$backend conditions do not replay and unused work stays lazy"; else test-fail "$backend condition replay or branch effects"; fi
	rm "$work/attempts"
	test-step "$backend preserves execution and lexical failures"
	reject REF_MISSING $'if true\n\tdef local = "private"\nprintf "%s" $local'
	reject DEP_CYCLE $'if true\n\tdef local = local\n\tprintf "%s" $local'
	check TOOL_MISSING $'let r = $?(kame-missing-condition-command)\nif r.ok\n\tprintf forbidden\nelse\n\tprintf %s $r.error.code'
	check RECIPE_TIMEOUT $'let r = $?(timeout: 0.05 sleep 30)\nif r.ok\n\tprintf forbidden\nelse\n\tprintf %s $r.error.code'
	check '' $'if (nop false)\n\tprintf no' --allow-run=/bin/printf
	status=0
	"${command[@]}" do run --allow-run=/bin/printf -l kash -c $'if $?(true).ok\n\tprintf no\nelse\n\tprintf forbidden' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend denied condition cannot select else"; else test-fail "$backend condition broadened grants"; fi
	check '' $'if true\n\tprintf changed > protected\nelse\n\tprintf changed > protected' -n "$work/policy.km" noop
	if [ ! -e "$work/protected" ]; then test-ok "$backend dry-run suppresses branch effects"; else test-fail "$backend dry-run materialized a branch"; fi
	test-step "$backend rejects malformed indentation and headers"
	for text in $'if true\nprintf no' $'else\n\tprintf no' $'if true; printf no\n\tprintf no' $'if [true, , false]\n\tprintf no' $'if\n\tprintf no' $'if true ? false\n\tprintf no' $'if true\n \tprintf no' $'if true\n  printf no\n   printf no' $'if true\n\tprintf no\nelse\n\tprintf no\nelif true\n\tprintf no'; do reject PARSE_ERR "$text"; done
	test-step "$backend JSON retains only selected effects"
	"${command[@]}" do run --json -l kash -c $'if false\n\tprintf forbidden\nelif (nop true)\n\tdef local = (out! "selected")\n\tprintf "%s" (str local)\nelse\n\tprintf forbidden' >"$work/$backend.jsonl" 2>"$work/err"
	if [ ! -s "$work/err" ] && jq -e -s 'all(.schema == 1 and .type != "diagnostic")' "$work/$backend.jsonl" >/dev/null && ! grep -q forbidden "$work/$backend.jsonl"; then test-ok "$backend selected JSON effects"; else test-fail "$backend conditional JSON"; fi
done
test-step "portable conditional events"
for backend in native wasm; do jq -cS 'del(.node,.request,.generation,.attempt,.runtimeMS,.elapsedMS)' "$work/$backend.jsonl" >"$work/$backend.norm"; done
if cmp -s "$work/native.norm" "$work/wasm.norm"; then test-ok "conditional event parity"; else test-fail "conditional events differ"; diff -u "$work/native.norm" "$work/wasm.norm" || true; fi
test-step "conditional AST and canonical formatting parity"
printf '%s\n' 'if (nop true)' '  def f x = x' '  if false' '    printf no' '  else' '    printf "%s" (f "yes")' >"$work/source.kash"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" -l kash "$work/source.kash" >"$work/native"
	node "$wasm_wrapper" do "$operation" -l kash "$work/source.kash" >"$work/wasm"
	if cmp -s "$work/native" "$work/wasm"; then test-ok "$operation parity"; else test-fail "$operation parity"; fi
done
"$CLI_BIN" do fmt -l kash "$work/native" >"$work/again"
if cmp -s "$work/native" "$work/again"; then test-ok "conditional formatting is idempotent"; else test-fail "formatting changed block structure"; fi
test-end
