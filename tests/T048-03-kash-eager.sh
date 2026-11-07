#!/usr/bin/env bash
# Spec: docs/spec/048-kash-practical-scripting.md — eager immutable bindings
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-03 Kash eager immutable bindings"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	invoke() { "${runner[@]}" do run -C "$work" --allow-run --allow-read --allow-write "$@"; }
	reject() {
		local code="$1" status=0
		shift
		invoke "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err"; then test-ok "$backend rejects $code";
		else test-fail "$backend expected $code ($status)"; cat "$work/out" "$work/err"; fi
	}
	cat >"$work/eager.kash" <<'KASH'
unused = $(printf forbidden)
let acquired = $(printf A)
let ignored = (out! "B")
printf "%s%s" $acquired $acquired
def probe item
	let value = $(printf %s $item)
	defer printf "%s" $value
	printf "%s" $value
(probe "C")
(probe "D")
for item in [1 2]
	if (eq? item item)
		let value = $(printf %s $item)
		printf "%s%s" $value $value
runner = (kash "let value = $(printf E); printf %s $value")
(runner)
let callback = ([] "F")
printf "%s" (callback)
let mixed = (list ([] 42) (out! "G") $(printf H))
printf "%s" (count mixed)
KASH
	invoke ./eager.kash >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = BAACCDD1122EFG3 ] && [ ! -s "$work/err" ]; then test-ok "$backend eager ordering, cleanup, fresh scopes and constructed scripts";
	else test-fail "$backend eager runtime"; cat "$work/out" "$work/err"; fi
	"${runner[@]}" do fmt -l kash "$work/eager.kash" >"$work/formatted.kash"
	invoke ./formatted.kash >"$work/formatted.out" 2>"$work/err"
	if cmp -s "$work/out" "$work/formatted.out"; then test-ok "$backend eager formatting roundtrip"; else test-fail "$backend eager formatting"; fi
	"${runner[@]}" do parse --json -l kash -c 'let value = 42' >"$work/ast"
	if grep -q '"eager":true' "$work/ast" && grep -q '"kind":"kash-let"' "$work/ast"; then test-ok "$backend AST preserves eager declaration and statement"; else test-fail "$backend eager AST"; cat "$work/ast"; fi
	reject BINDING_UNINITIALIZED -l kash -c $'value = 42\ndef probe\n\tproxy = value\n\tlet use = proxy ?? "fallback"\n\tlet value = 1\n(probe)'
	reject BINDING_UNINITIALIZED -l kash -c $'printf %s $value\nlet value = 1'
	reject DEF_INVALID -l kash -c 'let value = 1; (def value 2)'
	reject DEF_INVALID --define value=override -l kash -c 'let value = 1'
	reject DEF_INVALID --set value=2 -l kash -c 'let value = 1'
	printf 'let eager = $(touch ./executed)\n' >"$work/library.kash"
	printf '(include ./library.kash)\n' >"$work/include.kash"
	reject EXPR_INVALID ./include.kash
	invoke -n -l kash -c $'printf %s $value\nlet value = $(touch ./executed)\nprintf %s $value' >"$work/out" 2>"$work/err"
	if [ ! -s "$work/out" ] && [ ! -e "$work/executed" ]; then test-ok "$backend dry-run skips eager effects and defers reads"; else test-fail "$backend eager dry-run effects"; fi
	KAME_value=override invoke -l kash -c 'let value = 1; printf %s $value' >"$work/out" 2>"$work/err"
	if [ "$(cat "$work/out")" = 1 ]; then test-ok "$backend environment does not override eager values"; else test-fail "$backend eager environment override"; fi
done
test-end
