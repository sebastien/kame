#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper, --json event streams
# Spec: docs/spec/009-cli.md — JSON output
# Spec: docs/spec/013-tests.md — T010-14-wasm-json
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-14 wasm JSON event stream"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

project="$TMPDIR/wasm-json"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'EOF'
./out.txt :
	printf out > out.txt
	echo to-stdout
	echo to-stderr 1>&2
EOF

normalize() {
	jq -c 'del(.node,.request,.generation,.attempt)' | sort
}

test-step "a successful primary build streams native JSON Lines"
(cd "$project" && rm -f out.txt && node "$CLI_ROOT/dist/kame.js" --json ./out.txt) >"$project/wasm.jsonl" 2>"$project/wasm.err"
(cd "$project" && rm -f out.txt && "$CLI_BIN" --json ./out.txt) >"$project/native.jsonl" 2>"$project/native.err"
if [ ! -s "$project/wasm.err" ] && normalize <"$project/wasm.jsonl" >"$project/wasm.norm" && normalize <"$project/native.jsonl" >"$project/native.norm" && cmp -s "$project/wasm.norm" "$project/native.norm"; then
	test-ok "wasm --json matches native and leaves stderr unused"
else
	test-fail "wasm --json: err=$(cat "$project/wasm.err")"
fi
if jq -e -s 'all(.[]; .schema == 1 and (.type | type == "string"))' "$project/wasm.jsonl" >/dev/null; then
	test-ok "every line is a schema-1 JSON object"
else
	test-fail "stdout was not valid JSON Lines"
fi

test-step "a failing build stays valid JSON Lines with status 1"
cat >"$project/Makefile.kmk" <<'EOF'
./fail.txt :
	echo fail-out
	echo fail-err 1>&2
	false
EOF
set +e
(cd "$project" && node "$CLI_ROOT/dist/kame.js" --json ./fail.txt) >"$project/fail.jsonl" 2>"$project/fail.err"
status=$?
set -e
if [ "$status" = 1 ] && [ ! -s "$project/fail.err" ] && jq -e -s 'map(select(.type == "target-failed")) | length == 1' "$project/fail.jsonl" >/dev/null; then
	test-ok "failure emits a target-failed event with status 1 and no stderr"
else
	test-fail "failure: status=$status err=$(cat "$project/fail.err")"
fi

test-step "a failing build diagnostic matches native"
set +e
(cd "$project" && "$CLI_BIN" --json ./fail.txt) >"$project/fail.native.jsonl" 2>/dev/null
set -e
if normalize <"$project/fail.jsonl" >"$project/fail.wasm.norm" && normalize <"$project/fail.native.jsonl" >"$project/fail.native.norm" && cmp -s "$project/fail.wasm.norm" "$project/fail.native.norm"; then
	test-ok "failure diagnostics carry source, span, target stack, and cause like native"
else
	test-fail "failure diagnostics differ: $(diff "$project/fail.wasm.norm" "$project/fail.native.norm" | head -4)"
fi

test-end
