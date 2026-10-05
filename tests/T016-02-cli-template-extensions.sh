#!/usr/bin/env bash
# Spec: docs/spec/030-template-extensions.md — labels, matches, inference and host comments
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T016-02 template extensions"
test-step "build native and WASM runners"
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null

match_source='@match("./posts/demo.md")
@case(./posts/{slug:*}.md)
@(slug)
@else
other
@end(match)
'
hash_source='# @if(:true)
yes
# @end(if)
'
powershell_source='<# @if(:true) #>
yes
<# @end(if) #>
'
batch_source='REM @if(:true)
yes
:: @end(if)
'
list_loop='(render (cat "@for([item] xs)\n" "@" "(index)" ":" "@" "(key)" ":" "@" "(item)" ";\n@end(for)\n") [xs: ["a" "b"]] "plain")'
record_loop='(render (cat "@for([item] xs)\n" "@" "(index)" ":" "@" "(key)" ":" "@" "(item)" ";\n@end(for)\n") [xs: [first: "A" second: "B"]] "plain")'
inline_plain='(render "Hi @if(:true)there@end!" "plain")'
inline_html='(render "a<!-- @if(:true) -->b<!-- @end(if) -->c" "html")'
inline_c='(render "x/* @if(:true) */y/* @end(if) */z" "c")'
inline_powershell='(render "x<# @if(:true) #>y<# @end(if) #>z" "powershell")'
inline_for='(render (cat "<@for([item] xs)" "@" "(item)" "@end(for)>!") [xs: ["x" "y"]] "plain")'
inline_with='(render (cat "x@with([name: " "\"A\"" "])@" "(name)" "@end(with)z") "plain")'
inline_match='(render (cat "x@match(path)@case(./{slug:*}.md)" "@" "(slug)" "@else other@end(match)z") [path: "./a.md"] "plain")'
inline_raw='(render (cat "a@raw " "@" "(name)" "@end(raw)c") "plain")'
trim_markers='(render "left   @-if(:true)-  right  @-end(if)- \r\n\t tail" "plain")'
trim_else='(render "A@if(:false) B@else-  C@end(if)D" "plain")'
format_source='hello @if(:true)yes@end world'
nested_loop='(render (cat "@for([group] groups)\nouter:" "@" "(index)" "[\n@for([item] group)\n" "@" "(index)" ":" "@" "(item)" ",\n@end(for)\n]" "@" "(index)" "\n@end(for)\n") [groups: [["a" "b"] ["c"]]] "plain")'

