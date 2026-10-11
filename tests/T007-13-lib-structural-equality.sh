#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — binary and recursive equality
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T007-13 structural equality"
test-step "build native and WASM artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
work="$TMPDIR/equality"
mkdir -p "$work"
printf '\0\377\r\n' >"$work/first"
cp "$work/first" "$work/same"
printf '\0\376\r\n' >"$work/different"
printf '\0\377\r' >"$work/short"
cat >"$work/bytes.km" <<'KM'
left = (read "./first")
same = (read "./same")
different = (read "./different")
short = (read "./short")
result = [(eq left same) (is left same) (ne left different) (ne left short) (ne left "") (eq [left [data: left]] [same [data: same]]) (count (filter [left different same] left))]
KM
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local want="$1"; shift
		if timeout 20 "${runner[@]}" "$@" >"$work/out" 2>"$work/err" && [ "$(cat "$work/out")" = "$want" ]; then test-ok "$backend equality"; else test-fail "$backend equality: $*"; cat "$work/out" "$work/err"; fi
	}
	reject() {
		local status=0
		timeout 20 "${runner[@]}" do run -c "$1" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q EXPR_INVALID "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects unsupported equality"; else test-fail "$backend equality rejection: $1"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend ordered lists, unordered records and kind sensitivity"
	for op in eq is '=' '=='; do
		check ':true' do run --lang expr -c "($op [1 [2 :nil] [a: 3 b: [4]]] [1.0 [2 :nil] [b: [4] a: 3]])"
		check ':false' do run --lang expr -c "($op [1 2] [2 1])"
		check ':false' do run --lang expr -c "($op [a: 1] [b: 1])"
		check ':false' do run --lang expr -c "($op [] (parse-json \"{}\"))"
	done
	for op in ne '!='; do check ':true' do run --lang expr -c "($op [1] [\"1\"])"; done
	check ':true' do run --lang expr -c '(eq (parse-json "{}") (parse-json "{}"))'
	check '[[a: 1 b: 2] [b: 2 a: 1]]' do run --lang expr -c '(filter [[a: 1 b: 2] [a: 2] [b: 2 a: 1]] [b: 2 a: 1])'
	check '[[2]]' do run --lang expr -c '(filter-out [[1] [2] [1.0]] [1])'
	check ':false' do run --lang expr -c '(eq 9007199254740992 9007199254740993)'
	check ':false' do run --lang expr -c '(eq 9007199254740993 9007199254740992.0)'
	check ':true' do run --lang expr -c '(eq 9007199254740992 9007199254740992.0)'
	check ':false' do run --lang expr -c '(eq 9223372036854775807 9223372036854775808.0)'
	check ':true' do run --lang expr -c '(eq -9223372036854775808 -9223372036854775808.0)'
	check ':false' do run --lang expr -c '(eq :nil :false)'
	check '[:true :true :true :true :true :true 2]' do run --allow-read -C "$work" bytes.km result
	check '[true,false]' do run --lang kash -c '(out [(eq [1] [1.0]) (is [1] [2])])'
	test-step "$backend opaque values are errors even behind mismatches"
	for text in '(eq count count)' '(ne count :nil)' '(eq [0 count] [1])' '(is [] [count])' '(eq [f: count] [other: 1])' '(eq ./a/{name} "a")' '(eq (resource "mem://test/a") :nil)' '(filter [[count]] [])' '(eq [([x] x)] [])' '(lt [] [])' '(eq [1] [1] [1])'; do reject "$text"; done
done
test-end
