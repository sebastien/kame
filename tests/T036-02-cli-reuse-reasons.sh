#!/usr/bin/env bash
# Spec: docs/spec/036-cli_ansi.md — reuse-reason presentation
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T036-02 CLI reuse reasons"
test-step "build both hosts"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	test-step "$backend reuse decisions, deduplication and privacy"
	python3 "$CLI_ROOT/tests/reuse-reasons.py" "$CLI_ROOT" "$backend" "$CLI_BIN"
	test-ok "$backend reuse-reason contracts"
done
test-end