for host in native wasm; do
	if [ "$host" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	printf '%s' "$format_source" | "${runner[@]}" do fmt --lang template --comment plain >"$TMPDIR/$host.format.out" 2>"$TMPDIR/$host.format.err"
	if [ "$(cat "$TMPDIR/$host.format.out")" = 'hello @if(:true)yes@end(if) world' ]; then
		test-ok "$host formats template directives canonically"
	else
		test-fail "$host template format: $(cat "$TMPDIR/$host.format.err") $(cat "$TMPDIR/$host.format.out")"
	fi
	"${runner[@]}" do render --comment plain -c "$match_source" >"$TMPDIR/$host.match.out" 2>"$TMPDIR/$host.match.err"
	if [ "$(cat "$TMPDIR/$host.match.out")" = "demo" ]; then
		test-ok "$host matches a template clause and binds named captures"
	else
		test-fail "$host document match: $(cat "$TMPDIR/$host.match.err")"
	fi
	"${runner[@]}" do render --comment auto -c "$hash_source" >"$TMPDIR/$host.auto.out" 2>"$TMPDIR/$host.auto.err"
	if [ "$(cat "$TMPDIR/$host.auto.out")" = "yes" ]; then
		test-ok "$host infers a unique comment style from content"
	else
		test-fail "$host content style inference: $(cat "$TMPDIR/$host.auto.err")"
	fi
	printf '%s' "$powershell_source" >"$TMPDIR/$host.ps1"
	"${runner[@]}" do render "$TMPDIR/$host.ps1" >"$TMPDIR/$host.ps1.out" 2>"$TMPDIR/$host.ps1.err"
	if [ "$(cat "$TMPDIR/$host.ps1.out")" = "yes" ]; then
		test-ok "$host parses PowerShell block comments"
	else
		test-fail "$host PowerShell comments: $(cat "$TMPDIR/$host.ps1.err")"
	fi
	"${runner[@]}" do render --comment batch -c "$batch_source" >"$TMPDIR/$host.batch.out" 2>"$TMPDIR/$host.batch.err"
	if [ "$(cat "$TMPDIR/$host.batch.out")" = "yes" ]; then
		test-ok "$host parses batch comment directives"
	else
		test-fail "$host batch comments: $(cat "$TMPDIR/$host.batch.err")"
	fi
	"${runner[@]}" do run --lang expr -c "$inline_plain" >"$TMPDIR/$host.inline-plain.out" 2>"$TMPDIR/$host.inline-plain.err"
	if [ "$(cat "$TMPDIR/$host.inline-plain.out")" = '"Hi there!"' ]; then
		test-ok "$host renders plain inline blocks across text boundaries"
	else
		test-fail "$host plain inline block: $(cat "$TMPDIR/$host.inline-plain.err") $(cat "$TMPDIR/$host.inline-plain.out")"
	fi
	"${runner[@]}" do run --lang expr -c "$inline_html" >"$TMPDIR/$host.inline-html.out" 2>"$TMPDIR/$host.inline-html.err"
	if [ "$(cat "$TMPDIR/$host.inline-html.out")" = '"abc"' ]; then
		test-ok "$host renders HTML inline blocks across text boundaries"
	else
		test-fail "$host HTML inline block: $(cat "$TMPDIR/$host.inline-html.err") $(cat "$TMPDIR/$host.inline-html.out")"
	fi
	"${runner[@]}" do run --lang expr -c "$inline_c" >"$TMPDIR/$host.inline-c.out" 2>"$TMPDIR/$host.inline-c.err"
	if [ "$(cat "$TMPDIR/$host.inline-c.out")" = '"xyz"' ]; then
		test-ok "$host renders C inline block comments"
	else
		test-fail "$host C inline block: $(cat "$TMPDIR/$host.inline-c.err") $(cat "$TMPDIR/$host.inline-c.out")"
	fi
	"${runner[@]}" do run --lang expr -c "$inline_powershell" >"$TMPDIR/$host.inline-powershell.out" 2>"$TMPDIR/$host.inline-powershell.err"
	if [ "$(cat "$TMPDIR/$host.inline-powershell.out")" = '"xyz"' ]; then
		test-ok "$host renders PowerShell inline block comments"
	else
		test-fail "$host PowerShell inline block: $(cat "$TMPDIR/$host.inline-powershell.err") $(cat "$TMPDIR/$host.inline-powershell.out")"
	fi
	for block in for with match raw; do
		value="inline_$block"
		expected=''
		case "$block" in
			for) expected='"<xy>!"' ;;
			with) expected='"xAz"' ;;
			match) expected='"xaz"' ;;
			raw) expected='"a @(name)c"' ;;
		esac
		"${runner[@]}" do run --lang expr -c "${!value}" >"$TMPDIR/$host.inline-$block.out" 2>"$TMPDIR/$host.inline-$block.err"
		if [ "$(cat "$TMPDIR/$host.inline-$block.out")" = "$expected" ]; then
			test-ok "$host renders inline $block blocks"
		else
			test-fail "$host inline $block block: $(cat "$TMPDIR/$host.inline-$block.err") $(cat "$TMPDIR/$host.inline-$block.out")"
		fi
	done
	"${runner[@]}" do run --lang expr -c "$trim_markers" >"$TMPDIR/$host.trim.out" 2>"$TMPDIR/$host.trim.err"
	if [ "$(cat "$TMPDIR/$host.trim.out")" = '"leftrighttail"' ]; then
		test-ok "$host trims adjacent ASCII whitespace across CRLF"
	else
		test-fail "$host trim markers: $(cat "$TMPDIR/$host.trim.err") $(cat "$TMPDIR/$host.trim.out")"
	fi
	"${runner[@]}" do run --lang expr -c "$trim_else" >"$TMPDIR/$host.trim-else.out" 2>"$TMPDIR/$host.trim-else.err"
	if [ "$(cat "$TMPDIR/$host.trim-else.out")" = '"ACD"' ]; then
		test-ok "$host applies right trimming to the selected else branch"
	else
		test-fail "$host else trim: $(cat "$TMPDIR/$host.trim-else.err") $(cat "$TMPDIR/$host.trim-else.out")"
	fi
	"${runner[@]}" do run --lang expr -c "$list_loop" >"$TMPDIR/$host.list-loop.out" 2>"$TMPDIR/$host.list-loop.err"
	if [ "$(cat "$TMPDIR/$host.list-loop.out")" = '"0::a;\n1::b;\n"' ]; then
		test-ok "$host binds loop indices and nil list keys"
	else
		test-fail "$host list loop bindings: $(cat "$TMPDIR/$host.list-loop.err") $(cat "$TMPDIR/$host.list-loop.out")"
	fi
	"${runner[@]}" do run --lang expr -c "$record_loop" >"$TMPDIR/$host.record-loop.out" 2>"$TMPDIR/$host.record-loop.err"
	if [ "$(cat "$TMPDIR/$host.record-loop.out")" = '"0:first:A;\n1:second:B;\n"' ]; then
		test-ok "$host binds record loop keys in source order"
	else
		test-fail "$host record loop bindings: $(cat "$TMPDIR/$host.record-loop.err") $(cat "$TMPDIR/$host.record-loop.out")"
	fi
	"${runner[@]}" do run --lang expr -c "$nested_loop" >"$TMPDIR/$host.nested-loop.out" 2>"$TMPDIR/$host.nested-loop.err"
	if [ "$(cat "$TMPDIR/$host.nested-loop.out")" = '"outer:0[\n0:a,\n1:b,\n]0\nouter:1[\n0:c,\n]1\n"' ]; then
		test-ok "$host restores outer loop indices after nested loops"
	else
		test-fail "$host nested loop bindings: $(cat "$TMPDIR/$host.nested-loop.err") $(cat "$TMPDIR/$host.nested-loop.out")"
	fi
done

test-end
