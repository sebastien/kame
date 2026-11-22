#!/usr/bin/env bash
# Spec: docs/spec/048-kash-practical-scripting.md — local libraries and source identity
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-02 Kash libraries and source identity"
cli_build
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make -C "$CLI_ROOT" dist-wasm >/dev/null; fi
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work/scripts/lib/nested" "$work/caller"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "${KAME_TEST_WASM_WRAPPER:-$CLI_ROOT/dist/kame.js}"); fi
	invoke() { "${runner[@]}" do run -C "$work/caller" --allow-run --allow-read --allow-write "$@"; }
	reject() {
		local code="$1" status=0
		shift
		invoke "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ] && [ ! -e "$work/caller/executed" ]; then
			test-ok "$backend rejects $code before effects"
		else
			test-fail "$backend expected $code ($status)"
			cat "$work/out" "$work/err"
		fi
	}
	cat >"$work/scripts/lib/nested/value.kash" <<'KASH'
# Nested libraries use their authored source directory.
def nested ()
	printf "%s" (str module/dir)
KASH
	cat >"$work/scripts/lib/common.kash" <<'KASH'
include ./nested/value.kash
def unused = $(touch ./executed)
def assets () = str module/dir
def show ()
	printf "%s\n" (assets ())
	nested
KASH
	cat >"$work/scripts/main.kash" <<'KASH'
show
include ./lib/common.kash
printf "\n%s" (str module/dir)
KASH
	test-step "$backend authored identity and forward library definitions"
	invoke ../scripts/main.kash >"$work/out" 2>"$work/err"
	printf '%s\n%s\n%s' "$work/scripts/lib" "$work/scripts/lib/nested" "$work/scripts" >"$work/expected"
	if cmp -s "$work/expected" "$work/out" && [ ! -s "$work/err" ] && [ ! -e "$work/caller/executed" ]; then
		test-ok "$backend includes are lazy and source-relative, not caller-relative"
	else test-fail "$backend library output"; cat "$work/out" "$work/err"; fi
	printf 'include ./lib/common.kash\ndef helper ()\n\tshow\nhelper\n' >"$work/scripts/wrapped.kash"
	invoke ../scripts/wrapped.kash >"$work/out" 2>"$work/err"
	printf '%s\n%s' "$work/scripts/lib" "$work/scripts/lib/nested" >"$work/expected"
	if cmp -s "$work/expected" "$work/out"; then test-ok "$backend authored wrappers preserve authored helper identity"; else test-fail "$backend authored wrapper identity"; cat "$work/err"; fi
	ln -s lib "$work/scripts/alias"
	printf 'include ./alias/common.kash\nshow\n' >"$work/scripts/lexical.kash"
	invoke ../scripts/lexical.kash >"$work/out" 2>"$work/err"
	printf '%s\n%s' "$work/scripts/alias" "$work/scripts/alias/nested" >"$work/expected"
	if cmp -s "$work/expected" "$work/out"; then test-ok "$backend module/dir keeps lexical symlink spelling"; else test-fail "$backend module/dir resolved symlinks"; fi
	invoke -n ../scripts/main.kash >"$work/out" 2>"$work/err"
	if [ ! -s "$work/out" ] && [ ! -e "$work/caller/executed" ]; then test-ok "$backend dry-run does not demand library work"; else test-fail "$backend dry-run effects"; fi
	"${runner[@]}" do fmt -l kash "$work/scripts/main.kash" >"$work/scripts/formatted.kash"
	invoke ../scripts/formatted.kash >"$work/out" 2>"$work/err"
	printf '%s\n%s\n%s' "$work/scripts/lib" "$work/scripts/lib/nested" "$work/scripts" >"$work/expected"
	if cmp -s "$work/expected" "$work/out"; then test-ok "$backend formatting preserves includes"; else test-fail "$backend formatted library"; fi
	cat >"$work/scripts/shared.kash" <<'KASH'
def helper first second third
    printf %s $first
    printf %s $second
    printf %s $third
