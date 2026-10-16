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
(include ./nested/values.kmk)
(include ./nested/rules.kmk)

default : leaf
	@(out! "@{message}\n")
KMK
printf '(include ./base.kmk)\nmessage = (str greeting " include")\n' >"$work/project/nested/values.kmk"
printf 'greeting = "nested"\n' >"$work/project/nested/base.kmk"
cat >"$work/project/nested/rules.kmk" <<'KMK'
leaf :
	@(out! "leaf\n")
./artifact.txt :
	@(yield! message)
tooled :
	@{x/sh} -c true
broken :
	@(out! missing)
KMK

compare() {
	local native_status=0 wasm_status=0
	"$CLI_BIN" "$@" >"$work/native.out" 2>"$work/native.err" || native_status=$?
	if [ "${!#}" = ./artifact.txt ]; then rm -f "$work/project/artifact.txt"; fi
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
compare do plan --json -C "$work/project" -f Makefile.kmk default
compare do inputs --json -C "$work/project" -f Makefile.kmk default
compare do outputs --json -C "$work/project" -f Makefile.kmk default
compare do span -C "$work/project" -f Makefile.kmk default
compare do tools -C "$work/project" -f Makefile.kmk
compare do tools check -C "$work/project" -f Makefile.kmk tooled
compare do cat -C "$work/project" -f Makefile.kmk message
compare do cat -C "$work/project" -f Makefile.kmk ./artifact.txt

test-step "relative -C labels and include paths resolve from original cwd"
(
	cd "$work"
	compare -C project default
	compare do plan --json -C project -f Makefile.kmk default
)

test-step "included evaluation diagnostics retain authored source and spans"
reject REF_MISSING do cat -C "$work/project" -f Makefile.kmk broken
if cmp -s "$work/native.err" "$work/wasm.err"; then test-ok "included evaluation diagnostic bytes match"; else
	test-fail "included evaluation diagnostic differs"
	diff -u "$work/native.err" "$work/wasm.err" || true
fi

printf '(include ./nested/cycle.kmk)\n' >"$work/project/cycle.kmk"
printf '(include ../cycle.kmk)\n' >"$work/project/nested/cycle.kmk"
printf '(include ./absent.kmk)\n' >"$work/project/missing.kmk"
printf '(include ./nested/invalid.kmk)\n' >"$work/project/invalid.kmk"
printf 'name = [\n' >"$work/project/nested/invalid.kmk"
printf '(include ./nested/duplicate.kmk)\n(include ./nested/duplicate.kmk)\n' >"$work/project/duplicate.kmk"
printf 'name = "duplicate"\n' >"$work/project/nested/duplicate.kmk"
printf '(include ./empty.kmk)\n(include ./empty.kmk)\ndefault :\n\t@(out! "ok")\n' >"$work/project/repeated.kmk"
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
reject FEATURE_UNSUP do plan -c '(include ./unused.kmk)' default

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

test-step "duplicate diagnostics distinguish authored declarations and nested include occurrences"
printf '(include ./first.kmk)\n(include ./second.kmk)\n' >"$work/project/nested-conflict.kmk"
printf '(include ./shared.kmk)\n' >"$work/project/first.kmk"
printf '(include ./shared.kmk)\n' >"$work/project/second.kmk"
printf 'SETTING = 1\n' >"$work/project/shared.kmk"
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	for mode in plan run; do
		status=0
		if [ "$mode" = plan ]; then invocation=(do plan --json -C "$work/project" -f nested-conflict.kmk default); else invocation=(do run --json -C "$work/project" ./nested-conflict.kmk default); fi
		"${runner[@]}" "${invocation[@]}" >"$work/conflict.json" 2>"$work/error" || status=$?
		if [ "$status" = 1 ] && [ ! -s "$work/error" ] && jq -e -s 'map(select(.type == "diagnostic"))[0].diagnostic |
			.code == "DEF_INVALID" and .message == "duplicate definition: SETTING" and
			(.source | endswith("/shared.kmk")) and .span == {start:0,end:7} and
			.related[0].source == .source and .related[0].span == .span and
			(.frames | length) == 4 and
			(.frames[0].source | endswith("/second.kmk")) and
			(.frames[2].source | endswith("/first.kmk")) and
			.frames[1].span.start > .frames[3].span.start and (.tips | length) > 0
		' "$work/conflict.json" >/dev/null; then test-ok "$backend $mode retains both inclusion paths"; else test-fail "$backend $mode lost include provenance"; cat "$work/conflict.json" "$work/error"; fi
	done
	status=0
	"${runner[@]}" do run --json --lang km -c 'SETTING = 1' -c 'SETTING = 2' >"$work/conflict.json" 2>"$work/error" || status=$?
	if [ "$status" = 1 ] && jq -e -s 'map(select(.type == "diagnostic"))[0].diagnostic | .source == "<command:2>" and .related[0].source == "<command:1>" and .span == .related[0].span' "$work/conflict.json" >/dev/null; then test-ok "$backend separate fragments retain original declaration"; else test-fail "$backend fragment provenance"; fi
	for format in human plain; do
		status=0
		"${runner[@]}" --color=never --diagnostic-format="$format" do plan -C "$work/project" -f nested-conflict.kmk default >"$work/output" 2>"$work/error" || status=$?
		if [ "$status" = 1 ] && grep -q 'duplicate definition: SETTING' "$work/error" && grep -q 'original declaration' "$work/error" && grep -q 'first.kmk' "$work/error" && grep -q 'second.kmk' "$work/error"; then test-ok "$backend $format renders conflict context"; else test-fail "$backend $format conflict context"; cat "$work/error"; fi
	done
done
printf 'ITEMS ?= []\nITEMS += ["x"]\n' >"$work/project/contributions.kmk"
printf 'ITEMS = []\n(include ./contributions.kmk)\n(include ./contributions.kmk)\ndefault : ; [shell: kash]\n\t(out! (json ITEMS))\n' >"$work/project/consumer.kmk"
compare -C "$work/project" -f consumer.kmk default
if [ "$(cat "$work/native.out")" = '["x","x"]' ]; then test-ok "consumer defaults and repeated contributions retain source semantics"; else test-fail "repeated inclusion was silently deduplicated"; fi

test-end
