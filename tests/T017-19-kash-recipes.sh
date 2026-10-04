#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-19 Kash recipe defaults and reusable scripts"
test-step "build native and WASM artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
for backend in native wasm; do
 test-step "$backend interpreter metadata and reusable script lifecycle"
 python3 "$CLI_ROOT/tests/recipe-settings.py" "$CLI_ROOT" "$backend" "$CLI_BIN"
 python3 "$CLI_ROOT/tests/kash-recipes.py" "$CLI_ROOT" "$backend" "$CLI_BIN"
 test-ok "$backend Kash recipes and script calls"
done
test-end
