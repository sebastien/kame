#!/usr/bin/env bash
# Native host fault/lookup tests; Windows branches here are policy checks only.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null && pwd)
mkdir -p "$root/build"
temporary=$(mktemp -d "$root/build/native-host.XXXXXX")
trap 'rm -rf "$temporary"' EXIT
for name in posix argv; do
  "${CC:-clang}" -std=gnu11 -O1 -g -Wall -Wextra -Werror \
    -fsanitize=address,undefined -fno-omit-frame-pointer \
    "$root/tools/native/test-$name.c" -o "$temporary/test-$name"
  ASAN_OPTIONS=detect_leaks=1:halt_on_error=1 UBSAN_OPTIONS=halt_on_error=1 \
    timeout --kill-after=2 90 "$temporary/test-$name"
done
