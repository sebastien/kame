#!/usr/bin/env bash
# Spec: docs/spec/048-kash-practical-scripting.md — deliberate script exit
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-04 Kash deliberate exit"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
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
	run_case 64 '' 'exit 64; printf forbidden'
	run_case 255 '' 'exit 255'
	run_case 17 '' 'exit (parse-json (line $(printf 17)))'
	run_case 7 innerouter $'defn stop\n\tdefer printf inner\n\texit 7\n\tprintf forbidden\ndefer printf outer\n(stop)\nprintf forbidden'
	run_case 23 '' $'runner = (kash "exit 23; printf forbidden")\n(runner) ?? "fallback"\nprintf forbidden'
	run_case 0 '' $'defn stop\n\texit 0\nif (stop)\n\tprintf forbidden\nprintf forbidden'
	run_case 23 '' $'defn stop\n\texit 23\n(proxy) = (stop)\n(? (proxy) "fallback")\nprintf forbidden'
	run_case 25 '' $'for item in [1 2]\n\tif (eq? item 1)\n\t\texit 25\nprintf forbidden'
	run_case 0 joinedclean $'defer printf clean\nsh -c "sleep 0.02; printf joined" &\nexit 0'
	run_case 23 clean $'defer printf clean\nsh -c "sleep 1; printf forbidden" &\nexit 23'
	run_case 23 '' $'sh -c "sleep 1; printf forbidden" &\nexit 23'
	run_case 1 '' $'defn stop\n\tfalse &\n\texit 0\n(stop) ?? "fallback"\nprintf forbidden'
	run_case 1 '' $'defn stop\n\tdefer false\n\texit 0\n(? (stop) "fallback")\nprintf forbidden'
	run_case 19 clean $'defn stop\n\tdefer printf clean\n\tdefer false\n\texit 19\n(stop) ?? "fallback"\nprintf forbidden'
	run_case 0 operandclean $'defn stop\n\tdefer printf clean\n\texit (let [unused (out! "operand")] 0)\n(stop)'
	run_case 0 operandclean $'defer printf clean\nexit (let [unused (out! "operand")] 0)'
	for text in 'exit' 'exit 0 ?' 'exit 0 ?? 1' 'exit 0 1' 'exit 0 > ./executed' 'exit 0 | cat' 'exit 0 &' 'printf x | exit 0' 'defer exit 0'; do reject PARSE_ERR "$text"; done
	for value in '"3"' ':nil' ':true' '-1' '256' '[0]' '0.5'; do reject EXPR_INVALID "exit $value"; done
	reject PHASE_INVALID $'defn stop\n\texit (let [unused (out! "forbidden")] 0)\ndefer (stop)'
	invoke -n -l kash -c 'exit (let [unused (write! ./executed "forbidden")] 0); printf forbidden' >"$work/out" 2>"$work/err"
	if [ ! -s "$work/out" ] && [ ! -e "$work/executed" ]; then test-ok "$backend dry-run does not evaluate exit operand"; else test-fail "$backend dry-run exit effects"; fi
	status=0
	invoke --json -l kash -c 'exit 64' -c 'printf forbidden' >"$work/events" 2>"$work/err" || status=$?
	if [ "$status" = 64 ] && grep -q '"termination":{"status":64}' "$work/events" && grep -q '"exitStatus":64' "$work/events" && ! grep -q 'CONTROL_EXIT\|"type":"diagnostic"\|"frames"' "$work/events"; then test-ok "$backend typed JSON termination agrees with summary without a backtrace";
	else test-fail "$backend JSON exit ($status)"; cat "$work/events" "$work/err"; fi
	cat >"$work/recipes.kmk" <<'KMK'
default : first second
first : ; [shell: kash]
	printf A
	exit 0
	printf forbidden
second : ; [shell: kash]
	printf B
KMK
	invoke ./recipes.kmk >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = AB ]; then test-ok "$backend recipe exit ends its attempt, not the scheduler"; else test-fail "$backend recipe boundary"; cat "$work/out" "$work/err"; fi
	printf 'default : ./missing\n./missing : ; [shell: kash]\n\texit 0\n' >"$work/missing.kmk"
	status=0
	invoke ./missing.kmk >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q OUTPUT_MISSING "$work/err"; then test-ok "$backend successful recipe exit still verifies outputs"; else test-fail "$backend exited recipe output verification"; cat "$work/err"; fi
	printf 'default : ./artifact\n./artifact : ; [shell: kash]\n\tdefer printf clean\n\t(yield! "content")\n\texit 0\n\t(yield! "forbidden")\n' >"$work/artifact.kmk"
	invoke ./artifact.kmk >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/artifact")" = content ] && [ "$(cat "$work/out")" = clean ]; then test-ok "$backend successful recipe exit publishes retained effects after cleanup"; else test-fail "$backend exited recipe publication"; cat "$work/out" "$work/err"; fi
	"${runner[@]}" do fmt -l kash -c $'defn stop status\n\texit $status\n(stop 0)' >"$work/formatted.kash"
	invoke ./formatted.kash >"$work/out" 2>"$work/err"
	if [ ! -s "$work/out" ] && [ ! -s "$work/err" ]; then test-ok "$backend exit formatting roundtrip"; else test-fail "$backend exit formatting"; fi
	"${runner[@]}" do parse --json -l kash -c 'exit 0' >"$work/ast"
	if grep -q '"kind":"kash-exit"' "$work/ast"; then test-ok "$backend AST preserves exit as control flow"; else test-fail "$backend exit AST"; fi
done
test-end
