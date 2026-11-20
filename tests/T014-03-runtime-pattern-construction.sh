#!/usr/bin/env bash
# Spec: docs/spec/029-pattern-extensions.md — runtime pattern values
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T014-03 runtime pattern construction"
test-step "build native and WASM runners"
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}

for host in native wasm; do
	if [ "$host" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	"${runner[@]}" do run --lang expr -c '(pattern (str "./" "{" "name:*" "}.c"))' >"$TMPDIR/$host.out" 2>"$TMPDIR/$host.err"
	if [ "$(cat "$TMPDIR/$host.out")" = './{name:*}.c' ]; then
		test-ok "$host explicitly constructs a pattern value"
	else
		test-fail "$host runtime pattern result: $(cat "$TMPDIR/$host.err")"
	fi
	"${runner[@]}" do run --lang expr -c '((output => ./src/demo.c |> capture (pattern (str "./src/" "{" "name:*" "}.c")) |> bind ${output}/{name}.o) "./build")' >"$TMPDIR/$host.replace.out" 2>"$TMPDIR/$host.replace.err"
	if [ "$(cat "$TMPDIR/$host.replace.out")" = './build/demo.o' ]; then
		test-ok "$host applies runtime match and expansion patterns"
	else
		test-fail "$host runtime pattern replacement: $(cat "$TMPDIR/$host.replace.err")"
	fi
	status=0
	"${runner[@]}" do run --lang expr -c '(pattern (str "./" "{" "name:*" "}" "{" "_0" "}"))' >"$TMPDIR/$host.invalid.out" 2>"$TMPDIR/$host.invalid.err" || status=$?
	if [ "$status" = 1 ] && grep -q PAT_INVALID "$TMPDIR/$host.invalid.err"; then
		test-ok "$host rejects invalid runtime pattern composition"
	else
		test-fail "$host accepted invalid runtime pattern composition"
	fi
done

test-end
