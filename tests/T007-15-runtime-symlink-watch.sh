#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — tracked symlink destinations
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T007-15 dangling symlink watch"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	test-step "$backend detects changes between absent symlink destinations"
	python3 "$CLI_ROOT/tests/native-data.py" "$CLI_ROOT" "$backend" "$CLI_BIN" watch
	test-ok "$backend symlink watch"
done
test-end
