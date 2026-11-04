#!/usr/bin/env bash
# Spec: docs/spec/036-cli_ansi.md — terminal taskbar progress and ETA
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T036-04 OSC progress"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
python3 "$CLI_ROOT/tests/osc-progress.py" "$CLI_BIN" "$CLI_ROOT/dist/kame.js" "$TMPDIR"
test-ok "native/WASM ETA progress, fallback and terminal cleanup"
test-end
