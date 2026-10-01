#!/usr/bin/env bash
# Spec: docs/spec/017-kash.md — Standalone source parsing and formatting
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T017-06 Kash source parser and formatter parity"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/kash-source"
mkdir -p "$work"
cat >"$work/source.kash" <<'KASH'
# source comment
name = "World"; count = 42; flag = :true;
(banner WHO) = (cat "Hello, " WHO)
alias = ${name}
values = @([name count])
literal = "$(printf not-executed)"
capture = $(printf "%s" @(banner name) | cat)
:cwd . :timeout @(1) :MODE test printf "%s" $values | cat > "output file"
echo revision=$name # inline comment
printf "%s" "semicolon;in word"; echo escaped\;separator
printf "%s" @(cat
    "multiline;"
    "expression")
echo joined \
    words
// literal-command
KASH

test-step "schema, parser ownership and byte-for-byte backend parity"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" --lang kash "$work/source.kash" >"$work/native.$operation"
	node "$CLI_ROOT/dist/kame.js" do "$operation" --lang kash "$work/source.kash" >"$work/wasm.$operation"
	if cmp -s "$work/native.$operation" "$work/wasm.$operation"; then test-ok "$operation source parity"; else test-fail "$operation source parity"; fi
done
test-step "short language option is identical on both hosts"
for operation in parse fmt; do
	"$CLI_BIN" do "$operation" -l kash "$work/source.kash" >"$work/short.native"
	node "$CLI_ROOT/dist/kame.js" do "$operation" -l kash "$work/source.kash" >"$work/short.wasm"
	if cmp -s "$work/native.$operation" "$work/short.native" && cmp -s "$work/native.$operation" "$work/short.wasm"; then test-ok "$operation -l parity"; else test-fail "$operation -l parity"; fi
done
if grep -q '"command-graph"' "$work/native.parse" && grep -q '"command-setup"' "$work/native.parse" && grep -q '"command-redirection"' "$work/native.parse" && grep -q '"command-capture"' "$work/native.parse"; then test-ok "AST retains delegated process boundaries"; else test-fail "AST omitted process boundaries"; fi
if grep -q '"kind":"command"' "$work/native.parse" && grep -q '"valueKind":"expression"' "$work/native.parse"; then test-ok "commands and strict definitions remain distinct"; else test-fail "definition/command AST boundary"; fi

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	test-step "$backend: stdin, suffix aliases and canonical formatting"
	"${command[@]}" do parse --lang kash <"$work/source.kash" >"$work/stdin"
	if grep -q '"source":"<stdin>"' "$work/stdin"; then test-ok "$backend stdin source identity"; else test-fail "$backend stdin source identity"; fi
	cp "$work/native.fmt" "$work/formatted.ksh"
	"${command[@]}" do fmt --lang kash "$work/formatted.ksh" >"$work/again"
	if cmp -s "$work/native.fmt" "$work/again"; then test-ok "$backend formatting is idempotent"; else test-fail "$backend formatting changed on second pass"; fi
	"${command[@]}" do fmt --lang kash -n "$work/formatted.ksh" >/dev/null
	test-ok "$backend accepts canonical .ksh source"
	"${command[@]}" do fmt --lang kash -i "$work/formatted.ksh"
	if cmp -s "$work/native.fmt" "$work/formatted.ksh"; then test-ok "$backend in-place formatting"; else test-fail "$backend changed canonical source"; fi

	test-step "$backend: parse and format never execute source effects"
	printf 'touch "%s"\nunused = $(touch "%s")\n' "$work/executed" "$work/captured" >"$work/effects.kash"
	"${command[@]}" do parse --lang kash "$work/effects.kash" >/dev/null
	"${command[@]}" do fmt --lang kash "$work/effects.kash" >/dev/null
	if [ ! -e "$work/executed" ] && [ ! -e "$work/captured" ]; then test-ok "$backend performs no process effects"; else test-fail "$backend executed while inspecting"; fi

	test-step "$backend: malformed sources retain diagnostics and are never rewritten"
	for text in 'value = one two' 'value = 1; value = 2' 'printf one |' 'printf one > file | cat' 'printf $(echo one; echo two)' ':cwd . :cwd . printf one' 'if cat { echo yes }' 'printf one &&'; do
		printf '%s\n' "$text" >"$work/invalid.kash"
		cp "$work/invalid.kash" "$work/original"
		parse_status=0
		"${command[@]}" do parse --lang kash "$work/invalid.kash" >"$work/invalid.json" 2>"$work/parse.err" || parse_status=$?
		fmt_status=0
		"${command[@]}" do fmt --lang kash -i "$work/invalid.kash" >"$work/stdout" 2>"$work/fmt.err" || fmt_status=$?
		if [ "$parse_status" = 1 ] && [ "$fmt_status" = 1 ] && grep -q PARSE_ERR "$work/invalid.json" && cmp -s "$work/original" "$work/invalid.kash"; then test-ok "$backend rejects without rewriting: $text"; else test-fail "$backend accepted or rewrote invalid source: $text"; fi
	done
done
test-end
