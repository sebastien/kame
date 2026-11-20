#!/usr/bin/env bash
# Spec: docs/spec/029-pattern-extensions.md — regex groups in rule targets
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T004-15 rule regex captures"
test-step "build native and WASM runners"
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}

work="$TMPDIR/rule-regex-captures"
project="$work/project"
mkdir -p "$project/src"
cat >"$project/Makefile.km" <<'KAME'
rule ./src/{name:~[a-z]+}.c : ./src/{name}.h
	cat $inputs.0 > $outputs.0
rule ./{name:~[a-z]+}.name :
	printf %s $_0 > $outputs.0
KAME
printf 'regex-rule-capture-ok\n' >"$project/src/demo.h"

for host in native wasm; do
	if [ "$host" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	"${runner[@]}" do fmt --lang km "$project/Makefile.km" >"$work/$host.formatted"
	"${runner[@]}" do fmt --lang km "$work/$host.formatted" >"$work/$host.formatted.again"
	if cmp -s "$work/$host.formatted" "$work/$host.formatted.again" && grep -q 'name:~\[a-z\]+' "$work/$host.formatted"; then
		test-ok "$host formats regex rule targets idempotently"
	else
		test-fail "$host regex target formatting"
	fi
	rm -f "$project/src/demo.c"
	if (cd "$project" && "${runner[@]}" ./src/demo.c) >"$work/$host.out" 2>"$work/$host.err" && [ "$(cat "$project/src/demo.c")" = "regex-rule-capture-ok" ]; then
		test-ok "$host selects regex output templates and renders named input captures"
	else
		test-fail "$host rule regex capture: $(cat "$work/$host.err")"
	fi
	if (cd "$project" && "${runner[@]}" ./demo.name) >"$work/$host.scope.out" 2>"$work/$host.scope.err" && [ "$(cat "$project/demo.name")" = "demo" ]; then
		test-ok "$host exposes regex output captures to positional recipe bindings"
	else
		test-fail "$host regex capture scope: $(cat "$work/$host.scope.err")"
	fi
done

test-end
