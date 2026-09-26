#!/usr/bin/env bash
# Spec: docs/spec/012-streams.md — Materialization protocol
# Spec: docs/spec/006-runtime.md — Execution Events
# Spec: docs/spec/013-tests.md — T012-01-streams-events
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T012-01 event stream invariants"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy project streams-events

test-step "every target completes or fails exactly once"
(
	cd streams-events
	cli_run -- --json ./build/app
	cli_expect_status 0
	cli_expect_jsonl "$CLI_OUT"
	if jq -e -s '
		([.[] | select(.type == "target-completed") | .target] | group_by(.) | all(.[]; length == 1))
		and (([.[] | select(.type == "target-failed" or .type == "target-cancelled")] | length) == 0)
	' "$CLI_OUT" >/dev/null; then
		test-ok "terminal events are unique"
	else
		test-fail "duplicate or unexpected terminal events"
	fi
)

test-step "process exits match process starts"
(
	cd streams-events
	rm -rf build runs.log
	cli_run -- --json ./build/app
	cli_expect_status 0
	starts="$(jq -s '[.[] | select(.type == "process-started")] | length' "$CLI_OUT")"
	exits="$(jq -s '[.[] | select(.type == "process-exited")] | length' "$CLI_OUT")"
	if [ "$starts" = "$exits" ] && [ "$starts" -ge 3 ]; then
		test-ok "process starts and exits balance ($starts)"
	else
		test-fail "process events unbalanced: $starts starts, $exits exits"
	fi
)

test-step "target values precede completion for file outputs"
(
	cd streams-events
	rm -rf build runs.log
	cli_run -- --json ./build/app
	cli_expect_status 0
	if jq -e -s '
		([.[] | select(.type == "target-value" and .target == "./build/app")] | length) == 1
		and (([.[] | select(.type == "dependency")] | length) >= 1)
	' "$CLI_OUT" >/dev/null; then
		test-ok "value and dependency events present"
	else
		test-fail "missing value or dependency events"
	fi
)

test-step "failed targets emit a terminal failure exactly once"
fixture_copy errors streams-errors
(
	cd streams-errors
	cli_run -- --json ./failing.out
	cli_expect_status 1
	if jq -e -s '([.[] | select(.type == "target-failed" and .target == "./failing.out")] | length) == 1' "$CLI_OUT" >/dev/null; then
		test-ok "single target-failed event"
	else
		test-fail "target-failed event count is not one"
	fi
)

test-end
