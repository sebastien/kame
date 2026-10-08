#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — freestanding build boundary.
#
# Translate the portable build runtime and fail if it acquired a hosted
# transitive import. solod.dev/so/os is the marker: when it appears in the
# generated tree, the runtime is no longer portable and could not link for
# wasm32-freestanding. Translating ./program pulls in every package it uses,
# so this covers core, diagnostic, host, lang/*, and operations too.
set -euo pipefail

ROOT="$(CDPATH= cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
KAME_DIR="$ROOT/src/go/kame"
OUT="$ROOT/build/wasm/portable"

for pkg in ./program ./cli; do
	rm -rf "$OUT"
	mkdir -p "$OUT"
	(cd "$KAME_DIR" && so translate -o "$OUT" "$pkg" >/dev/null)
	if [ -d "$OUT/so/os" ]; then
		echo "wasm-portable: $pkg acquired a hosted import: solod.dev/so/os" >&2
		exit 1
	fi
done

echo "wasm-portable: ./program and ./cli translate with no hosted imports"
