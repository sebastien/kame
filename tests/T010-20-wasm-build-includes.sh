#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md, docs/spec/010-wasm.md — build source includes
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T010-20 build includes and authored diagnostics"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/build-includes"
mkdir -p "$work/project/nested"
cat >"$work/project/Makefile.kmk" <<'KMK'
include ./nested/values.kmk
include ./nested/rules.kmk

default : leaf
	@(out "@(message)\n")
KMK
printf 'include ./base.kmk\nmessage = (cat greeting " include")\n' >"$work/project/nested/values.kmk"
printf 'greeting = "nested"\n' >"$work/project/nested/base.kmk"
cat >"$work/project/nested/rules.kmk" <<'KMK'
leaf :
	@(out "leaf\n")
./artifact.txt :
	@(yield message)
tooled :
	@(x/sh) -c true
broken :
	@(out missing)
KMK

compare() {
	local native_status=0 wasm_status=0
	"$CLI_BIN" "$@" >"$work/native.out" 2>"$work/native.err" || native_status=$?
	node "$CLI_ROOT/dist/kame.js" "$@" >"$work/wasm.out" 2>"$work/wasm.err" || wasm_status=$?
	if [ "$native_status" = 0 ] && [ "$wasm_status" = 0 ] && cmp -s "$work/native.out" "$work/wasm.out"; then
		test-ok "source parity: $*"
	else
		test-fail "source parity: $* (native=$native_status wasm=$wasm_status)"
		cat "$work/native.err" "$work/wasm.err"
	fi
}
reject() {
	local expected="$1"
	shift
	local native_status=0 wasm_status=0
	"$CLI_BIN" "$@" >"$work/native.out" 2>"$work/native.err" || native_status=$?
	node "$CLI_ROOT/dist/kame.js" "$@" >"$work/wasm.out" 2>"$work/wasm.err" || wasm_status=$?
	if [ "$native_status" = 1 ] && [ "$wasm_status" = 1 ] && [ ! -s "$work/native.out" ] && [ ! -s "$work/wasm.out" ] && grep -q "$expected" "$work/native.err" && grep -q "$expected" "$work/wasm.err"; then
		test-ok "rejection parity: $expected"
	else
		test-fail "rejection parity: $expected (native=$native_status wasm=$wasm_status)"
		cat "$work/native.err" "$work/wasm.err"
	fi
}

test-step "discovery and every inspection surface expand nested includes"
compare -C "$work/project" default
compare -C "$work/project" -f Makefile.kmk default
compare do plan -C "$work/project" -f Makefile.kmk default
compare do inputs -C "$work/project" -f Makefile.kmk default
compare do outputs -C "$work/project" -f Makefile.kmk default
compare do span -C "$work/project" -f Makefile.kmk default
compare do tools -C "$work/project" -f Makefile.kmk
compare do tools check -C "$work/project" -f Makefile.kmk tooled
compare do cat -C "$work/project" -f Makefile.kmk message
compare do cat -C "$work/project" -f Makefile.kmk ./artifact.txt

test-step "relative -C labels and include paths resolve from original cwd"
(
	cd "$work"
	compare -C project default
	compare do plan -C project -f Makefile.kmk default
)

test-step "included evaluation diagnostics retain authored source and spans"
reject REF_MISSING do cat -C "$work/project" -f Makefile.kmk broken
if cmp -s "$work/native.err" "$work/wasm.err"; then test-ok "included evaluation diagnostic bytes match"; else
	test-fail "included evaluation diagnostic differs"
	diff -u "$work/native.err" "$work/wasm.err" || true
fi

printf 'include ./nested/cycle.kmk\n' >"$work/project/cycle.kmk"
printf 'include ../cycle.kmk\n' >"$work/project/nested/cycle.kmk"
printf 'include ./absent.kmk\n' >"$work/project/missing.kmk"
printf 'include ./nested/invalid.kmk\n' >"$work/project/invalid.kmk"
printf 'name = [\n' >"$work/project/nested/invalid.kmk"
printf 'include ./nested/duplicate.kmk\ninclude ./nested/duplicate.kmk\n' >"$work/project/duplicate.kmk"
printf 'name = "duplicate"\n' >"$work/project/nested/duplicate.kmk"
printf 'include ./empty.kmk\ninclude ./empty.kmk\ndefault :\n\t@(out "ok")\n' >"$work/project/repeated.kmk"
: >"$work/project/empty.kmk"
test-step "include preflight rejects cycles, missing and malformed sources, and duplicates"
reject DEP_CYCLE -C "$work/project" -f cycle.kmk
reject FS_ERR do plan -C "$work/project" -f missing.kmk default
reject PARSE_ERR do plan -C "$work/project" -f invalid.kmk default
if cmp -s "$work/native.err" "$work/wasm.err"; then test-ok "included parse diagnostic bytes match"; else
	test-fail "included parse diagnostic differs"
	diff -u "$work/native.err" "$work/wasm.err" || true
fi
reject DEF_INVALID do plan -C "$work/project" -f duplicate.kmk default
compare -C "$work/project" -f repeated.kmk default
reject FEATURE_UNSUP do plan -c 'include ./unused.kmk' default

test-step "JSON diagnostics preserve the complete authored diagnostic list"
for file in invalid.kmk duplicate.kmk; do
	native_status=0
	wasm_status=0
	"$CLI_BIN" do plan --json -C "$work/project" -f "$file" default >"$work/native.json" 2>"$work/native.err" || native_status=$?
	node "$CLI_ROOT/dist/kame.js" do plan --json -C "$work/project" -f "$file" default >"$work/wasm.json" 2>"$work/wasm.err" || wasm_status=$?
	jq -sS . "$work/native.json" >"$work/native.canonical"
	jq -sS . "$work/wasm.json" >"$work/wasm.canonical"
	if [ "$native_status" = 1 ] && [ "$wasm_status" = 1 ] && [ ! -s "$work/native.err" ] && [ ! -s "$work/wasm.err" ] && cmp -s "$work/native.canonical" "$work/wasm.canonical"; then test-ok "included JSON diagnostic parity: $file"; else
		test-fail "included JSON diagnostic parity: $file"
		diff -u "$work/native.canonical" "$work/wasm.canonical" || true
	fi
done

test-end
