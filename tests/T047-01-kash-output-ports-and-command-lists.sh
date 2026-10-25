#!/usr/bin/env bash
# Spec: docs/spec/047-kash-output-ports-and-command-lists.md
# shellcheck disable=SC1010,SC1091,SC2016
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T047-01 Kash output ports, command lists, and exit fidelity"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	invoke() { "${runner[@]}" do run -C "$work" --allow-run --allow-read --allow-write "$@"; }
	check() {
		local expected_out="$1" expected_err="$2" source="$3" status=0
		invoke -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected_out" ] && [ "$(cat "$work/err")" = "$expected_err" ]; then test-ok "$backend output routing"; else
			test-fail "$backend output routing (status=$status, expected stdout=$(printf '%q' "$expected_out") stderr=$(printf '%q' "$expected_err"))"
			cat "$work/out" "$work/err"
		fi
	}
	reject() {
		local code="$1" source="$2" status=0
		invoke -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err"; then test-ok "$backend rejects $code"; else
			test-fail "$backend expected $code ($status)"
			cat "$work/err"
		fi
	}
	expect_status() {
		local expected="$1" code="$2" source="$3" status=0
		invoke -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = "$expected" ] && { [ -z "$code" ] || grep -q "$code" "$work/err"; }; then test-ok "$backend exit $expected"; else
			test-fail "$backend expected exit $expected/$code, got $status"
			cat "$work/err"
		fi
	}
	test-step "$backend reserved presentation ports and capture"
	check output warning $'printf output >&out\nprintf warning >&err'
	check capture '' $'value = $(printf capture >&out)\nprintf "%s" $value'
	check inherited '' $'helper = (kash """value = $(printf inherited >&out)\nprintf "%s" $value""")\n(helper)'
	check '<>' warning $'value = $(printf warning >&err)\nprintf "<%s>" $value'
	check presentation executable-stderr 'sh -c "printf executable-stderr >&2; printf presentation" >&out'
	check x x 'printf x |(broadcast) (>&out, >&err)'
	status=0
	invoke -l kash -c 'head -c 262144 /dev/zero >&err' >"$work/large.out" 2>"$work/large.err" || status=$?
	if [ "$status" = 0 ] && [ ! -s "$work/large.out" ] && [ "$(wc -c <"$work/large.err")" -eq 262144 ]; then test-ok "$backend large error-port output drains"; else test-fail "$backend large error-port output ($status)"; fi
	status=0
	invoke -l kash -c $'value = $((printf left >&err, printf right >&err))\nprintf "<%s>" $value' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 0 ] && [ "$(cat "$work/out")" = '<>' ] && grep -q left "$work/err" && grep -q right "$work/err"; then test-ok "$backend zero-output capture keeps error ports live"; else test-fail "$backend error-only capture"; cat "$work/out" "$work/err"; fi
	check OK WARN $'printf "WARN\\nOK\\n" |(frame :lines) |(filter (includes? "WARN" item) | cat >&err) | cat'
	check ab '' $'value = $(printf a >&out && printf b >&out)\nprintf "%s" $value'
	reject KASH_AMBIGUOUS_CAPTURE $'value = $((printf a, printf b))\nprintf "%s" $value'
	printf preserve >"$work/preserve"
	reject PARSE_ERR 'printf changed > ./preserve >&err'
	if [ "$(cat "$work/preserve")" = preserve ]; then test-ok "$backend conflict preflight preserves files"; else test-fail "$backend truncated a conflicting destination"; fi
	test-step "$backend short-circuiting, recovery, and conditions"
	marker="$work/skipped"
	source="false && touch $marker || true"$'\n'"true || touch $marker"$'\n''printf done'
	check done '' "$source"
	if [ ! -e "$marker" ]; then test-ok "$backend skipped operands have no effects"; else test-fail "$backend ran a skipped operand"; fi
	status=0
	"${runner[@]}" do run -C "$work" --allow-run -l kash -c "true || touch $marker" >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 0 ] && [ ! -e "$marker" ]; then test-ok "$backend skipped operand does not require write grant"; else test-fail "$backend checked skipped operand grant ($status)"; cat "$work/err"; fi
	check '' '' 'true || ./not-installed-kame-test-command'
	check C '' 'true || printf B && printf C'
	check localaa '' $'value = (let [ignored (out! "local")] "a")\nprintf "%s" $value && printf "%s" $value $(printf "")'
	check fallback '' 'sh -c "exit 7" || printf fallback'
	expect_status 7 RECIPE_FAIL $'sh -c "exit 7" && touch list-skipped\nprintf after-failure'
	if [ ! -e "$work/list-skipped" ]; then test-ok "$backend failed list stops later statements"; else test-fail "$backend ran after failed list"; fi
	check accepted '' 'sh -c "exit 7"? && printf accepted'
	check yes '' $'if sh -c "exit 7" ||\n\tsh -c "exit 0"\n\tprintf yes\nelse\n\tprintf no'
	check yes '' $'if test "$(printf x)" = x\n\tprintf yes'
	check yes '' $'if false || test "$(printf x)" = x\n\tprintf yes'
	check '' '' $'while test "$(printf x)" = y\n\tprintf no'
	reject PARSE_ERR $'printf a\n|| printf b'
	reject PARSE_ERR $'printf a\n&& printf b'
	malformed="touch $marker &&"
	expect_status 1 PARSE_ERR "$malformed"
	if [ ! -e "$marker" ]; then test-ok "$backend malformed skipped syntax has no effects"; else test-fail "$backend ran before static validation"; fi
	reject PARSE_ERR "defer touch $marker && touch $marker"
	reject PARSE_ERR "touch $marker && touch $marker &"
	if [ ! -e "$marker" ]; then test-ok "$backend forbidden compound forms have no effects"; else test-fail "$backend ran a rejected compound form"; fi
	expect_status 1 TOOL_MISSING "./missing-kame-command || touch $marker"
	if [ ! -e "$marker" ]; then test-ok "$backend launch failure is not recoverable by ||"; else test-fail "$backend recovered a launch failure"; fi
	status=0
	invoke --timeout 100 -l kash -c 'sleep 2 || touch skipped-timeout' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q RECIPE_TIMEOUT "$work/err" && [ ! -e "$work/skipped-timeout" ]; then test-ok "$backend timeout is not recoverable by ||"; else test-fail "$backend timeout recovery"; cat "$work/err"; fi
	test-step "$backend cumulative capture, signals, and cleanup status"
	check beforeafter '' $'value = $(sh -c "printf before; exit 7" || printf after)\nprintf "%s" $value'
	status=0
	invoke --capture-limit 5 -l kash -c $'value = $(printf 123 && printf 456 || touch capture-fallback)\nprintf "%s" $value' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q CAPTURE_LIMIT "$work/err" && [ ! -e "$work/capture-fallback" ]; then test-ok "$backend cumulative capture is fatal"; else test-fail "$backend capture overflow recovery ($status)"; cat "$work/err"; fi
	check signal-recovered '' 'sh -c "kill -TERM \$\$" || printf signal-recovered'
	expect_status 143 RECIPE_FAIL 'sh -c "kill -TERM \$\$"'
	expect_status 42 RECIPE_FAIL 'sh -c "exit 42"'
	expect_status 42 RECIPE_FAIL $'sh -c "exit 42"\ndefer sh -c "exit 9"'
	expect_status 9 RECIPE_FAIL $'defer sh -c "exit 42"\ndefer sh -c "exit 9"'
	printf 'sh -c "exit 42"\n' >"$work/nested.kash"
	if [ "$backend" = native ]; then nested="$CLI_BIN"; else nested="node $CLI_ROOT/dist/kame.js"; fi
	expect_status 42 RECIPE_FAIL "$nested do run --allow-run ./nested.kash"
	test-step "$backend JSON summary preserves explicit process status"
	status=0
	invoke --json -l kash -c 'sh -c "exit 42"' >"$work/status.jsonl" 2>"$work/status.err" || status=$?
	if [ "$status" = 42 ] && node --input-type=module - "$work/status.jsonl" <<'NODE'
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
const records = readFileSync(process.argv[2], 'utf8').trim().split('\n').map(line => JSON.parse(line));
const summary = records.findLast(record => record.type === 'summary');
assert.equal(summary?.exitStatus, 42);
NODE
	then test-ok "$backend JSON and process exit status agree"; else test-fail "$backend JSON status mismatch ($status)"; cat "$work/status.jsonl" "$work/status.err"; fi
done
test-end
