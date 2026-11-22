#!/usr/bin/env bash
# Spec: docs/spec/055-unified-kame-kash-language.md — executable lookup and unit state
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-16 Kash explicit environment namespace"
test-step "build artifacts"
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
cli_build
work="$TMPDIR/environment"
mkdir -p "$work"
export KASH_TEST_ENV='host value with spaces'
unset KASH_TEST_MISSING || true
cat >"$work/authority.km" <<'KAME'
task permitted ; {authority: {unset: ["KASH_TEST_ENV"]}}
	set env/KASH_TEST_ENV = ""
	sh -c 'printf "<%s>" "${KASH_TEST_ENV-missing}"'
	set env/KASH_TEST_ENV = nil
	sh -c 'printf "<%s>" "${KASH_TEST_ENV-missing}"'
task undelegated
	set env/KASH_TEST_ENV = nil
	touch ./forbidden
task malformed : side-effect ; {authority: {unset: ["KASH_TEST_ENV", "KASH_TEST_ENV"]}}
	true
task side-effect
	touch ./forbidden
task empty ; {tty: true}
task foreign : side-effect ; {shell: "sh", tty: true}
	touch ./forbidden
rule ./expression : side-effect ; {tty: true} = "forbidden"
task terminal ; {tty: true, foreground: true}
	touch ./forbidden
