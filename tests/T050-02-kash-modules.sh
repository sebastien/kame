#!/usr/bin/env bash
# Spec: docs/spec/050-kash-libraries.md — authored Kash module values
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T050-02 Kash module values"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work/lib"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	cat >"$work/lib/helper.kmk" <<'KASH'
def helper
	(write! ./helper-result "reached")
KASH
	cat >"$work/lib/main.kash" <<'KASH'
(include ./helper.kmk)
value = "first"
unused = (fail! "must stay lazy")
def build
	(helper)
	(write! ./origin (source-dir))
KASH
	cat >"$work/Kamefile" <<'KAME'
SHELL = kash
module = (kash ./lib/main.kash)
./out : ./input
	(write! ./out (get "value" module))
KAME
	printf input >"$work/input"
	"${runner[@]}" -C "$work" -n ./out >"$work/log" 2>"$work/err"
	if [ ! -e "$work/out" ]; then test-ok "$backend dry-run does not execute module bodies"; else test-fail "$backend dry-run executed module body"; fi
	"${runner[@]}" -C "$work" ./out >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/out")" = first ]; then test-ok "$backend get demands only selected field"; else test-fail "$backend module get"; fi
	before=$(stat -c %y "$work/out")
	"${runner[@]}" -C "$work" ./out >"$work/log" 2>"$work/err"
	if [ "$(stat -c %y "$work/out")" = "$before" ]; then test-ok "$backend unchanged module remains fresh"; else test-fail "$backend unchanged module rebuilt"; fi
	printf 'def helper\n\t(write! ./helper-result "changed")\n' >"$work/lib/helper.kmk"
	"${runner[@]}" -C "$work" ./out >"$work/log" 2>"$work/err"
	if [ "$(stat -c %y "$work/out")" = "$before" ]; then test-ok "$backend unreached helper does not rebuild"; else test-fail "$backend unreached helper rebuilt"; fi
	cat >"$work/with-helper.kmk" <<'KAME'
SHELL = kash
module = (kash ./lib/main.kash)
./with-helper : ./input
	(module.build)
	(write! ./with-helper "ok")
KAME
	"${runner[@]}" -C "$work" -f with-helper.kmk -n ./with-helper >"$work/log" 2>"$work/err"
	if [ ! -e "$work/helper-result" ] && [ ! -e "$work/with-helper" ]; then test-ok "$backend dry-run skips demanded function body"; else test-fail "$backend dry-run ran function body"; fi
	"${runner[@]}" -C "$work" -f with-helper.kmk ./with-helper >"$work/log" 2>"$work/err"
	before=$(stat -c %y "$work/with-helper")
	"${runner[@]}" -C "$work" -f with-helper.kmk ./with-helper >"$work/log" 2>"$work/err"
	if [ "$(stat -c %y "$work/with-helper")" = "$before" ]; then test-ok "$backend reached helper remains fresh"; else test-fail "$backend reached helper rebuilt"; fi
	printf 'def helper\n\t(write! ./helper-result "new bytes")\n' >"$work/lib/helper.kmk"
	"${runner[@]}" -C "$work" -f with-helper.kmk ./with-helper >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/helper-result")" = 'new bytes' ]; then test-ok "$backend reached helper change invalidates"; else test-fail "$backend reached helper stayed fresh"; fi
	cat >"$work/call.km" <<'KAME'
module = (kash ./lib/main.kash)
(module.build)
KAME
	status=0
	"${runner[@]}" do run -C "$work" ./call.km >"$work/log" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/err"; then test-ok "$backend source loading does not grant writes"; else test-fail "$backend module widened grants"; fi
	"${runner[@]}" do run -C "$work" --allow-write=. ./call.km >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/helper-result")" = 'new bytes' ] && [ "$(cat "$work/origin")" = "$work/lib" ]; then test-ok "$backend nested helper and source origin"; else test-fail "$backend nested helper or origin"; fi
	mkdir -p "$work/caller"
	printf 'module = (kash ./main.kash)\n((get "build" module))\n' >"$work/lib/relative.km"
	"${runner[@]}" do run -C "$work/caller" --allow-write=. "$work/lib/relative.km" >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/caller/helper-result")" = 'new bytes' ] && [ "$(cat "$work/caller/origin")" = "$work/lib" ]; then test-ok "$backend get calls field relative to authored source across cwd"; else test-fail "$backend get function or source-relative load"; fi
	printf '(kash ./lib/main.txt)\n' >"$work/malformed.km"
	status=0
	"${runner[@]}" do run -C "$work" ./malformed.km >"$work/log" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q EXPR_INVALID "$work/err"; then test-ok "$backend malformed module path rejected"; else test-fail "$backend malformed module path accepted"; fi
	cat >"$work/missing.km" <<'KAME'
