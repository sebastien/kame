#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Cat
# Spec: docs/spec/006-runtime.md — Materialization Result
# Spec: docs/spec/013-tests.md — T009-06-cli-cat
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T009-06 CLI cat"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy basic cat-basic

test-step "cat materializes a file target and writes exact bytes"
(
	cd cat-basic
	rm -f ./out/greeting.txt
	cli_run -- do cat ./out/greeting.txt
	cli_expect_status 0
	cli_expect_stdout "hello
"
	cli_expect_file ./out/greeting.txt "hello
"
	cli_expect_stderr_empty
)

test-step "cat writes definition values without a trailing newline"
(
	cd cat-basic
	cli_run -- do cat GREETING
	cli_expect_status 0
	cli_expect_stdout "hello"

	cli_run -- do cat LIST
	cli_expect_status 0
	cli_expect_stdout "[alpha beta gamma]"
)

test-step "cat prints an existing file that has no rule"
(
	cd cat-basic
	printf 'plain bytes' >plain.txt
	cli_run -- do cat ./plain.txt
	cli_expect_status 0
	cli_expect_stdout "plain bytes"
)

test-step "cat resolves targets relative to -C"
cli_run --dir cat-basic -- do cat -C . ./out/greeting.txt
cli_expect_status 0
cli_expect_stdout "hello
"

test-step "cat rejects a task without an artifact"
(
	cd cat-basic
	cli_run -- do cat touch-only
	cli_expect_status 1
	cli_expect_stderr_contains "NO_ARTIFACT"
	cli_expect_printable "$CLI_ERR"
)

test-step "cat requires exactly one target"
(
	cd cat-basic
	cli_run -- do cat
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_VALUE_INVALID"

	cli_run -- do cat ./out/greeting.txt GREETING
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_VALUE_INVALID"
)

test-step "cat reports a missing target"
(
	cd cat-basic
	cli_run -- do cat ./missing.txt
	cli_expect_status 1
	cli_expect_stderr_contains "TGT_NO_RULE"
	cli_expect_printable "$CLI_ERR"
)

test-step "cat adds no newline for artifact bytes"
(
	cd cat-basic
	printf 'no-newline-here' >artifact.bin
	cli_run -- do cat ./artifact.bin
	cli_expect_status 0
	cli_expect_stdout "no-newline-here"
)

test-end
