#!/usr/bin/env bash
# Spec: docs/spec/011-diagnostics.md — Codes
# Spec: docs/spec/005-evaluation.md — Failures
# Spec: docs/spec/013-tests.md — T005-05-eval-errors
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T005-05 evaluation failure diagnostics"

test-step "toolchain and binary"
cli_require_tools
cli_build

# Function: diagnosis EXPR CODE
diag() {
	cli_run -- do run --lang expr -c "$1"
	cli_expect_status 1 "$1"
	cli_expect_stderr_contains "$2"
	cli_expect_printable "$CLI_ERR"
	if grep -Eq "\\[$2\\]: .+" "$CLI_ERR"; then
		test-ok "message present for $2"
	else
		test-fail "empty message for $2"
	fi
}

test-step "reference failures"
diag 'unknown-name' 'REF_MISSING'
diag '(r => r.zzz) {a: 1}' 'REF_MISSING'
diag '(l => l.5) [1]' 'SEL_INDEX_INVALID'
diag 'captures' 'SEL_NO_CONTEXT'

test-step "operation failures"
diag '(no-such-operation 1)' 'OP_UNKNOWN'
diag 'count ()' 'EXPR_INVALID'
diag '(first 1)' 'EXPR_INVALID'
diag '(join "-" [1])' 'EXPR_INVALID'
diag '(nth "x" (list 1))' 'EXPR_INVALID'

test-step "special-form failures"
diag 'eval "let x = 1"' 'PARSE_ERR'
diag 'eval "def x = 1"' 'PARSE_ERR'
diag '(eval 42)' 'EXPR_INVALID'

test-step "capability failures"
diag '(read "x")' 'CAP_DENIED'
diag '(env "HOME")' 'CAP_DENIED'
diag '(sh! "true")' 'CAP_DENIED'
diag '(write! "x" "y")' 'CAP_DENIED'

test-step "deep mutual recursion reports a diagnostic instead of crashing"
cli_run -- do run \
	-c 'def walk value = if value == 0 then 0 else step (value - 1)' \
	-c 'def step value = walk (value - 1)' \
	-c '(walk 8192)'
cli_expect_status 1
cli_expect_stderr_contains EVAL_DEPTH
cli_expect_stdout_empty

test-step "failures write nothing to stdout"
cli_run -- do run --lang expr -c 'unknown-name'
cli_expect_stdout_empty
cli_expect_stderr_contains 'REF_MISSING'

test-end
