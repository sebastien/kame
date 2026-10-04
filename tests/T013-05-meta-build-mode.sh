#!/usr/bin/env bash
# Spec: docs/spec/013-tests.md — per-artifact version metadata and incrementality
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T013-05 per-artifact build mode"
test-step "build current native artifacts"
cli_require_tools
cli_build
CLI_BIN="$CLI_ROOT/build/kame.debug" cli_build
if [ ! -x "$CLI_ROOT/dist/kame" ] || cli_sources_newer "$CLI_ROOT/dist/kame"; then
 make -C "$CLI_ROOT" dist/kame > "$TMPDIR/release-build.log" 2>&1
fi

test-step "debug and release artifacts report independent modes"
"$CLI_ROOT/build/kame.debug" --version > "$TMPDIR/debug-version"
"$CLI_ROOT/dist/kame" --version > "$TMPDIR/release-version"
if rg -q '; debug\)$' "$TMPDIR/debug-version" && rg -q '; release\)$' "$TMPDIR/release-version"; then test-ok "debug and release modes belong to their artifacts"; else test-fail "native artifact build modes"; fi

test-step "the instrumented CLI reports sanitize when selected"
if [ "$CLI_BIN" = "$CLI_ROOT/build/kame.sanitize" ]; then
 "$CLI_BIN" --version > "$TMPDIR/sanitize-version"
 if rg -q '; sanitize\)$' "$TMPDIR/sanitize-version"; then test-ok "sanitizer artifact reports sanitize"; else test-fail "sanitizer artifact mode"; fi
fi

test-step "switching mode does not rewrite shared generated metadata"
metadata="$CLI_ROOT/src/go/kame/cmd/kame/version_generated.go"
before="$(stat -c %y "$metadata")"
cp "$metadata" "$TMPDIR/version-before"
KAME_BUILD_MODE=debug "$CLI_ROOT/tools/generate-version.sh"
KAME_BUILD_MODE=release "$CLI_ROOT/tools/generate-version.sh"
if cmp -s "$metadata" "$TMPDIR/version-before" && [ "$(stat -c %y "$metadata")" = "$before" ] && ! rg -q 'const buildMode' "$metadata"; then test-ok "shared version source is independent of artifact mode"; else test-fail "mode switch rewrote generated source"; fi

test-step "establish successful artifact execution contexts"
# GNU Make does not publish Kame's scoped file-context records. Materialize once
# before testing reuse so an old/missing context record is not called unchanged.
"$CLI_ROOT/build/kame.debug" --json -C "$CLI_ROOT" ./build/kame.debug ./dist/kame > "$TMPDIR/context-events" 2> "$TMPDIR/context-err"

test-step "Kame skips unchanged debug and release file outputs"
debug_before="$(stat -c %y "$CLI_ROOT/build/kame.debug")"
release_before="$(stat -c %y "$CLI_ROOT/dist/kame")"
"$CLI_ROOT/build/kame.debug" --json -C "$CLI_ROOT" ./build/kame.debug ./dist/kame > "$TMPDIR/build-events" 2> "$TMPDIR/build-err"
if [ "$(stat -c %y "$CLI_ROOT/build/kame.debug")" = "$debug_before" ] && [ "$(stat -c %y "$CLI_ROOT/dist/kame")" = "$release_before" ] && ! rg -q '"type":"process-started"' "$TMPDIR/build-events"; then test-ok "unchanged artifact modes do not relink"; else test-fail "unchanged Kame native outputs relinked"; fi
test-end
