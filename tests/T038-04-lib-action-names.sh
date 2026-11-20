#!/usr/bin/env bash
# Spec: docs/spec/038-direct-kash-and-values.md — explicit action names
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T038-04 explicit library names"
test-step "build artifacts"
cd "$CLI_ROOT"
cli_build
wasm_runner="$CLI_ROOT/src/js/kame.js"
if [ -z "${KAME_WASM:-}" ]; then
	make dist-wasm >/dev/null
	wasm_runner="$CLI_ROOT/dist/kame.js"
fi
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_runner"); fi
	test-step "$backend unknown names and lexical bindings"
	for name in write out err run pipe shell sh shellrun shell-template fail eq is ne lt gt lte gte 'has?' replace regex-replace cat; do
		status=0
		"${runner[@]}" do run --lang expr -c "$name" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
		if [ "$status" = 1 ] && [ ! -s "$TMPDIR/out" ] && grep -Fq "REF_MISSING" "$TMPDIR/err"; then test-ok "$backend rejects $name"; else test-fail "$backend unknown reference $name"; cat "$TMPDIR/out" "$TMPDIR/err"; fi
		status=0
		"${runner[@]}" do run --lang expr -c "($name (fail! \"must not evaluate\"))" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
		if [ "$status" = 1 ] && [ ! -s "$TMPDIR/out" ] && grep -Fq "OP_UNKNOWN" "$TMPDIR/err"; then test-ok "$backend rejects application of $name"; else test-fail "$backend unknown operation $name"; cat "$TMPDIR/out" "$TMPDIR/err"; fi
		actual="$("${runner[@]}" do run --lang expr -c "($name => $name 42) (x => x)")"
		if [ "$actual" = 42 ]; then test-ok "$backend lexical $name"; else test-fail "$backend lexical $name: $actual"; fi
	done
	test-step "$backend canonical actions and process syntax"
	status=0
	"${runner[@]}" do run --lang kash -c 'let value = (fail! "stop") ?? "fallback"; out! value' >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
	if [ "$status" = 1 ] && [ ! -s "$TMPDIR/out" ] && grep -Fq USER_ERROR "$TMPDIR/err"; then test-ok "$backend authored failure is unrecoverable"; else test-fail "$backend authored failure recovery boundary"; cat "$TMPDIR/out" "$TMPDIR/err"; fi
	actual="$("${runner[@]}" do run --lang expr -c '(emit => emit "ok") out!')"
	if [ "$actual" = 'ok"ok"' ]; then test-ok "$backend action callable"; else test-fail "$backend action callable: $actual"; fi
	actual="$("${runner[@]}" do run --allow-run --lang kash -c 'printf "%s" process | cat')"
	if [ "$actual" = process ]; then test-ok "$backend canonical pipeline and external cat"; else test-fail "$backend canonical pipeline: $actual"; fi
done
test-end
