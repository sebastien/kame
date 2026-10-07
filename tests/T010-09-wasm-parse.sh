#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper
# Spec: docs/spec/004-language.md — parse
# Spec: docs/spec/013-tests.md — T010-09-wasm-parse
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-09 wasm parse"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

fixtures="$CLI_ROOT/src/go/kame/cmd/kame/testdata/parse"
work="$TMPDIR/wasm-parse"
mkdir -p "$work"

test-step "parse output matches native byte-for-byte"
for pair in "expr expr" "template template" "rule rule" "script script"; do
	# shellcheck disable=SC2086
	set -- $pair
	lang="$1"
	file="$2"
	set +e
	node "$CLI_ROOT/dist/kame.js" do parse --json --lang "$lang" "$fixtures/$file.km" >"$work/wasm.json" 2>"$work/wasm.err"
	wasm_status=$?
	"$CLI_BIN" do parse --json --lang "$lang" "$fixtures/$file.km" >"$work/native.json" 2>"$work/native.err"
	native_status=$?
	set -e
	if [ "$wasm_status" = "$native_status" ] && cmp -s "$work/wasm.json" "$work/native.json"; then
		test-ok "parity: $lang"
	else
		test-fail "parity: $lang (wasm=$wasm_status native=$native_status)"
	fi
done

test-step "parse reports errors in the document and exits 1"
set +e
node "$CLI_ROOT/dist/kame.js" do parse --json --lang expr "$fixtures/invalid-expr.km" >"$work/wasm.json" 2>"$work/wasm.err"
wasm_status=$?
"$CLI_BIN" do parse --json --lang expr "$fixtures/invalid-expr.km" >"$work/native.json" 2>"$work/native.err"
native_status=$?
set -e
if [ "$wasm_status" = 1 ] && [ "$native_status" = 1 ] && cmp -s "$work/wasm.json" "$work/native.json"; then
	test-ok "invalid expression matches native and exits 1"
else
	test-fail "invalid expression: wasm=$wasm_status native=$native_status"
fi

test-step "parse reads stdin and validates options"
node "$CLI_ROOT/dist/kame.js" do parse --json --lang script <"$fixtures/script.km" >"$work/wasm.json"
"$CLI_BIN" do parse --json --lang script <"$fixtures/script.km" >"$work/native.json"
if cmp -s "$work/wasm.json" "$work/native.json"; then
	test-ok "stdin parse matches native"
else
	test-fail "stdin parse differs"
fi
set +e
node "$CLI_ROOT/dist/kame.js" do parse >"$work/out" 2>"$work/err"
missing_status=$?
node "$CLI_ROOT/dist/kame.js" do parse --lang nope >"$work/out" 2>"$work/err"
invalid_status=$?
set -e
if [ "$missing_status" = 2 ] && [ "$invalid_status" = 2 ]; then
	test-ok "missing and invalid --lang exit 2"
else
	test-fail "option statuses: missing=$missing_status invalid=$invalid_status"
fi

test-end
