#!/usr/bin/env bash
# Spec: docs/spec/005-evaluation.md — first-class operations and syntax-only forms
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T007-12 first-class operation values"
test-step "build native and WASM artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
work="$TMPDIR/operations"
mkdir -p "$work"
printf first >"$work/first"
printf second >"$work/second"
cat >"$work/values.km" <<'KM'
size = count
predicate = exists?
callbacks = [uppercase, count]
record = {size: count}
files = [./first, ./missing, ./second]
selected = (filter predicate files)
contents = (map read selected)
result = [size [1, 2], map count [[1], [2, 3]], reduce str "" ["a", "b"]]
KM
cat >"$work/values.kash" <<'KASH'
def size = count
(out! (size [1, 2]))
(out! (json (map uppercase ["a", "b"])))
KASH
cat >"$work/Makefile.km" <<'KM'
predicate = exists?
files = [./first, ./missing, ./second]
inputs = (filter predicate files)
task default : ${inputs}
	(out! (json (map uppercase ["a", "b"])))
KM
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	check() {
		local want="$1"; shift
		if timeout 20 "${runner[@]}" "$@" >"$work/out" 2>"$work/err" && [ "$(cat "$work/out")" = "$want" ]; then test-ok "$backend operation value"; else test-fail "$backend operation value: $*"; cat "$work/out" "$work/err"; fi
	}
	reject() {
		local code="$1"; shift
		local status=0
		timeout 20 "${runner[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code"; else test-fail "$backend operation rejection: $*"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend pure callbacks, lazy aliases and lexical precedence"
	check '[2, [1, 2], "ab"]' do run "$work/values.km" result
	check '["A", "B"]' do run --lang expr -c '(map uppercase ["a", "b"])'
	check 3 do run --lang expr -c '((f => f [1, 2, 3]) count)'
	check 7 do run --lang expr -c '((count => count []) (x => 7))'
	check 2 do run --lang expr -c '((ops => ops.f [1, 2]) {f: count})'
	check '[1, 2]' do run --lang expr -c '((ops => map ops.0 [[1], [2, 3]]) [count, uppercase])'
	check true do run --lang expr -c '((same => same 1 1) eq?)'
	check '<operation:count>' do run "$work/values.km" size
	check '2["A","B"]' do run "$work/values.kash"
	MODE=local check local do run --lang kash --allow-env=MODE -c '(out! env/MODE)'
	test-step "$backend host-backed callbacks and build planning"
	check '[./first, ./second]' do run --allow-read -C "$work" values.km selected
	check '[first, second]' do run --allow-read -C "$work" values.km contents
	check '["A","B"]' --allow-read --allow-run -C "$work" default
	check '<operation:exists?>' do run "$work/values.km" predicate
	test-step "$backend subject-last composition and conventional comparisons"
	check 7 do run --lang expr -c '[[[7]]] |> nth 0 |> nth 0 |> nth 0'
	check 42 do run --lang expr -c '{other: 1} |> get "missing" 42'
	check 2 do run --lang expr -c '{x: 1} |> put "x" 2 |> get "x"'
	check '"a:b"' do run --lang expr -c '"a-b" |> split "-" |> join ":"'
	check '"é"' do run --lang expr -c '"aéz" |> slice 1 -1'
	check '"x-b-x"' do run --lang expr -c '"a-b-a" |> subst "a" "x"'
	check true do run --lang expr -c '("abc" |> includes? "b") and ("abc" |> starts? "ab") and ("abc" |> ends? "bc")'
	check '"zab"' do run --lang expr -c '["a", "b"] |> reduce str "z"'
	check '[[1], [1]]' do run --lang expr -c '[[1], [2], [1]] |> filter [1]'
	check '"b"' do run --lang expr -c '"a/b" |> relpath "a"'
	check '"Hi Ada"' do run --lang expr -c '"""Hi @{name}""" |> render {name: "Ada"} "plain"'
	check 0 do run --lang expr --allow-run -c '"printf ok" |> sh! {} |> get "status"'
	check '[true, false]' do run --lang expr -c '[(2 |> gt? 3), (2 |> gt? _ 3)]'
	for expression in '(nth [1] 0)' '(get {x: 1} "x")' '(put {x: 1} "x" 2)' '(slice [1] 0)' '(join ["a"] "-")' '(map [1] (x => x))' '(flatmap [1] (x => [x]))' '(filter [1] (x => true))' '(filter-out [1] (x => true))' '(reduce str ["a"] "")' '(apply [1] (x => x))' '(render "hello" 1)' '(render 42 {x: 1})'; do
		reject EXPR_INVALID do run --lang expr -c "$expression"
	done
	reject EXPR_INVALID do run --lang expr --allow-run -c '(sh! "printf forbidden" {x: 1})'
	reject EXPR_INVALID do run --lang expr --allow-run -c '(sh! {cwd: false} "printf forbidden")'
	test-step "$backend operation contracts and special-form distinction"
	reject CAP_DENIED do run -c '(map read ["blocked"])'
	reject CAP_DENIED do run -c '((f => f missing) read)'
	reject EXPR_INVALID do run -c '((f => f ()) count)'
	reject EXPR_INVALID do run -c '(map count [1])'
	reject EXPR_INVALID do run -c '((count => count []) 0)'
	reject REF_MISSING do run -c $'count = missing\n(count [])'
	reject REF_MISSING do run -c unknown
	reject OP_UNKNOWN do run -c '(unknown ())'
	for form in if let def set and or with match '?'; do reject PARSE_ERR do run --lang expr -c "(map ($form) [])"; done
	for form in run! capture! pipe! await; do reject EXPR_INVALID do run --lang expr -c "(map $form [])"; done
	check '[3]' do run --lang expr -c '(map eval ["1 + 2"])'
	check 1 do run --lang expr -c 'if true then 1 else missing'
	check 2 do run --lang expr -c '((x => x) 2)'
	check 3 do run --lang expr -c 'missing ?? 3'
done
test-end
