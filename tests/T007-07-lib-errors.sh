#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — Operation Contract
# Spec: docs/spec/011-diagnostics.md — EXPR_INVALID, OP_UNKNOWN
# Spec: docs/spec/013-tests.md — T007-07-lib-errors
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T007-07 library failure messages"

test-step "toolchain and binary"
cli_require_tools
cli_build

diag() { # EXPR CODE
	cli_run -- do expr -c "$1"
	cli_expect_status 1 "$1"
	cli_expect_stderr_contains "$2"
	cli_expect_printable "$CLI_ERR"
	if grep -Eq "$2: .+" "$CLI_ERR"; then
		test-ok "message present for $1"
	else
		test-fail "empty message for $1"
	fi
}

test-step "unknown operations name the operation"
diag '(definitely-not-an-operation 1)' 'OP_UNKNOWN'
cli_run -- do expr -c '(definitely-not-an-operation 1)'
cli_expect_status 1
cli_expect_stderr_contains 'definitely-not-an-operation'

test-step "arity failures carry messages"
diag '(count)' 'EXPR_INVALID'
diag '(count 1 2)' 'EXPR_INVALID'
diag '(not)' 'EXPR_INVALID'
diag '(nth (list 1))' 'EXPR_INVALID'

test-step "kind failures carry messages"
diag '(first 1)' 'EXPR_INVALID'
diag '(join (list 1) "-")' 'EXPR_INVALID'
diag '(slice 1 0 2)' 'EXPR_INVALID'
diag '(sorted "abc")' 'EXPR_INVALID'

test-step "failure diagnostics stay printable"
cli_run -- do expr -c '(no-such-operation 1)'
cli_expect_printable "$CLI_ERR"
cli_expect_printable "$CLI_OUT"

test-end
