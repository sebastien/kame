#!/usr/bin/env bash
# Spec: docs/spec/048-kash-practical-scripting.md — deliberate script exit
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-04 Kash deliberate exit"
cli_build
wasm_runner="$CLI_ROOT/src/js/kame.js"
if [ -z "${KAME_WASM:-}" ]; then
	make -C "$CLI_ROOT" dist-wasm >/dev/null
	wasm_runner="$CLI_ROOT/dist/kame.js"
fi
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_runner"); fi
	invoke() { "${runner[@]}" do run -C "$work" --allow-run --allow-read --allow-write "$@"; }
	run_case() {
		local expected="$1" output="$2" text="$3" status=0
		invoke -l kash -c "$text" -c 'printf forbidden-later-fragment' >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = "$expected" ] && [ "$(cat "$work/out")" = "$output" ] && ! grep -q CONTROL_EXIT "$work/err"; then test-ok "$backend exit $expected preserves boundary and output";
		else test-fail "$backend exit expected $expected ($status)"; cat "$work/out" "$work/err"; fi
	}
	reject() {
		local code="$1" text="$2" status=0
		invoke -l kash -c "$text" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code";
		else test-fail "$backend expected $code ($status)"; cat "$work/out" "$work/err"; fi
	}
	run_case 0 '' 'exit 0; printf forbidden'
	run_case 0 '' 'exit'
	run_case 64 '' 'exit 64; printf forbidden'
	run_case 255 '' 'exit 255'
	run_case 17 '' 'exit (parse-json (line $(printf 17)))'
	run_case 7 innerouter $'def stop ()\n\tdefer printf inner\n\texit 7\n\tprintf forbidden\ndefer printf outer\n(stop ())\nprintf forbidden'
	run_case 23 '' $'def stop ()\n\texit 23\n\tprintf forbidden\n(stop () ?? "fallback")\nprintf forbidden'
	run_case 0 '' $'def stop ()\n\texit 0\nif (stop ())\n\tprintf forbidden\nprintf forbidden'
	run_case 23 '' $'def stop ()\n\texit 23\ndef proxy () = stop ()\n(proxy () ?? "fallback")\nprintf forbidden'
	run_case 25 '' $'for item in [1, 2]\n\tif item == 1\n\t\texit 25\nprintf forbidden'
	run_case 0 joinedclean $'defer printf clean\nsh -c "sleep 0.02; printf joined" &\nexit 0'
	run_case 23 clean $'defer printf clean\nsh -c "sleep 1; printf forbidden" &\nexit 23'
	run_case 23 '' $'sh -c "sleep 1; printf forbidden" &\nexit 23'
	run_case 1 '' $'def stop ()\n\tfalse &\n\texit 0\n(stop () ?? "fallback")\nprintf forbidden'
	run_case 1 '' $'def stop ()\n\tdefer false\n\texit 0\n(stop () ?? "fallback")\nprintf forbidden'
	run_case 19 clean $'def stop ()\n\tdefer printf clean\n\tdefer false\n\texit 19\n(stop () ?? "fallback")\nprintf forbidden'
	run_case 0 operandclean $'def operand ()\n\tout! "operand"\n\treturn 0\ndef stop ()\n\tdefer printf clean\n\texit (operand ())\n(stop ())'
	run_case 0 operandclean $'def operand ()\n\tout! "operand"\n\treturn 0\ndefer printf clean\nexit (operand ())'
	run_case 0 beforecleanupafter $'def stop ()\n\tdefer printf cleanup\n\texit 0\n\tprintf forbidden\nlet text = $(printf before && stop && printf forbidden)\nout! $text after\nexit'
	run_case 0 beforecleanup:17:after $'def stop ()\n\tdefer printf cleanup\n\texit 17\nlet result = $?(printf before && stop && printf forbidden)\nout! $result.value ":" (fmt result.status) ":after"\nexit'
	run_case 0 19:19:2:true:after $'def stop ()\n\texit 19\nlet result = $?(stop | cat)\nout! (fmt result.status) ":" (fmt result.stages.0.status) ":" (fmt (count result.graph.nodes)) ":" (fmt result.graph.terminal) ":after"\nexit'
	run_case 0 before:19:2:after $'def stop ()\n\texit 19\nlet result = $?(printf before && stop | cat || printf forbidden)\nout! $result.value ":" (fmt result.status) ":" (fmt (count result.stages)) ":after"\nexit'
	run_case 0 cleanup:1:RECIPE_FAIL:after $'def stop ()\n\tdefer false\n\tdefer printf cleanup\n\texit 0\nlet result = $?(stop)\nout! $result.value ":" (fmt result.status) ":" $result.error.code ":after"\nexit'
	run_case 0 19:USER_ERROR:2:after $'def stop ()\n\tdefer (fail! "cleanup failure")\n\texit 19\nlet result = $?(stop | cat)\nout! (fmt result.status) ":" $result.error.code ":" (fmt (count result.stages)) ":after"\nexit'
	reject CAP_DENIED $'def stop ()\n\tdefer (env "KAME_EXIT_DENIED")\n\texit 0\nlet result = $?(stop)\nprintf forbidden'
	reject USER_ERROR $'def stop ()\n\tdefer (env "KAME_EXIT_DENIED")\n\tdefer (fail! "cleanup failure")\n\texit 0\nlet result = $?(stop)\nprintf forbidden'
	if grep -q CAP_DENIED "$work/err"; then test-ok "$backend retains the later cleanup denial"; else test-fail "$backend missing cleanup denial"; fi
	for text in 'exit 0 ?' 'exit 0 ?? 1' 'exit 0 1' 'exit 0 > ./executed' 'exit 0 | cat' 'exit 0 &' 'printf x | exit 0' 'defer exit 0'; do reject PARSE_ERR "$text"; done
	for value in '"3"' 'nil' 'true' '-1' '256' '[0]' '0.5'; do reject EXPR_INVALID "exit $value"; done
	reject PHASE_INVALID $'def operand ()\n\tout! "forbidden"\n\treturn 0\ndef stop ()\n\texit (operand ())\ndefer (stop ())'
	invoke -n -l kash -c $'def operand ()\n\t(write! ./executed "forbidden")\n\treturn 0\nexit (operand ())\nprintf forbidden' >"$work/out" 2>"$work/err"
	if [ ! -s "$work/out" ] && [ ! -e "$work/executed" ]; then test-ok "$backend dry-run does not evaluate exit operand"; else test-fail "$backend dry-run exit effects"; fi
	status=0
	invoke --json -l kash -c 'exit 64' -c 'printf forbidden' >"$work/events" 2>"$work/err" || status=$?
	if [ "$status" = 64 ] && grep -q '"termination":{"status":64}' "$work/events" && grep -q '"exitStatus":64' "$work/events" && ! grep -q 'CONTROL_EXIT\|"type":"diagnostic"\|"frames"' "$work/events"; then test-ok "$backend typed JSON termination agrees with summary without a backtrace";
	else test-fail "$backend JSON exit ($status)"; cat "$work/events" "$work/err"; fi
	cat >"$work/recipes.km" <<'KAME'
