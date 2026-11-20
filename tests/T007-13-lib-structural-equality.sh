#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — binary and recursive equality
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T007-13 structural equality"
test-step "build native and WASM artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
wasm_runner="$CLI_ROOT/src/js/kame.js"
if [ -z "${KAME_WASM:-}" ]; then
	make dist-wasm >/dev/null
	wasm_runner="$CLI_ROOT/dist/kame.js"
fi
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
result = [left == same, eq? left same, left != different, ne? left short, left != "", [left, {data: left}] == [same, {data: same}], count (filter left [left, different, same])]
KM
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_runner"); fi
	check() {
		local want="$1"; shift
		if timeout 20 "${runner[@]}" "$@" >"$work/out" 2>"$work/err" && [ "$(cat "$work/out")" = "$want" ]; then test-ok "$backend equality"; else test-fail "$backend equality: $*"; cat "$work/out" "$work/err"; fi
	}
	reject() {
		local status=0
		timeout 20 "${runner[@]}" do run --lang expr -c "$1" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q EXPR_INVALID "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects unsupported equality"; else test-fail "$backend equality rejection: $1"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend ordered lists, unordered records and kind sensitivity"
	check true do run --lang expr -c '[1, [2, nil], {a: 3, b: [4]}] == [1.0, [2, nil], {b: [4], a: 3}]'
	check true do run --lang expr -c 'eq? [1, [2, nil], {a: 3, b: [4]}] [1.0, [2, nil], {b: [4], a: 3}]'
	check false do run --lang expr -c '[1, 2] == [2, 1]'
	check false do run --lang expr -c '{a: 1} == {b: 1}'
	check false do run --lang expr -c '[] == {}'
	check true do run --lang expr -c '[1] != ["1"]'
	check true do run --lang expr -c 'ne? [1] ["1"]'
	check true do run --lang expr -c 'eq? (parse-json "{}") {}'
	check '[{a: 1, b: 2}, {b: 2, a: 1}]' do run --lang expr -c 'filter {b: 2, a: 1} [{a: 1, b: 2}, {a: 2}, {b: 2, a: 1}]'
	check '[[2]]' do run --lang expr -c 'filter-out [1] [[1], [2], [1.0]]'
	check false do run --lang expr -c '9007199254740992 == 9007199254740993'
	check false do run --lang expr -c '9007199254740993 == 9007199254740992.0'
	check true do run --lang expr -c '9007199254740992 == 9007199254740992.0'
	check false do run --lang expr -c '9223372036854775807 == 9223372036854775808.0'
	check true do run --lang expr -c '-9223372036854775808 == -9223372036854775808.0'
	check false do run --lang expr -c 'nil == false'
	check '[true, true, true, false]' do run --lang expr -c '[:done == :done, :done != "done", [:+files] == [:+files], :+files == :-files]'
	check '[true, false]' do run --lang expr -c '[in? :item [:item, "item"], in? :item ["item"]]'
	check '[:done, :+files, :-dir]' do run --lang expr -c 'eval (fmt [:done, :+files, :-dir])'
	check false do run --lang expr -c 'string? :done'
	check '"done"' do run --lang expr -c 'str :done'
	reject 'json {state: :done}'
	check yes do render -c $'@match :else\n@case "else"\nwrong\n@case :else\nyes\n@else\nwrong\n@end match'
	check accepted do run --allow-run --lang kash -c $'if $?(false ?).state == :completed\n  out! "accepted"'
	printf '%s\n' 'MODE ?= "default"' 'rule ./mode = fmt MODE' >"$work/mode.km"
	"${runner[@]}" do plan --json -C "$work" -f mode.km --set 'MODE={state: :done, path: ./mode}' ./mode >"$work/out" 2>"$work/err"
	if jq -e '.producers[0].configuration.MODE == {state: {kind: "symbol", data: "done"}, path: {kind: "path", data: "./mode"}}' "$work/out" >/dev/null; then
		test-ok "$backend retains symbol and path kinds in typed configuration JSON"
	else
		test-fail "$backend configuration lost its literal kinds"
		cat "$work/out" "$work/err"
	fi
	check '[true, true, true, true, true, true, 2]' --allow-read -C "$work" -f bytes.km result
	check '[true,false]' do run --lang kash -c 'out! ([[1] == [1.0], [1] == [2]])'

	test-step "$backend opaque values are errors even behind mismatches"
	for text in 'eq? count count' 'ne? count nil' '[0, count] == [1]' '[] == [count]' '{f: count} == {other: 1}' './a/{name:*} == "a"' '(resource "mem://test/a") == nil' 'filter [] [[count]]' '[(x => x)] == []' 'lt? [] []' 'eq? [1] [1] [1]'; do reject "$text"; done
done
test-end
