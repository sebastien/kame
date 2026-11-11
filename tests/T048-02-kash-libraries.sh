#!/usr/bin/env bash
# Spec: docs/spec/048-kash-practical-scripting.md — local libraries and source identity
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-02 Kash libraries and source identity"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work/scripts/lib/nested" "$work/caller"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
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
	cat >"$work/scripts/lib/nested/value.km" <<'KASH'
# Suffix does not choose the included grammar.
def nested
	printf "%s" (source-dir)
KASH
	cat >"$work/scripts/lib/common.kash" <<'KASH'
(include ./nested/value.km)
unused = $(touch ./executed)
(assets) = (source-dir)
def show
	printf "%s\n" (assets)
	(nested)
KASH
	cat >"$work/scripts/main.kash" <<'KASH'
(show)
(include ./lib/common.kash)
printf "\n%s" (source-dir)
KASH
	test-step "$backend authored identity and forward library definitions"
	invoke ../scripts/main.kash >"$work/out" 2>"$work/err"
	printf '%s\n%s\n%s' "$work/scripts/lib" "$work/scripts/lib/nested" "$work/scripts" >"$work/expected"
	if cmp -s "$work/expected" "$work/out" && [ ! -s "$work/err" ] && [ ! -e "$work/caller/executed" ]; then
		test-ok "$backend includes are lazy and source-relative, not caller-relative"
	else test-fail "$backend library output"; cat "$work/out" "$work/err"; fi
	printf '(include ./lib/common.kash)\nhelper = (kash "(show)")\n(helper)\n' >"$work/scripts/constructed.kash"
	invoke ../scripts/constructed.kash >"$work/out" 2>"$work/err"
	printf '%s\n%s' "$work/scripts/lib" "$work/scripts/lib/nested" >"$work/expected"
	if cmp -s "$work/expected" "$work/out"; then test-ok "$backend constructed callers preserve authored helper identity"; else test-fail "$backend constructed caller identity"; cat "$work/err"; fi
	ln -s lib "$work/scripts/alias"
	printf '(include ./alias/common.kash)\n(show)\n' >"$work/scripts/lexical.kash"
	invoke ../scripts/lexical.kash >"$work/out" 2>"$work/err"
	printf '%s\n%s' "$work/scripts/alias" "$work/scripts/alias/nested" >"$work/expected"
	if cmp -s "$work/expected" "$work/out"; then test-ok "$backend source-dir keeps lexical symlink spelling"; else test-fail "$backend source-dir resolved symlinks"; fi
	invoke -n ../scripts/main.kash >"$work/out" 2>"$work/err"
	if [ ! -s "$work/out" ] && [ ! -e "$work/caller/executed" ]; then test-ok "$backend dry-run does not demand library work"; else test-fail "$backend dry-run effects"; fi
	"${runner[@]}" do fmt -l kash "$work/scripts/main.kash" >"$work/scripts/formatted.kash"
	invoke ../scripts/formatted.kash >"$work/out" 2>"$work/err"
	printf '%s\n%s\n%s' "$work/scripts/lib" "$work/scripts/lib/nested" "$work/scripts" >"$work/expected"
	if cmp -s "$work/expected" "$work/out"; then test-ok "$backend formatting preserves includes"; else test-fail "$backend formatted library"; fi
	cat >"$work/scripts/shared.kmk" <<'KAME'
