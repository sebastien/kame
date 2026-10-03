#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper
# Spec: docs/spec/013-tests.md — T010-06-wasm-cli
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-06 wasm JavaScript CLI"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

run() {
	set +e
	node "$CLI_ROOT/dist/kame.js" "$@" >"$work/out" 2>"$work/err"
	status=$?
	set -e
}

test-step "version and help do not require the module"
run --version
if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "kame $(cat "$CLI_ROOT/VERSION")" ] && [ ! -s "$work/err" ]; then
	test-ok "--version prints kame VERSION on stdout"
else
	test-fail "--version: status=$status out=$(cat "$work/out") err=$(cat "$work/err")"
fi
run --help
if [ "$status" = 0 ] && [ -s "$work/out" ]; then
	test-ok "--help prints usage and exits 0"
else
	test-fail "--help: status=$status"
fi

test-step "do run --lang expr matches native output byte-for-byte"
for expression in '(count [1 2 3])' '(join ["a" "b"] ":")' '(uppercase "kame")' '(let [a 10] (out "hello" a))' '(yield "generated")'; do
	node "$CLI_ROOT/dist/kame.js" do run --lang expr -c "$expression" >"$work/wasm.out"
	"$CLI_BIN" do run --lang expr -c "$expression" >"$work/native.out"
	if cmp -s "$work/wasm.out" "$work/native.out"; then
		test-ok "parity: $expression"
	else
		test-fail "parity: $expression (wasm=$(cat "$work/wasm.out") native=$(cat "$work/native.out"))"
	fi
done
run do run --lang expr -c '(err "notice")'
wasm_status=$status
cp "$work/out" "$work/wasm.err-effect.out"
cp "$work/err" "$work/wasm.err-effect.err"
"$CLI_BIN" do run --lang expr -c '(err "notice")' >"$work/native.err-effect.out" 2>"$work/native.err-effect.err"
if [ "$wasm_status" = 0 ] && cmp -s "$work/wasm.err-effect.out" "$work/native.err-effect.out" && cmp -s "$work/wasm.err-effect.err" "$work/native.err-effect.err"; then
	test-ok "parity: err preserves stdout and stderr"
else
	test-fail "err parity: wasm-out=$(cat "$work/wasm.err-effect.out") wasm-err=$(cat "$work/wasm.err-effect.err") native-out=$(cat "$work/native.err-effect.out") native-err=$(cat "$work/native.err-effect.err")"
fi
printf '(join ["wasm" "source"] "-")\n' >"$work/expression.kmk"
node "$CLI_ROOT/dist/kame.js" do run --lang expr "$work/expression.kmk" >"$work/wasm.expr"
"$CLI_BIN" do run --lang expr "$work/expression.kmk" >"$work/native.expr"
if cmp -s "$work/wasm.expr" "$work/native.expr" && [ "$(cat "$work/wasm.expr")" = '"wasm-source"' ]; then
	test-ok "do run --lang expr FILE reads an expression file and matches native"
else
	test-fail "do run --lang expr FILE: wasm=$(cat "$work/wasm.expr") native=$(cat "$work/native.expr")"
fi

test-step "capability grants are denied by default"
run do run --lang expr -c '(read "VERSION")'
if [ "$status" = 1 ] && [ ! -s "$work/out" ] && grep -q 'CAP_DENIED' "$work/err"; then
	test-ok "read without --allow-read is denied"
else
	test-fail "read was not denied: status=$status out=$(cat "$work/out") err=$(cat "$work/err")"
fi
run do run --lang expr --allow-read -c '(read "VERSION")'
if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$(cat "$CLI_ROOT/VERSION")" ]; then
	test-ok "read with --allow-read succeeds"
else
	test-fail "read with grant failed: status=$status out=$(cat "$work/out")"
fi
run do run --lang expr --allow-run -c '(shell "printf run-ok")'
if [ "$status" = 0 ] && [ "$(cat "$work/out")" = '[status: 0 stdout: run-ok stderr: ]' ]; then
	test-ok "shell with --allow-run succeeds"
else
	test-fail "shell with grant failed: status=$status out=$(cat "$work/out")"
fi

test-step "diagnostic flags report the current stage"
run --wasm-abi-info
if [ "$status" = 0 ] && node -e 'const i=JSON.parse(require("node:fs").readFileSync(process.argv[1],"utf8")); if(i.schema!==1||i.stage!==2||!i.exports.includes("kame_wasm_step"))process.exit(1)' "$work/out"; then
	test-ok "--wasm-abi-info reports schema, stage, and exports"
else
	test-fail "--wasm-abi-info: $(cat "$work/out")"
fi
run --wasm-self-test
if [ "$status" = 0 ] && node -e 'const i=JSON.parse(require("node:fs").readFileSync(process.argv[1],"utf8")); if(!i.selfTest||i.selfTest.pure!=="\"a:b\"")process.exit(1)' "$work/out"; then
	test-ok "--wasm-self-test evaluates a pure expression"
else
	test-fail "--wasm-self-test: $(cat "$work/out")"
fi

test-step "unsupported work and usage errors use the right status"
run do nope
if [ "$status" = 2 ] && grep -q 'CMD_UNKNOWN' "$work/err"; then
	test-ok "unknown command exits 2"
else
	test-fail "unknown command: status=$status"
fi
run --nope
if [ "$status" = 2 ] && grep -q 'OPT_UNKNOWN' "$work/err"; then
	test-ok "unknown option exits 2"
else
	test-fail "unknown option: status=$status"
fi

test-end
