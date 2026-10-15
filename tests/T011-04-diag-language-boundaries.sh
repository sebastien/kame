#!/usr/bin/env bash
# Spec: docs/spec/011-diagnostics.md — language-boundary recovery
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T011-04 language-boundary recovery"
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	reject() {
		local code="$1" guidance="$2"; shift 2
		for format in human plain json; do
			local options=(--color=never --diagnostic-format="$format")
			local report="$work/error" status=0
			if [ "$format" = json ]; then options=(--json); report="$work/output"; fi
			"${runner[@]}" "${options[@]}" "$@" >"$work/output" 2>"$work/error" || status=$?
			if [ "$status" = 1 ] && grep -Fq "$code" "$report" && grep -Fq -- "$guidance" "$report"; then
				test-ok "$backend $format $code recovery"
			else
				test-fail "$backend $format $code recovery ($status)"
				cat "$work/output" "$work/error"
			fi
		done
	}
	test-step "$backend reference versus call recovery"
	reject EXPR_INVALID '@(f)' do run --lang expr -c '(let [f ([] "ok")] "@{f}")'
	reject EXPR_INVALID '@{name}' do run --lang expr -c '(let [name "ok"] "@(name)")'
	reject PARSE_ERR 'requires a reference' do run --lang expr -c '"@{(str "x")}"'
	reject PARSE_ERR 'expected' do run --lang expr -c '"@{name'
	reject PARSE_ERR 'expected' do run --lang kash -c 'printf prefix-(str "unclosed"'
	test-step "$backend parser ownership and authority"
	reject SEL_INDEX_INVALID 'literal at-sign' do render --comment plain -c '--mtime=@0'
	reject SEL_NO_CONTEXT 'literal at-sign' do render --comment plain -c '@<'
	reject CAP_DENIED '--allow-env=NAME' do run --lang expr -c '(env "MODE")'
	reject PARSE_ERR 'target : inputs ; [shell: kash]' do run --lang kmk -c 'show ; [shell: kash] :'
	reject PARSE_ERR 'direct Kash' do run --allow-run --lang kash -c 'printf @{name}'
	reject PARSE_ERR './ for a file path' do run --lang kmk -c 'out.o :'
	test-step "$backend scalar destinations never reveal evaluated secrets"
	secret='boundary-secret-that-must-not-be-disclosed'
	for setup in '' ':cwd ' ':timeout ' ':MODE '; do
		if [ -z "$setup" ]; then source='printf prefix-(nop [hidden: (env "RECOVERY_SECRET")])'; else source="${setup}"'prefix-(nop [hidden: (env "RECOVERY_SECRET")]) printf forbidden'; fi
		RECOVERY_SECRET="$secret" reject EXPR_INVALID 'record' do run --allow-run --allow-env=RECOVERY_SECRET --lang kash -c "$source"
		if grep -Fq "$secret" "$work/output" "$work/error"; then test-fail "$backend evaluated secret in diagnostics"; else test-ok "$backend evaluated secret remains private"; fi
	done
done
test-end