helper = (kash """printf %s $args.0
printf %s $args.1
printf %s $args.2""")
KAME
	"${runner[@]}" do fmt -l kmk "$work/scripts/shared.kmk" >"$work/scripts/shared-formatted.kmk"
	if grep -qx 'helper =' "$work/scripts/shared-formatted.kmk"; then test-ok "$backend formatter emits multiline definition"; else test-fail "$backend fixture did not exercise multiline definition"; fi
	printf '(include ./shared-formatted.kmk)\n(helper "a" "b" "c")\n' >"$work/scripts/shared-caller.kash"
	invoke ../scripts/shared-caller.kash >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = abc ]; then test-ok "$backend kmk-formatted definition includes from Kash"; else test-fail "$backend formatted shared definition"; cat "$work/err"; fi
	printf '(include ./shared-formatted.kmk)\nSHELL = kash\nall :\n\t(helper "a" "b" "c")\n' >"$work/scripts/shared-caller.kmk"
	"${runner[@]}" -C "$work/scripts" -f shared-caller.kmk all >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = abc ]; then test-ok "$backend kmk-formatted definition includes from kmk"; else test-fail "$backend kmk shared definition"; cat "$work/err"; fi
	printf 'broken =\n  (first 1)\n' >"$work/scripts/shared-broken.kmk"
	printf '(include ./shared-broken.kmk)\nprintf %%s $broken\n' >"$work/scripts/shared-broken.kash"
	status=0
	invoke --json ../scripts/shared-broken.kash >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q 'shared-broken.kmk' "$work/out" "$work/err"; then test-ok "$backend multiline include retains source"; else test-fail "$backend multiline include provenance"; fi
	reject PARSE_ERR -l kash -c $'missing =\nprintf bad\n'
	test-step "$backend source context and library boundaries"
	reject SEL_NO_CONTEXT -l kash -c 'printf %s (source-dir)'
	printf 'printf %%s (source-dir)\n' | reject SEL_NO_CONTEXT -l kash -
	reject SEL_NO_CONTEXT -l kash -c 'helper = (kash "printf %s (source-dir)"); (helper)'
	printf '(eval "(source-dir)")\n' >"$work/scripts/generated.kash"
	reject SEL_NO_CONTEXT ../scripts/generated.kash
	reject FEATURE_UNSUP -l kash -c '(include ./library.kash)'
	printf '(include ./library.kash)\n' | reject FEATURE_UNSUP -l kash -
	reject EXPR_INVALID -l kash -c '(include? ./library.kash)'
	reject EXPR_INVALID -l kash -c $'def hidden\n\t(include ./library.kash)\n'
	reject EXPR_INVALID -l kash -c '(if :false (include ./library.kash) :nil)'
	printf 'printf forbidden\n' >"$work/scripts/lib/invalid.kash"
	printf 'touch ./executed\n(include ./lib/invalid.kash)\n' >"$work/scripts/invalid.kash"
	reject PARSE_ERR ../scripts/invalid.kash
	printf 'value = 1\n' >"$work/scripts/lib/repeated.kash"
	printf 'touch ./executed\n(include ./lib/repeated.kash)\n(include ./lib/repeated.kash)\n' >"$work/scripts/repeated.kash"
	reject DEF_INVALID ../scripts/repeated.kash
	printf '(include ./cycle.kash)\n' >"$work/scripts/cycle.kash"
	reject DEP_CYCLE ../scripts/cycle.kash
	printf '(include ./absent.kash)\n' >"$work/scripts/missing.kash"
	reject FS_ERR ../scripts/missing.kash
	printf 'def broken\n\t(first 1)\n' >"$work/scripts/lib/broken.kash"
	printf '(include ./lib/broken.kash)\n(broken)\n' >"$work/scripts/broken.kash"
	status=0
	invoke --json ../scripts/broken.kash >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q '"kind":"include"' "$work/out" "$work/err" && grep -q 'lib/broken.kash' "$work/out" "$work/err"; then test-ok "$backend runtime failure retains included source and frame"; else test-fail "$backend runtime include provenance"; cat "$work/out" "$work/err"; fi
	test-step "$backend includes preserve execution authority"
	"${runner[@]}" do run -C "$work/caller" --allow-run ../scripts/main.kash >"$work/out" 2>"$work/err"
	printf '%s\n%s\n%s' "$work/scripts/lib" "$work/scripts/lib/nested" "$work/scripts" >"$work/expected"
	if cmp -s "$work/expected" "$work/out"; then test-ok "$backend source-dir needs no filesystem grant"; else test-fail "$backend source-dir unnecessarily needs read authority"; fi
	status=0
	"${runner[@]}" do run -C "$work/caller" --allow-read ../scripts/main.kash >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend libraries cannot grant process authority"; else test-fail "$backend authority ($status)"; cat "$work/err"; fi
done
test-end
