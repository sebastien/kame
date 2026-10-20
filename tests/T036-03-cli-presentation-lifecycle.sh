#!/usr/bin/env bash
# Spec: docs/spec/036-cli_ansi.md — watch and terminal presentation
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T036-03 terminal and watch presentation"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
test-step "terminal reflow, raw child cursor ownership and watch cycles"
python3 "$CLI_ROOT/tests/cli-presentation.py" "$CLI_BIN" "$CLI_ROOT/dist/kame.js" lifecycle
test-ok "native and WASM terminal/watch presentation"
test-end
