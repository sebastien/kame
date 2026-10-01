#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-16 Kash explicit environment namespace"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/environment"
mkdir -p "$work"
export KASH_TEST_ENV='host value with spaces'
export KASH_TEST_PATTERN='{name:*}.c'
unset KASH_TEST_MISSING || true
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local expected="$1" source="$2"; shift 2
		local grants=(--allow-run --allow-env)
		for arg in "$@"; do if [[ "$arg" == --allow-* ]]; then grants=(); break; fi; done
		local status=0
		"${command[@]}" do run -C "$work" "${grants[@]}" "$@" -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend explicit env"; else test-fail "$backend environment (status $status)"; cat "$work/err"; fi
	}
	reject() {
		local code="$1" source="$2"; shift 2
		local grants=(--allow-run --allow-env)
		for arg in "$@"; do if [[ "$arg" == --allow-* ]]; then grants=(); break; fi; done
		local status=0
		"${command[@]}" do run -C "$work" "${grants[@]}" "$@" -l kash -c "$source" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend preserves $code"; else test-fail "$backend environment boundary"; cat "$work/err"; fi
	}
	test-step "$backend reads only explicit named variables"
	check "$KASH_TEST_ENV" 'printf "%s" $env.KASH_TEST_ENV'
	check "$KASH_TEST_ENV.c" 'printf "%s" ${env.KASH_TEST_ENV}.c'
	check "$KASH_TEST_ENV" 'printf "%s" @(env.KASH_TEST_ENV)'
	check "$KASH_TEST_ENV" 'printf "%s" @(env "KASH_TEST_ENV")'
	check "$KASH_TEST_ENV" 'value = env.KASH_TEST_ENV; printf "%s" $value'
	check "$KASH_TEST_ENV" 'value = $env.KASH_TEST_ENV; printf "%s" $value'
	check h 'printf "%s" $env.KASH_TEST_ENV.0'
	check binding 'KASH_TEST_ENV = "binding"; printf "%s" $KASH_TEST_ENV'
	check binding 'PATH = "binding"; /bin/printf "%s" $PATH'
	reject REF_MISSING 'printf "%s" $KASH_TEST_ENV'
	reject EXPR_INVALID 'printf "%s" $env.KASH_TEST_MISSING'
	check fallback 'value = $env.KASH_TEST_MISSING ?? "fallback"; printf "%s" $value'
	reject REF_MISSING 'printf "%s" $env'
	check '' 'unused = $env.KASH_TEST_ENV' --allow-run
	check fallback $'if @(:false)\n\tprintf "%s" $env.KASH_TEST_ENV\nelse\n\tprintf fallback' --allow-run
	test-step "$backend applies configured overrides and scoped env grants"
	check configured 'printf "%s" $env.KASH_TEST_ENV' --env KASH_TEST_ENV=configured
	check '' 'printf "%s" $env.KASH_TEST_ENV' --env KASH_TEST_ENV=
	check "$KASH_TEST_ENV" 'printf "%s" $env.KASH_TEST_ENV' --allow-run --allow-env=KASH_TEST_ENV
	check "$KASH_TEST_ENV" ':KASH_TEST_ENV child printf "%s" $env.KASH_TEST_ENV'
	reject CAP_DENIED 'printf "%s" $env.KASH_TEST_ENV' --allow-run --allow-env=KASH_TEST_OTHER
	reject CAP_DENIED 'value = $env.KASH_TEST_ENV ?? "forbidden"; printf "%s" $value' --allow-run
	reject CAP_DENIED 'printf "%s" $(printf "%s" $env.KASH_TEST_ENV)' --allow-run
	check 'host value with spacestail' 'printf "%s" $env.KASH_TEST_ENV $(printf tail)'
	check "$KASH_TEST_ENV" $'if @(:true)\n\tvalue = $env.KASH_TEST_ENV\n\tprintf "%s" $value'
	check found $'match @(env.KASH_TEST_ENV)\n\tcase "host value with spaces"\n\t\tprintf found'
	test-step "$backend reserves env bindings without changing plain Kame"
	for text in 'env = "forbidden"' '(f env) = env' 'printf @([env] env)' 'printf $env.0' 'printf $env.{PATH}'; do reject PARSE_ERR "$text"; done
	for text in 'printf @(let [env 1] "forbidden")' 'printf @(def env "forbidden")' 'printf @(with [env: 1] "forbidden")'; do reject DEF_INVALID "$text"; done
	reject DEF_INVALID $'match @("x")\n\tcase "{env:*}"\n\t\tprintf forbidden'
	reject DEF_INVALID 'printf @(match "x" ["{env:*}" "forbidden"])'
	"${command[@]}" do run -l km -c $'env = [VALUE: "Kame"]\nenv.VALUE' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = '"Kame"' ]; then test-ok "$backend leaves Kame reference semantics unchanged"; else test-fail "$backend rewrote plain Kame env"; fi
	"${command[@]}" do run --allow-run --allow-env=KASH_TEST_ENV -l expr -c '$(printf "%s" $env.KASH_TEST_ENV)' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = '"host value with spaces"' ]; then test-ok "$backend embedded captures delegate env lookup to Kash"; else test-fail "$backend embedded env lookup"; fi
	"${command[@]}" do run --allow-run --allow-env=KASH_TEST_ENV -l km -c '(env KEY) = "shadow"' -l kash -c 'printf "%s" $env.KASH_TEST_ENV' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = "$KASH_TEST_ENV" ]; then test-ok "$backend namespace cannot be shadowed by another language fragment"; else test-fail "$backend shadowed environment authority"; fi
done
test-step "explicit environment AST and canonical formatting parity"
printf '%s\n' 'value = env.KASH_TEST_ENV' 'printf "%s" ${env.KASH_TEST_ENV} @(env.KASH_TEST_ENV)' >"$work/source.kash"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" -l kash "$work/source.kash" >"$work/native"
	node "$CLI_ROOT/dist/kame.js" do "$operation" -l kash "$work/source.kash" >"$work/wasm"
	if cmp -s "$work/native" "$work/wasm"; then test-ok "$operation parity"; else test-fail "$operation parity"; fi
done
"$CLI_BIN" do fmt -l kash "$work/native" >"$work/again"
if cmp -s "$work/native" "$work/again"; then test-ok "environment formatting is idempotent"; else test-fail "formatting changed env semantics"; fi
test-end
