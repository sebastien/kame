#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper
# Spec: docs/spec/006-runtime.md — graph inspection
# Spec: docs/spec/013-tests.md — T010-12-wasm-graph
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-12 wasm inputs, outputs, and span"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

fixture="$CLI_ROOT/tests/data/cli/plan"
work="$TMPDIR/wasm-graph"
mkdir -p "$work"

compare() {
	local kind="$1"
	shift
	set +e
	(cd "$fixture" && node "$CLI_ROOT/dist/kame.js" do "$kind" --json "$@") >"$work/wasm.out" 2>"$work/wasm.err"
	local wasm_status=$?
	(cd "$fixture" && "$CLI_BIN" do "$kind" --json "$@") >"$work/native.out" 2>"$work/native.err"
	local native_status=$?
	set -e
	if [ "$wasm_status" = "$native_status" ] && cmp -s "$work/wasm.out" "$work/native.out"; then
		test-ok "$kind $* (exit $wasm_status)"
	else
		test-fail "$kind $*: wasm=$wasm_status native=$native_status"
	fi
}

test-step "graph inspection matches native"
compare inputs ./artifact.txt
compare outputs ./artifact.txt
compare span ./artifact.txt
compare span --expand ./artifact.txt
compare inputs --depth -1 default
compare outputs --depth -1 default
compare span --expand --depth -1 default

test-step "graph option and target errors"
compare inputs --expand ./artifact.txt
set +e
(cd "$fixture" && node "$CLI_ROOT/dist/kame.js" do inputs ./no-such-target) >"$work/out" 2>"$work/err"
status=$?
set -e
if [ "$status" = 1 ] && grep -q 'TGT_NO_RULE' "$work/err"; then
	test-ok "missing graph target reports TGT_NO_RULE with status 1"
else
	test-fail "missing graph target: status=$status"
fi

test-end
