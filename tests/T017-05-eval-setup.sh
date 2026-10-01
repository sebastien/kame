#!/usr/bin/env bash
# Spec: docs/spec/017-kash.md — Stage setup and inherited execution policy
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T017-05 Kash stage setup parity"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/setup"
mkdir -p "$work/left" "$work/right" "$work/left/bin"
printf '%s' left >"$work/left/input"
printf '%s' right >"$work/right/input"
cp /usr/bin/printf "$work/left/bin/stage-print"
printf '%s\n' '#!/bin/sh' 'printf "%s" "$$" > "$1"' 'sleep 30' >"$work/linger"
chmod +x "$work/linger"

check() {
	local expression="$1" expected_status="$2" expected_stdout="$3" code="$4"
	shift 4
	set +e
	"${command[@]}" do expr --allow-run "--allow-read=$work" "--allow-write=$work" "$@" -c "$expression" >"$work/stdout" 2>"$work/stderr"
	local status=$?
	set -e
	if [ "$status" = "$expected_status" ] && [ "$(cat "$work/stdout")" = "$expected_stdout" ]; then test-ok "$backend: $expression"; else test-fail "$backend: status=$status: $expression"; fi
	if [ -n "$code" ]; then
		if grep -q "$code" "$work/stderr"; then test-ok "$backend: $code"; else test-fail "$backend: missing $code"; fi
	fi
}

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	test-step "$backend: independent directories, environment and PATH"
	check "\$(:cwd \"$work/left\" pwd)" 0 "\"$work/left\\n\"" ''
	check "\$(:cwd \"$work/left\" cat < input | :cwd \"$work/right\" cat > output)" 0 '""' ''
	if [ "$(cat "$work/right/output")" = left ]; then test-ok "$backend uses each endpoint cwd"; else test-fail "$backend ignored stage cwd"; fi
	check "\$(< input :cwd \"$work/right\" cat)" 0 '"right"' ''
	check "(let [dir \"$work/left\"] \$(:cwd \$dir :PATH bin stage-print found))" 0 '"found"' ''
	check '$(:KASH_SETUP_VALUE hello /usr/bin/printenv KASH_SETUP_VALUE)' 0 '"hello\n"' ''
	check '$(:KASH_SETUP_VALUE left /usr/bin/printenv KASH_SETUP_VALUE | :KASH_SETUP_VALUE right /bin/sh -c "cat; printenv KASH_SETUP_VALUE")' 0 '"left\nright\n"' ''
	check '(cat $(:KASH_SETUP_VALUE one printenv KASH_SETUP_VALUE) $(:KASH_SETUP_VALUE two printenv KASH_SETUP_VALUE))' 0 '"one\ntwo\n"' ''
	check '$(:KASH_SETUP_VALUE @(:false) printenv KASH_SETUP_VALUE)' 0 '"false\n"' ''
	check '$(:KASH_SETUP_VALUE "" printenv KASH_SETUP_VALUE)' 0 '"\n"' ''
	check '$(printf "%s" :cwd :timeout :KASH_SETUP_VALUE)' 0 '":cwd:timeout:KASH_SETUP_VALUE"' ''

	test-step "$backend: validation and unchanged grants"
	for expression in '$(:cwd "" pwd)' '$(:timeout 0 true)' '$(:timeout -1 true)' '$(:timeout NaN true)' '$(:timeout Infinity true)' '$(:timeout @([1]) true)' '$(:X @([1]) true)'; do
		check "$expression" 1 '' EXPR_INVALID
	done
	for expression in '$(:cwd . :cwd . pwd)' '$(:X a :X b true)' '$(:async true true)'; do check "$expression" 1 '' PARSE_ERR; done
	check "\$(:cwd \"$work/left\" cat < /etc/passwd > output)" 1 '' CAP_DENIED
	printf '%s' sentinel >"$work/protected"
	check "\$(:cwd \"$work/missing\" printf invalid > \"$work/protected\")" 1 '' HOST_FAIL
	if [ "$(cat "$work/protected")" = sentinel ]; then test-ok "$backend validates cwd before truncation"; else test-fail "$backend truncated output on invalid cwd"; fi
	printf '%s\n' 'result = (nop $(:timeout 100 sleep 5))' >"$work/policy.kmk"
	set +e
	"${command[@]}" --timeout 50 -f "$work/policy.kmk" result >"$work/stdout" 2>"$work/stderr"
	status=$?
	set -e
	if [ "$status" = 1 ] && grep -q RECIPE_TIMEOUT "$work/stderr"; then test-ok "$backend preserves caller timeout"; else test-fail "$backend relaxed caller timeout"; fi
	check '$(:timeout 0.05 sleep 5 | cat)' 1 '' RECIPE_TIMEOUT
	check '$(:timeout 0.05 printf ok | sleep 0.15)' 0 '""' ''
	check '$(:timeout 999999999 true)' 0 '""' ''
	check '$(:timeout 1 printf abc)' 1 '' CAPTURE_LIMIT --capture-limit 2

	test-step "$backend: stage cwd cannot broaden scoped executable grants"
	set +e
	"${command[@]}" do expr --allow-run=/usr/bin/printf -c "\$(:cwd \"$work/left\" ./bin/stage-print denied > output)" >"$work/stdout" 2>"$work/stderr"
	status=$?
	set -e
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/stderr"; then test-ok "$backend preserves caller run roots"; else test-fail "$backend broadened run grants"; fi

	test-step "$backend: timeout reaps the entire configured graph"
	check "\$(:timeout 0.15 \"$work/linger\" \"$work/$backend.first.pid\" | \"$work/linger\" \"$work/$backend.second.pid\")" 1 '' RECIPE_TIMEOUT
	for file in "$work/$backend.first.pid" "$work/$backend.second.pid"; do
		if [ -s "$file" ] && ! kill -0 "$(cat "$file")" 2>/dev/null; then test-ok "$backend stage reaped"; else test-fail "$backend stage was not launched or reaped"; fi
	done
done

test-step "setup parsing and formatting parity"
printf '%s\n' '$(:cwd ${directory} :timeout @(0.1) :MODE production cat | :MODE test cat :ordinary)' >"$work/expression.km"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" --lang expr "$work/expression.km" >"$work/native"
	node "$CLI_ROOT/dist/kame.js" do "$operation" --lang expr "$work/expression.km" >"$work/wasm"
	if cmp -s "$work/native" "$work/wasm"; then test-ok "$operation setup parity"; else test-fail "$operation setup parity"; fi
done
test-end
