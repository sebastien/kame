#!/usr/bin/env bash
# Spec: docs/spec/003-posix-process.md — Process execution
# Spec: docs/spec/009-cli.md — Run
# Spec: docs/spec/013-tests.md — T003-01-host-process
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T003-01 POSIX process behavior"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy project host-process

test-step "large stdout streams without truncation"
(
	cd host-process
	printf 'task big :\n\tseq 1 2000\n' >Big.lmk
	cli_run -- -f Big.lmk big
	cli_expect_status 0
	lines="$(wc -l <"$CLI_OUT" | tr -d ' ')"
	if [ "$lines" = 2000 ]; then
		test-ok "streamed 2000 lines"
	else
		test-fail "streamed $lines lines, wanted 2000"
	fi
)

test-step "stdout and stderr stay on their streams"
(
	cd host-process
	printf 'task streams :\n\techo to-stdout\n\techo to-stderr >&2\n' >Streams.lmk
	cli_run -- -f Streams.lmk streams
	cli_expect_status 0
	cli_expect_stdout "to-stdout
"
	cli_expect_stderr_contains 'to-stderr'
	if grep -q 'to-stdout' "$CLI_ERR"; then
		test-fail "stdout leaked into stderr"
	else
		test-ok "stdout did not leak"
	fi
)

test-step "recipe exit codes surface as RECIPE_FAIL"
(
	cd host-process
	printf 'task fail :\n\texit 7\n' >Exit.lmk
	cli_run -- -f Exit.lmk fail
	cli_expect_status 1
	cli_expect_stderr_contains 'RECIPE_FAIL'
)

test-step "--retry re-runs failed commands"
(
	cd host-process
	cp "$(fixture_path host/retry.lmk)" Retry.lmk
	cli_run -- -f Retry.lmk --retry 1 ./retry.out
	cli_expect_status 0
	cli_expect_file ./retry.out "ok
"
)

test-step "--shell selects the recipe executable"
(
	cd host-process
	printf 'task shell :\n\techo shell-ran\n' >Shell.lmk
	cli_run -- -f Shell.lmk --shell /bin/sh --shell -c shell
	cli_expect_status 0
	cli_expect_stdout "shell-ran
"
)

test-step "--env entries reach recipes"
(
	cd host-process
	printf 'task env :\n\techo "value=$HOST_TEST_VALUE"\n' >Env.lmk
	cli_run -- -f Env.lmk --env HOST_TEST_VALUE=from-cli env
	cli_expect_status 0
	cli_expect_stdout "value=from-cli
"
)

test-step "commands run in the build directory"
(
	cd host-process
	expected="$PWD"
	printf 'task cwd :\n\tpwd\n' >Cwd.lmk
	cli_run -- -f Cwd.lmk cwd
	cli_expect_status 0
	if [ "$(cat "$CLI_OUT")" = "$expected" ]; then
		test-ok "recipe cwd is the build directory"
	else
		test-fail "recipe cwd was $(cat "$CLI_OUT"), wanted $expected"
	fi
)

test-end
