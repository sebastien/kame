#!/usr/bin/env bash
# Spec: docs/spec/012-streams.md — host publication backpressure
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T012-04 WASM publication backpressure"
test-step "build WASM host"
cli_require_tools
cd "$CLI_ROOT"
make dist-wasm >/dev/null
for scenario in stdout stderr json argv cache cancel timeout watch; do
 test-step "public pipe pressure: $scenario"
 if python3 "$CLI_ROOT/tests/backpressure.py" "$CLI_ROOT" "$scenario"; then
  test-ok "$scenario pauses producers and resumes or terminates correctly"
 else
  test-fail "$scenario publication backpressure"
 fi
done
test-end
