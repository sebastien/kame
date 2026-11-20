#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md, docs/spec/010-wasm.md — build source includes
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T010-20 build includes and authored diagnostics"
test-step "build artifacts"
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
cli_build
work="$TMPDIR/build-includes"
mkdir -p "$work/project/nested"
cat >"$work/project/Makefile.km" <<'KM'
include ./nested/values.km
include ./nested/rules.km

task default : leaf
	(out! "${message}\n")
KM
printf 'include ./base.km\nmessage = (str greeting " include")\n' >"$work/project/nested/values.km"
printf 'greeting = "nested"\n' >"$work/project/nested/base.km"
cat >"$work/project/nested/rules.km" <<'KM'
task leaf
	(out! "leaf\n")
rule ./artifact.txt = message
task tooled
	x/sh -c true
task broken
	(out! missing)
KM

compare() {
	local native_status=0 wasm_status=0
	"$CLI_BIN" "$@" >"$work/native.out" 2>"$work/native.err" || native_status=$?
	if [ "${!#}" = ./artifact.txt ]; then rm -f "$work/project/artifact.txt"; fi
	node "$wasm_wrapper" "$@" >"$work/wasm.out" 2>"$work/wasm.err" || wasm_status=$?
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
	node "$wasm_wrapper" "$@" >"$work/wasm.out" 2>"$work/wasm.err" || wasm_status=$?
	if [ "$native_status" = 1 ] && [ "$wasm_status" = 1 ] && [ ! -s "$work/native.out" ] && [ ! -s "$work/wasm.out" ] && grep -q "$expected" "$work/native.err" && grep -q "$expected" "$work/wasm.err"; then
		test-ok "rejection parity: $expected"
	else
		test-fail "rejection parity: $expected (native=$native_status wasm=$wasm_status)"
		cat "$work/native.err" "$work/wasm.err"
	fi
}

test-step "Kamefile discovery and explicit rule-language selection"
mkdir -p "$work/kamefile"
printf 'task default\n\t(out! "Kamefile\\n")\n' >"$work/kamefile/Kamefile"
printf 'invalid fallback (\n' >"$work/kamefile/Makefile.km"
compare -C "$work/kamefile"
compare -C "$work/kamefile" default
compare -C "$work/kamefile" -f Kamefile default
compare -C "$work/kamefile" ./Kamefile default
compare do plan --json -C "$work/kamefile" default

test-step "discovery and every inspection surface expand nested includes"
compare -C "$work/project" default
compare -C "$work/project" -f Makefile.km default
compare do plan --json -C "$work/project" -f Makefile.km default
compare do inputs --json -C "$work/project" -f Makefile.km default
compare do outputs --json -C "$work/project" -f Makefile.km default
compare do span -C "$work/project" -f Makefile.km default
compare do tools -C "$work/project" -f Makefile.km
compare do tools check -C "$work/project" -f Makefile.km tooled
compare do cat -C "$work/project" -f Makefile.km message
compare do cat -C "$work/project" -f Makefile.km ./artifact.txt

test-step "relative -C labels and include paths resolve from original cwd"
(
	cd "$work"
	compare -C project default
	compare do plan --json -C project -f Makefile.km default
)

test-step "included evaluation diagnostics retain authored source and spans"
reject REF_MISSING do cat -C "$work/project" -f Makefile.km broken
if cmp -s "$work/native.err" "$work/wasm.err"; then test-ok "included evaluation diagnostic bytes match"; else
	test-fail "included evaluation diagnostic differs"
	diff -u "$work/native.err" "$work/wasm.err" || true
fi

printf 'include ./nested/cycle.km\n' >"$work/project/cycle.km"
printf 'include ../cycle.km\n' >"$work/project/nested/cycle.km"
printf 'include ./absent.km\n' >"$work/project/missing.km"
printf 'include ./nested/invalid.km\n' >"$work/project/invalid.km"
printf 'name = [\n' >"$work/project/nested/invalid.km"
printf 'include ./nested/duplicate.km\ninclude ./nested/duplicate.km\n' >"$work/project/duplicate.km"
printf 'name = "duplicate"\n' >"$work/project/nested/duplicate.km"
printf 'include ./empty.km\ninclude ./empty.km\ntask default\n\t(out! "ok")\n' >"$work/project/repeated.km"
: >"$work/project/empty.km"
test-step "include preflight rejects cycles, missing and malformed sources, and duplicates"
reject DEP_CYCLE -C "$work/project" -f cycle.km
reject FS_ERR do plan -C "$work/project" -f missing.km default
reject PARSE_ERR do plan -C "$work/project" -f invalid.km default
if cmp -s "$work/native.err" "$work/wasm.err"; then test-ok "included parse diagnostic bytes match"; else
	test-fail "included parse diagnostic differs"
	diff -u "$work/native.err" "$work/wasm.err" || true
