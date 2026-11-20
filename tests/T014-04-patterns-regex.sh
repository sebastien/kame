#!/usr/bin/env bash
# Spec: docs/spec/029-pattern-extensions.md — regular-expression patterns and capture operations
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T014-04 regex patterns and capture operations"
test-step "build native and WASM runners"
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
printf -v budget_subject '%1600s' ''
budget_subject=${budget_subject// /a}

for host in native wasm; do
	if [ "$host" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	"${runner[@]}" do run --lang expr -c './src/txto/blog/2026/deep/post.txto |> capture ./src/txto/{prefix:**}/{name:*}.txto |> bind ./build/data/{prefix}/{name}.json' >"$TMPDIR/$host.nested-pattern.out"
	if [ "$(cat "$TMPDIR/$host.nested-pattern.out")" = './build/data/blog/2026/deep/post.json' ]; then test-ok "$host backtracks named recursive path captures"; else test-fail "$host nested recursive path capture: $(cat "$TMPDIR/$host.nested-pattern.out")"; fi
	printf '(nop ./{name:~[a-z]+}.txt)\n' >"$TMPDIR/$host.regex.expr"
	"${runner[@]}" do fmt --lang expr "$TMPDIR/$host.regex.expr" >"$TMPDIR/$host.regex.formatted"
	"${runner[@]}" do fmt --lang expr "$TMPDIR/$host.regex.formatted" >"$TMPDIR/$host.regex.formatted.again"
	if cmp -s "$TMPDIR/$host.regex.formatted" "$TMPDIR/$host.regex.formatted.again" && grep -q 'name:~\[a-z\]+' "$TMPDIR/$host.regex.formatted"; then
		test-ok "$host formats regex expression patterns idempotently"
	else
		test-fail "$host regex expression formatting: $(cat "$TMPDIR/$host.regex.formatted")"
	fi
	status=0
	"${runner[@]}" do run --lang expr -c '(str ./{name:~(a|)}.txt)' >"$TMPDIR/$host.regex.invalid.out" 2>"$TMPDIR/$host.regex.invalid.err" || status=$?
	if [ "$status" != 0 ] && grep -q PARSE_ERR "$TMPDIR/$host.regex.invalid.err" && grep -q ':1:' "$TMPDIR/$host.regex.invalid.err"; then
		test-ok "$host reports authored source locations for malformed regex patterns"
	else
		test-fail "$host malformed regex diagnostic: $(cat "$TMPDIR/$host.regex.invalid.err")"
	fi
	"${runner[@]}" do run --lang expr -c 'regex-group 1 (regex-match (pattern "{name:~[a-z]+}{~[0-9]+}") "demo42")' >"$TMPDIR/$host.capture.out" 2>"$TMPDIR/$host.capture.err"
	if [ "$(cat "$TMPDIR/$host.capture.out")" = '"42"' ]; then
		test-ok "$host returns positional regex captures"
	else
		test-fail "$host positional capture: $(cat "$TMPDIR/$host.capture.err")"
	fi
	"${runner[@]}" do run --lang expr -c 'regex-group "name" (regex-match (pattern "{name:~[a-z]+}") "demo")' >"$TMPDIR/$host.named.out" 2>"$TMPDIR/$host.named.err"
	if [ "$(cat "$TMPDIR/$host.named.out")" = '"demo"' ]; then
		test-ok "$host returns named regex captures"
	else
		test-fail "$host named capture: $(cat "$TMPDIR/$host.named.err")"
	fi
	"${runner[@]}" do run --lang expr -c 'regex-group 0 (regex-match (pattern "{~(ab|cd)+}x") "abcdx")' >"$TMPDIR/$host.group.out" 2>"$TMPDIR/$host.group.err"
	if [ "$(cat "$TMPDIR/$host.group.out")" = '"abcd"' ]; then
		test-ok "$host supports grouping, alternation and repetition"
	else
		test-fail "$host group and alternation: $(cat "$TMPDIR/$host.group.err")"
	fi
	"${runner[@]}" do run --lang expr -c 'resub (pattern "{name:~[a-z]+}") ./pre{name}post "demo"' >"$TMPDIR/$host.replace.out" 2>"$TMPDIR/$host.replace.err"
	if [ "$(cat "$TMPDIR/$host.replace.out")" = './predemopost' ]; then
		test-ok "$host replaces using named regex captures"
	else
		test-fail "$host regex replacement: $(cat "$TMPDIR/$host.replace.err")"
	fi
	status=0
	"${runner[@]}" do run --lang expr -c '(pattern (str "{" "~[" "}"))' >"$TMPDIR/$host.invalid.out" 2>"$TMPDIR/$host.invalid.err" || status=$?
	if [ "$status" = 1 ] && grep -q PAT_INVALID "$TMPDIR/$host.invalid.err"; then
		test-ok "$host rejects malformed regex patterns"
	else
		test-fail "$host malformed regex diagnostic: $(cat "$TMPDIR/$host.invalid.err")"
	fi
	for expression in \
		"regex-match (pattern \"{~a*}z\") \"$budget_subject\"" \
		"regex-group 0 (regex-match (pattern \"{~a*}z\") \"$budget_subject\")" \
		"resub (pattern \"{~a*}z\") \"x\" \"$budget_subject\""; do
		status=0
		"${runner[@]}" do run --lang expr -c "$expression" >"$TMPDIR/$host.budget.out" 2>"$TMPDIR/$host.budget.err" || status=$?
		if [ "$status" = 1 ] && grep -q PAT_LIMIT "$TMPDIR/$host.budget.err" && [ ! -s "$TMPDIR/$host.budget.out" ]; then
			test-ok "$host ${expression%% *} preserves the regex budget failure"
		else
			test-fail "$host ${expression%% *} regex budget: $(cat "$TMPDIR/$host.budget.err")"
		fi
	done
done

test-end
