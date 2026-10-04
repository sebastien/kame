#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — native watch source graph reload
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T009-14 native watch source graph and active inputs"
test-step "build native CLI"
cli_build
test-step "source reload, repair, active-input invalidation and obsolete-subscription cleanup"
python3 "$CLI_ROOT/tests/watch-runtime.py" "$CLI_BIN"
test-ok "native watch reloads sources and tracks only active inputs"
test-end
