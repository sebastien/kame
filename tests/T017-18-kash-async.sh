#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-18 invocation-owned async graphs"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/async"
mkdir -p "$work"
printf 'noop :\n\ttrue\nopen-gate :\n\ttouch gate\n' >"$work/policy.kmk"
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local expected="$1" source="$2"; shift 2
		local status=0
		timeout 10 "${command[@]}" do run --allow-run -C "$work" "$@" -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend async"; else test-fail "$backend async (status $status)"; cat "$work/err"; fi
	}
	reject() {
		local code="$1" source="$2"; shift 2
		local status=0
		timeout 10 "${command[@]}" do run --allow-run -C "$work" "$@" -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err"; then test-ok "$backend preserves $code"; else test-fail "$backend async failure (status $status)"; cat "$work/err"; fi
	}
	test-step "$backend starts whole graphs without blocking the next statement and joins them"
	check nextasync 'sh -c "while test ! -f gate; do sleep .01; done; printf async" &; printf next; touch gate'
	rm "$work/gate"
	check nextASYNC 'sh -c "while test ! -f gate; do sleep .01; done; printf async" | tr a-z A-Z &; printf next; touch gate'
	rm "$work/gate"
	check nextfallback 'false ? sh -c "while test ! -f gate; do sleep .01; done; printf fallback" &; printf next; touch gate'
	rm "$work/gate"
	check next 'false ? &; printf next'
	check '' 'unused = @(run :async :true "touch" "forbidden")'
	if [ ! -e "$work/forbidden" ]; then test-ok "$backend unused handle definitions stay lazy"; else test-fail "$backend eager async definition"; fi
	test-step "$backend demands handles lazily, supports repeat-safe await and accounts for observed failures"
	check true0 'job = @(run :async :true "true"); printf "%s" @(bool job); completion = @(await job); printf "%s" $completion.status'
	check 00 'job = @(run :async :true "sh" "-c" "printf x >> attempts"); first = @(await job); second = @(await job); printf "%s%s" $first.status $second.status'
	if [ "$(cat "$work/attempts")" = x ]; then test-ok "$backend repeated awaits never relaunch"; else test-fail "$backend await replay"; fi
	rm "$work/attempts"
	check handledhandled 'job = @(run :async :true "false"); first = @(await job) ?? "handled"; second = @(await job) ?? "handled"; printf "%s%s" $first $second'
	check handled 'job = @(run :async :true "kame-missing-async-executable"); completion = @(await job) ?? "handled"; printf "%s" $completion'
	reject RECIPE_FAIL 'false &; printf foreground'
	reject RECIPE_FAIL 'job = @(run :async :true "false"); printf "%s" @(bool job)'
	reject RECIPE_FAIL 'job = @(run :async :true "false"); printf "%s" @(await job)'
	reject HOST_FAIL 'kame-missing-async-executable &'
	reject RECIPE_TIMEOUT ':timeout .05 sleep 30 &' --timeout 2000
	reject RECIPE_TIMEOUT 'sleep 30 &' --timeout 100
	reject EXPR_INVALID 'printf "%s" @(await 1)'
	reject EXPR_INVALID 'job = @(run :async :true "true"); printf "%s" @(str job)'
	test-step "$backend cancellation reaps background groups on source failure"
	reject REF_MISSING 'sh -c "sleep 30; touch forbidden" &; printf "%s" $missing'
	if [ ! -e "$work/forbidden" ]; then test-ok "$backend failed source cancelled background effects"; else test-fail "$backend background escaped invocation"; fi
	test-step "$backend scoped authority and dry-run never broaden or launch work"
	status=0
	"${command[@]}" do run --allow-run=/bin/printf -l kash -c 'true &' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/err"; then test-ok "$backend async preserves scoped run grants"; else test-fail "$backend async authority"; fi
	check '' 'job = @(run :async :true "touch" "forbidden"); completion = @(await job); printf "%s" @(str completion)' -n "$work/policy.kmk" noop
	if [ ! -e "$work/forbidden" ]; then test-ok "$backend dry-run validates handles without launch"; else test-fail "$backend dry-run async"; fi
	test-step "$backend rejects async outside complete command statements"
	for text in 'printf hello && printf world' 'printf $(true &)' 'job = true &' $'if true &\n\tprintf forbidden' 'true & false'; do reject PARSE_ERR "$text"; done
	test-step "$backend owned async work survives ordered fragment boundaries"
	"${command[@]}" do run --allow-run -C "$work" -l kash -c 'job = @(run :async :true "sh" "-c" "while test ! -f gate; do sleep .01; done; printf async"); printf "%s" @(bool job)' -l km -c $'(run "printf" "next")\n(run "touch" "gate")\ncompletion = (await job)\ncompletion.status' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = truenextasync0 ]; then test-ok "$backend shared handle ownership"; else test-fail "$backend fragment-local handle"; cat "$work/err"; fi
	rm "$work/gate"
	timeout 10 "${command[@]}" do run -C "$work" -l kash -c 'sh -c "while test ! -f gate; do sleep .01; done; printf async" &' -l kmk -f "$work/policy.kmk" open-gate >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = async ]; then test-ok "$backend legacy recipe and background graph share invocation ownership"; else test-fail "$backend recipe blocked background dispatch"; fi
	rm "$work/gate"
	test-step "$backend JSON reports unobserved async failure without leaking streams"
	status=0
	"${command[@]}" do run --json -l kash -c 'false &' >"$work/json" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && [ ! -s "$work/err" ] && jq -e -s 'any(.diagnostic.code == "RECIPE_FAIL")' "$work/json" >/dev/null; then test-ok "$backend failed join is a structured diagnostic"; else test-fail "$backend async JSON failure"; fi
	test-step "$backend interruption cancels and reaps an already-started background graph"
	"${command[@]}" do run -C "$work" -l kash -c 'sh -c "printf \$\$ > background.pid; sleep 30" &; sleep 30' >"$work/out" 2>"$work/err" &
	runner=$!
	for attempt in {1..500}; do [ -s "$work/background.pid" ] && break; sleep .01; done
	if [ -s "$work/background.pid" ]; then
		background=$(cat "$work/background.pid")
		kill -TERM "$runner"
		status=0
		wait "$runner" || status=$?
		if [ "$status" = 143 ] && ! kill -0 "$background" 2>/dev/null; then test-ok "$backend interrupted invocation reaped owned work"; else test-fail "$backend interruption leaked a process or wrong status $status"; fi
		rm "$work/background.pid"
	else
		kill -TERM "$runner" || true
		wait "$runner" || true
		test-fail "$backend background startup handshake"
	fi
	test-step "$backend direct Kash aliases and expression compatibility share async ownership"
	printf '%s\n' 'sh -c "while test ! -f gate; do sleep .01; done; printf async" &; printf next; touch gate' >"$work/direct.ksh"
	timeout 10 "${command[@]}" -C "$work" "$work/direct.ksh" >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = nextasync ]; then test-ok "$backend direct .ksh joins graphs"; else test-fail "$backend direct async dispatch"; fi
	rm "$work/gate"
	timeout 10 "${command[@]}" do run --lang expr --allow-run -c '(let [job (run :async :true "true")] (await job))' >"$work/out" 2>"$work/err"
	if grep -q 'status: 0' "$work/out"; then test-ok "$backend deprecated expression spelling uses shared await"; else test-fail "$backend separate expression executor"; fi
done
test-step "async parser and canonical formatter parity"
printf '%s\n' 'printf first | cat &' 'false ? printf fallback &' 'false ? &' >"$work/source.kash"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" -l kash "$work/source.kash" >"$work/native"
	node "$CLI_ROOT/dist/kame.js" do "$operation" -l kash "$work/source.kash" >"$work/wasm"
	if cmp -s "$work/native" "$work/wasm"; then test-ok "$operation parity"; else test-fail "$operation async parity"; fi
done
"$CLI_BIN" do fmt -l kash "$work/native" >"$work/again"
if cmp -s "$work/native" "$work/again"; then test-ok "async formatting is idempotent"; else test-fail "async formatter moved graph boundary"; fi
test-end