KAME
mkdir -p "$work/first" "$work/second"
printf '#!/bin/sh\nprintf "first\\n"\n' >"$work/first/kame-scope-probe"
printf '#!/bin/sh\nprintf "second\\n"\n' >"$work/second/kame-scope-probe"
chmod +x "$work"/{first,second}/kame-scope-probe
path_program=$(cat <<'KASH'
def choose value
	set env/PATH = value
	return nil
def inner ()
	choose "./first"
	kame-scope-probe
kame-scope-probe
choose "./second"
kame-scope-probe
(out! $(inner))
kame-scope-probe
KASH
)
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$wasm_wrapper"); fi
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
	test-step "$backend reads named variables and honors lexical bindings"
	check "$KASH_TEST_ENV" 'printf "%s" ${env/KASH_TEST_ENV}'
	check "$KASH_TEST_ENV.c" 'printf "%s" ${env/KASH_TEST_ENV}.c'
	check "$KASH_TEST_ENV" 'printf "%s" (nop env/KASH_TEST_ENV)'
	check "$KASH_TEST_ENV" 'printf "%s" ${env/"KASH_TEST_ENV"}'
	check "$KASH_TEST_ENV" 'let value = env/KASH_TEST_ENV; printf "%s" $value'
	check "$KASH_TEST_ENV" 'printf "%s" $KASH_TEST_ENV'
	check h 'printf "%s" ${env/KASH_TEST_ENV.0}'
	check binding 'def KASH_TEST_ENV = "binding"; printf "%s" $KASH_TEST_ENV'
	check binding 'def PATH = "binding"; /bin/printf "%s" $PATH'
	reject ENV_VAR_MISSING 'printf "%s" $KASH_TEST_MISSING'
	check '<>' 'printf "<%s>" ${env/KASH_TEST_MISSING}'
	check '<>' 'printf "<%s>" "${env/KASH_TEST_MISSING}"'
	check fallback 'let value = env/KASH_TEST_MISSING ?? "fallback"; printf "%s" $value'
	check '' 'def unused = env/KASH_TEST_ENV' --allow-run
	check fallback $'if false\n\tprintf "%s" ${env/KASH_TEST_ENV}\nelse\n\tprintf fallback' --allow-run
	test-step "$backend applies configured overrides and scoped env grants"
	check configured 'printf "%s" ${env/KASH_TEST_ENV}' --env KASH_TEST_ENV=configured
	check '' 'printf "%s" ${env/KASH_TEST_ENV}' --env KASH_TEST_ENV=
	check "$KASH_TEST_ENV" 'printf "%s" ${env/KASH_TEST_ENV}' --allow-run --allow-env=KASH_TEST_ENV
	check "$KASH_TEST_ENV" 'KASH_TEST_ENV: child printf "%s" ${env/KASH_TEST_ENV}'
	reject CAP_DENIED 'printf "%s" ${env/KASH_TEST_ENV}' --allow-run --allow-env=KASH_TEST_OTHER
	reject CAP_DENIED 'let value = env/KASH_TEST_ENV ?? "forbidden"; printf "%s" $value' --no-default-grants --allow-run
	reject CAP_DENIED 'printf "%s" $(printf "%s" ${env/KASH_TEST_ENV})' --no-default-grants --allow-run
	check 'host value with spacestail' 'printf "%s" ${env/KASH_TEST_ENV} $(printf tail)'
	check "$KASH_TEST_ENV" $'if true\n\tlet value = env/KASH_TEST_ENV\n\tprintf "%s" $value'
	check found $'match env/KASH_TEST_ENV\n\t"host value with spaces" =>\n\t\tprintf found'
	test-step "$backend executable lookup follows PATH within its execution unit"
	PATH="$work/first:$PATH" check $'first\nsecond\nfirst\nsecond' "$path_program" --allow-run --allow-env=PATH
	test-step "$backend scopes environment removal to rule authority"
	check '<session>' $'set env/KASH_TEST_ENV = "session"\nsh -c \'printf "<%s>" "$KASH_TEST_ENV"\'' --allow-run
	reject CAP_DENIED 'set env/KASH_TEST_ENV = nil' --allow-env=KASH_TEST_ENV
	status=0
	"${command[@]}" do run -C "$work" --allow-run --allow-env=KASH_TEST_MISSING authority.km permitted >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 0 ] && [ "$(cat "$work/out")" = '<><missing>' ]; then
		test-ok "$backend authorized removal needs no read grant"
	else
		test-fail "$backend authorized environment overlay (status $status)"
		cat "$work/err"
	fi
	for rejection in undelegated:CAP_DENIED malformed:EXPR_INVALID foreign:FEATURE_UNSUP ./expression:FEATURE_UNSUP; do
		target="${rejection%:*}"
		code="${rejection##*:}"
		status=0
		"${command[@]}" do run -C "$work" --allow-run --allow-write --allow-env=KASH_TEST_ENV authority.km "$target" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -e "$work/forbidden" ] && [ ! -e "$work/expression" ]; then
			test-ok "$backend $target authority fails before recipe effects"
		else
			test-fail "$backend $target authority boundary (status $status)"
			cat "$work/err"
		fi
	done
	if "${command[@]}" do run -C "$work" authority.km empty >"$work/out" 2>"$work/err"; then
		test-ok "$backend bodyless declaration ignores the terminal grant"
	else
		test-fail "$backend rejected a bodyless declaration with a terminal grant"
		cat "$work/err"
	fi
	if [ "$backend" = wasm ]; then
		status=0
		"${command[@]}" do run -C "$work" --allow-run --allow-write --json authority.km terminal >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && jq -es 'any(.[]; .diagnostic.code == "FEATURE_UNSUP") and all(.[]; .type != "process-started")' "$work/out" >/dev/null && [ ! -e "$work/forbidden" ]; then
			test-ok "wasm rejects terminal handoff before spawn"
		else
			test-fail "wasm terminal handoff boundary (status $status)"
			cat "$work/out" "$work/err"
		fi
	fi
	test-step "$backend keeps lexical env bindings separate from the namespace"
	for text in 'printf ${env/}' 'printf ${env/KASH_TEST_ENV/other}' 'printf ${env/KASH_TEST_ENV.{PATH}}'; do reject PARSE_ERR "$text"; done
	check "lexical/$KASH_TEST_ENV" 'def env = "lexical"; printf "%s/%s" $env ${env/KASH_TEST_ENV}'
	check parameter 'def show env = env; printf "%s" (show "parameter")'
	check lambda 'printf "%s" ((env => env) "lambda")'
	check x $'match "x"\n\t"{env:*}" =>\n\t\tprintf "%s" $env'
	"${command[@]}" do run --print-result -l km -c $'env = {VALUE: "Kame"}\n(get "VALUE" env)' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = '"Kame"' ]; then test-ok "$backend Kame retains explicit access to a lexical env binding"; else test-fail "$backend reserved bare env in Kame"; fi
	"${command[@]}" do run --allow-run --allow-env=KASH_TEST_ENV -l expr -c '$(printf "%s" ${env/KASH_TEST_ENV})' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = '"host value with spaces"' ]; then test-ok "$backend embedded captures delegate env lookup to Kash"; else test-fail "$backend embedded env lookup"; fi
	"${command[@]}" do run --allow-run --allow-env=KASH_TEST_ENV -l km -c 'def env key = "shadow"' -l kash -c 'printf "%s" ${env/KASH_TEST_ENV}' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = "$KASH_TEST_ENV" ]; then test-ok "$backend namespace cannot be shadowed by another language fragment"; else test-fail "$backend shadowed environment authority"; fi
done
test-step "explicit environment AST and canonical formatting parity"
printf '%s\n' 'let value = env/KASH_TEST_ENV' 'printf "%s" ${env/KASH_TEST_ENV} (nop env/KASH_TEST_ENV)' >"$work/source.kash"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" -l kash "$work/source.kash" >"$work/native"
	node "$wasm_wrapper" do "$operation" -l kash "$work/source.kash" >"$work/wasm"
	if cmp -s "$work/native" "$work/wasm"; then test-ok "$operation parity"; else test-fail "$operation parity"; fi
done
"$CLI_BIN" do fmt -l kash "$work/native" >"$work/again"
if cmp -s "$work/native" "$work/again"; then test-ok "environment formatting is idempotent"; else test-fail "formatting changed env semantics"; fi
test-end
