#!/usr/bin/env bash
# Spec: docs/spec/038-direct-kash-and-values.md — explicit action names
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T038-04 action-name migration"
test-step "build artifacts"
cd "$CLI_ROOT"
cli_build
make dist-wasm >/dev/null
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	test-step "$backend migration diagnostics and lexical bindings"
	for name in write yield out err run pipe shell sh shellrun shell-template fail; do
		for expression in "$name" "($name (fail! \"must not evaluate\"))"; do
			status=0
			"${runner[@]}" do run --lang expr -c "$expression" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
			if [ "$status" = 1 ] && [ ! -s "$TMPDIR/out" ] && grep -Fq "renamed to \`$name!\`" "$TMPDIR/err"; then test-ok "$backend rejects $expression"; else test-fail "$backend migration for $expression"; cat "$TMPDIR/out" "$TMPDIR/err"; fi
		done
		actual="$("${runner[@]}" do run --lang expr -c "(let [$name ([x] x)] ($name 42))")"
		if [ "$actual" = 42 ]; then test-ok "$backend lexical $name"; else test-fail "$backend lexical $name: $actual"; fi
	done
	test-step "$backend canonical actions and process syntax"
	actual="$("${runner[@]}" do run --lang expr -c '(let [emit out!] (emit "ok"))')"
	if [ "$actual" = 'ok"ok"' ]; then test-ok "$backend action callable"; else test-fail "$backend action callable: $actual"; fi
	actual="$("${runner[@]}" do run --allow-run --lang expr -c '(let [r (pipe! (run! "printf" "process") (run! "cat"))] r.status)')"
	if [ "$actual" = process0 ]; then test-ok "$backend canonical pipeline and external cat"; else test-fail "$backend canonical pipeline: $actual"; fi
done
test-end
