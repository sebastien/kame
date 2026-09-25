#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Run
# Spec: docs/spec/013-tests.md — T009-09-cli-run-parity
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T009-09 primary invocation and do run parity"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy project run-parity

test-step "do run and the primary invocation share output and status"
(
	cd run-parity
	cli_run -- ./build/main.o
	cli_expect_status 0
	primary_out="$(cat "$CLI_OUT")"
	cp "$CLI_ERR" primary.err
	primary_status="$CLI_STATUS"

	rm -rf build runs.log
	cli_run -- do run ./build/main.o
	cli_expect_status 0
	cli_expect_stdout "$primary_out"
	if cmp -s primary.err "$CLI_ERR"; then
		test-ok "status lines match"
	else
		test-fail "status lines differ"
	fi
)

test-step "do run accepts the shared option surface"
(
	cd run-parity
	rm -rf build runs.log
	cli_run -- do run -j 2 --force ./build/app
	cli_expect_status 0
	cli_expect_file ./build/app
)

test-step "do run --json emits JSON Lines on stdout"
(
	cd run-parity
	rm -rf build runs.log
	cli_run -- do run --json ./build/main.o
	cli_expect_status 0
	cli_expect_jsonl "$CLI_OUT"
	cli_expect_stderr_empty
	cli_expect_event "$CLI_OUT" 'target-completed' '.target == "./build/main.o"'
)

test-step "do run failure uses the build exit status"
fixture_copy errors run-errors
(
	cd run-errors
	cli_run -- do run ./failing.out
	cli_expect_status 1
	cli_expect_stderr_contains 'RECIPE_FAIL'
)

test-step "do run without targets mirrors default selection"
(
	cd run-parity
	cli_run -- do run
	cli_expect_status 0
	cli_expect_stderr_contains '[./build/app] complete'
)

test-end
