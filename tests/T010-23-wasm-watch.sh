#!/usr/bin/env bash
# Spec: docs/spec/020-watch.md — retained WASM watch sessions
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T010-23 wasm watch source graph and active inputs"
test-step "build WASM CLI"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
test-step "source reload, scoped environments, repair, bounded output, invalidation and cancellation"
python3 "$CLI_ROOT/tests/watch-runtime.py" node "$CLI_ROOT/dist/kame.js"
test-ok "WASM watch tracks active inputs and reaps children on cancellation"
test-end
