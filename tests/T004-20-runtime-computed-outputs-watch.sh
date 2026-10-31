#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — computed target watch replacement
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T004-20 computed output watch replacement"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	test-step "$backend replaces the retained computed target index"
	python3 "$CLI_ROOT/tests/computed-outputs.py" "$CLI_ROOT" "$backend" "$CLI_BIN" watch
	test-ok "$backend computed output watch"
done
test-end
