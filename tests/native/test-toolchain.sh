#!/usr/bin/env bash
# Verify that native artifacts are Go binaries built without the C toolchain.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null && pwd)
binary=${KAME_TOOLCHAIN_TEST_BINARY:-$root/build/kame.debug}
if [ ! -x "$binary" ]; then make -C "$root" build; fi
metadata=$(go version -m "$binary")
grep -q 'path[[:space:]]*kame/cmd/kame' <<<"$metadata"
grep -q 'CGO_ENABLED=0' <<<"$metadata"
if grep -q 'solod.dev' <<<"$metadata"; then
    printf '%s\n' 'native binary still depends on Solod' >&2
    exit 1
fi
identity=$("$binary" --json --version)
python3 -c 'import json,sys; assert json.load(sys.stdin)["runtime"] == "Go"' <<<"$identity"
printf '%s\n' 'native toolchain: pure Go build metadata verified'
