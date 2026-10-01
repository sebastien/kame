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
	cli_run -- do expr -c "$1"
	cli_expect_status 0 "$1"
	cli_expect_stdout "$2" "$1"
	cli_expect_stderr_empty
}

diag() { # EXPR CODE
	cli_run -- do expr -c "$1"
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

test-step "text operations validate arguments"
diag '(join "not-a-list" "-")' 'EXPR_INVALID'
diag '(join (list 1) "-")' 'EXPR_INVALID'
diag '(split "abc" "")' 'EXPR_INVALID'

test-end
