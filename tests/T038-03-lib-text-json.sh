#!/usr/bin/env bash
# Spec: docs/spec/038-direct-kash-and-values.md — explicit text and JSON boundaries
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T038-03 scalar text and JSON"
test-step "build artifacts"
cd "$CLI_ROOT"
cli_build
make dist-wasm >/dev/null
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local want="$1" expression="$2"
		if "${runner[@]}" do run --lang expr -c "$expression" >"$TMPDIR/out" 2>"$TMPDIR/err" && [ "$(cat "$TMPDIR/out")" = "$want" ]; then test-ok "$backend $expression"; else test-fail "$backend $expression"; cat "$TMPDIR/out" "$TMPDIR/err"; fi
	}
	reject() {
		local expression="$1" code="$2" status=0
		"${runner[@]}" do run --lang expr -c "$expression" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$TMPDIR/err" && [ ! -s "$TMPDIR/out" ]; then test-ok "$backend rejects $expression"; else test-fail "$backend $expression ($status)"; cat "$TMPDIR/out" "$TMPDIR/err"; fi
	}
	test-step "$backend scalar concatenation and explicit JSON round trips"
	check '""' '(str)'
	check '"x=42/1.5/true/false/nil/./file"' '(str "x=" 42 "/" 1.5 "/" :true "/" :false "/" :nil "/" ./file)'
	check '"\"text\""' '(json "text")'
	check '"null"' '(json :nil)'
	check '"{\"a\":[true,null],\"z\":2}"' '(json [z: 2 a: [:true :nil]])'
	check ':true' '(eq (parse-json (json [z: 2 a: [:true :nil]])) [a: [:true :nil] z: 2])'
	check ':true' '(eq (parse-json (json "a\n\t\\\"")) "a\n\t\\\"")'
	for operation in str json; do
		for value in '([x] x)' './path/{*}' '(resource "mem://x/file")' '[bad: ([x] x)]'; do reject "($operation $value)" EXPR_INVALID; done
	done
	reject '(str [])' EXPR_INVALID
	reject '(str [a: 1])' EXPR_INVALID
	reject '(json)' EXPR_INVALID
	reject '(json 1 2)' EXPR_INVALID
done
test-end
