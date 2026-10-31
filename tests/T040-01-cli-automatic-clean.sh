#!/usr/bin/env bash
# Spec: docs/spec/040-automatic-clean.md — historical output ownership and safety
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T040-01 automatic clean"
test-step "build native and WASM runners"
cli_require_tools
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
test-step "historical ledger, rule changes, protection and filesystem safety"
python3 "$CLI_ROOT/tests/automatic-clean.py" "$CLI_BIN" "$CLI_ROOT/dist/kame.js"
test-ok "automatic cleanup preserves uncertain and important artifacts"
test-end
