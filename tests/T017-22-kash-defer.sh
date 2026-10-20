#!/usr/bin/env bash
# Spec: docs/spec/017-kash.md — invocation-scoped deferred cleanup
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-22 Kash deferred cleanup"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local expected="$1" text="$2" code="${3:-}" status=0
		timeout -k 2 15 "${runner[@]}" do run -C "$work" --allow-run --allow-read --allow-write -l kash -c "$text" >"$work/out" 2>"$work/err" || status=$?
		printf '%s' "$expected" >"$work/expected"
		local expected_status=0
		if [ -n "$code" ]; then expected_status=1; fi
		if cmp -s "$work/out" "$work/expected" && [ "$status" = "$expected_status" ] && { [ -z "$code" ] || grep -q "$code" "$work/err"; }; then test-ok "$backend defer"; else test-fail "$backend defer (status $status, expected $code)"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend LIFO, registration snapshots, delayed expressions and lexical scope"
	check bodyBA 'defer printf A; defer (out! "B"); printf body'
	check bodyAB 'defer (printf A, sh -c "sleep .05; printf B") |(merge :buffer 1) cat; printf body'
	check bodyAA 'defer printf A |(broadcast) (cat, cat); printf body'
	check $'bodyA\n' 'defer printf "A\n" |(frame :lines) |(each) cat; printf body'
	check $'bodyA\n' 'defer printf "A\n" |(frame :lines) |(filter :true | cat) | cat; printf body'
	check bodyA $'stream cleanup\ndefer (printf A >&cleanup, &cleanup | cat)\nprintf body'
	check bodyA 'defer cat <(printf A); printf body'
	check 'bodybranchlast' $'defer printf last\nif true\n  value = "branch"\n  defer (out! value)\nelse\n  defer printf forbidden\nprintf body'
	check bodyinnerafterouter 'nested = (kash "defer printf inner; printf body"); defer printf outer; (nested); printf after'
	check AB 'defer printf "%s" (str $(printf A) $(printf B))'
	check once 'defer (let [] (out! "once") (run! :async :true "true"))'
	check bodyfinallyafterouter 'defer printf outer; (finally! ([] (out! "body")) ([] (out! "finally"))); printf after'
	check bodykashfinallyouter 'nested = (kash "defer printf kash; printf body"); defer printf outer; (finally! ([] (nested)) ([] (out! "finally")))'
	printf previous >"$work/redirection"
	check previous 'defer printf replaced > ./redirection; cat < ./redirection'
	if [ "$(cat "$work/redirection")" = replaced ]; then test-ok "$backend redirection opens only at cleanup"; else test-fail "$backend deferred redirection"; fi
	printf before >"$work/value"
	check afterbefore 'defer printf "%s" (text (read ./value)); defer (out! (text (read ./value))); (write! ./value "after")'
	# printenv adds an LF; compare exactly rather than stripping captures.
	check $'parentchild\n' 'defer :MODE child printenv MODE; printf parent'
	check cleanup 'defer printf cleanup; false' RECIPE_FAIL
	check last 'defer printf last; defer false' RECIPE_FAIL
	check last 'defer printf last; defer (fail! "cleanup failure"); (fail! "primary failure")' USER_ERROR
	if grep -q 'primary failure' "$work/err" && grep -q 'cleanup failure' "$work/err"; then test-ok "$backend preserves primary and cleanup diagnostics"; else test-fail "$backend lost failure context"; fi
	check last 'defer false ?; defer printf last'
	test-step "$backend source is parsed before effects; read-only execution never registers cleanup"
	check '' $'printf forbidden\ndefer printf x &' PARSE_ERR
	check '' $'printf forbidden\ndefer' PARSE_ERR
	check '' $'printf forbidden\ndefer (out! "x") extra' PARSE_ERR
	"${runner[@]}" do run --dry-run -C "$work" -l kash -c 'defer (write! ./forbidden "no"); defer touch ./forbidden' >"$work/out" 2>"$work/err"
	if [ ! -e "$work/forbidden" ]; then test-ok "$backend dry-run has no deferred effects"; else test-fail "$backend dry-run cleanup ran"; fi
	test-step "$backend cleanup after denied capabilities retains only original authority"
	status=0
	"${runner[@]}" do run -C "$work" --allow-write -l kash -c 'defer (write! ./cleaned "yes"); (env "KAME_DEFER_DENIED")' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/err" && [ "$(cat "$work/cleaned")" = yes ]; then test-ok "$backend denial still finalizes"; else test-fail "$backend denial skipped cleanup"; cat "$work/err"; fi
	test-step "$backend background work joins before successful cleanup"
	check backgroundcleanup 'defer printf cleanup; sh -c "sleep .05; printf background" &'
	test-step "$backend temporary cleanup preserves unrelated files"
	printf keep >"$work/unrelated"
	check '' 'unused = $(mktemp -d ./owned.XXXXXX)'
	check '' 'directory = (strip $(mktemp -d ./owned.XXXXXX)); defer rm -rf $directory; touch ${directory}/partial; false' RECIPE_FAIL
	if [ "$(cat "$work/unrelated")" = keep ] && ! compgen -G "$work/owned.*" >/dev/null; then test-ok "$backend removes only authored temporary workspace"; else test-fail "$backend temporary ownership example"; fi
	test-step "$backend deferred cleanup in direct Kash recipes"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
good :
	defer printf cleanup
	printf body
bad :
	defer (write! ./recipe-cleaned "yes")
	false
KMK
	"${runner[@]}" -C "$work" good >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = bodycleanup ]; then test-ok "$backend recipe lifetime"; else test-fail "$backend recipe lifetime"; fi
	status=0
	"${runner[@]}" -C "$work" bad >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && [ -e "$work/recipe-cleaned" ]; then test-ok "$backend failed recipe finalizes"; else test-fail "$backend failed recipe cleanup"; cat "$work/err"; fi
	test-step "$backend formatter preserves deferred forms and literal command names"
	printf '%s\n' 'defer printf "%s" "two words"' 'if true' '  defer (out! "late")' 'printf "%s" "defer"' >"$work/source.kash"
	"${runner[@]}" do fmt -l kash "$work/source.kash" >"$work/formatted"
	"${runner[@]}" do fmt -l kash "$work/formatted" >"$work/again"
	if cmp -s "$work/formatted" "$work/again"; then test-ok "$backend defer formatter is idempotent"; else test-fail "$backend defer formatting"; fi
done
test-end