module = (kash ./lib/main.kash)
(out! (get "helper" "absent" module))
KAME
	"${runner[@]}" do run -C "$work" ./missing.km >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/log")" = absent ]; then test-ok "$backend nested helpers are not fields"; else test-fail "$backend helper exposed as field"; fi
	printf 'module = (kash ./lib/main.kash)\n(out! module.helper)\n' >"$work/field.km"
	status=0
	"${runner[@]}" do run -C "$work" ./field.km >"$work/log" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q REF_MISSING "$work/err"; then test-ok "$backend missing dotted field"; else test-fail "$backend missing dotted field accepted"; fi
	printf 'module = (kash ./lib/main.kash)\n(out! (set "value" "x" module))\n' >"$work/record.km"
	status=0
	"${runner[@]}" do run -C "$work" ./record.km >"$work/log" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q EXPR_INVALID "$work/err"; then test-ok "$backend module is not a record"; else test-fail "$backend module treated as record"; fi
	printf 'value = "duplicate"\n' >"$work/lib/helper.kmk"
	status=0
	"${runner[@]}" do run -C "$work" ./missing.km >"$work/log" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q DEF_INVALID "$work/err"; then test-ok "$backend duplicate definition rejected"; else test-fail "$backend duplicate module definition accepted"; fi
	printf 'def helper\n\t(write! ./helper-result "new bytes")\n' >"$work/lib/helper.kmk"
	touch -r "$work/lib/main.kash" "$work/old-mtime"
	printf 'value = "second"\n' >"$work/lib/main.kash"
	touch -r "$work/old-mtime" "$work/lib/main.kash"
	"${runner[@]}" -C "$work" ./out >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/out")" = second ]; then test-ok "$backend changed bytes with preserved mtime invalidate"; else test-fail "$backend changed module stayed fresh"; fi
	printf 'not a definition\n' >"$work/lib/main.kash"
	status=0
	"${runner[@]}" do run -C "$work" ./missing.km >"$work/log" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q PARSE_ERR "$work/err" && grep -q main.kash "$work/err"; then test-ok "$backend invalid module rejected at load"; else test-fail "$backend invalid module accepted"; fi
	printf '(include ./loop.kash)\n' >"$work/lib/main.kash"
	printf '(include ./main.kash)\n' >"$work/lib/loop.kash"
	status=0
	"${runner[@]}" do run -C "$work" ./missing.km >"$work/log" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q DEP_CYCLE "$work/err"; then test-ok "$backend lexical include cycle rejected"; else test-fail "$backend module include cycle"; fi
	printf '(include ./helper.kmk)\nvalue = "safe"\n' >"$work/lib/main.kash"
	printf 'printf forbidden\n' >"$work/lib/helper.kmk"
	status=0
	"${runner[@]}" do run -C "$work" ./missing.km >"$work/log" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && grep -q PARSE_ERR "$work/err" && grep -q helper.kmk "$work/err" && grep -q include "$work/err"; then test-ok "$backend nested parse error retains include frame"; else test-fail "$backend nested parse diagnostic"; fi
	printf 'value = "ONE"\n' >"$work/identity-one.kash"
	printf 'value = "TWO"\n' >"$work/identity-two.kash"
	printf 'child = (kash ./identity-one.kash)\n' >"$work/identity-a.kash"
	printf 'child = (kash ./identity-two.kash)\n' >"$work/identity-b.kash"
	printf 'a = (kash ./identity-a.kash)\nb = (kash ./identity-b.kash)\n(out! a.child.value)\n(out! b.child.value)\n' >"$work/identity.km"
	"${runner[@]}" do run -C "$work" ./identity.km >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/log")" = ONETWO ]; then test-ok "$backend module source spans have distinct loader states"; else test-fail "$backend module loader state alias"; fi
	printf 'value = x\n' >"$work/scope.kash"
	for extra in '' 'n (kash ./identity-one.kash)'; do
		printf '(let [m (kash ./scope.kash) %s x "later"] (out! m.value))\n' "$extra" >"$work/scope.km"
		"${runner[@]}" do run -C "$work" ./scope.km >"$work/log" 2>"$work/err"
		if [ "$(cat "$work/log")" = later ]; then test-ok "$backend module retains resumed lexical scope ($extra)"; else test-fail "$backend module retained abandoned scope"; fi
	done
done
test-end
