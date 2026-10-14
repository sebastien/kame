#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — option consistency and language-tool sources
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T009-17 CLI options"
test-step "build both hosts"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
test-step "source, action, option, and effect-boundary matrix"
python3 "$CLI_ROOT/tests/cli-options.py" "$CLI_BIN" "$CLI_ROOT/dist/kame.js"
test-ok "native and WASM command options agree"
test-end
