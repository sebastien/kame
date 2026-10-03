#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Help and Version, Source Discovery
# Spec: docs/spec/013-tests.md — T009-01-cli-help
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T009-01 CLI help and version"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "help prints the overview on stdout and exits 0"
cli_run -- --help
cli_expect_status 0
cli_expect_stdout_contains "Usage:" "kame do COMMAND" "Build options:" "Commands (kame do COMMAND):"
cli_expect_stderr_empty

test-step "short help flag"
cli_run -- -h
cli_expect_status 0
cli_expect_stdout_contains "Usage:"
cli_expect_stderr_empty

test-step "version prints 'kame VERSION (BUILD_ID; BUILD_TIME; BUILD_MODE)' on stdout and exits 0"
cli_run -- --version
cli_expect_status 0
cli_expect_stdout_matches "^kame $(<"$CLI_ROOT/VERSION") \([^;]*; [^;]*; [^;]*\)$"
cli_expect_stderr_empty

test-step "short version flag"
cli_run -- -V
cli_expect_status 0
cli_expect_stdout_matches "^kame $(<"$CLI_ROOT/VERSION") \([^;]*; [^;]*; [^;]*\)$"

test-step "help takes precedence over version"
cli_run -- --version --help
cli_expect_status 0
cli_expect_stdout_contains "Usage:"

test-step "help takes precedence over normal execution"
cli_run -- --help some-target
cli_expect_status 0
cli_expect_stdout_contains "Usage:"
cli_expect_stderr_empty

test-step "do namespace overview"
cli_run -- do
cli_expect_status 0
cli_expect_stdout_contains "kame do COMMAND" "Commands:" "parse" "run"
cli_expect_stderr_empty

cli_run -- do --help
cli_expect_status 0
cli_expect_stdout_contains "Commands:"

cli_run -- do help
cli_expect_status 0
cli_expect_stdout_contains "Commands:"

test-step "per-command help"
for command in plan cat inputs outputs span parse fmt run; do
	cli_run -- do "$command" --help
	cli_expect_status 0
	cli_expect_stdout_contains "Usage: kame do $command"
	cli_expect_stderr_empty
done

test-step "unknown do command is a usage error with a hint"
cli_run -- do bogus
cli_expect_status 2
cli_expect_stderr_contains "CMD_UNKNOWN" "unknown command: bogus" "kame do --help"

cli_run -- do help bogus
cli_expect_status 2
cli_expect_stderr_contains "CMD_UNKNOWN" "kame do --help"

test-step "help token after -- is a target, not a request"
cli_run -- -- --help
cli_expect_status_nonzero
if grep -q "Usage:" "$CLI_OUT"; then
	test-fail "stdout of '-- --help' must not be the overview"
else
	test-ok "no overview for '-- --help'"
fi
cli_expect_stderr_contains "BUILD_NO_SOURCE"

test-step "help token as an option value is that option's value"
cli_run -- do run --lang expr -c --help
if grep -q "Usage:" "$CLI_OUT" || grep -q "Usage:" "$CLI_ERR"; then
	test-fail "option value requested help"
else
	test-ok "option value was not treated as a help request"
fi

test-step "bare invocation without a build source prints the overview and exits 0"
cli_run --
cli_expect_status 0
cli_expect_stdout_contains "Usage:"
cli_expect_stderr_empty

test-step "any explicit argument restores BUILD_NO_SOURCE"
cli_run -- -C .
cli_expect_status 1
cli_expect_stderr_contains "BUILD_NO_SOURCE"
cli_expect_stdout_empty

cli_run -- --force
cli_expect_status 1
cli_expect_stderr_contains "BUILD_NO_SOURCE"

test-step "an explicitly requested missing source is FS_ERR"
cli_run -- -f missing.kmk
cli_expect_status 1
cli_expect_stderr_contains "FS_ERR" "cannot read source"

test-end
