#!/usr/bin/env bash
# Spec: docs/spec/038-direct-kash-and-values.md — computed paths and captures
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T038-05 computed paths"
# The suite owner supplies fresh artifacts; this fixture must not race builds.
for backend in native wasm; do
	test-step "$backend computed path acceptance"
	if timeout -k 2 180 python3 "$CLI_ROOT/tests/computed-paths.py" "$CLI_ROOT" "$backend" "$CLI_BIN" "$TMPDIR"; then
		test-ok "$backend computed path contracts"
	else
		test-fail "$backend computed path contracts"
	fi
done
test-end
