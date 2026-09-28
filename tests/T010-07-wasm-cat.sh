#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper
# Spec: docs/spec/009-cli.md — Cat and primary invocation
# Spec: docs/spec/013-tests.md — T010-07-wasm-cat
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-07 wasm cat and primary materialization"
cd "$CLI_ROOT"

make dist-wasm >/dev/null

project="$TMPDIR/wasm-cat"
mkdir -p "$project/out"
cat >"$project/Makefile.kmk" <<'EOF'
GREETING = hello
SHELLVAL = (shell "printf hi")
./out/greeting.txt :
	printf hello > out/greeting.txt
run :
	true
EOF
printf 'plain bytes' >"$project/plain.txt"

wasm() {
	set +e
	(cd "$project" && node "$CLI_ROOT/dist/kame.js" "$@") >"$project/out.txt" 2>"$project/err.txt"
	status=$?
	set -e
}

test-step "do cat prints definition values and file artifacts"
wasm do cat GREETING
if [ "$status" = 0 ] && [ "$(cat "$project/out.txt")" = "hello" ] && [ ! -s "$project/err.txt" ]; then
	test-ok "cat definition value"
else
	test-fail "cat GREETING: status=$status out=$(cat "$project/out.txt")"
fi
rm -f "$project/out/greeting.txt"
wasm do cat ./out/greeting.txt
if [ "$status" = 0 ] && [ "$(cat "$project/out.txt")" = "hello" ] && [ "$(cat "$project/out/greeting.txt")" = "hello" ]; then
	test-ok "cat materializes and prints exact file bytes"
else
	test-fail "cat file: status=$status out=$(cat "$project/out.txt")"
fi
wasm do cat ./plain.txt
if [ "$status" = 0 ] && [ "$(cat "$project/out.txt")" = "plain bytes" ]; then
	test-ok "cat prints an existing file with no rule"
else
	test-fail "cat plain file: status=$status out=$(cat "$project/out.txt")"
fi
wasm do cat run
if [ "$status" = 1 ] && grep -q 'NO_ARTIFACT' "$project/err.txt"; then
	test-ok "cat reports NO_ARTIFACT for a task"
else
	test-fail "cat task: status=$status err=$(cat "$project/err.txt")"
fi
wasm do cat A B
if [ "$status" = 2 ] && grep -q 'OPT_VALUE_INVALID' "$project/err.txt"; then
	test-ok "cat rejects more than one target with status 2"
else
	test-fail "cat arity: status=$status"
fi

test-step "cat prints a collected shell definition as a status record"
(cd "$project" && "$CLI_BIN" do cat SHELLVAL) >"$project/native.shell" 2>/dev/null
wasm do cat SHELLVAL
if [ "$status" = 0 ] && cmp -s "$project/out.txt" "$project/native.shell"; then
	test-ok "cat shell definition matches native status record"
else
	test-fail "cat shell: wasm=$(cat "$project/out.txt") native=$(cat "$project/native.shell")"
fi

test-step "primary invocation prints definition values and builds files quietly"
wasm GREETING
if [ "$status" = 0 ] && [ "$(cat "$project/out.txt")" = "hello" ]; then
	test-ok "primary invocation prints a definition value"
else
	test-fail "primary GREETING: status=$status out=$(cat "$project/out.txt")"
fi
rm -f "$project/out/greeting.txt"
wasm ./out/greeting.txt
if [ "$status" = 0 ] && [ ! -s "$project/out.txt" ] && [ "$(cat "$project/out/greeting.txt")" = "hello" ]; then
	test-ok "primary invocation builds a file without printing it"
else
	test-fail "primary file: status=$status out=$(cat "$project/out.txt")"
fi

test-step "source discovery honors -C and -c"
wasm do cat -C "$project" GREETING
if [ "$status" = 0 ] && [ "$(cat "$project/out.txt")" = "hello" ]; then
	test-ok "cat resolves the source and target under -C"
else
	test-fail "cat -C: status=$status out=$(cat "$project/out.txt")"
fi
wasm do cat -c 'value = 7' value
if [ "$status" = 0 ] && [ "$(cat "$project/out.txt")" = "7" ]; then
	test-ok "cat uses an inline source"
else
	test-fail "cat -c: status=$status out=$(cat "$project/out.txt")"
fi

test-end
