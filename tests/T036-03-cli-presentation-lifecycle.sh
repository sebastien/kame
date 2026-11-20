#!/usr/bin/env bash
# Spec: docs/spec/036-cli_ansi.md — watch and terminal presentation
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T036-03 terminal and watch presentation"
cli_build
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make -C "$CLI_ROOT" dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
wasm_binary=${KAME_WASM:-"$CLI_ROOT/dist/kame.wasm"}
test-step "shared presentation ABI is read-only and reclaims scratch memory"
node "$CLI_ROOT/tests/wasm/test-presentation.mjs" "$wasm_binary"
test-ok "presentation query/copy and instance lifetime"
test-step "terminal reflow, raw child cursor ownership and watch cycles"
python3 "$CLI_ROOT/tests/cli-presentation.py" "$CLI_BIN" "$wasm_wrapper" lifecycle
test-ok "native and WASM terminal/watch presentation"
test-end
