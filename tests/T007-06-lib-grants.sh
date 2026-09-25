#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — Shell Operations, Environment
# Spec: docs/spec/009-cli.md — Expression
# Spec: docs/spec/013-tests.md — T007-06-lib-grants
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T007-06 library capabilities and shell"

test-step "toolchain and binary"
cli_require_tools
cli_build

operation_fixture_copy lib-grants lib-grants

test-step "capabilities are denied by default"
for expr in '(read "a.txt")' '(write "x.txt" "x")' '(env "HOME")' '(shell "true")'; do
	cli_run --dir lib-grants -- do expr -c "$expr"
	cli_expect_status 1 "$expr"
	cli_expect_stderr_contains 'CAP_DENIED'
done

test-step "unrestricted grants permit operations"
cli_run --dir lib-grants -- do expr --allow-read -c '(read "a.txt")'
cli_expect_status 0
cli_expect_stdout "alpha
"

cli_run --dir lib-grants -- do expr --allow-write -c '(write "written.txt" "data")'
cli_expect_status 0
cli_expect_file lib-grants/written.txt 'data'

test-step "rooted grants limit filesystem access"
cli_run --dir lib-grants -- do expr "--allow-read=$PWD/lib-grants" -c '(read "a.txt")'
cli_expect_status 0
cli_expect_stdout "alpha
"

cli_run --dir lib-grants -- do expr "--allow-read=$PWD/lib-grants" -c '(read "../secret.txt")'
cli_expect_status 1
cli_expect_stderr_contains 'CAP_DENIED'

cli_run --dir lib-grants -- do expr "--allow-write=$PWD/lib-grants" -c '(write "ok.txt" "ok")'
cli_expect_status 0

cli_run --dir lib-grants -- do expr "--allow-write=$PWD/lib-grants" -c '(write "../outside.txt" "no")'
cli_expect_status 1
cli_expect_stderr_contains 'CAP_DENIED'

test-step "wildcard cannot escape the granted root"
cli_run --dir lib-grants -- do expr "--allow-read=$PWD/lib-grants" -c '(wildcard "../*.txt")'
cli_expect_status 1
cli_expect_stderr_contains 'CAP_DENIED'

test-step "environment grants scope variable names"
cli_run --dir lib-grants -- do expr --allow-env -c '(env "HOME")'
cli_expect_status 0
cli_expect_stdout "$TEST_PATH"

cli_run --dir lib-grants -- do expr --allow-env=HOME -c '(env "HOME")'
cli_expect_status 0
cli_expect_stdout "$TEST_PATH"

cli_run --dir lib-grants -- do expr --allow-env=OTHER_NAME -c '(env "HOME")'
cli_expect_status 1
cli_expect_stderr_contains 'CAP_DENIED'

test-step "shell captures status and separate streams"
cli_run --dir lib-grants -- do expr --allow-run -c '(shell "echo out; echo err >&2; exit 3")'
cli_expect_status 0
cli_expect_stdout_contains 'status: 3' 'stdout: out' 'stderr: err'

cli_run --dir lib-grants -- do expr --allow-run -c '(shell "echo ok")'
cli_expect_status 0
cli_expect_stdout_contains 'status: 0' 'stdout: ok'

test-step "shell is denied without the run capability"
cli_run --dir lib-grants -- do expr -c '(shell "echo hi")'
cli_expect_status 1
cli_expect_stderr_contains 'CAP_DENIED'

test-end
