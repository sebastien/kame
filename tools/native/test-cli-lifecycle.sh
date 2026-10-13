#!/usr/bin/env bash
# Repeat the exact compiled Solod Run lifecycle, not Go compatibility stubs.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null && pwd)
mkdir -p "$root/build"
temporary=$(mktemp -d "$root/build/cli-lifecycle.XXXXXX")
trap 'rm -rf "$temporary"' EXIT
(
  cd "$root/src/go/kame"
  so translate -o "$temporary/c" ./cmd/kame
)
mapfile -t sources < <(find "$temporary/c" -name '*.c' ! -path '*/main.c')
"${CC:-clang}" -std=gnu11 -O1 -g -fsanitize=address,undefined -fno-omit-frame-pointer \
  -I "$temporary/c" -I "$root/src/go/kame/host/posix" \
  "$root/tools/native/test-cli-lifecycle.c" "${sources[@]}" -lm -lpthread \
  -o "$temporary/cli-lifecycle"
cd "$root"
ASAN_OPTIONS=detect_leaks=1:halt_on_error=1 UBSAN_OPTIONS=halt_on_error=1 \
  timeout --kill-after=2 90 "$temporary/cli-lifecycle"
