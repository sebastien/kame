#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — typed records, predicates, digests and links
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T007-14 native data operations"
test-step "build native and WASM artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
for backend in native wasm; do
	test-step "$backend native data acceptance"
	if python3 tests/native_data.py "$CLI_ROOT" "$backend" "$CLI_BIN"; then test-ok "$backend typed data and symlinks"; else test-fail "$backend native data acceptance failed"; fi
done
test-end
