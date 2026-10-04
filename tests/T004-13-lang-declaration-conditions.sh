#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — declaration selection and gated includes
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T004-13 declaration conditions"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
for backend in native wasm; do
 test-step "$backend selects declarations before loading includes or effects"
 if python3 "$CLI_ROOT/tests/declaration-conditions.py" "$CLI_ROOT" "$backend" "$CLI_BIN"; then
  test-ok "$backend conditional declarations, configuration, diagnostics and formatting"
 else
  test-fail "$backend declaration condition assertions failed"
 fi
done
test-end
