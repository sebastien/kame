#!/usr/bin/env bash
# Spec: docs/spec/003-posix-process.md — Process execution
# Spec: docs/spec/009-cli.md — Run
# Spec: docs/spec/013-tests.md — T003-01-host-process
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T003-01 POSIX process behavior"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy project host-process

test-step "large stdout streams without truncation"
(
	cd host-process
	printf 'task big :\n\tseq 1 2000\n' >Big.kmk
	cli_run -- -f Big.kmk big
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
	printf 'task streams :\n\tprintf "%%s\\n" "$STREAM_OUT"\n\techo to-stderr >&2\n' >Streams.kmk
	cli_run --env STREAM_OUT=to-stdout -- -f Streams.kmk streams
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
	printf 'task fail :\n\texit 7\n' >Exit.kmk
	cli_run -- -f Exit.kmk fail
	cli_expect_status 1
	cli_expect_stderr_contains 'RECIPE_FAIL'
)

test-step "--retry re-runs failed commands"
(
	cd host-process
	cp "$(fixture_path host/retry.kmk)" Retry.kmk
	cli_run -- -f Retry.kmk --retry 1 ./retry.out
	cli_expect_status 0
	cli_expect_file ./retry.out "ok
"
)

test-step "--shell selects the recipe executable"
(
	cd host-process
	printf 'task shell :\n\techo shell-ran\n' >Shell.kmk
	cli_run -- -f Shell.kmk --shell /bin/sh --shell -c shell
	cli_expect_status 0
	cli_expect_stdout "shell-ran
"
)

test-step "--env entries reach recipes"
(
	cd host-process
	printf 'task env :\n\techo "value=$HOST_TEST_VALUE"\n' >Env.kmk
	cli_run -- -f Env.kmk --env HOST_TEST_VALUE=from-cli env
	cli_expect_status 0
	cli_expect_stdout "value=from-cli
"
)

test-step "commands run in the build directory"
(
	cd host-process
	expected="$PWD"
	printf 'task cwd :\n\tpwd\n' >Cwd.kmk
	cli_run -- -f Cwd.kmk cwd
	cli_expect_status 0
	if [ "$(cat "$CLI_OUT")" = "$expected" ]; then
		test-ok "recipe cwd is the build directory"
	else
		test-fail "recipe cwd was $(cat "$CLI_OUT"), wanted $expected"
	fi
)

test-end
