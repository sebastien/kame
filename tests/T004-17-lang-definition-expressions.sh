#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — definition expressions and textual migration
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T004-17 expression definition values"
test-step "build native and WASM artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
work="$TMPDIR/definitions"
mkdir -p "$work"
cat >"$work/values.km" <<'KM'
alias = number
number = 42
fraction = 1.5
flag = :true
nothing = :nil
text = "line\n\"quoted\" @(number)"
raw = """@(missing) {(missing)}"""
unused = (fail "unused definition evaluated")
fallback ?= number
fallback ?= (fail "skipped default evaluated")
(identity value) = value
result = [(identity alias) fraction flag nothing ["word" alias] [answer: fallback] text raw]
KM
cp "$work/values.km" "$work/values.kmk"
# Kash retains its existing standalone '=' header grammar.
sed '/fallback ?=/d' "$work/values.km" >"$work/values.kash"
printf '\nfallback = number\n(out result)\n' >>"$work/values.kash"
expected='[42 1.5 :true :nil ["word" 42] [answer: 42] "line\n\"quoted\" 42" "@(missing) {(missing)}"]'
kash_expected='[42,1.5,true,nil,["word",42],{"answer":42},"line\n\"quoted\" 42","@(missing) {(missing)}"]'
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local want="$1"; shift
		if timeout 20 "${runner[@]}" "$@" >"$work/out" 2>"$work/err" && [ "$(cat "$work/out")" = "$want" ]; then
			test-ok "$backend expression definitions"
		else
			test-fail "$backend expression definitions: $*"
			cat "$work/out" "$work/err"
		fi
	}
	reject() {
		local code="$1"; shift
		local status=0
		timeout 20 "${runner[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code"; else test-fail "$backend definition rejection"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend typed values, aliases, function bodies and lazy defaults"
	# Existing CLI display and output-effect framing remain unchanged.
	check "$expected" do run "$work/values.km" result
	check 42 do cat -f "$work/values.kmk" fallback
	check "$kash_expected" do run "$work/values.kash"
	check '"@(missing)=literal"' do run "$work/values.km" number --define 'number=@(missing)=literal'
	check 7 do run -c $'x ?= 7\nx ?= missing\nx'
	check 42 do run -c 'alias = later' -c 'later = 42' -c alias
	reject DEP_CYCLE do run -c $'a = b\nb = a\na'
	reject REF_MISSING do run -c $'text = unbound\ntext'
	reject DEF_INVALID do run -c $'x = 1\nx = 2\nx'
	cat >"$work/callable.kmk" <<'KMK'
BUILD_JSON = (replace ./{prefix:**}/{name:*}.txto ./build/{prefix}/{name}.json)
KMK
	local_status=0
	timeout 20 "${runner[@]}" do cat -f "$work/callable.kmk" BUILD_JSON >"$work/out" 2>"$work/err" || local_status=$?
	location_ok=1
	if [ "$backend" = native ]; then grep -q 'callable.kmk:1:' "$work/err" || location_ok=0; fi
	if [ "$local_status" = 1 ] && grep -q EXPR_INVALID "$work/err" && grep -q 'invocation-scoped' "$work/err" && grep -q 'map' "$work/err" && [ "$location_ok" = 1 ]; then test-ok "$backend explains invocation-scoped callable definitions"; else test-fail "$backend callable boundary diagnostic"; cat "$work/out" "$work/err"; fi
	for lang in km kmk kash; do
		for rhs in 'one two' '"one" "two"' '-Wall -O2' '(broken'; do
			reject PARSE_ERR do run --lang "$lang" -c "value = $rhs"
		done
	done
	test-step "$backend expression formatting and parser parity"
	for lang in km kmk kash; do
		"${runner[@]}" do fmt --lang "$lang" "$work/values.$lang" >"$work/$backend.$lang.fmt"
		"${runner[@]}" do fmt --lang "$lang" "$work/$backend.$lang.fmt" >"$work/again"
		if cmp -s "$work/$backend.$lang.fmt" "$work/again"; then test-ok "$backend $lang format idempotence"; else test-fail "$backend $lang formatting"; fi
	done
	check 42 do run --lang km "$work/$backend.km.fmt" alias
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
alias = number
number = 42
default :
	printf "%s" $alias
KMK
	check 42 -C "$work" default
done
for lang in km kmk kash; do
	if cmp -s "$work/native.$lang.fmt" "$work/wasm.$lang.fmt"; then test-ok "$lang native/WASM format parity"; else test-fail "$lang format parity"; fi
done
test-end
