#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — pure computed output registration
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T004-16 computed outputs"
test-step "build native and WASM runners"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
status=0
for backend in native wasm; do
	test-step "$backend computed output acceptance"
	if python3 tests/computed_outputs.py "$CLI_ROOT" "$backend" "$CLI_BIN"; then
		test-ok "$backend computed outputs"
	else
		status=1
	fi
done
if [ "$status" != 0 ]; then test-fail "computed output acceptance failed"; fi
test-end
