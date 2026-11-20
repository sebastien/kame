#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-19 Kash recipe defaults and lexical functions"
test-step "build native and WASM artifacts"
cd "$CLI_ROOT"
if [ -z "${KAME_WASM:-}" ]; then make dist-wasm >/dev/null; fi
cli_build
for backend in native wasm; do
 test-step "$backend interpreter metadata and lexical function lifecycle"
  python3 "$CLI_ROOT/tests/recipe-settings.py" "$CLI_ROOT" "$backend" "$CLI_BIN" values
  python3 "$CLI_ROOT/tests/kash-recipes.py" "$CLI_ROOT" "$backend" "$CLI_BIN" values
 test-ok "$backend Kash recipes and function calls"
done
test-end
