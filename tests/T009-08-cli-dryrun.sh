#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Invocation (--dry-run)
# Spec: docs/spec/006-runtime.md — Yield and Effects
# Spec: docs/spec/013-tests.md — T009-08-cli-dryrun
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T009-08 dry-run performs no effects"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy json dry-run

test-step "dry-run builds no files and writes no effects"
(
	cd dry-run
	cli_run -- --output text -n ./text.out
	cli_expect_status 0
	cli_expect_no_file ./text.out
	cli_expect_stderr_contains 'started [./text.out]' '[./text.out] complete'

	cli_run -- -n speak
	cli_expect_status 0
	cli_expect_stdout_empty
)

test-step "dry-run does not execute failing recipes"
(
	cd dry-run
	cli_run -- -n ./fail.out
	cli_expect_status 0
	cli_expect_no_file ./fail.out
)

test-step "dry-run reports out effects without writing"
(
	cd dry-run
	cli_run -- -n speak
	cli_expect_status 0
	cli_expect_no_file ./speak
)

test-step "plan never executes its target"
fixture_copy plan dry-run-plan
(
	cd dry-run-plan
	cli_run -- do plan ./artifact.txt
	cli_expect_status 0
	cli_expect_no_file ./runs.log
	cli_expect_no_file ./artifact.txt
)

test-end
