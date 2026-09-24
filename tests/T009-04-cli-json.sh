#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Primary Output
# Spec: docs/spec/006-runtime.md — Execution Events
# Spec: docs/spec/013-tests.md — T009-04-cli-json
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T009-04 JSON event stream"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy json json-events

test-step "every event line is a schema-1 JSON object and stderr stays unused"
(
	cd json-events
	cli_run -- --json ./text.out
	cli_expect_status 0
	cli_expect_jsonl "$CLI_OUT"
	cli_expect_stderr_empty
	if jq -e -s 'all(.[]; .schema == 1 and (.type | type == "string"))' "$CLI_OUT" >/dev/null; then
		test-ok "all events carry schema and type"
	else
		test-fail "event objects missing schema/type"
	fi
	cli_expect_stderr_empty
)

test-step "file rules emit lifecycle events in order"
(
	cd json-events
	rm -f ./text.out
	cli_run -- --json ./text.out
	cli_expect_status 0
	cli_expect_event "$CLI_OUT" 'target-started' '.target == "./text.out"'
	cli_expect_event "$CLI_OUT" 'process-started' '.target == "./text.out"'
	cli_expect_event "$CLI_OUT" 'process-exited' '.target == "./text.out"'
	cli_expect_event "$CLI_OUT" 'target-completed' '.target == "./text.out"'
	sequence="$(jq -r 'select(.target == "./text.out") | .type' "$CLI_OUT" | tr '\n' ' ')"
	case "$sequence" in
	*"target-started process-started process-exited target-value target-completed "*)
		test-ok "file rule lifecycle sequence"
		;;
	*)
		test-fail "unexpected file rule sequence: $sequence"
		;;
	esac
)

test-step "process stdout and stderr become distinct events"
(
	cd json-events
	cli_run -- --json speak
	cli_expect_status 0
	cli_expect_event "$CLI_OUT" 'stdout' '.target == "speak" and .data == "to-stdout\n"'
	cli_expect_event "$CLI_OUT" 'stderr' '.target == "speak" and .data == "to-stderr\n"'
)

test-step "binary chunks use base64 with an explicit encoding"
(
	cd json-events
	cli_run -- --json ./binary.out
	cli_expect_status 0
	cli_expect_event "$CLI_OUT" 'stdout' '.encoding == "base64" and (.data | length > 0)'
)

test-step "failures emit a terminal event and a diagnostic event"
(
	cd json-events
	cli_run -- --json ./fail.out
	cli_expect_status 1
	cli_expect_jsonl "$CLI_OUT"
	cli_expect_event "$CLI_OUT" 'target-failed' '.target == "./fail.out"'
	cli_expect_event "$CLI_OUT" 'target-failed' '.diagnostic.code == "RECIPE_FAIL"'
	cli_expect_stderr_empty
)

test-step "cached replays are marked"
(
	cd json-events
	cli_run -- --json speak
	cli_expect_status 0
	cli_run -- --json speak
	cli_expect_status 0
	cli_expect_event "$CLI_OUT" 'stdout' '.cached == true'
)

test-step "process events carry a request identity"
(
	cd json-events
	rm -f ./text.out
	cli_run -- --json ./text.out
	cli_expect_status 0
	cli_expect_event "$CLI_OUT" 'process-started' '.request != null'
)

test-end
