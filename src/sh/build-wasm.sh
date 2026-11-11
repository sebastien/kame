#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
export GOCACHE=${GOCACHE:-/tmp/kame-go-cache}
mkdir -p "$root/build/wasm"
output=$(mktemp "$root/build/wasm/.kame.wasm.XXXXXX")
trap 'rm -f "$output"' EXIT
CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm "${GO:-go}" -C "$root/src/go/kame" build -buildmode=c-shared -o "$output" ./cmd/kame-wasm
mv "$output" "$root/build/wasm/kame.wasm"