KASH
	"${runner[@]}" do fmt -l kash "$work/scripts/shared.kash" >"$work/scripts/shared-formatted.kash"
	if grep -qx 'def helper first second third' "$work/scripts/shared-formatted.kash" && grep -q $'^\tprintf' "$work/scripts/shared-formatted.kash"; then test-ok "$backend formatter emits an indented function body"; else test-fail "$backend formatter omitted the body"; fi
	printf 'include ./shared-formatted.kash\nhelper a b c\n' >"$work/scripts/shared-caller.kash"
	invoke ../scripts/shared-caller.kash >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = abc ]; then test-ok "$backend formatted library includes from Kash"; else test-fail "$backend formatted Kash include"; cat "$work/err"; fi
	printf 'include ./shared-formatted.kash\ntask all\n\thelper a b c\n' >"$work/scripts/Kamefile"
	"${runner[@]}" -C "$work/scripts" all >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = abc ]; then test-ok "$backend formatted library includes from Kame"; else test-fail "$backend formatted Kame include"; cat "$work/err"; fi
	printf 'def broken =\n  first 1\n' >"$work/scripts/shared-broken.kash"
	printf 'include ./shared-broken.kash\nprintf %%s $broken\n' >"$work/scripts/broken-caller.kash"
	status=0
	invoke --json ../scripts/broken-caller.kash >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q 'shared-broken.kash' "$work/out" "$work/err"; then test-ok "$backend multiline include retains source"; else test-fail "$backend multiline include provenance"; fi
	reject PARSE_ERR -l kash -c $'def missing =\nprintf bad\n'
	test-step "$backend source context and library boundaries"
	reject SEL_NO_CONTEXT -l kash -c 'printf %s (str module/dir)'
	printf 'printf %%s (str module/dir)\n' | reject SEL_NO_CONTEXT -l kash -
	printf '(eval "(str module/dir)")\n' >"$work/scripts/generated.kash"
	reject SEL_NO_CONTEXT ../scripts/generated.kash
	reject RES_INVALID -l kash -c 'include ./library.kash'
	printf 'include ./library.kash\n' | reject RES_INVALID -l kash -
	reject RES_INVALID -l kash -c 'include? ./library.kash'
	reject PARSE_ERR -l kash -c $'def hidden ()\n\tinclude ./library.kash\n'
	reject PARSE_ERR -l kash -c $'if false\n\tinclude ./library.kash'
	printf 'def included = "absolute"\n' >"$work/scripts/lib/absolute.kash"
	invoke -l kash -c "include $work/scripts/lib/absolute.kash"$'\nout! $included' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = absolute ]; then test-ok "$backend inline absolute include loads a Kash library"; else test-fail "$backend inline absolute include"; fi
	HOME="$work/scripts/lib" invoke -l kash -c $'include ~/absolute.kash\nout! $included' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = absolute ]; then test-ok "$backend inline home-relative include resolves the home directory"; else test-fail "$backend inline home-relative include"; fi
	printf 'value = 1\n' >"$work/scripts/lib/incompatible.km"
	printf 'touch ./executed\ninclude ./lib/incompatible.km\n' >"$work/scripts/incompatible.kash"
	reject RES_INVALID ../scripts/incompatible.kash
	printf 'printf forbidden\n' >"$work/scripts/lib/invalid.kash"
	printf 'touch ./executed\ninclude ./lib/invalid.kash\n' >"$work/scripts/invalid.kash"
	reject DEF_INVALID ../scripts/invalid.kash
	printf 'def value = 1\n' >"$work/scripts/lib/repeated.kash"
	printf 'touch ./executed\ninclude ./lib/repeated.kash\ninclude ./lib/repeated.kash\n' >"$work/scripts/repeated.kash"
	reject DEF_INVALID ../scripts/repeated.kash
	printf 'include ./cycle.kash\n' >"$work/scripts/cycle.kash"
	reject DEP_CYCLE ../scripts/cycle.kash
	printf 'include ./absent.kash\n' >"$work/scripts/missing.kash"
	reject FS_ERR ../scripts/missing.kash
	printf 'def broken ()\n\t(first 1)\n' >"$work/scripts/lib/broken.kash"
	printf 'include ./lib/broken.kash\nbroken\n' >"$work/scripts/broken.kash"
	status=0
	invoke --json ../scripts/broken.kash >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q '"kind":"include"' "$work/out" "$work/err" && grep -q 'lib/broken.kash' "$work/out" "$work/err"; then test-ok "$backend runtime failure retains included source and frame"; else test-fail "$backend runtime include provenance"; cat "$work/out" "$work/err"; fi
	test-step "$backend includes preserve execution authority"
	"${runner[@]}" do run -C "$work/caller" --no-default-grants --allow-run ../scripts/main.kash >"$work/out" 2>"$work/err"
	printf '%s\n%s\n%s' "$work/scripts/lib" "$work/scripts/lib/nested" "$work/scripts" >"$work/expected"
	if cmp -s "$work/expected" "$work/out"; then test-ok "$backend module/dir needs no filesystem grant"; else test-fail "$backend module/dir unnecessarily needs read authority"; fi
	status=0
	"${runner[@]}" do run -C "$work/caller" --no-default-grants --allow-read ../scripts/main.kash >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend libraries cannot grant process authority"; else test-fail "$backend authority ($status)"; cat "$work/err"; fi
done
test-end
