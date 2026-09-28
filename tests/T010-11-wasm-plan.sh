#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper
# Spec: docs/spec/006-runtime.md — planning
# Spec: docs/spec/013-tests.md — T010-11-wasm-plan
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-11 wasm plan"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

fixture="$CLI_ROOT/tests/data/cli/plan"
work="$TMPDIR/wasm-plan"
mkdir -p "$work"

test-step "plan JSON matches native"
for target in default ./artifact.txt ./capture-x.o ./dynamic.out; do
	set +e
	(cd "$fixture" && node "$CLI_ROOT/dist/kame.js" do plan "$target") >"$work/wasm.json" 2>"$work/wasm.err"
	wasm_status=$?
	(cd "$fixture" && "$CLI_BIN" do plan "$target") >"$work/native.json" 2>"$work/native.err"
	native_status=$?
	set -e
	if [ "$wasm_status" = "$native_status" ] && cmp -s "$work/wasm.json" "$work/native.json"; then
		test-ok "parity: $target"
	else
		test-fail "parity: $target (wasm=$wasm_status native=$native_status)"
	fi
done

test-step "a missing target fails with status 1"
set +e
(cd "$fixture" && node "$CLI_ROOT/dist/kame.js" do plan ./no-such-target) >"$work/wasm.out" 2>"$work/wasm.err"
wasm_status=$?
(cd "$fixture" && "$CLI_BIN" do plan ./no-such-target) >"$work/native.out" 2>"$work/native.err"
native_status=$?
set -e
if [ "$wasm_status" = 1 ] && [ "$native_status" = 1 ] && grep -q 'TGT_NO_RULE' "$work/wasm.err"; then
	test-ok "missing target reports TGT_NO_RULE with status 1"
else
	test-fail "missing target: wasm=$wasm_status native=$native_status"
fi

test-end
