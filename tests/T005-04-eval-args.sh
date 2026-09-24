#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Expression
# Spec: docs/spec/005-evaluation.md — Arguments
# Spec: docs/spec/013-tests.md — T005-04-eval-args
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T005-04 expression arguments and input sources"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "arguments after -- are available as @*"
cli_run -- do expr -c '(join @* "-")' -- a b c
cli_expect_status 0
cli_expect_stdout 'a-b-c'

cli_run -- do expr -c '(str @#)' -- a b c
cli_expect_status 0
cli_expect_stdout '3'

cli_run -- do expr -c '@_' -- first second
cli_expect_status 0
cli_expect_stdout 'first'

cli_run -- do expr -c '@1' -- first second
cli_expect_status 0
cli_expect_stdout 'second'

test-step "expression read from stdin"
printf '(join ["x" "y"] "")' >expression.lm
cli_run --stdin expression.lm -- do expr
cli_expect_status 0
cli_expect_stdout 'xy'
cli_expect_stderr_empty

test-step "expression read from a file"
cli_run -- do expr expression.lm
cli_expect_status 0
cli_expect_stdout 'xy'

test-step "cwd is available to path operations"
mkdir -p work
cli_run -- do expr -C work -c '(abspath ".")'
cli_expect_status 0
if [ "$(cat "$CLI_OUT")" = "$PWD/work" ]; then
	test-ok "abspath resolved against -C"
else
	test-fail "abspath was $(cat "$CLI_OUT"), wanted $PWD/work"
fi

test-step "input source conflicts and missing values"
cli_run -- do expr -c '(list 1)' expression.lm
cli_expect_status 2
cli_expect_stderr_contains 'OPT_CONFLICT'

cli_run -- do expr -c
cli_expect_status 2
cli_expect_stderr_contains 'OPT_NO_VALUE'

cli_run -- do expr --bogus
cli_expect_status 2
cli_expect_stderr_contains 'OPT_UNKNOWN'

test-end
