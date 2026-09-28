#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper
# Spec: docs/spec/004-language.md — format
# Spec: docs/spec/013-tests.md — T010-10-wasm-fmt
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-10 wasm fmt"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

fixtures="$CLI_ROOT/tests/data/cli/fmt"
work="$TMPDIR/wasm-fmt"
mkdir -p "$work"

test-step "formatting matches native and the canonical fixture"
node "$CLI_ROOT/dist/kame.js" do fmt "$fixtures/unformatted.kmk" >"$work/wasm.out"
"$CLI_BIN" do fmt "$fixtures/unformatted.kmk" >"$work/native.out"
if cmp -s "$work/wasm.out" "$work/native.out" && cmp -s "$work/wasm.out" "$fixtures/canonical.kmk"; then
	test-ok "default format matches native and canonical"
else
	test-fail "default format differs"
fi
node "$CLI_ROOT/dist/kame.js" do fmt --indent spaces --indent-width 2 "$fixtures/unformatted.kmk" >"$work/wasm.out"
"$CLI_BIN" do fmt --indent spaces --indent-width 2 "$fixtures/unformatted.kmk" >"$work/native.out"
if cmp -s "$work/wasm.out" "$work/native.out"; then
	test-ok "spaces and width match native"
else
	test-fail "spaces format differs"
fi

test-step "check mode reports changed files and exits 1"
set +e
node "$CLI_ROOT/dist/kame.js" do fmt -n "$fixtures/unformatted.kmk" >"$work/wasm.out" 2>"$work/wasm.err"
wasm_status=$?
"$CLI_BIN" do fmt -n "$fixtures/unformatted.kmk" >"$work/native.out" 2>"$work/native.err"
native_status=$?
set -e
if [ "$wasm_status" = 1 ] && [ "$native_status" = 1 ] && cmp -s "$work/wasm.out" "$work/native.out"; then
	test-ok "check mode matches native and exits 1"
else
	test-fail "check mode: wasm=$wasm_status native=$native_status"
fi

test-step "stdin and in-place formatting match native"
node "$CLI_ROOT/dist/kame.js" do fmt <"$fixtures/unformatted.kmk" >"$work/wasm.out"
"$CLI_BIN" do fmt <"$fixtures/unformatted.kmk" >"$work/native.out"
if cmp -s "$work/wasm.out" "$work/native.out"; then
	test-ok "stdin format matches native"
else
	test-fail "stdin format differs"
fi
cp "$fixtures/unformatted.kmk" "$work/inplace.kmk"
node "$CLI_ROOT/dist/kame.js" do fmt -i "$work/inplace.kmk"
if cmp -s "$work/inplace.kmk" "$fixtures/canonical.kmk"; then
	test-ok "in-place format writes canonical bytes"
else
	test-fail "in-place format differs from canonical"
fi

test-step "usage errors use status 2"
set +e
node "$CLI_ROOT/dist/kame.js" do fmt -i -n "$fixtures/unformatted.kmk" >"$work/out" 2>"$work/err"
conflict_status=$?
node "$CLI_ROOT/dist/kame.js" do fmt --lang nope "$fixtures/unformatted.kmk" >"$work/out" 2>"$work/err"
lang_status=$?
set -e
if [ "$conflict_status" = 2 ] && [ "$lang_status" = 2 ]; then
	test-ok "conflicting modes and invalid language exit 2"
else
	test-fail "usage statuses: conflict=$conflict_status lang=$lang_status"
fi

test-end
