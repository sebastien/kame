#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — Filesystem Operations
# Spec: docs/spec/013-tests.md — T007-05-lib-fs
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T007-05 library filesystem operations"

test-step "toolchain and binary"
cli_require_tools
cli_build

operation_fixture_copy lib-fs lib-fs

test-step "read returns file bytes"
cli_run --dir lib-fs -- do expr --allow-read -c '(read "a.txt")'
cli_expect_status 0
cli_expect_stdout "alpha
"
cli_expect_stderr_empty

cli_run --dir lib-fs -- do expr --allow-read -c '(read "sub/c.txt")'
cli_expect_status 0
cli_expect_stdout "gamma
"

test-step "exists? reports presence"
cli_run --dir lib-fs -- do expr --allow-read -c '(exists? "a.txt")'
cli_expect_status 0
cli_expect_stdout ':true'

cli_run --dir lib-fs -- do expr --allow-read -c '(exists? "missing.txt")'
cli_expect_status 0
cli_expect_stdout ':false'

test-step "stat returns stable metadata"
cli_run --dir lib-fs -- do expr --allow-read -c '(stat "a.txt")'
cli_expect_status 0
cli_expect_stdout_contains 'name: "a.txt"' 'size: 6' 'dir: :false'

test-step "wildcard returns sorted matching paths"
cli_run --dir lib-fs -- do expr --allow-read -c '(wildcard "*.txt")'
cli_expect_status 0
cli_expect_stdout '["./a.txt" "./b.txt"]'

cli_run --dir lib-fs -- do expr --allow-read -c '(wildcard "sub/*.txt")'
cli_expect_status 0
cli_expect_stdout '["./sub/c.txt"]'

cli_run --dir lib-fs -- do expr --allow-read -c '(wildcard "**/*.txt")'
cli_expect_status 0
cli_expect_stdout '["./a.txt" "./b.txt" "./sub/c.txt"]'

test-step "missing reads fail with FS_ERR"
cli_run --dir lib-fs -- do expr --allow-read -c '(read "missing.txt")'
cli_expect_status 1
cli_expect_stderr_contains 'FS_ERR'
cli_expect_printable "$CLI_ERR"

test-step "wildcard results are stable across runs"
cli_run --dir lib-fs -- do expr --allow-read -c '(wildcard "**/*.txt")'
first="$(cat "$CLI_OUT")"
cli_run --dir lib-fs -- do expr --allow-read -c '(wildcard "**/*.txt")'
if [ "$(cat "$CLI_OUT")" = "$first" ]; then
	test-ok "wildcard output is stable"
else
	test-fail "wildcard output changed between runs"
fi

test-end
