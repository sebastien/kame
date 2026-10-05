#!/usr/bin/env bash
# Spec: docs/spec/029-pattern-extensions.md — regular-expression patterns and capture operations
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T014-04 regex patterns and capture operations"
test-step "build native and WASM runners"
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null

for host in native wasm; do
	if [ "$host" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	printf '(str ./{name:~[a-z]+}.txt)\n' >"$TMPDIR/$host.regex.expr"
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
	"${runner[@]}" do run --lang expr -c '(str (capture 1 (regex-match (pattern (cat "{" "name:~[a-z]+" "}" "{" "~[0-9]+" "}")) "demo42")))' >"$TMPDIR/$host.capture.out" 2>"$TMPDIR/$host.capture.err"
	if [ "$(cat "$TMPDIR/$host.capture.out")" = '"42"' ]; then
		test-ok "$host returns positional regex captures"
	else
		test-fail "$host positional capture: $(cat "$TMPDIR/$host.capture.err")"
	fi
	"${runner[@]}" do run --lang expr -c '(str (capture "name" (regex-match (pattern (cat "{" "name:~[a-z]+" "}")) "demo")))' >"$TMPDIR/$host.named.out" 2>"$TMPDIR/$host.named.err"
	if [ "$(cat "$TMPDIR/$host.named.out")" = '"demo"' ]; then
		test-ok "$host returns named regex captures"
	else
		test-fail "$host named capture: $(cat "$TMPDIR/$host.named.err")"
	fi
	"${runner[@]}" do run --lang expr -c '(str (capture 0 (regex-match (pattern (cat "{" "~(ab|cd)+" "}x")) "abcdx")))' >"$TMPDIR/$host.group.out" 2>"$TMPDIR/$host.group.err"
	if [ "$(cat "$TMPDIR/$host.group.out")" = '"abcd"' ]; then
		test-ok "$host supports grouping, alternation and repetition"
	else
		test-fail "$host group and alternation: $(cat "$TMPDIR/$host.group.err")"
	fi
	"${runner[@]}" do run --lang expr -c '(str (regex-replace (pattern (cat "{" "name:~[a-z]+" "}")) (pattern (cat "pre{" "name" "}post")) "demo"))' >"$TMPDIR/$host.replace.out" 2>"$TMPDIR/$host.replace.err"
	if [ "$(cat "$TMPDIR/$host.replace.out")" = '"predemopost"' ]; then
		test-ok "$host replaces using named regex captures"
	else
		test-fail "$host regex replacement: $(cat "$TMPDIR/$host.replace.err")"
	fi
	status=0
	"${runner[@]}" do run --lang expr -c '(pattern (cat "{" "~[" "}"))' >"$TMPDIR/$host.invalid.out" 2>"$TMPDIR/$host.invalid.err" || status=$?
	if [ "$status" = 1 ] && grep -q PAT_INVALID "$TMPDIR/$host.invalid.err"; then
		test-ok "$host rejects malformed regex patterns"
	else
		test-fail "$host malformed regex diagnostic: $(cat "$TMPDIR/$host.invalid.err")"
	fi
done

test-end
