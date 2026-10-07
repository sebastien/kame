#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper
# Spec: docs/spec/009-cli.md — tools
# Spec: docs/spec/013-tests.md — T010-13-wasm-tools
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-13 wasm tools"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

project="$TMPDIR/wasm-tools"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'EOF'
default : ./out.txt

./out.txt :
	@(x/sh) -c 'echo hi' > @>
	@(x/definitely-not-a-real-tool-xyz) foo
EOF

test-step "tools JSON matches native including unresolved names"
set +e
(cd "$project" && node "$CLI_ROOT/dist/kame.js" do tools --json) >"$project/wasm.json" 2>"$project/wasm.err"
wasm_status=$?
(cd "$project" && "$CLI_BIN" do tools --json) >"$project/native.json" 2>"$project/native.err"
native_status=$?
set -e
if [ "$wasm_status" = "$native_status" ] && cmp -s "$project/wasm.json" "$project/native.json"; then
	test-ok "tools JSON matches native"
else
	test-fail "tools JSON differs (wasm=$(cat "$project/wasm.json") native=$(cat "$project/native.json"))"
fi
if grep -q '"path":""' "$project/wasm.json"; then
	test-ok "unresolved tool keeps an empty path"
else
	test-fail "unresolved tool path missing"
fi

test-step "tools rejects targets"
set +e
(cd "$project" && node "$CLI_ROOT/dist/kame.js" do tools ./out.txt) >"$project/out" 2>"$project/err"
status=$?
set -e
if [ "$status" = 2 ] && grep -q 'OPT_VALUE_INVALID' "$project/err"; then
	test-ok "tools rejects targets with status 2"
else
	test-fail "tools target rejection: status=$status"
fi

test-step "target-scoped checks match native without executing recipes"
cat >>"$project/Makefile.kmk" <<'EOF'
good :
	@(x/sh) -c 'printf tool-ok'
root : default
computed : @("default")
EOF
compare_check() {
	local wasm_status native_status
	set +e
	(cd "$project" && node "$CLI_ROOT/dist/kame.js" do tools check "$@") >"$project/wasm.json" 2>"$project/wasm.err"
	wasm_status=$?
	(cd "$project" && "$CLI_BIN" do tools check "$@") >"$project/native.json" 2>"$project/native.err"
	native_status=$?
	set -e
	# Invocation summaries use host elapsed time, not a parity golden.
	if [[ " $* " == *" --json "* ]]; then
		jq -c 'del(.elapsedMS)' "$project/wasm.json" >"$project/wasm.normalized"
		jq -c 'del(.elapsedMS)' "$project/native.json" >"$project/native.normalized"
		mv "$project/wasm.normalized" "$project/wasm.json"
		mv "$project/native.normalized" "$project/native.json"
	fi
	if [ "$wasm_status" = "$native_status" ] && cmp -s "$project/wasm.json" "$project/native.json" && cmp -s "$project/wasm.err" "$project/native.err"; then
		test-ok "tools check parity: $*"
	else
		test-fail "tools check mismatch: $* (wasm=$wasm_status native=$native_status): $(diff -u "$project/native.json" "$project/wasm.json" | head -12) $(diff -u "$project/native.err" "$project/wasm.err" | head -12)"
	fi
}
compare_check good
compare_check --json root
compare_check computed
compare_check --json computed computed
compare_check
compare_check --json missing

test-step "read-only host requests discover the real dependency plan without executing recipes"
mkdir -p "$project/input"
touch "$project/input/needs.txt"
printf default >"$project/dependency"
printf glob_root >"$project/next-dependency"
cat >>"$project/Makefile.kmk" <<'EOF'
glob_root : @((wildcard ./input/*.txt))
	touch should-not-run
./input/needs.txt :
	@(x/definitely-not-a-real-tool-xyz)
read_root : @((text (read "./dependency")))
env_root : @((env "KAME_TOOL_TARGET"))
exists_root : @((if (exists "./input/needs.txt") "default" "good"))
write_bad : @((write "./should-not-write" "forbidden"))
shell_bad : @((shell "touch should-not-spawn"))
read_error : @((text (read "./missing-input")))
two_reads : @((list (text (read "./dependency")) (text (read "./next-dependency"))))
two_globs : @((concat (wildcard ./input/*.txt) (wildcard ./input/*.txt)))
EOF
compare_check --json glob_root
compare_check glob_root
compare_check --json read_root
KAME_TOOL_TARGET=default compare_check --json env_root
KAME_TOOL_TARGET=good compare_check --json --env KAME_TOOL_TARGET=default env_root
compare_check --json exists_root
compare_check --json glob_root read_root glob_root
compare_check --json write_bad
compare_check --json shell_bad
compare_check --json read_error
compare_check --json two_reads
compare_check --json two_globs
if [ ! -e "$project/should-not-run" ] && [ ! -e "$project/should-not-write" ] && [ ! -e "$project/should-not-spawn" ]; then
	test-ok "tool inspection has no recipe, write, or process effects"
else
	test-fail "tool inspection executed a forbidden effect"
fi

test-step "a reached tool executes and an unused unavailable tool does not block it"
(cd "$project" && node "$CLI_ROOT/dist/kame.js" good) >"$project/wasm.out" 2>"$project/wasm.err"
if [ "$(cat "$project/wasm.out")" = tool-ok ]; then
	test-ok "WASM renders the resolved executable"
else
	test-fail "WASM did not execute the reached tool"
fi

test-end
