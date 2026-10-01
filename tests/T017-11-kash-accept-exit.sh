#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-11 Kash terminal exit acceptance"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/accept"
mkdir -p "$work"
printf '%s\n' '#!/bin/sh' 'kill -TERM $$' >"$work/signal"
chmod +x "$work/signal"

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local expected="$1"; shift
		local status=0
		"${command[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 0 ] && [ "$(cat "$work/out")" = "$expected" ]; then test-ok "$backend: $*"; else test-fail "$backend acceptance (status $status)"; cat "$work/err"; fi
	}
	reject() {
		local code="$1"; shift
		local status=0
		"${command[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err"; then test-ok "$backend never accepts $code"; else test-fail "$backend rejection (status $status)"; cat "$work/err"; fi
	}
	test-step "$backend accepts only process exit failures"
	check 'after' do run -l kash -c '/bin/sh -c "exit 7"?; printf after'
	check '"retained"' do run --allow-run -l expr -c '$(/bin/sh -c "printf retained; printf warning >&2; exit 7" ?)'
	if [ "$(cat "$work/err")" = warning ]; then test-ok "$backend retains live stderr"; else test-fail "$backend hid stderr or emitted an unhandled failure"; fi
	check '"retained"' do run --allow-run -l expr -c '$(printf retained | /bin/sh -c "cat; exit 7"?)'
	check 'after' do run -l kash -c '/bin/sh -c "exit 7" | cat ?; printf after'
	check 'after' do run -l kash -c "$work/signal?; printf after"
	check 'literal?' do run -l kash -c 'printf "%s" "literal?"'
	check 'literal?' do run -l kash -c 'printf "%s" literal\?'
	check 'name?' do run -l kash -c 'name? = "name?"; printf "%s" $name?'
	check '"nested"' do run --allow-run -l expr -c '$(printf "%s" $(/bin/sh -c "printf nested; exit 7"?))'
	reject RECIPE_FAIL do run -l kash -c '/bin/sh -c "exit 7"'
	reject HOST_FAIL do run -l kash -c 'kame-nonexistent-accept-test?'
	reject RECIPE_TIMEOUT do run -l kash -c ':timeout 0.05 sleep 30?'
	reject CAP_DENIED do run -c '$(printf denied?)'
	reject EXPR_INVALID do run -l kash -c 'printf @([bad: 1])?'
	reject CAPTURE_LIMIT do run --allow-run --capture-limit 1 -l expr -c '$(printf too-long?)'
	reject CAPTURE_ENCODING do run --allow-run -l expr -c '$(printf "\\377"?)'
	reject CAPTURE_LIMIT do run --allow-run --capture-limit 1 -l expr -c '$(/bin/sh -c "printf too-long; exit 7"?)'
	reject CAPTURE_ENCODING do run --allow-run -l expr -c '$(/bin/sh -c "printf \"\\377\"; exit 7"?)'
	test-step "$backend rejects misplaced acceptance and raw value recovery"
	for text in '?' 'printf hi ??' 'printf hi? | cat' 'printf hi? printf fallback'; do reject PARSE_ERR do run -l kash -c "$text"; done
done

test-step "acceptance AST and canonical formatting preserve operator binding"
printf '%s\n' 'printf "literal?" | cat?; printf $(false?)' >"$work/source.kash"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" -l kash "$work/source.kash" >"$work/native"
	node "$CLI_ROOT/dist/kame.js" do "$operation" -l kash "$work/source.kash" >"$work/wasm"
	if cmp -s "$work/native" "$work/wasm"; then test-ok "$operation acceptance parity"; else test-fail "$operation acceptance parity"; fi
done
"$CLI_BIN" do fmt -l kash "$work/native" >"$work/again"
if cmp -s "$work/native" "$work/again"; then test-ok "acceptance formatting is idempotent"; else test-fail "acceptance format lost graph binding"; fi
test-end
