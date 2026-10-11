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
for target in default ./artifact.txt ./capture-x.o ./dynamic.out ./input.txt input.txt ./nope.txt nope.txt; do
	set +e
	(cd "$fixture" && node "$CLI_ROOT/dist/kame.js" do plan --json "$target") >"$work/wasm.json" 2>"$work/wasm.err"
	wasm_status=$?
	(cd "$fixture" && "$CLI_BIN" do plan --json "$target") >"$work/native.json" 2>"$work/native.err"
	native_status=$?
	set -e
	if [ "$wasm_status" = "$native_status" ] && cmp -s "$work/wasm.json" "$work/native.json"; then
		test-ok "parity: $target"
	else
		test-fail "parity: $target (wasm=$wasm_status native=$native_status)"
	fi
	if [[ "$target" == *input.txt || "$target" == *nope.txt ]]; then
		if [ "$native_status" = 0 ] && [ ! -s "$work/native.err" ] && [ ! -s "$work/wasm.err" ] && jq -e --arg name "$fixture/${target#./}" '
			.type == "plan" and (.resources | length == 1) and
			.resources[0].key == {kind: "file", name: $name} and
			.resources[0].root and .resources[0].roles == ["input"] and
			([.producers, .dependencies, .stages] | map(length)) == [0,0,0]
		' "$work/native.json" >/dev/null; then
			test-ok "external file root: $target"
		else
			test-fail "external file root: $target"
		fi
	fi
done

test-step "an unknown logical target fails with status 1"
set +e
(cd "$fixture" && node "$CLI_ROOT/dist/kame.js" do plan no-such-target) >"$work/wasm.out" 2>"$work/wasm.err"
wasm_status=$?
(cd "$fixture" && "$CLI_BIN" do plan no-such-target) >"$work/native.out" 2>"$work/native.err"
native_status=$?
set -e
if [ "$wasm_status" = 1 ] && [ "$native_status" = 1 ] && grep -q 'TGT_NO_RULE' "$work/wasm.err"; then
	test-ok "missing target reports TGT_NO_RULE with status 1"
else
	test-fail "missing target: wasm=$wasm_status native=$native_status"
fi

test-end
