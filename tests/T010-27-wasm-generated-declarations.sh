#!/usr/bin/env bash
# Spec: docs/spec/025-generated-declarations.md — native/WASM generated rule parity
# Spec: docs/spec/013-tests.md — T010-27-generated-declarations
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-27 wasm generated declarations"
cd "$CLI_ROOT"

test-step "build native and WASM CLIs"
if [ -n "${KAME_WASM:-}" ]; then
	wasm_cli="$CLI_ROOT/src/js/kame.js"
else
	make dist-wasm >/dev/null
	wasm_cli="$CLI_ROOT/dist/kame.js"
fi
cli_build

work="$TMPDIR/generated"
mkdir -p "$work"
cli="$wasm_cli"
cat >"$work/Makefile.km" <<'EOF'
MODULES = ["core", "cli"]
def module-check module =
	task check-${module}
		touch ./generated-$module
def module-checks () = map module-check MODULES
generate module-checks ()

def output-path name = ./${name}
def file-rule name =
	rule (output-path name) = "generated"
generate (file-rule "generated-artifact")
rule (output-path "authored-artifact") : ./generated-artifact = "authored"
EOF

test-step "plan output matches across hosts"
set +e
(cd "$work" && node "$cli" do plan --json check-core) >"$work/wasm.json" 2>"$work/wasm.err"
wasm_status=$?
(cd "$work" && "$CLI_BIN" do plan --json check-core) >"$work/native.json" 2>"$work/native.err"
native_status=$?
set -e
if [ "$wasm_status" = 0 ] && [ "$native_status" = 0 ] && cmp -s "$work/wasm.json" "$work/native.json" &&
	jq -e 'any(.producers[]; .target == "check-core" and .generator.name == "generate" and .generator.dependencies == ["MODULES"])' "$work/wasm.json" >/dev/null; then
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

test-step "parenthesized file outputs format and build on both hosts"
for backend in native wasm; do
	command=("$CLI_BIN")
	if [ "$backend" = wasm ]; then command=(node "$cli"); fi
	(
		cd "$work"
		"${command[@]}" do fmt --lang km Makefile.km >"$backend.formatted"
		"${command[@]}" do parse --lang km --check "$backend.formatted"
		"${command[@]}" ./authored-artifact
	)
	if grep -Fq 'rule (output-path name)' "$work/$backend.formatted" &&
		[ "$(cat "$work/generated-artifact")" = generated ] &&
		[ "$(cat "$work/authored-artifact")" = authored ]; then
		test-ok "$backend preserves and evaluates both parenthesized outputs"
	else
		test-fail "$backend parenthesized file output"
	fi
	rm -f "$work/generated-artifact" "$work/authored-artifact"
done

test-end
