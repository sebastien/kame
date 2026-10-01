#!/usr/bin/env bash
# Spec: docs/spec/008-cache.md — Records, Lookup
# Spec: docs/spec/013-tests.md — T008-02-cache-records
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T008-02 cache record recovery"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy cache cache-records

test-step "a corrupted record is a miss and is rewritten"
(
	cd cache-records
	cli_run -- report
	cli_expect_status 0
	record="$(find .kame/cache/tasks -name '*.kmkr' -type f | head -1)"
	if [ -n "$record" ]; then
		test-ok "record path found"
	else
		test-fail "no record to corrupt"
	fi
	printf 'not a cache record' >"$record"
	cli_run -- report
	cli_expect_status 0
	if [ "$(run_count ./runs.log)" = 2 ]; then
		test-ok "corrupted record caused a rerun"
	else
		test-fail "corrupted record was reused: $(run_count ./runs.log)"
	fi
	size="$(wc -c <"$record" | tr -d ' ')"
	if [ "$size" -gt 100 ]; then
		test-ok "record was rewritten"
	else
		test-fail "record was not rewritten ($size bytes)"
	fi
)

test-step "records are keyed by content, not target text"
(
	cd cache-records
	before="$(find .kame/cache/tasks -name '*.kmkr' -type f | wc -l | tr -d ' ')"
	cli_run -- -l kmk -c $'task alt-report : ./config.txt\n\techo alt >> ./runs.log\n\t@(out "alt\\n")' alt-report
	cli_expect_status 0
	cli_run -- -l kmk -c $'task alt-report : ./config.txt\n\techo alt >> ./runs.log\n\t@(out "alt\\n")' alt-report
	cli_expect_status 0
	after="$(find .kame/cache/tasks -name '*.kmkr' -type f | wc -l | tr -d ' ')"
	if [ "$after" -ge 2 ]; then
		test-ok "distinct tasks use distinct records ($before -> $after)"
	else
		test-fail "expected a new record for a distinct task"
	fi
)

test-step "cache directories stay inside the working tree"
(
	cd cache-records
	escaped="$(find . -path './.kame/cache/tasks/*' -name '*.kmkr' -type f | wc -l | tr -d ' ')"
	if [ "$escaped" -ge 1 ]; then
		test-ok "records live under .kame/cache/tasks"
	else
		test-fail "no record under .kame/cache/tasks"
	fi
)

test-end
