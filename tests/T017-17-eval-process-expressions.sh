#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-17 expression process graphs"
test-step "build artifacts"
cd "$CLI_ROOT"
if [ -n "${KAME_WASM:-}" ]; then wasm_cli="$CLI_ROOT/src/js/kame.js"; else make dist-wasm >/dev/null; wasm_cli="$CLI_ROOT/dist/kame.js"; fi
cli_build
work="$TMPDIR/process-expressions"
mkdir -p "$work/sub"
printf 'task noop\n\ttrue\n' >"$work/policy.km"
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$wasm_cli"); fi
	check() {
		local expected="$1" source="$2"; shift 2
		local status=0
		"${command[@]}" do run --allow-run -C "$work" "$@" -l expr -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend process value"; else test-fail "$backend process value (status $status)"; cat "$work/err"; fi
	}
	reject() {
		local code="$1" source="$2" expected="${3:-1}"
		local status=0
		"${command[@]}" do run --allow-run -C "$work" -l expr -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = "$expected" ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend preserves $code"; else test-fail "$backend process failure"; cat "$work/err"; fi
	}
	test-step "$backend streams argv graphs and exposes immutable completion metadata"
	check 'hello0' '((r => r.status) (run! "printf" "%s" "hello"))'
	check 'HELLO0' '((r => r.status) (pipe! (run! "printf" "hello") (run! "tr" "a-z" "A-Z")))'
	check 'a b;*.c0' '((r => r.status) (run! "printf" "%s" ["a b", ";", "*.c"]))'
	check '0' '((r => r.status) (run! "printf" "%s" []))'
	check '0' '((r => r.status) (run! :async false "true"))'
	check 'unbounded stream0' '((r => r.status) (run! "printf" "unbounded stream"))' --capture-limit 1
	"${command[@]}" do run --allow-run -l expr -c '((r => r.status) (run! "sh" "-c" "printf \"\\377\""))' >"$work/binary" 2>"$work/err"
	printf '\3770' >"$work/expected-binary"
	if cmp -s "$work/binary" "$work/expected-binary"; then test-ok "$backend process values never decode binary stdout"; else test-fail "$backend binary stream"; fi
	check 'false' '((r => r.stdoutCaptured) (run! "true"))'
	check 'false' '((r => r.stderrCaptured) (run! "true"))'
	check '[{status: 0, signal: 0, outcome: 0, effectiveStatus: 0}]' '((r => r.stages) (run! "true"))'
	check '[{status: 0, signal: 0, outcome: 0, effectiveStatus: 0}, {status: 0, signal: 0, outcome: 0, effectiveStatus: 0}]' '((r => r.stages) (pipe! (run! "true") (run! "true")))'
	check '0' '((r => r.signal) (run! "true"))'
	check 'literal :cwd0' '((r => r.status) (run! "printf" "%s" "literal :cwd"))'
	check '0' '((r => r.status) (run! :VALUE "child value" "sh" "-c" "test \"$VALUE\" = \"child value\""))'
	check "${work}/sub"$'\n0' '((r => r.status) (run! :cwd ./sub "pwd"))'
	check 'CHILD0' '((r => r.status) (pipe! (run! :VALUE "child" "sh" "-c" "printf \"$VALUE\"") (run! "tr" "a-z" "A-Z")))'
	test-step "$backend lazy process definitions and stable call identities"
	"${command[@]}" do run --print-result --allow-run -C "$work" -l km -c $'unused = (run! "touch" "forbidden")\ndef f X = (run! "sh" "-c" "printf x >> attempts")\na = (f 1)\nb = (f 1)\n{count: (count [a, b]), status: a.status}' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = '{count: 2, status: 0}' ] && [ "$(cat "$work/attempts")" = xx ] && [ ! -e "$work/forbidden" ]; then test-ok "$backend lazy run calls are correlated"; else test-fail "$backend lazy run replay"; fi
	rm "$work/attempts"
	check 'firstsecond0' '((r => r.status) (run! "printf" "%s%s" $(printf first) $(printf second)))'
	check '0' '((r later => r.status) (run! "sh" "-c" "printf x >> attempts") $(printf later))'
	if [ "$(cat "$work/attempts")" = x ]; then test-ok "$backend completed graph is not replayed across a later wait"; else test-fail "$backend replayed graph"; fi
	rm "$work/attempts"
	check 'nil' '(if false then (run! "touch" "forbidden") else nil)'
	"${command[@]}" do run --allow-run -C "$work" -l kash -c $'if true\n\tlet result = (pipe! (run! "printf" "branch") (run! "cat"))\n\tprintf "%s" $result.status\nprintf done' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = branch0done ]; then test-ok "$backend lexical Kash definitions share the graph executor"; else test-fail "$backend Kash process expressions"; fi
	test-step "$backend rejects malformed whole graphs before evaluating stages"
	for text in '(run!)' '(run! :cwd)' '(run! :cwd ./sub)' '(run! :cwd ./sub :cwd ./sub "true")' '(run! :bad-key "x" "true")' '(pipe! (run! "true"))' '(pipe! (run! $(touch forbidden)) 1)' '(pipe! (run! $(touch forbidden)) (run! :async false "true"))' '(run! ["true"])' '(run! "printf" ["ok", ["nested"]])' '(run! :cwd ["sub"] "true")' '(run! :timeout 0 "true")' '(run! :async 1 "true")' '(run! "printf" (run! "true"))'; do reject EXPR_INVALID "$text"; done
	if [ ! -e "$work/forbidden" ]; then test-ok "$backend invalid and unused graphs never launch"; else test-fail "$backend launched malformed graph"; fi
	reject RECIPE_FAIL '(run! "false")'
	reject RECIPE_FAIL '(pipe! (run! "false") (run! "true"))'
	reject RECIPE_FAIL '(pipe! (run! "sh" "-c" "exit 3") (run! "sh" "-c" "exit 7"))' 7
	if grep -q 'status 7' "$work/err"; then test-ok "$backend selects the rightmost failure"; else test-fail "$backend aggregate status"; fi
	reject TOOL_MISSING '(run! "kame-missing-process-expression")'
	status=0
	"${command[@]}" do run --allow-run=/bin/printf -l expr -c '(run! "printf" "forbidden")' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend scoped grants forbid PATH lookup"; else test-fail "$backend run broadened grants"; fi
	"${command[@]}" do run --allow-run=/bin/printf -l expr -c '((r => r.status) (run! "/bin/printf" "explicit"))' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = explicit0 ]; then test-ok "$backend explicit executable grants"; else test-fail "$backend explicit executable"; fi
	test-step "$backend preserves mixed-session dry run and JSON parity"
	"${command[@]}" do run -C "$work" -n "$work/policy.km" noop -l expr -c '(run! "touch" "forbidden")' >"$work/out" 2>"$work/err"
	if [ ! -e "$work/forbidden" ] && [ ! -s "$work/out" ]; then test-ok "$backend dry-run does not launch expressions"; else test-fail "$backend dry-run effects"; fi
	"${command[@]}" do run --allow-run --json -l expr -c '((r => r) (pipe! (run! "printf" "hello") (run! "tr" "a-z" "A-Z")))' >"$work/$backend.jsonl" 2>"$work/err"
	if [ ! -s "$work/err" ] && jq -e -s 'all(.schema == 1 and .type != "diagnostic")' "$work/$backend.jsonl" >/dev/null; then test-ok "$backend structured JSON result"; else test-fail "$backend process JSON"; fi
	test-step "$backend recipe captures preserve no-op builds"
	cache_work="$work/cache-$backend"
	mkdir -p "$cache_work"
	cat >"$cache_work/Makefile.km" <<'KAME'
