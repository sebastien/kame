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
nested_loop='(render (cat "@for([group] groups)\nouter:" "@" "(index)" "[\n@for([item] group)\n" "@" "(index)" ":" "@" "(item)" ",\n@end(for)\n]" "@" "(index)" "\n@end(for)\n") [groups: [["a" "b"] ["c"]]] "plain")'

for host in native wasm; do
	if [ "$host" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
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
