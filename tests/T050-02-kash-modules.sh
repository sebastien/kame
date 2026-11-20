#!/usr/bin/env bash
# Spec: docs/spec/050-kash-libraries.md — imported Kash modules
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T050-02 Kash modules"
cli_build
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make -C "$CLI_ROOT" dist-wasm >/dev/null; fi
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work/lib" "$work/caller"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "${KAME_TEST_WASM_WRAPPER:-$CLI_ROOT/dist/kame.js}"); fi

	run_module() {
		"${runner[@]}" do run -C "$work" "$@" >"$work/log" 2>"$work/err"
	}

	expect_error() {
		local code="$1" message="$2" status=0
		shift 2
		run_module "$@" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && grep -Fq "$message" "$work/err"; then
			test-ok "$backend $code includes $message"
		else
			test-fail "$backend expected $code mentioning $message, status=$status: $(cat "$work/err")"
		fi
	}

	cat >"$work/lib/helper.kash" <<'KASH'
def helper ()
	(write! ./helper-result "reached")
KASH
	cat >"$work/lib/main.kash" <<'KASH'
include ./helper.kash
def value = "first"
def unused = fail! "must stay lazy"
def build ()
	helper
	(write! ./origin (str module/dir))
export value, build
export value
KASH
	cat >"$work/Kamefile" <<'KAME'
import ./lib/main as lib
from ./lib/main.kash import build as selected
rule ./out : ./input = lib/value
rule ./with-helper : ./input
	lib/build
	out! ok > $outputs.0
rule ./with-selected : ./input
	selected
	out! selected > $outputs.0
