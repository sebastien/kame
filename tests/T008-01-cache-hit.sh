#!/usr/bin/env bash
# Spec: docs/spec/008-cache.md — Records, Lookup, Commit
# Spec: docs/spec/013-tests.md — T008-01-cache-hit
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T008-01 cached task lookup"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy cache cache-hit

test-step "a successful cached task writes a record"
(
	cd cache-hit
	cli_run -- report
	cli_expect_status 0
	cli_expect_file ./config.txt
	if [ "$(run_count ./runs.log)" = 1 ]; then
		test-ok "recipe ran once"
	else
		test-fail "recipe runs: $(run_count ./runs.log)"
	fi
	records="$(find .littlemake/cache/tasks -name '*.lmkr' -type f 2>/dev/null | wc -l | tr -d ' ')"
	if [ "$records" -ge 1 ]; then
		test-ok "cache record written"
	else
		test-fail "no cache record written"
	fi
)

test-step "a second run replays retained output without re-executing"
(
	cd cache-hit
	cli_run -- --json report
	cli_expect_status 0
	cli_expect_event "$CLI_OUT" 'stdout' '.cached == true and (.data | length > 0)'
	if [ "$(run_count ./runs.log)" = 1 ]; then
		test-ok "recipe not re-executed on a cache hit"
	else
		test-fail "recipe re-executed: $(run_count ./runs.log)"
	fi
)

test-step "--force bypasses the cache"
(
	cd cache-hit
	cli_run -- --force report
	cli_expect_status 0
	if [ "$(run_count ./runs.log)" = 2 ]; then
		test-ok "force re-executed the recipe"
	else
		test-fail "force did not re-execute: $(run_count ./runs.log)"
	fi
)

test-step "input changes invalidate the record"
(
	cd cache-hit
	printf 'changed\n' >config.txt
	cli_run -- report
	cli_expect_status 0
	if [ "$(run_count ./runs.log)" = 3 ]; then
		test-ok "input change invalidated the cache"
	else
		test-fail "input change did not invalidate: $(run_count ./runs.log)"
	fi
)

test-step "environment changes invalidate the record"
(
	cd cache-hit
	rm -rf .littlemake runs.log
	cli_run -- --env CACHE_VALUE=one env-report
	cli_expect_status 0
	cli_run -- --json --env CACHE_VALUE=one env-report
	cli_expect_status 0
	cli_expect_event "$CLI_OUT" 'stdout' '.cached == true'
	cli_run -- --env CACHE_VALUE=two env-report
	cli_expect_status 0
	cli_run -- --json --env CACHE_VALUE=two env-report
	cli_expect_status 0
	cli_expect_event "$CLI_OUT" 'stdout' '.cached == true'
	if [ "$(run_count ./runs.log)" = 2 ]; then
		test-ok "environment change re-executed once"
	else
		test-fail "environment change runs: $(run_count ./runs.log)"
	fi
	if [ "$(sed -n 1p ./runs.log)" = "env=one" ] && [ "$(sed -n 2p ./runs.log)" = "env=two" ]; then
		test-ok "each environment value ran once"
	else
		test-fail "runs.log = $(tr '\n' ' ' <./runs.log)"
	fi
)

test-step "failed cached tasks do not commit a record"
(
	cd cache-hit
	rm -rf .littlemake
	cli_run -- -c $'task failing :\n\texit 1' failing
	cli_expect_status 1
	records="$(find .littlemake -name '*.lmkr' -type f 2>/dev/null | wc -l | tr -d ' ')" || true
	if [ "$records" = 0 ]; then
		test-ok "no record committed for a failed task"
	else
		test-fail "$records record(s) committed for a failed task"
	fi
)

test-end
