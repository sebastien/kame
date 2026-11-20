#!/usr/bin/env bash
# Spec: docs/spec/036-cli_ansi.md — terminal taskbar progress and ETA
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T036-04 OSC progress"
cli_build
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make -C "$CLI_ROOT" dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
python3 "$CLI_ROOT/tests/osc-progress.py" "$CLI_BIN" "$wasm_wrapper" "$TMPDIR"
test-ok "native/WASM ETA progress, fallback and terminal cleanup"
test-end
