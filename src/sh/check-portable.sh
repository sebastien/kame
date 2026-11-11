#!/usr/bin/env bash
# Compile the portable runtime and enforce the Go WASI reactor import boundary.
set -euo pipefail
ROOT="$(CDPATH= cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
bash "$ROOT/src/sh/build-wasm.sh"
node "$ROOT/tests/wasm/check-imports.mjs" "$ROOT/build/wasm/kame.wasm"
