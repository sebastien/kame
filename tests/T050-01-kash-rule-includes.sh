#!/usr/bin/env bash
# Spec: docs/spec/050-kash-libraries.md — flat Kash library includes
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T050-01 Kash rule includes"
cli_build
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make -C "$CLI_ROOT" dist-wasm >/dev/null; fi
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work/lib"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "${KAME_TEST_WASM_WRAPPER:-$CLI_ROOT/dist/kame.js}"); fi
	cat >"$work/lib/helper.kash" <<'KASH'
def helper ()
	(write! ./out "ok")
	(write! ./source (str module/dir))
KASH
	cat >"$work/lib/main.kash" <<'KASH'
include ./helper.kash
def build ()
	helper
KASH
	printf 'def second ()\n\t(write! ./second "ok")\n' >"$work/lib/second.ksh"
	cat >"$work/Kamefile" <<'KAME'
include ./lib/main.kash
if true
	include ./lib/second.ksh
else
	include ./absent.kash
include? ./absent.kash
rule ./out : ./input
	build
	second
KAME
	printf input >"$work/input"
	"${runner[@]}" -C "$work" -n ./out >"$work/dry" 2>"$work/err"
	if [ ! -e "$work/out" ]; then test-ok "$backend dry-run skips library body"; else test-fail "$backend dry-run executed library body"; fi
	"${runner[@]}" -C "$work" ./out >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/out")" = ok ]; then test-ok "$backend nested library callable"; else test-fail "$backend nested library callable"; fi
	if [ "$(cat "$work/second")" = ok ]; then test-ok "$backend conditional ksh include"; else test-fail "$backend conditional ksh include"; fi
	if [ "$(cat "$work/source")" = "$work/lib" ]; then test-ok "$backend helper retains lexical source directory"; else test-fail "$backend helper source directory"; fi
	before=$(stat -c %y "$work/out")
	"${runner[@]}" -C "$work" ./out >"$work/log" 2>"$work/err"
	if [ "$(stat -c %y "$work/out")" = "$before" ]; then test-ok "$backend unchanged library remains fresh"; else test-fail "$backend unchanged library rebuilt"; fi
	printf 'def helper ()\n\t(write! ./out "changed")\n' >"$work/lib/helper.kash"
	"${runner[@]}" -C "$work" ./out >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/out")" = changed ]; then test-ok "$backend changed nested library invalidates output"; else test-fail "$backend changed library was reused"; fi
	cat >"$work/lib/selected.kash" <<'KASH'
include ./unused.kash
include ./used.kash
def selected ()
	used
KASH
	printf 'def unused ()\n\t(write! ./ignored "old")\n' >"$work/lib/unused.kash"
	printf 'def used ()\n\t(write! ./selected-out "old")\n' >"$work/lib/used.kash"
	cat >"$work/Kamefile" <<'KAME'
include ./lib/selected.kash
rule ./selected-out : ./input
	selected
KAME
	"${runner[@]}" -C "$work" ./selected-out >"$work/log" 2>"$work/err"
	before=$(stat -c %y "$work/selected-out")
	stamp=$(stat -c %y "$work/lib/unused.kash")
	printf 'def unused ()\n\t(write! ./ignored "new")\n' >"$work/lib/unused.kash"
	touch -d "$stamp" "$work/lib/unused.kash"
	"${runner[@]}" -C "$work" ./selected-out >"$work/log" 2>"$work/err"
	if [ "$(stat -c %y "$work/selected-out")" = "$before" ]; then test-ok "$backend unreached flat include edit stays fresh"; else test-fail "$backend unreached flat include rebuilt"; fi
	stamp=$(stat -c %y "$work/lib/used.kash")
	printf 'def used ()\n\t(write! ./selected-out "new")\n' >"$work/lib/used.kash"
	touch -d "$stamp" "$work/lib/used.kash"
	"${runner[@]}" -C "$work" ./selected-out >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/selected-out")" = new ]; then test-ok "$backend reached flat include bytes invalidate despite preserved mtime"; else test-fail "$backend reached flat include stayed fresh"; fi
	cat >"$work/Kamefile" <<'KAME'
