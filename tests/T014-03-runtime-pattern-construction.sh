#!/usr/bin/env bash
# Spec: docs/spec/029-pattern-extensions.md — runtime pattern values
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T014-03 runtime pattern construction"
test-step "build native and WASM runners"
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null

for host in native wasm; do
	if [ "$host" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	"${runner[@]}" do run --lang expr -c '(str (pattern (cat "./" "{" "name:*" "}.c")))' >"$TMPDIR/$host.out" 2>"$TMPDIR/$host.err"
	if [ "$(cat "$TMPDIR/$host.out")" = '"./{name:*}.c"' ]; then
		test-ok "$host explicitly constructs a pattern value"
	else
		test-fail "$host runtime pattern result: $(cat "$TMPDIR/$host.err")"
	fi
	"${runner[@]}" do run --lang expr -c '(replace (pattern (cat "./src/" "{" "name:*" "}.c")) (pattern (cat "./build/" "{" "name" "}.o")) "./src/demo.c")' >"$TMPDIR/$host.replace.out" 2>"$TMPDIR/$host.replace.err"
	if [ "$(cat "$TMPDIR/$host.replace.out")" = '"./build/demo.o"' ]; then
		test-ok "$host applies runtime match and expansion patterns"
	else
		test-fail "$host runtime pattern replacement: $(cat "$TMPDIR/$host.replace.err")"
	fi
	status=0
	"${runner[@]}" do run --lang expr -c '(pattern (cat "./" "{" "name:*" "}" "{" "_0" "}"))' >"$TMPDIR/$host.invalid.out" 2>"$TMPDIR/$host.invalid.err" || status=$?
	if [ "$status" = 1 ] && grep -q PAT_INVALID "$TMPDIR/$host.invalid.err"; then
		test-ok "$host rejects invalid runtime pattern composition"
	else
		test-fail "$host accepted invalid runtime pattern composition"
	fi
done

test-end
