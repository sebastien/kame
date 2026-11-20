#!/usr/bin/env bash
# Spec: docs/spec/036-cli_ansi.md — command presentation and exact output
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T036-01 CLI presentation"
test-step "build both hosts"
cli_build
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make -C "$CLI_ROOT" dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
test-step "command, JSON and exact-byte presentation matrix"
python3 "$CLI_ROOT/tests/cli-presentation.py" "$CLI_BIN" "$wasm_wrapper" values
test-ok "native and WASM presentation contracts"
test-end