fi
reject DEF_INVALID do plan -C "$work/project" -f duplicate.km default
compare -C "$work/project" -f repeated.km default
reject RES_INVALID do plan -c 'include ./unused.km' default
compare do plan --json -c "include $work/project/nested/rules.km" tooled

test-step "JSON diagnostics preserve the complete authored diagnostic list"
for file in invalid.km duplicate.km; do
	native_status=0
	wasm_status=0
	"$CLI_BIN" do plan --json -C "$work/project" -f "$file" default >"$work/native.json" 2>"$work/native.err" || native_status=$?
	node "$wasm_wrapper" do plan --json -C "$work/project" -f "$file" default >"$work/wasm.json" 2>"$work/wasm.err" || wasm_status=$?
	jq -sS . "$work/native.json" >"$work/native.canonical"
	jq -sS . "$work/wasm.json" >"$work/wasm.canonical"
	if [ "$native_status" = 1 ] && [ "$wasm_status" = 1 ] && [ ! -s "$work/native.err" ] && [ ! -s "$work/wasm.err" ] && cmp -s "$work/native.canonical" "$work/wasm.canonical"; then test-ok "included JSON diagnostic parity: $file"; else
		test-fail "included JSON diagnostic parity: $file"
		diff -u "$work/native.canonical" "$work/wasm.canonical" || true
	fi
done

test-step "duplicate diagnostics distinguish authored declarations and nested include occurrences"
printf 'include ./first.km\ninclude ./second.km\n' >"$work/project/nested-conflict.km"
printf 'include ./shared.km\n' >"$work/project/first.km"
printf 'include ./shared.km\n' >"$work/project/second.km"
printf 'SETTING = 1\n' >"$work/project/shared.km"
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	for mode in plan run; do
		status=0
		if [ "$mode" = plan ]; then invocation=(do plan --json -C "$work/project" -f nested-conflict.km default); else invocation=(do run --json -C "$work/project" ./nested-conflict.km default); fi
		"${runner[@]}" "${invocation[@]}" >"$work/conflict.json" 2>"$work/error" || status=$?
		if [ "$status" = 1 ] && [ ! -s "$work/error" ] && jq -e -s 'map(select(.type == "diagnostic"))[0].diagnostic |
			.code == "DEF_INVALID" and .message == "duplicate definition: SETTING" and
			(.source | endswith("/shared.km")) and .span == {start:0,end:7} and
			.related[0].source == .source and .related[0].span == .span and
			(.frames | length) == 4 and
			(.frames[0].source | endswith("/second.km")) and
			(.frames[2].source | endswith("/first.km")) and
			.frames[1].span.start > .frames[3].span.start and (.tips | length) > 0
		' "$work/conflict.json" >/dev/null; then test-ok "$backend $mode retains both inclusion paths"; else test-fail "$backend $mode lost include provenance"; cat "$work/conflict.json" "$work/error"; fi
	done
	status=0
	"${runner[@]}" do run --json --lang km -c 'SETTING = 1' -c 'SETTING = 2' >"$work/conflict.json" 2>"$work/error" || status=$?
	if [ "$status" = 1 ] && jq -e -s 'map(select(.type == "diagnostic"))[0].diagnostic | .source == "<command:2>" and .related[0].source == "<command:1>" and .span == .related[0].span' "$work/conflict.json" >/dev/null; then test-ok "$backend separate fragments retain original declaration"; else test-fail "$backend fragment provenance"; fi
	for format in human plain; do
		status=0
		"${runner[@]}" --color=never --diagnostic-format="$format" do plan -C "$work/project" -f nested-conflict.km default >"$work/output" 2>"$work/error" || status=$?
		if [ "$status" = 1 ] && grep -q 'duplicate definition: SETTING' "$work/error" && grep -q 'original declaration' "$work/error" && grep -q 'first.km' "$work/error" && grep -q 'second.km' "$work/error"; then test-ok "$backend $format renders conflict context"; else test-fail "$backend $format conflict context"; cat "$work/error"; fi
	done
done
printf 'ITEMS ?= []\nITEMS += ["x"]\n' >"$work/project/contributions.km"
printf 'ITEMS = []\ninclude ./contributions.km\ninclude ./contributions.km\ntask default\n\t(out! (json ITEMS))\n' >"$work/project/consumer.km"
compare -C "$work/project" -f consumer.km default
if [ "$(cat "$work/native.out")" = '["x","x"]' ]; then test-ok "consumer defaults and repeated contributions retain source semantics"; else test-fail "repeated inclusion was silently deduplicated"; fi

test-end
