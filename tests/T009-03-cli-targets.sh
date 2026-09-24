#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Target Selection
# Spec: docs/spec/006-runtime.md — Target Names, Rule Selection
# Spec: docs/spec/013-tests.md — T009-03-cli-targets
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T009-03 CLI target selection"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "explicit target materializes its file rule through the default task"
fixture_copy basic basic
(
	cd basic
	cli_run -- default
	cli_expect_status 0
	cli_expect_stdout "default done
"
	cli_expect_file ./out/greeting.txt "hello
"
	cli_expect_stderr_contains "[default] started" "[default] complete"
)

test-step "implicit default target"
(
	cd basic
	cli_run --
	cli_expect_status 0
	cli_expect_stdout "default done
"
)

test-step "without a default, literal named targets are listed in declaration order"
TWO_TASKS=$'task first :\ntask second :'
cli_run -- -c "$TWO_TASKS"
cli_expect_status 0
cli_expect_stdout "first
second
"
cli_expect_stderr_empty

test-step "multiple targets run in argument order"
(
	cd basic
	rm -f ./out/greeting.txt
	cli_run -- touch-only ./out/greeting.txt
	cli_expect_status 0
	cli_expect_stderr_contains "[touch-only] started" "[touch-only] complete" "[./out/greeting.txt] started" "[./out/greeting.txt] complete"
	cli_expect_file ./out/greeting.txt "hello
"
)

test-step "multiple roots share one dependency execution"
fixture_copy project project
(
	cd project
	cli_run -- ./build/main.o ./build/app
	cli_expect_status 0
	cli_expect_file ./build/app
	if [ "$(run_count ./runs.log)" = 2 ]; then
		test-ok "shared prerequisite compiled once"
	else
		test-fail "expected two compilations, got $(run_count ./runs.log): $(tr '\n' ' ' <./runs.log)"
	fi
	if [ "$(grep -c -F 'compile-./build/main.o' ./runs.log)" = 1 ]; then
		test-ok "shared main.o compiled once"
	else
		test-fail "main.o compiled more than once"
	fi
)

test-step "path targets normalize across ./"
(
	cd basic
	rm -f ./out/greeting.txt
	cli_run -- out/greeting.txt
	cli_expect_status 0
	cli_expect_file ./out/greeting.txt "hello
"
)

test-step "definitions requested as targets print their value"
(
	cd basic
	cli_run -- GREETING
	cli_expect_status 0
	cli_expect_stdout "hello"

	cli_run -- LIST
	cli_expect_status 0
	cli_expect_stdout "[alpha beta gamma]"
)

test-step "unknown targets fail with TGT_NO_RULE"
(
	cd basic
	cli_run -- no-such-target
	cli_expect_status 1
	cli_expect_stderr_contains "TGT_NO_RULE"
	cli_expect_printable "$CLI_ERR"

	cli_run -- ./no-such-file.txt
	cli_expect_status 1
	cli_expect_stderr_contains "TGT_NO_RULE"
	cli_expect_printable "$CLI_ERR"
)

test-step "ambiguous template targets fail with TGT_AMBIG"
fixture_copy errors errors
(
	cd errors
	cli_run -- ./amb-z.c
	cli_expect_status 1
	cli_expect_stderr_contains "TGT_AMBIG"
	cli_expect_printable "$CLI_ERR"
)

test-step "a target named do is requested after --"
DO_SOURCE=$'task do :\n\t@(out "do-target\\n")'
cli_run -- -c "$DO_SOURCE" -- do
cli_expect_status 0
cli_expect_stdout "do-target
"

test-step "bare tasks rerun on every request"
(
	cd basic
	rm -f ./out/greeting.txt
	cli_run -- default
	cli_expect_status 0
	cli_run -- default
	cli_expect_status 0
	cli_expect_stdout "default done
"
)

test-end
