#!/usr/bin/env bash
# Spec: docs/spec/017-kash.md — Pipelines, streaming, graph failures and capabilities
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T017-03 embedded Kash pipeline parity"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/pipelines"
mkdir -p "$work"

parity() {
	local expression="$1" expected="$2" code="${3:-}"
	set +e
	"$CLI_BIN" do expr --allow-run --capture-limit=32 -c "$expression" >"$work/native.out" 2>"$work/native.err"
	local native_status=$?
	node "$CLI_ROOT/dist/kame.js" do expr --allow-run --capture-limit=32 -c "$expression" >"$work/wasm.out" 2>"$work/wasm.err"
	local wasm_status=$?
	set -e
	if [ "$native_status" = "$expected" ] && [ "$wasm_status" = "$expected" ] && cmp -s "$work/native.out" "$work/wasm.out"; then test-ok "$expression"; else test-fail "pipeline backend mismatch: native=$native_status wasm=$wasm_status: $expression"; fi
	if [ -n "$code" ]; then
		if grep -q "$code" "$work/native.err" && grep -q "$code" "$work/wasm.err"; then test-ok "$code on both backends"; else test-fail "pipeline diagnostic mismatch: $code"; fi
	fi
}

test-step "stream intermediate data beyond capture and OS pipe buffers"
parity '$(head -c 4194304 /dev/zero | cat | wc -c)' 0
grep -Fq '"4194304\n"' "$work/native.out"
parity '$(printf "abc\n"|tr a-z A-Z|cat)' 0
grep -q '"ABC' "$work/native.out"
parity '$(printf "a|b" | cat)' 0
parity '$(printf "%s" $(printf nested | tr a-z A-Z) | cat)' 0
parity '(map ["a" "b"] ([x] $(printf "%s" $x | tr a-z A-Z)))' 0
parity '(cat $(printf a | cat) $(printf b | cat))' 0

test-step "all stages contribute to failure"
parity '$(sh -c "exit 7" | cat)' 1 RECIPE_FAIL
grep -q 'status 7' "$work/native.err"
grep -q 'status 7' "$work/wasm.err"
parity '$(printf x | sh -c "cat; exit 9" | cat)' 1 RECIPE_FAIL
parity '$(sh -c "exit 7" | sh -c "cat; exit 9" | cat)' 1 RECIPE_FAIL
grep -q 'status 9' "$work/native.err"
grep -q 'status 9' "$work/wasm.err"
parity '$(printf x | sh -c "cat; exit 11")' 1 RECIPE_FAIL
parity '$(yes | head -c 1)' 1 RECIPE_FAIL
grep -q 'status 141' "$work/native.err"
grep -q 'status 141' "$work/wasm.err"
parity '$(printf "\\377" | cat)' 1 CAPTURE_ENCODING
parity '$(head -c 33 /dev/zero | cat)' 1 CAPTURE_LIMIT
parity '$(sleep 30 | no-such-kash-executable)' 1 HOST_FAIL
parity '$(no-such-kash-executable | sleep 30)' 1 HOST_FAIL
parity '$(sleep 30 | no-such-kash-executable | sleep 30)' 1 HOST_FAIL

test-step "every stage's stderr is live and stdout is captured once"
parity '$(sh -c "printf one >&2; printf x" | sh -c "printf two >&2; cat")' 0
for backend in native wasm; do
	grep -q one "$work/$backend.err"
	grep -q two "$work/$backend.err"
done

test-step "invalid arguments and denied later stages launch nothing"
marker="$work/should-not-exist"
expression="\$(sh -c \"touch '$marker'\" | printf @([invalid: 1]))"
parity "$expression" 1 EXPR_INVALID
if [ ! -e "$marker" ]; then test-ok "all argv validated before launch"; else test-fail "invalid later stage launched earlier work"; fi
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	set +e
	"${command[@]}" do expr --allow-run=/usr/bin/printf -c "\$(/usr/bin/printf x | /bin/sh -c \"touch '$marker'\")" >"$work/$backend.out" 2>"$work/$backend.err"
	status=$?
	set -e
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/$backend.err" && [ ! -e "$marker" ]; then test-ok "$backend validates every executable grant"; else test-fail "$backend leaked stage authorization"; fi
done

test-step "pipeline syntax and formatting parity"
printf '%s\n' '$(printf "a|b"|tr @("a" | uppercase) X|cat)' >"$work/expression.km"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" --lang expr "$work/expression.km" >"$work/native.out"
	node "$CLI_ROOT/dist/kame.js" do "$operation" --lang expr "$work/expression.km" >"$work/wasm.out"
	if cmp -s "$work/native.out" "$work/wasm.out"; then test-ok "$operation pipeline parity"; else test-fail "$operation pipeline parity"; fi
done
for invalid in '$(| cat)' '$(printf x |)' '$(printf x || cat)' '$(printf x | | cat)'; do
	parity "$invalid" 1 PARSE_ERR
done

test-step "timeout and capture overflow terminate and reap every stage"
pid_token='\$\$'
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	for scenario in timeout overflow; do
		first="$work/$backend-$scenario-first.pid"
		last="$work/$backend-$scenario-last.pid"
		if [ "$scenario" = timeout ]; then producer='sleep 30'; code=RECIPE_TIMEOUT; else producer=yes; code=CAPTURE_LIMIT; fi
		printf '%s\n' "result = \$(sh -c \"printf '%s' $pid_token > '$first'; exec $producer\" | sh -c \"printf '%s' $pid_token > '$last'; exec cat\")" >"$work/cleanup.kmk"
		set +e
		"${command[@]}" --timeout 200 --capture-limit 32 -f "$work/cleanup.kmk" result >"$work/$backend.out" 2>"$work/$backend.err"
		status=$?
		set -e
		if [ "$status" = 1 ] && grep -q "$code" "$work/$backend.err"; then test-ok "$backend $scenario reports $code"; else test-fail "$backend $scenario cleanup failed"; fi
		for file in "$first" "$last"; do
			if [ ! -e "$file" ] || ! kill -0 "$(cat "$file")" 2>/dev/null; then test-ok "$backend stage is reaped"; else test-fail "$backend left stage running"; fi
		done
	done
done

test-end