rule ./plain : ./input
	let captured = $(sh -c "printf x >> plain-log; cat input")
	return captured
rule ./typed : ./input
	let captured = (capture! {stdout: true} "sh" "-c" "printf x >> typed-log; cat input")
	return captured.stdout
KAME
	for phase in first noop changed noop; do
		if [ "$phase" != noop ]; then printf '%s' "$phase" >"$cache_work/input"; fi
		"${command[@]}" -C "$cache_work" ./plain ./typed >"$work/out" 2>"$work/err"
		expected=x
		if [ "$(cat "$cache_work/input")" = changed ]; then expected=xx; fi
		if cmp -s "$cache_work/input" "$cache_work/plain" && cmp -s "$cache_work/input" "$cache_work/typed" && [ "$(cat "$cache_work/plain-log")" = "$expected" ] && [ "$(cat "$cache_work/typed-log")" = "$expected" ]; then
			test-ok "$backend $phase capture outputs and execution counts"
		else
			test-fail "$backend $phase capture freshness"; cat "$work/err"
		fi
	done
done
test-step "portable process result events"
for backend in native wasm; do jq -cS 'del(.node,.request,.generation,.attempt,.elapsedMS)' "$work/$backend.jsonl" >"$work/$backend.norm"; done
if cmp -s "$work/native.norm" "$work/wasm.norm"; then test-ok "process expression event parity"; else test-fail "process events differ"; diff -u "$work/native.norm" "$work/wasm.norm" || true; fi
test-end
