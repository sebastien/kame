#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Parse, Help and Version
# Spec: docs/spec/013-tests.md — T004-04-lang-parse-usage
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T004-04 parse usage"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "--lang is required"
cli_run -- do parse
cli_expect_status 2
cli_expect_stderr_contains "OPT_NO_VALUE"

cli_run -- do parse --lang
cli_expect_status 2
cli_expect_stderr_contains "OPT_NO_VALUE"

test-step "only known languages are accepted"
for lang in expr template rule script; do
	cli_run --stdin /dev/null -- do parse "--lang=$lang"
	# Empty stdin may be a parse error, but the language itself is accepted.
	if [ "$CLI_STATUS" = 2 ]; then
		test-fail "language $lang was rejected"
	else
		test-ok "language $lang accepted"
	fi
done

cli_run -- do parse --lang bogus
cli_expect_status 2
cli_expect_stderr_contains "OPT_VALUE_INVALID" "invalid language"

test-step "unknown options are rejected"
cli_run -- do parse --bogus
cli_expect_status 2
cli_expect_stderr_contains "OPT_UNKNOWN"

test-step "help wins over execution"
cli_run -- do parse --help
cli_expect_status 0
cli_expect_stdout_contains "Usage: littlemake do parse"
cli_expect_stderr_empty

cli_run -- do help parse
cli_expect_status 0
cli_expect_stdout_contains "Usage: littlemake do parse"

test-end
