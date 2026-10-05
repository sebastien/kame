#!/usr/bin/env bash
# Spec: docs/spec/025-generated-declarations.md — native/WASM generated rule parity
# Spec: docs/spec/013-tests.md — T010-27-generated-declarations
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-27 generated declarations"
cd "$CLI_ROOT"

test-step "build native and WASM CLIs"
make dist-wasm >/dev/null
cli_build

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cli="$CLI_ROOT/dist/kame.js"
cat >"$work/Makefile.kmk" <<'EOF'
MODULES = ["core" "cli"]
generate module-checks = (map ([module] [kind: "task" target: (join (list "check-" module) "") inputs: [] order-only: [] recipe: (list (join (list "touch generated-" module) ""))]) MODULES)
EOF

test-step "plan output matches across hosts"
set +e
(cd "$work" && node "$cli" do plan --json check-core) >"$work/wasm.json" 2>"$work/wasm.err"
wasm_status=$?
(cd "$work" && "$CLI_BIN" do plan --json check-core) >"$work/native.json" 2>"$work/native.err"
native_status=$?
set -e
if [ "$wasm_status" = 0 ] && [ "$native_status" = 0 ] && cmp -s "$work/wasm.json" "$work/native.json" && grep -q 'module-checks' "$work/wasm.json"; then
	test-ok "generated plans and provenance match"
else
	test-fail "generated plan parity: wasm=$wasm_status native=$native_status"
fi

test-step "generated rules materialize on both hosts"
if (cd "$work" && node "$cli" check-core) && [ -f "$work/generated-core" ]; then
	test-ok "WASM generated task executes"
else
	test-fail "WASM generated task failed"
fi
rm -f "$work/generated-core"
if (cd "$work" && "$CLI_BIN" check-cli) && [ -f "$work/generated-cli" ]; then
	test-ok "native generated task executes"
else
	test-fail "native generated task failed"
fi

test-end
