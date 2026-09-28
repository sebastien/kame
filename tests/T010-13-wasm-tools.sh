#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper
# Spec: docs/spec/009-cli.md — tools
# Spec: docs/spec/013-tests.md — T010-13-wasm-tools
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-13 wasm tools"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

project="$TMPDIR/wasm-tools"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'EOF'
default : ./out.txt

./out.txt :
	@(x/sh) -c 'echo hi' > @>
	@(x/definitely-not-a-real-tool-xyz) foo
EOF

test-step "tools JSON matches native including unresolved names"
set +e
(cd "$project" && node "$CLI_ROOT/dist/kame.js" do tools) >"$project/wasm.json" 2>"$project/wasm.err"
wasm_status=$?
(cd "$project" && "$CLI_BIN" do tools) >"$project/native.json" 2>"$project/native.err"
native_status=$?
set -e
if [ "$wasm_status" = "$native_status" ] && cmp -s "$project/wasm.json" "$project/native.json"; then
	test-ok "tools JSON matches native"
else
	test-fail "tools JSON differs (wasm=$(cat "$project/wasm.json") native=$(cat "$project/native.json"))"
fi
if grep -q '"path":""' "$project/wasm.json"; then
	test-ok "unresolved tool keeps an empty path"
else
	test-fail "unresolved tool path missing"
fi

test-step "tools rejects targets"
set +e
(cd "$project" && node "$CLI_ROOT/dist/kame.js" do tools ./out.txt) >"$project/out" 2>"$project/err"
status=$?
set -e
if [ "$status" = 2 ] && grep -q 'OPT_VALUE_INVALID' "$project/err"; then
	test-ok "tools rejects targets with status 2"
else
	test-fail "tools target rejection: status=$status"
fi

test-end
