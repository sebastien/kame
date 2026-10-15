#!/usr/bin/env bash
# Spec: docs/spec/005-evaluation.md — first-class operations and syntax-only forms
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T007-12 first-class operation values"
test-step "build native and WASM artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
work="$TMPDIR/operations"
mkdir -p "$work"
printf first >"$work/first"
printf second >"$work/second"
cat >"$work/values.km" <<'KM'
size = count
predicate = exists?
callbacks = [uppercase count]
record = [size: count]
files = [./first ./missing ./second]
selected = (filter predicate files)
contents = (map read selected)
result = [(size [1 2]) (map count [[1] [2 3]]) (reduce str "" ["a" "b"])]
KM
cat >"$work/values.kash" <<'KASH'
size = count
(out! (size [1 2]))
(out! (map uppercase ["a" "b"]))
KASH
cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
predicate = exists?
files = [./first ./missing ./second]
inputs = (filter predicate files)
default : @(inputs)
	(out! (map uppercase ["a" "b"]))
KMK
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
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
	check '[2 [1 2] "ab"]' do run "$work/values.km" result
	check '["A" "B"]' do run --lang expr -c '(map uppercase ["a" "b"])'
	check 3 do run --lang expr -c '(let [f count] (f [1 2 3]))'
	check 7 do run --lang expr -c '(let [count ([x] 7)] (count []))'
	check 2 do run --lang expr -c '(let [ops [f: count]] (ops.f [1 2]))'
	check '[1 2]' do run --lang expr -c '(let [ops [count uppercase]] (map ops.0 [[1] [2 3]]))'
	check ':true' do run --lang expr -c '(let [same =] (same 1 1))'
	check '<operation:count>' do run "$work/values.km" size
	check '2["A","B"]' do run "$work/values.kash"
	MODE=local check local do run --lang kash --allow-env=MODE -c '(out! (env "MODE"))'
	test-step "$backend host-backed callbacks and build planning"
	check '["./first" "./second"]' do run --allow-read -C "$work" values.km selected
	check '[first second]' do run --allow-read -C "$work" values.km contents
	check '["A","B"]' --allow-read --allow-run -C "$work" default
	check '<operation:exists?>' do run "$work/values.km" predicate
	test-step "$backend subject-last composition and conventional comparisons"
	check 7 do run --lang expr -c '(let [value [[[7]]]] (value | nth 0 | nth 0 | nth 0))'
	check 42 do run --lang expr -c '(let [value [other: 1]] (value | get "missing" 42))'
	check 2 do run --lang expr -c '(let [value [x: 1]] (value | set-field "x" 2 | get "x"))'
	check '"a:b"' do run --lang expr -c '("a-b" | split "-" | join ":")'
	check '"é"' do run --lang expr -c '("aéz" | slice 1 -1)'
	check '"x-b-x"' do run --lang expr -c '("a-b-a" | sub "a" "x")'
	check ':true' do run --lang expr -c '(and ("abc" | includes? "b") ("abc" | starts? "ab") ("abc" | ends? "bc"))'
	check '"zab"' do run --lang expr -c '(let [value ["a" "b"]] (value | reduce str "z"))'
	check '[[1] [1]]' do run --lang expr -c '(let [value [[1] [2] [1]]] (value | filter [1]))'
	check '"b"' do run --lang expr -c '("a/b" | relpath "a")'
	check '"Hi Ada"' do run --lang expr -c '("""Hi @(name)""" | render [name: "Ada"] "plain")'
	check 0 do run --lang expr --allow-run -c '("printf ok" | shell! (parse-json "{}") | get "status")'
	check '[:true :false]' do run --lang expr -c '[(2 | gt 3) (2 | gt _ 3)]'
	for expression in '(nth [1] 0)' '(get [x: 1] "x")' '(set-field [x: 1] "x" 2)' '(slice [1] 0)' '(join ["a"] "-")' '(map [1] ([x] x))' '(flatmap [1] ([x] [x]))' '(filter [1] ([x] :true))' '(filter-out [1] ([x] :true))' '(reduce str ["a"] "")' '(apply [1] ([x] x))' '(render "hello" [x: 1])'; do
		reject EXPR_INVALID do run --lang expr -c "$expression"
	done
	reject EXPR_INVALID do run --lang expr --allow-run -c '(shell! "printf forbidden" [x: 1])'
	test-step "$backend operation contracts and special-form distinction"
	reject CAP_DENIED do run -c '(map read ["blocked"])'
	reject CAP_DENIED do run -c '(let [f read] (f missing))'
	reject EXPR_INVALID do run -c '(let [f count] (f))'
	reject EXPR_INVALID do run -c '(map count [1])'
	reject EXPR_INVALID do run -c '(let [count 0] (count []))'
	reject REF_MISSING do run -c $'count = missing\n(count [])'
	reject REF_MISSING do run -c unknown
	reject OP_UNKNOWN do run -c '(unknown)'
	for form in if let def eval and or with match run! pipe! await '?'; do reject EXPR_INVALID do run -c "(map $form [])"; done
	check 1 do run --lang expr -c '(if :true 1 missing)'
	check 2 do run --lang expr -c '(let [x 2] x)'
	check 3 do run --lang expr -c '(? missing 3)'
done
test-end
