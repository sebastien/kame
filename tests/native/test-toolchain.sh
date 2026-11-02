#!/usr/bin/env bash
# Exercise the real toolchain recipe without changing the developer's tools.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null && pwd)
temporary=$(mktemp -d "$root/build/toolchain.XXXXXX")
trap 'rm -rf "$temporary"' EXIT
driver=${KAME_TOOLCHAIN_TEST_BINARY:-kame}
mkdir -p "$temporary/src/go/kame" "$temporary/bin"
cp "$root/Makefile.kmk" "$temporary/"
cp "$root/src/go/kame/go.mod" "$temporary/src/go/kame/"
cat >"$temporary/bin/so" <<'SH'
#!/bin/sh
exit 0
SH
cat >"$temporary/bin/go" <<'SH'
#!/bin/sh
set -eu
case "$1" in
env) echo go1.27.3 ;;
version)
    printf '{"GoVersion":"%s","Main":{"Version":"%s"}}\n' "$MOCK_SO_GO" "$MOCK_SO_VERSION"
    ;;
list) echo v0.4.0 ;;
install)
    test "$2" = solod.dev/cmd/so@v0.4.0
    test "$GOBIN" = "$MOCK_BIN"
    test "$GOTOOLCHAIN" = local
    echo install >> "$MOCK_INSTALL_LOG"
    exit "${MOCK_INSTALL_STATUS:-0}"
    ;;
*) exit 99 ;;
esac
SH
chmod +x "$temporary/bin/so" "$temporary/bin/go"
export MOCK_BIN="$temporary/bin" MOCK_INSTALL_LOG="$temporary/install.log"
export MOCK_SO_GO=go1.27.3 MOCK_SO_VERSION=v0.4.0
cd "$temporary"
run_check() {
	"$driver" --tool "go=$MOCK_BIN/go" --tool "so=$MOCK_BIN/so" toolchain
}

# Matching tools do not trigger a download, even on repeated invocations.
run_check
run_check
test ! -e "$MOCK_INSTALL_LOG"

# Both a stale Go compiler and a stale Solod version rebuild the pinned version.
export MOCK_SO_GO=go1.26.8
run_check
test "$(wc -l <"$MOCK_INSTALL_LOG")" -eq 1
export MOCK_SO_GO=go1.27.3 MOCK_SO_VERSION=v0.3.0
run_check
test "$(wc -l <"$MOCK_INSTALL_LOG")" -eq 2

# A failed rebuild must stop dependent work, not let the old compiler run.
export MOCK_INSTALL_STATUS=1
cat >>Makefile.kmk <<'KMK'

consumer : | toolchain
	touch ./consumer-ran
KMK
if "$driver" --tool "go=$MOCK_BIN/go" --tool "so=$MOCK_BIN/so" consumer >"$temporary/failure.log" 2>&1; then
	echo 'toolchain: failed install unexpectedly succeeded' >&2
	exit 1
fi
grep -q 'RECIPE_FAIL' "$temporary/failure.log"
test ! -e consumer-ran
echo 'toolchain checks passed'
