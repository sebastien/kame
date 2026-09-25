#!/usr/bin/env bash
# Spec: docs/spec/006-runtime.md — Yield and Effects
# Spec: docs/spec/007-library.md — Build Effects
# Spec: docs/spec/013-tests.md — T006-05-runtime-effects
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T006-05 runtime effects"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy effects effects

test-step "out and err effects keep their streams and order"
(
	cd effects
	cli_run -- -f Effects.lmk default
	cli_expect_status 0
	# Deferred effects commit in source order before the one shell command.
	cli_expect_stdout "first
third
command-last
"
	cli_expect_stderr_contains 'second-error'
	if grep -q 'command-last' <(head -2 "$CLI_OUT"); then
		test-fail "shell command output ran before deferred effects"
	else
		test-ok "deferred effects preceded the shell command"
	fi
)

test-step "dry-run reports effects without running the command"
(
	cd effects
	cli_run -- -n -f Effects.lmk default
	cli_expect_status 0
	if grep -q 'command-last' "$CLI_OUT"; then
		test-fail "dry-run executed the recipe command"
	else
		test-ok "dry-run skipped the recipe command"
	fi
	cli_expect_stdout_contains 'first' 'third'
)

test-step "write is a deferred effect committed during execution"
(
	cd effects
	cli_run -- ./write.out
	cli_expect_status 0
	cli_expect_file ./generated.txt 'written payload'
)

test-step "dry-run does not commit write effects"
(
	cd effects
	rm -f ./generated.txt
	cli_run -- -n ./write.out
	cli_expect_status 0
	cli_expect_no_file ./generated.txt
)

test-end
