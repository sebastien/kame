#!/usr/bin/env bash
# Spec: docs/spec/022-target-arguments.md — WASM target arguments
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T010-24 WASM target arguments"
test-step "build WASM CLI"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
test-step "binding, defaults, dependency scope, identity and rejection"
python3 "$CLI_ROOT/tests/target-arguments.py" node "$CLI_ROOT/dist/kame.js"
test-ok "WASM CLI binds named target arguments consistently"
test-end
