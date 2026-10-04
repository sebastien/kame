#!/usr/bin/env bash
# Spec: docs/spec/022-target-arguments.md — native target arguments
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T009-15 CLI target arguments"
test-step "build native CLI"
cli_build
test-step "binding, defaults, dependency scope, identity and rejection"
python3 "$CLI_ROOT/tests/target-arguments.py" "$CLI_BIN"
test-ok "native CLI binds named target arguments consistently"
test-end
