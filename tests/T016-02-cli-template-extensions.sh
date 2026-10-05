#!/usr/bin/env bash
# Spec: docs/spec/030-template-extensions.md — labels, matches, inference and host comments
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T016-01 template extensions"
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
done

test-end