KAME
	printf input >"$work/input"
	"${runner[@]}" -C "$work" -n ./out ./with-helper ./with-selected >"$work/log" 2>"$work/err"
	if [ ! -e "$work/out" ] && [ ! -e "$work/helper-result" ]; then test-ok "$backend dry-run leaves module values and commands unexecuted"; else test-fail "$backend dry-run executed the module"; fi
	"${runner[@]}" -C "$work" ./out >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/out")" = first ]; then test-ok "$backend only the reached export is demanded"; else test-fail "$backend selected module export"; fi
	before=$(stat -c %y "$work/out")
	"${runner[@]}" -C "$work" ./out >"$work/log" 2>"$work/err"
	if [ "$(stat -c %y "$work/out")" = "$before" ]; then test-ok "$backend unchanged module remains fresh"; else test-fail "$backend unchanged module rebuilt"; fi
	printf 'def helper ()\n\t(write! ./helper-result "changed")\n' >"$work/lib/helper.kash"
	"${runner[@]}" -C "$work" ./out >"$work/log" 2>"$work/err"
	if [ "$(stat -c %y "$work/out")" = "$before" ]; then test-ok "$backend unreached helper does not rebuild"; else test-fail "$backend unreached helper rebuilt"; fi

	"${runner[@]}" -C "$work" ./with-helper ./with-selected >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/helper-result")" = changed ] && [ "$(cat "$work/with-helper")" = ok ] && [ "$(cat "$work/with-selected")" = selected ]; then
		test-ok "$backend qualified and selective imports call the same exported function"
	else
		test-fail "$backend qualified or selective import"
	fi
	before=$(stat -c %y "$work/with-helper")
	"${runner[@]}" -C "$work" ./with-helper >"$work/log" 2>"$work/err"
	if [ "$(stat -c %y "$work/with-helper")" = "$before" ]; then test-ok "$backend reached helper remains fresh"; else test-fail "$backend reached helper rebuilt"; fi
	stamp=$(stat -c %y "$work/lib/helper.kash")
	printf 'def helper ()\n\t(write! ./helper-result "new bytes")\n' >"$work/lib/helper.kash"
	touch -d "$stamp" "$work/lib/helper.kash"
	"${runner[@]}" -C "$work" ./with-helper >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/helper-result")" = 'new bytes' ]; then test-ok "$backend changed helper bytes invalidate with preserved mtime"; else test-fail "$backend reached helper stayed fresh"; fi

	printf 'from ./lib/main.kash import build\ndef call-build () = build ()\n' >"$work/call.km"
	expect_error CAP_DENIED write ./call.km -c '(call-build ())'
	run_module --allow-write=. ./call.km -c '(call-build ())'
	if [ "$(cat "$work/origin")" = "$work/lib" ]; then test-ok "$backend module loading preserves grants and lexical origin"; else test-fail "$backend module lexical origin"; fi
	printf 'import ./main.kash as lib\ndef relative-build () = lib/build ()\n' >"$work/lib/relative.km"
	"${runner[@]}" do run -C "$work/caller" --allow-write=. "$work/lib/relative.km" -c '(relative-build ())' >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/caller/helper-result")" = 'new bytes' ] && [ "$(cat "$work/caller/origin")" = "$work/lib" ]; then test-ok "$backend import is relative to authored source across cwd"; else test-fail "$backend source-relative import"; fi

	printf 'import ./lib/main.kash as lib\nprivate = lib/helper\n' >"$work/import.km"
	expect_error REF_MISSING helper ./import.km --entry private
	printf 'from ./lib/main.kash import helper\ndefault = helper\n' >"$work/private.km"
	expect_error REF_MISSING helper ./private.km --entry default
	printf 'import "./lib/main.kash"\n' >"$work/quoted.km"
	expect_error PARSE_ERR quoted.km ./quoted.km
	printf 'import ${source}\n' >"$work/computed.km"
	expect_error PARSE_ERR computed.km ./computed.km
	printf 'import ./document.kmt\n' >"$work/malformed.km"
	expect_error RES_INVALID document.kmt ./malformed.km
	printf 'import ./lib/main.kash as lib\nimport ./lib/main.kash as lib\n' >"$work/duplicate-alias.km"
	expect_error DEF_INVALID 'duplicate module namespace: lib' ./duplicate-alias.km
	printf 'import ./lib/std.kash\n' >"$work/reserved.km"
	expect_error DEF_INVALID 'unreserved namespace alias: std' ./reserved.km
	cp "$work/lib/main.kash" "$work/lib/std.kash"
	printf 'import ./lib/std.kash as library\nanswer = library/value\n' >"$work/allowed-alias.km"
	run_module ./allowed-alias.km --entry answer
	if [ "$(cat "$work/log")" = '"first"' ]; then test-ok "$backend explicit alias permits a reserved filename stem"; else test-fail "$backend valid alias did not bind the module"; fi
	printf 'from ./lib/main.kash import value\nvalue = 0\n' >"$work/duplicate-definition.km"
	expect_error DEF_INVALID 'duplicate imported binding: value' ./duplicate-definition.km
	printf 'from ./lib/main.kash import value\nfrom ./lib/main.kash import build as value\n' >"$work/duplicate-selective.km"
	expect_error DEF_INVALID 'duplicate imported binding: value' ./duplicate-selective.km
	printf 'export missing\n' >"$work/lib/unknown.kash"
	printf 'import ./lib/unknown.kash\nanswer = unknown/missing\n' >"$work/unknown-export.km"
	expect_error DEF_INVALID 'cannot export unknown definition: missing' ./unknown-export.km --entry answer

	printf 'from ./lib/absent import value\ndefault = value\n' >"$work/missing.km"
	expect_error FS_ERR absent.kash ./missing.km --entry default
	for suffix in km kash; do : >"$work/lib/ambiguous.$suffix"; done
	printf 'from ./lib/ambiguous import value\ndefault = value\n' >"$work/ambiguous.km"
	expect_error RES_INVALID ambiguous.kash ./ambiguous.km --entry default
	if grep -Fq ambiguous.km "$work/err"; then test-ok "$backend ambiguous import names both candidates"; else test-fail "$backend ambiguous import omitted a candidate"; fi

	printf 'def value = "duplicate"\n' >"$work/lib/helper.kash"
	expect_error DEF_INVALID value ./import.km --entry private
	printf 'def helper ()\n\t(write! ./helper-result "new bytes")\n' >"$work/lib/helper.kash"
	stamp=$(stat -c %y "$work/lib/main.kash")
	printf 'def value = "second"\ndef build () = nil\nexport value, build\n' >"$work/lib/main.kash"
	touch -d "$stamp" "$work/lib/main.kash"
	"${runner[@]}" -C "$work" ./out >"$work/log" 2>"$work/err"
	if [ "$(cat "$work/out")" = second ]; then test-ok "$backend changed reached export invalidates with preserved mtime"; else test-fail "$backend changed module stayed fresh"; fi

	printf 'not a definition\n' >"$work/lib/main.kash"
	expect_error DEF_INVALID main.kash ./import.km --entry private
	printf 'import ./loop.kash\n' >"$work/lib/main.kash"
	printf 'import ./main.kash\n' >"$work/lib/loop.kash"
	expect_error DEP_CYCLE loop.kash ./import.km --entry private
	printf 'include ./helper.kash\ndef value = "safe"\nexport value\n' >"$work/lib/main.kash"
	printf 'printf forbidden\n' >"$work/lib/helper.kash"
	expect_error DEF_INVALID helper.kash ./import.km --entry private
	if grep -Fq include "$work/err"; then test-ok "$backend nested module error retains its include frame"; else test-fail "$backend nested module error omitted its include frame"; fi
	printf 'include ./incompatible.km\n' >"$work/lib/main.kash"
	printf 'value = "wrong grammar"\n' >"$work/lib/incompatible.km"
	expect_error RES_INVALID incompatible.km ./import.km --entry private

	printf 'def value = "ONE"\nexport value\n' >"$work/identity-one.kash"
	printf 'def value = "TWO"\nexport value\n' >"$work/identity-two.kash"
	printf 'import ./identity-one.kash as child\ndef value = child/value\nexport value\n' >"$work/identity-a.kash"
	printf 'import ./identity-two.kash as child\ndef value = child/value\nexport value\n' >"$work/identity-b.kash"
	printf 'import ./identity-a.kash as a\nimport ./identity-b.kash as b\nfirst = a/value\nsecond = b/value\n' >"$work/identity.km"
	run_module ./identity.km -c '(out! first)' -c '(out! second)'
	if [ "$(cat "$work/log")" = ONETWO ]; then test-ok "$backend source-relative nested imports have distinct identities"; else test-fail "$backend module loader state alias"; fi

	cat >"$work/lib/from-kame.km" <<'KAME'
def greet who = str "hello " who
export greet
rule ./unregistered = "forbidden"
KAME
	cat >"$work/from-kame.kash" <<'KASH'
import ./lib/from-kame.km as lib
out! (lib/greet "Kash")
KASH
	cat >"$work/from-kame.km" <<'KAME'
import ./lib/from-kame.km as lib
task default
	out! (lib/greet "Kame")
KAME
	run_module ./from-kame.kash
	if [ "$(cat "$work/log")" = 'hello Kash' ]; then test-ok "$backend Kash can import a Kame function"; else test-fail "$backend cross-language import"; fi
	"${runner[@]}" do list --json -C "$work" -f from-kame.km >"$work/log" 2>"$work/err"
	if jq -e '. == ["default"]' "$work/log" >/dev/null; then test-ok "$backend import registers only the caller's targets"; else test-fail "$backend import registered the library's rule"; fi
done
test-end