task default : first second
task first
	printf A
	exit 0
	printf forbidden
task second
	printf B
KAME
	invoke ./recipes.km >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = AB ]; then test-ok "$backend recipe exit ends its attempt, not the scheduler"; else test-fail "$backend recipe boundary"; cat "$work/out" "$work/err"; fi
	printf 'task default : ./missing\nrule ./missing\n\texit 0\n' >"$work/missing.km"
	status=0
	invoke ./missing.km >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q OUTPUT_MISSING "$work/err"; then test-ok "$backend successful recipe exit still verifies outputs"; else test-fail "$backend exited recipe output verification"; cat "$work/err"; fi
	printf 'task default : ./artifact\nrule ./artifact\n\tdefer printf clean\n\t(write! ./artifact "content")\n\texit 0\n\treturn "forbidden"\n' >"$work/artifact.km"
	invoke ./artifact.km >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/artifact")" = content ] && [ "$(cat "$work/out")" = clean ]; then test-ok "$backend successful recipe exit commits the explicit write after cleanup"; else test-fail "$backend exited recipe publication"; cat "$work/out" "$work/err"; fi
	"${runner[@]}" do fmt -l kash -c $'def stop status\n\texit status\n(stop 0)' >"$work/formatted.kash"
	invoke ./formatted.kash >"$work/out" 2>"$work/err"
	if [ ! -s "$work/out" ] && [ ! -s "$work/err" ]; then test-ok "$backend exit formatting roundtrip"; else test-fail "$backend exit formatting"; fi
	"${runner[@]}" do parse --json -l kash -c 'exit 0' >"$work/ast"
	if grep -q '"kind":"kash-exit"' "$work/ast"; then test-ok "$backend AST preserves exit as control flow"; else test-fail "$backend exit AST"; fi
done
test-end
