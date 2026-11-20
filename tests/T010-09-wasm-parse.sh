#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper
# Spec: docs/spec/004-language.md — parse
# Spec: docs/spec/013-tests.md — T010-09-wasm-parse
# Spec: docs/spec/055-unified-kame-kash-language.md — context-selected grammar
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-09 wasm parse"
cd "$CLI_ROOT"

test-step "build artifacts"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
cli_build
wasm_wrapper="${KAME_TEST_WASM_WRAPPER:-$CLI_ROOT/dist/kame.js}"

fixtures="$CLI_ROOT/src/go/kame/cmd/kame/testdata/parse"
work="$TMPDIR/wasm-parse"
mkdir -p "$work"

test-step "parse output matches native byte-for-byte"
for pair in "expr expr" "template template" "km rule" "kash action"; do
	# shellcheck disable=SC2086
	set -- $pair
	lang="$1"
	file="$2"
	set +e
	node "$wasm_wrapper" do parse --json --lang "$lang" "$fixtures/$file.km" >"$work/wasm.json" 2>"$work/wasm.err"
	wasm_status=$?
	"$CLI_BIN" do parse --json --lang "$lang" "$fixtures/$file.km" >"$work/native.json" 2>"$work/native.err"
	native_status=$?
	set -e
	if [ "$wasm_status" = "$native_status" ] && cmp -s "$work/wasm.json" "$work/native.json"; then
		test-ok "parity: $lang"
	else
		test-fail "parity: $lang (wasm=$wasm_status native=$native_status)"
	fi
done

test-step "foreign recipes expose the document AST and authored raw text"
recipe=$'SHELL = "/bin/sh"\ntask report\n\t@raw\n\tprintf "%s" \'@{missing}\'\n\t@end raw'
node "$wasm_wrapper" do parse --json --lang km -c "$recipe" >"$work/wasm.json"
"$CLI_BIN" do parse --json --lang km -c "$recipe" >"$work/native.json"
if cmp -s "$work/wasm.json" "$work/native.json" &&
	jq -e '.ast.items[1].rule | .bodyDocument.kind == "template" and
		([.bodyDocument.body | .. | objects | .text? // empty] | any(contains("@{missing}"))) and
		all(.body[]; has("template") | not)' "$work/native.json" >/dev/null; then
	test-ok "raw recipe text appears in the document tree"
else
	test-fail "foreign recipe document AST differs"
fi

test-step "template document diagnostics match native"
template=$'@if true\nhello\n'
set +e
node "$wasm_wrapper" do parse --json --lang template -c "$template" >"$work/wasm.json"
wasm_status=$?
"$CLI_BIN" do parse --json --lang template -c "$template" >"$work/native.json"
native_status=$?
set -e
if [ "$wasm_status" = 1 ] && [ "$native_status" = 1 ] &&
	cmp -s "$work/wasm.json" "$work/native.json" &&
	jq -e '.diagnostic | .code == "TPL_BLOCK" and .span == {start: 0, end: 8}' "$work/native.json" >/dev/null; then
	test-ok "unterminated template directive retains its authored span"
else
	test-fail "template diagnostic: wasm=$wasm_status native=$native_status"
fi

test-step "context selects the grammar independently of PATH"
mkdir -p "$work/bin"
printf '#!/bin/sh\nexit 99\n' >"$work/bin/report"
chmod +x "$work/bin/report"
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	for pair in "expr application" "kash command"; do
		read -r lang kind <<<"$pair"
		args=(--lang "$lang" -c 'report value')
		"${runner[@]}" do parse --json "${args[@]}" >"$work/$backend-$lang.json"
		PATH="$work/bin:$PATH" "${runner[@]}" do parse --json "${args[@]}" >"$work/with-tool.json"
		"${runner[@]}" do fmt "${args[@]}" >"$work/fmt"
		PATH="$work/bin:$PATH" "${runner[@]}" do fmt "${args[@]}" >"$work/fmt-with-tool"
		if jq -e --arg kind "$kind" '(if .ast.kind == "script" then .ast.items[0].kind else .ast.kind end) == $kind' "$work/$backend-$lang.json" >/dev/null &&
			cmp -s "$work/$backend-$lang.json" "$work/with-tool.json" &&
			cmp -s "$work/fmt" "$work/fmt-with-tool"; then
			test-ok "$backend $lang grammar and formatting are independent of the executable"
		else
			test-fail "$backend $lang context or PATH independence"
		fi
	done
done

test-step "bindings and imported source leave grammar and formatting unchanged"
module="$work/dependency.kash"
source="$work/context.kash"
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	for variant in baseline lexical import; do
		binding=filler
		if [ "$variant" != baseline ]; then binding=report; fi
		printf 'def member arg = arg\nexport member\n' >"$module"
		if [ "$variant" = import ]; then printf 'def [\n' >"$module"; fi
		cat >"$source" <<KASH
import ./dependency.kash as vendor
def $binding arg = arg
let chosen = report value
vendor/member value
report value
KASH
		"${runner[@]}" do parse --json --lang kash "$source" >"$work/context-$variant.json"
		"${runner[@]}" do fmt --lang kash "$source" >"$work/context-$variant.kash"
	done
	if jq -e '.ast.items | .[-3].definition.expression.items[0].kind == "application" and
		(.[-2:] | map(.kind) == ["command", "command"])' "$work/context-baseline.json" >/dev/null &&
		cmp -s <(jq -c '.ast.items[-3:]' "$work/context-baseline.json") <(jq -c '.ast.items[-3:]' "$work/context-lexical.json") &&
		cmp -s <(tail -n 3 "$work/context-baseline.kash") <(tail -n 3 "$work/context-lexical.kash") &&
		cmp -s "$work/context-lexical.json" "$work/context-import.json" &&
		cmp -s "$work/context-lexical.kash" "$work/context-import.kash"; then
		test-ok "$backend parses and formats independently of lexical and imported bindings"
	else
		test-fail "$backend changed command or application grammar with a binding or import"
	fi
done

test-step "parse reports errors in the document and exits 1"
set +e
node "$wasm_wrapper" do parse --json --lang expr "$fixtures/invalid-expr.km" >"$work/wasm.json" 2>"$work/wasm.err"
wasm_status=$?
"$CLI_BIN" do parse --json --lang expr "$fixtures/invalid-expr.km" >"$work/native.json" 2>"$work/native.err"
native_status=$?
set -e
if [ "$wasm_status" = 1 ] && [ "$native_status" = 1 ] && cmp -s "$work/wasm.json" "$work/native.json"; then
	test-ok "invalid expression matches native and exits 1"
else
	test-fail "invalid expression: wasm=$wasm_status native=$native_status"
fi

test-step "parse reads stdin and validates options"
node "$wasm_wrapper" do parse --json --lang km <"$fixtures/rule.km" >"$work/wasm.json"
"$CLI_BIN" do parse --json --lang km <"$fixtures/rule.km" >"$work/native.json"
if cmp -s "$work/wasm.json" "$work/native.json"; then
	test-ok "stdin parse matches native"
else
	test-fail "stdin parse differs"
fi
set +e
node "$wasm_wrapper" do parse >"$work/out" 2>"$work/err"
missing_status=$?
node "$wasm_wrapper" do parse --lang nope >"$work/out" 2>"$work/err"
invalid_status=$?
set -e
if [ "$missing_status" = 2 ] && [ "$invalid_status" = 2 ]; then
	test-ok "missing and invalid --lang exit 2"
else
	test-fail "option statuses: missing=$missing_status invalid=$invalid_status"
fi

test-end