include ./lib/unused.kash
value = "old"
rule ./root-out : ./input
	(write! ./root-out value)
KAME
	"${runner[@]}" -C "$work" ./root-out >"$work/log" 2>"$work/err"
	stamp=$(stat -c %y "$work/Kamefile")
	sed -i 's/value = "old"/value = "new"/' "$work/Kamefile"
	touch -d "$stamp" "$work/Kamefile"
	"${runner[@]}" -C "$work" ./root-out >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/root-out")" = new ]; then test-ok "$backend reached root definition remains tracked"; else test-fail "$backend root definition stayed stale"; fi
	printf 'def needle = "a"\n' >"$work/lib/predicate.kash"
	cat >"$work/Kamefile" <<'KAME'
include ./lib/predicate.kash
rule ./predicate-out : ./input
	printf "a\nb\n" |(frame :lines) |(filter (eq? item needle) | cat > ./predicate-out) | cat
KAME
	"${runner[@]}" -C "$work" ./predicate-out >"$work/log" 2>"$work/err"
	stamp=$(stat -c %y "$work/lib/predicate.kash")
	printf 'def needle = "b"\n' >"$work/lib/predicate.kash"
	touch -d "$stamp" "$work/lib/predicate.kash"
	"${runner[@]}" -C "$work" ./predicate-out >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/predicate-out")" = b ]; then test-ok "$backend stream predicate source edit invalidates"; else test-fail "$backend stream predicate stayed stale"; fi
	cat >"$work/lib/script.kash" <<'KASH'
def run-script ()
	true
export run-script
KASH
	cat >"$work/Kamefile" <<'KAME'
import ./lib/script.kash
callable = script/run-script
rule ./wrapped : ./input
	$callable
	(write! ./wrapped "ok")
KAME
	"${runner[@]}" -C "$work" ./wrapped >"$work/log" 2>"$work/err"
	before=$(stat -c %y "$work/wrapped")
	"${runner[@]}" -C "$work" ./wrapped >"$work/log" 2>"$work/err"
	if [ "$(stat -c %y "$work/wrapped")" = "$before" ]; then test-ok "$backend retained script callable remains fresh"; else test-fail "$backend retained script callable rebuilt"; fi
	printf 'printf forbidden\n' >"$work/lib/bad.kash"
	printf 'include ./lib/bad.kash\n' >"$work/bad.km"
	status=0
	"${runner[@]}" -C "$work" -f bad.km default >"$work/log" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q DEF_INVALID "$work/err" && grep -q bad.kash "$work/err"; then test-ok "$backend invalid library is located"; else test-fail "$backend invalid library diagnostic"; fi
	printf 'rule ./legal = "included"\n' >"$work/lib/rules.km"
	printf 'include ./lib/rules.km\n' >"$work/value.km"
	"${runner[@]}" do run -C "$work" ./value.km --entry ./legal >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/legal")" = included ]; then test-ok "$backend Kame include registers rules"; else test-fail "$backend Kame include omitted rule"; fi
	status=0
	"${runner[@]}" do run -C "$work" -l km -c 'import ./sample.kash' >"$work/log" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q RES_INVALID "$work/err"; then test-ok "$backend inline relative import requires a file-backed source"; else test-fail "$backend inline relative import source context"; fi
	printf 'def selected = true\n' >"$work/lib/choice.kash"
	printf 'include ./lib/choice.kash\n' >"$work/first.km"
	printf 'if selected\n	include ./lib/chosen.km\n' >"$work/next.km"
	printf 'result = "chosen"\n' >"$work/lib/chosen.km"
	"${runner[@]}" do run -C "$work" ./first.km ./next.km -c '(out! result)' >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/log")" = chosen ]; then test-ok "$backend earlier Kash include feeds later composition"; else test-fail "$backend earlier include selection"; fi
done
test-end
