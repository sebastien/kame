#!/usr/bin/env bash
# Spec: docs/spec/017-kash.md — Native/WASM embedded-command parity
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T017-02 embedded Kash backend parity"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/kash-parity"
mkdir -p "$work"

parity() {
	local expression="$1" expected_status="$2"
	shift 2
	set +e
	"$CLI_BIN" do run --lang expr "$@" -c "$expression" >"$work/native.out" 2>"$work/native.err"
	local native_status=$?
	node "$CLI_ROOT/dist/kame.js" do run --lang expr "$@" -c "$expression" >"$work/wasm.out" 2>"$work/wasm.err"
	local wasm_status=$?
	set -e
	if [ "$native_status" = "$expected_status" ] && [ "$wasm_status" = "$expected_status" ] && cmp -s "$work/native.out" "$work/wasm.out"; then
		test-ok "$expression"
	else
		test-fail "backend mismatch: native=$native_status wasm=$wasm_status: $expression"
	fi
}

test-step "captured values and distinct function/callback execution"
parity '(cat $(printf a) $(printf b))' 0 --allow-run
parity '(cat (eval "$(printf a)") (eval "$(printf b)"))' 0 --allow-run
parity '$(printf "one\ntwo\n")' 0 --allow-run
parity '(let [files ["a b" "c"]] $(printf "[%s]" $files))' 0 --allow-run
parity '(map ["a" "b"] ([x] $(printf "%s" $x)))' 0 --allow-run
parity '(let [f ([x] $(printf "%s" $x))] (cat (f "a") (f "b")))' 0 --allow-run
parity '$(printf "%s" $(printf nested))' 0 --allow-run
parity '$(printf "[%s]" @(["a b" "c"]))' 0 --allow-run
parity '$(printf "%s" @("a" | uppercase))' 0 --allow-run
parity '"$(printf literal)"' 0

test-step "capabilities, exit failures, invalid encoding, capture overflow"
parity '$(printf abc)' 0 --allow-run --capture-limit 3
parity '$(printf abcd)' 1 --allow-run --capture-limit=3
grep -q CAPTURE_LIMIT "$work/native.err"
grep -q CAPTURE_LIMIT "$work/wasm.err"
parity '(cat $(printf abc) $(printf def))' 0 --allow-run --capture-limit=3
parity '$(printf "%s" $(printf abcd))' 1 --allow-run --capture-limit=3
parity '$(printf "é")' 0 --allow-run --capture-limit=2
parity '$(printf "é")' 1 --allow-run --capture-limit=1
parity '$(head -c 1048577 /dev/zero)' 0 --allow-run --capture-limit=1048577
for invalid in 0 -1 nope 999999999999999999999999 ''; do
	parity '$(printf unused)' 2 "--capture-limit=$invalid"
done

test-step "lazy definitions inherit the invocation capture limit"
printf '%s\n' 'value = $(printf abcd)' >"$work/definition.kmk"
for limit in 3 4; do
	set +e
	"$CLI_BIN" --capture-limit="$limit" -f "$work/definition.kmk" value >"$work/native.out" 2>"$work/native.err"
	native_status=$?
	node "$CLI_ROOT/dist/kame.js" --capture-limit="$limit" -f "$work/definition.kmk" value >"$work/wasm.out" 2>"$work/wasm.err"
	wasm_status=$?
	set -e
	if [ "$limit" = 3 ]; then expected=1; else expected=0; fi
	if [ "$native_status" = "$expected" ] && [ "$wasm_status" = "$expected" ] && cmp -s "$work/native.out" "$work/wasm.out"; then
		test-ok "definition capture limit $limit"
	else
		test-fail "definition capture policy differs"
	fi
	if [ "$limit" = 3 ]; then
		grep -q CAPTURE_LIMIT "$work/native.err"
		grep -q CAPTURE_LIMIT "$work/wasm.err"
	fi
done
parity '$(printf denied)' 1
parity '$(sh -c "printf live >&2; exit 7")' 1 --allow-run
grep -q live "$work/native.err"
grep -q live "$work/wasm.err"
parity '$(printf "\\377")' 1 --allow-run
parity '$(head -c 1048577 /dev/zero)' 1 --allow-run
parity '$(no-such-executable-kash)' 1 --allow-run
parity '$(printf denied)' 1 --allow-run=/usr/bin
parity '$(/usr/bin/printf granted)' 0 --allow-run=/usr/bin

test-step "neither backend implicitly treats executable text as shell code"
printf '%s\n' 'printf unwanted-shell-fallback' >"$work/no-shebang"
chmod +x "$work/no-shebang"
parity "\$($work/no-shebang)" 1 --allow-run

test-step "AST and formatter parity with nested substitutions"
printf '%s\n' '(cat $(printf "%s" ${project.name}) [$(printf @(cat "a" "b"))])' >"$work/expression.km"
for command in parse fmt; do
	"$CLI_BIN" do "$command" --lang expr "$work/expression.km" >"$work/native.out"
	node "$CLI_ROOT/dist/kame.js" do "$command" --lang expr "$work/expression.km" >"$work/wasm.out"
	if cmp -s "$work/native.out" "$work/wasm.out"; then test-ok "$command parity"; else test-fail "$command parity"; fi
done

test-end
