#!/usr/bin/env bash
# Spec: docs/spec/006-runtime.md — Yield and Effects
# Spec: docs/spec/007-library.md — Build Effects
# Spec: docs/spec/013-tests.md — T006-04-runtime-yield
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T006-04 runtime yield"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy effects yield

test-step "yield writes its declared output"
(
	cd yield
	cli_run -- ./yield.out
	cli_expect_status 0
	cli_expect_file ./yield.out 'declared content'
	cli_expect_stderr_contains '[./yield.out] complete'
)

test-step "yield may compute its content from dynamic dependencies"
(
	cd yield
	cli_run -- ./dynamic-yield.out
	cli_expect_status 0
	cli_expect_file ./dynamic-yield.out '1'
)

test-step "yield with a shell command is OUTPUT_CONFLICT"
(
	cd yield
	cli_run -- ./conflict.out
	cli_expect_status 1
	cli_expect_stderr_contains 'OUTPUT_CONFLICT'
	cli_expect_printable "$CLI_ERR"
)

test-step "yield requires one file output"
(
	cd yield
	cli_run -- yielded-task
	cli_expect_status 1
	cli_expect_stderr_contains 'YIELD_INVALID'
	cli_expect_printable "$CLI_ERR"
)

test-step "yield never exposes its content on stdout"
(
	cd yield
	cli_run -- ./yield.out
	cli_expect_status 0
	cli_expect_stdout_empty
)

test-end
