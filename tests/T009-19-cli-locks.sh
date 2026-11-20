#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — observable lock waits and busy ownership
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T009-19 lock diagnostics"
cli_require_tools
cli_build
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make -C "$CLI_ROOT" dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
test-step "lock location, owner, elapsed wait, interruption and recovery"
if python3 "$CLI_ROOT/tests/lock-diagnostics.py" "$CLI_BIN" "$wasm_wrapper"; then
	test-ok "native, Node and launcher waits identify their blocker"
else
	test-fail "lock diagnostic checks failed"
fi
test-end
