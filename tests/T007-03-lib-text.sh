#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — Text Operations
# Spec: docs/spec/013-tests.md — T007-03-lib-text
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T007-03 library text operations"

test-step "toolchain and binary"
cli_require_tools
cli_build

evaluates() { # EXPR EXPECTED
	cli_run -- do run --lang expr -c "$1"
	cli_expect_status 0 "$1"
	cli_expect_stdout "$2" "$1"
	cli_expect_stderr_empty
}

diag() { # EXPR CODE
	cli_run -- do run --lang expr -c "$1"
	cli_expect_status 1 "$1"
	cli_expect_stderr_contains "$2"
	cli_expect_printable "$CLI_ERR"
}

test-step "join and split"
evaluates '(join (list "a" "b" "c") "-")' '"a-b-c"'
evaluates '(join (list) "-")' '""'
evaluates '(split "a-b-c" "-")' '["a" "b" "c"]'
evaluates '(split "abc" "-")' '["abc"]'

test-step "strip, replace"
evaluates '(strip "  padded  ")' '"padded"'
evaluates '(replace "a-b-a" "a" "x")' '"x-b-x"'
evaluates '(replace "abc" "z" "y")' '"abc"'

test-step "membership tests"
evaluates '(includes? "abc" "b")' ':true'
evaluates '(includes? "abc" "z")' ':false'
evaluates '(starts? "abc" "ab")' ':true'
evaluates '(starts? "abc" "b")' ':false'
evaluates '(ends? "abc" "bc")' ':true'
evaluates '(ends? "abc" "a")' ':false'

test-step "case conversion"
evaluates '(uppercase "abc")' '"ABC"'
evaluates '(lowercase "ABC")' '"abc"'
evaluates '(uppercase "MiXeD")' '"MIXED"'

test-step "terminal styles"
ESC=$'\033'
evaluates '(red "warning")' "\"${ESC}[31mwarning${ESC}[39m\""
evaluates '(green "ready")' "\"${ESC}[32mready${ESC}[39m\""
evaluates '(yellow "notice")' "\"${ESC}[33mnotice${ESC}[39m\""
evaluates '(blue "info")' "\"${ESC}[34minfo${ESC}[39m\""
evaluates '(magenta "value")' "\"${ESC}[35mvalue${ESC}[39m\""
evaluates '(cyan "hint")' "\"${ESC}[36mhint${ESC}[39m\""
evaluates '(black "dark")' "\"${ESC}[30mdark${ESC}[39m\""
evaluates '(white "light")' "\"${ESC}[37mlight${ESC}[39m\""
evaluates '(bold "strong")' "\"${ESC}[1mstrong${ESC}[22m\""
evaluates '(dim "quiet")' "\"${ESC}[2mquiet${ESC}[22m\""
evaluates '(red (bold "nested"))' "\"${ESC}[31m${ESC}[1mnested${ESC}[22m${ESC}[39m\""
evaluates '(str "plain")' '"plain"'

test-step "WASM terminal style parity"
make -C "$CLI_ROOT" dist-wasm >/dev/null
wasm_out="$TEST_PATH/wasm.out"
wasm_err="$TEST_PATH/wasm.err"
if node "$CLI_ROOT/dist/kame.js" do run --lang expr -c '(red "warning")' >"$wasm_out" 2>"$wasm_err"; then
	if [ "$(cat "$wasm_out")" = "\"${ESC}[31mwarning${ESC}[39m\"" ] && [ ! -s "$wasm_err" ]; then test-ok "WASM emits the same ANSI string value"; else test-fail "WASM terminal color value differs"; fi
else
	test-fail "WASM terminal color evaluation failed: $(cat "$wasm_err")"
fi

test-step "text operations validate arguments"
diag '(join "not-a-list" "-")' 'EXPR_INVALID'
diag '(join (list 1) "-")' 'EXPR_INVALID'
diag '(split "abc" "")' 'EXPR_INVALID'
diag '(red 42)' 'EXPR_INVALID'

test-end
