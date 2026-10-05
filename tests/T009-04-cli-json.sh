#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Primary Output
# Spec: docs/spec/006-runtime.md — Execution Events
# Spec: docs/spec/013-tests.md — T009-04-cli-json
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

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
	if jq -e -s 'any(.[]; .type == "process-started" and (.program | type == "string") and (.argv | type == "array") and (.argv | length <= 8) and ((.program | utf8bytelength) + ([.argv[] | utf8bytelength] | add // 0) <= 160) and (.environment == null)) and any(.[]; .type == "process-exited" and (.runtimeMS | type == "number") and .runtimeMS >= 0)' "$CLI_OUT" >/dev/null; then
		test-ok "process display is bounded and runtime is numeric without environment data"
	else
		test-fail "process display bounds or runtime field were wrong"
	fi
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

test-step "human progress displays the bounded process command and runtime"
(
	cd json-events
	rm -f ./text.out
	cli_run ./text.out
	cli_expect_status 0
	if grep -Eq 'process.*printf' "$CLI_ERR" && grep -Eq 'process finished in [0-9]+ms' "$CLI_ERR"; then
		test-ok "stderr shows process command and runtime"
	else
		test-fail "stderr is missing process command or runtime"
	fi
)

test-step "pipeline process display stops after four stages"
(
	cd json-events
	cli_run -- --json pipeline
	cli_expect_status 0
	if jq -e -s 'any(.[]; .type == "process-started" and .displayTruncated == true and ([.argv[] | select(. == "|")] | length <= 3) and ((.program | utf8bytelength) + ([.argv[] | utf8bytelength] | add // 0) <= 160))' "$CLI_OUT" >/dev/null; then
		test-ok "pipeline display is bounded to four stages"
	else
		test-fail "pipeline display exceeded four stages or display bounds"
	fi
)

test-step "progress honors color policy"
(
	cd json-events
	rm -f ./text.out
	cli_run -- --color auto --diagnostic-format human ./text.out
	cli_expect_status 0
	if grep -q $'\033' "$CLI_ERR"; then
		test-fail "auto color ignored NO_COLOR for redirected progress"
	else
		test-ok "auto color honors NO_COLOR for redirected progress"
	fi
	rm -f ./text.out
	cli_run -- --color always --diagnostic-format human ./text.out
	cli_expect_status 0
	if grep -q $'\033' "$CLI_ERR"; then
		test-ok "explicit color styles redirected progress"
	else
		test-fail "explicit color did not style redirected progress"
	fi
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

test-step "recipe failures map a source span"
(
	cd json-events
	cli_run -- --json ./fail.out
	cli_expect_status 1
	if jq -e -s 'any(.[]; .type == "target-failed" and .diagnostic.code == "RECIPE_FAIL" and .diagnostic.span.end > .diagnostic.span.start)' "$CLI_OUT" >/dev/null; then
		test-ok "RECIPE_FAIL carries a non-empty source span"
	else
		test-fail "RECIPE_FAIL span is missing or empty"
	fi
)

test-end
