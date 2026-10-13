#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — ordered lazy list contributions
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T004-18 declarative list contributions"
test-step "build native and WASM artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
work="$TMPDIR/contributions"
mkdir -p "$work"
cat >"$work/main.km" <<'KM'
items ?= [0]
items += [1]
(include ./module.km)
items += later
items ?= missing
later = [4 [5]]
unused += missing
KM
cat >"$work/module.km" <<'KM'
items += [2]
(include ./nested.km)
KM
printf 'items += [3 3]\n' >"$work/nested.km"
printf 'items += [2]\n' >"$work/selected.km"
printf 'items += missing\n' >"$work/skipped.km"
printf 'items += [1]\n(if (eq (count items) 1) (include ./selected.km) (include ./skipped.km))\nitems\n' >"$work/conditional.km"
cp "$work/main.km" "$work/main.kmk"
cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
items ?= []
items += [(env "MODE")]
items += ["tail"]
scoped : ; env "MODE=local"
	(out! items)
configured : ; env "KAME_items=scoped-override"
	(out! items)
outputs += ["computed"]
inputs += ["prerequisite"]
@(outputs) : @(inputs)
	(out! "computed")
prerequisite :
KMK
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	check() {
		local want="$1"; shift
		if timeout 20 "${runner[@]}" "$@" >"$work/out" 2>"$work/err" && [ "$(cat "$work/out")" = "$want" ]; then test-ok "$backend contribution result"; else test-fail "$backend contribution result: $*"; cat "$work/out" "$work/err"; fi
	}
	reject() {
		local code="$1"; shift
		local status=0
		timeout 20 "${runner[@]}" "$@" >"$work/out" 2>"$work/err" || status=$?
		if [ "$status" = 1 ] && grep -q "$code" "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend rejects $code"; else test-fail "$backend contribution rejection: $*"; cat "$work/out" "$work/err"; fi
	}
	test-step "$backend order, include expansion, defaults and forward references"
	check '[0 1 2 3 3 4 [5]]' do run "$work/main.km" items
	check '[0,1,2,3,3,4,[5]]' do cat -f "$work/main.kmk" items
	check '[1 2]' do run --print-result -c 'items += [1]' -c 'items += [2]' -c items
	check '[1 2]' do run --print-result -c $'items = [1]\nitems+= [2]\nitems'
	check '[]' do run --print-result -c $'items += []\nitems ?= missing\nitems'
	check '[1 2]' do run --print-result "$work/conditional.km"
	test-step "$backend overrides replace all operands and retain literal data"
	check '"@(missing)=literal"' do run -c $'items += missing\nitems += items' items --define 'items=@(missing)=literal'
	check '"operand"' do run "$work/main.km" items items=operand
	KAME_items=environment check '"environment"' do run "$work/main.km" items
	KAME_items=environment check '"cli"' do run "$work/main.km" items --define items=cli
	check '["local","tail"]' --allow-run --allow-env=MODE -C "$work" scoped
	check scoped-override -C "$work" configured
	check cli --define items=cli -C "$work" configured
	check computed -C "$work" computed
	test-step "$backend immutable registration, kind errors and cycle diagnostics"
	for text in $'items += [1]\nitems = [2]' $'items = []\nitems = []' $'(items) = []\nitems += []'; do reject DEF_INVALID do run -c "$text"; done
	for text in 'items += 1' 'items += "text"' 'items += [key: 1]' $'items = :nil\nitems += []'; do reject EXPR_INVALID do run -c "$text" items; done
	reject DEP_CYCLE do run -c $'items += items\nitems'
	reject DEP_CYCLE do run -c $'items += later\nlater = items\nitems'
	reject REF_MISSING do run -c 'items += missing' items
	reject PARSE_ERR do run -c '(items x) += [x]'
	reject PARSE_ERR do run --lang kash -c 'items += [1]'
	test-step "$backend authored AST and formatter preserve contributions"
	"${runner[@]}" do parse --json --lang km "$work/main.km" >"$work/$backend.json"
	if jq -e '[.ast.items[].definition | select(.contribution == true)] | length == 3' "$work/$backend.json" >/dev/null; then test-ok "$backend contribution AST"; else test-fail "$backend contribution AST"; fi
	"${runner[@]}" do fmt --lang km "$work/main.km" >"$work/$backend.fmt"
	"${runner[@]}" do fmt --lang km "$work/$backend.fmt" >"$work/again"
	if cmp -s "$work/$backend.fmt" "$work/again" && grep -q 'items +=' "$work/again"; then test-ok "$backend contribution format idempotence"; else test-fail "$backend contribution formatting"; fi
	check '[0 1 2 3 3 4 [5]]' do run --lang km "$work/$backend.fmt" items
done
if cmp -s "$work/native.fmt" "$work/wasm.fmt"; then test-ok 'native/WASM format parity'; else test-fail 'contribution format parity'; fi
test-end
