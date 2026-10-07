#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Plan
# Spec: docs/spec/006-runtime.md — Planning
# Spec: docs/spec/013-tests.md — T009-05-cli-plan
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T009-05 CLI plan inspection"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy plan plan

test-step "plan emits one JSON object per target and runs nothing"
(
	cd plan
	cli_run -- do plan ./artifact.txt
	cli_expect_status 0
	cli_expect_jsonl "$CLI_OUT"
	cli_expect_json_query "$CLI_OUT" '.schema' '1'
	cli_expect_json_query "$CLI_OUT" '.type' 'plan'
	cli_expect_json_query "$CLI_OUT" '.target' './artifact.txt'
	cli_expect_json_query "$CLI_OUT" '.inputs | join(",")' './input.txt'
	cli_expect_json_query "$CLI_OUT" '.outputs | join(",")' './artifact.txt'
	cli_expect_json_query "$CLI_OUT" '.captures | length' '0'
	cli_expect_json_query "$CLI_OUT" '.freshness' 'unknown'
	cli_expect_json_query "$CLI_OUT" '.rule.start | type' 'number'
	cli_expect_stderr_empty
	cli_expect_no_file ./runs.log
)

test-step "plan renders capture bindings for template targets"
(
	cd plan
	cli_run -- do plan ./capture-one.o
	cli_expect_status 0
	cli_expect_json_query "$CLI_OUT" '.captures[0].name' 'name'
	cli_expect_json_query "$CLI_OUT" '.captures[0].value' 'one'
	cli_expect_json_query "$CLI_OUT" '.inputs | join(",")' './src/one.c'
	cli_expect_json_query "$CLI_OUT" '.outputs | join(",")' './capture-one.o'
)

test-step "plan describes a task and its dependency"
(
	cd plan
	cli_run -- do plan default
	cli_expect_status 0
	cli_expect_json_query "$CLI_OUT" '.outputs | join(",")' 'default'
	cli_expect_json_query "$CLI_OUT" '.inputs | join(",")' './artifact.txt'
)

test-step "plan reports unknown freshness when the body may discover dependencies"
(
	cd plan
	cli_run -- do plan ./dynamic.out
	cli_expect_status 0
	cli_expect_json_query "$CLI_OUT" '.freshness' 'unknown'
	cli_expect_json_query "$CLI_OUT" '.inputs | length' '0'
)

test-step "plan does not infer body-less rule freshness from output timestamps"
(
	cd plan
	cli_run -- do plan ./static.out
	cli_expect_status 0
	cli_expect_json_query "$CLI_OUT" '.freshness' 'unknown'
	cli_expect_no_file ./static.out

	printf 'result' >./static.out
	set_mtime ./static.out 2000000000
	cli_run -- do plan ./static.out
	cli_expect_status 0
	cli_expect_json_query "$CLI_OUT" '.freshness' 'unknown'

	set_mtime ./static.out 1000000000
	cli_run -- do plan ./static.out
	cli_expect_status 0
	cli_expect_json_query "$CLI_OUT" '.freshness' 'unknown'
)

test-step "plan accepts multiple targets"
(
	cd plan
	cli_run -- do plan ./artifact.txt ./capture-one.o
	cli_expect_status 0
	cli_expect_jsonl "$CLI_OUT"
	if [ "$(jq -s -r '[.[].target] | join(",")' "$CLI_OUT")" = "./artifact.txt,./capture-one.o" ]; then
		test-ok "two plan objects in target order"
	else
		test-fail "plan targets were not emitted in order"
	fi
)

test-step "plan selects the default target when none is given"
(
	cd plan
	cli_run -- do plan
	cli_expect_status 0
	cli_expect_json_query "$CLI_OUT" '.target' 'default'
	cli_expect_json_query "$CLI_OUT" '.outputs | join(",")' 'default'
	cli_expect_json_query "$CLI_OUT" '.inputs | join(",")' './artifact.txt'
	cli_expect_stderr_empty
)

test-step "plan without a default fails with the available targets"
cli_run -- do plan -c 'task first :'
cli_expect_status 1
cli_expect_stdout_empty
cli_expect_stderr_contains "TGT_NO_DEFAULT" "available targets: first"

cli_run -- do plan --json -c 'task first :'
cli_expect_status 1
cli_expect_stderr_empty
cli_expect_json_query "$CLI_OUT" '.type' 'diagnostic'
cli_expect_json_query "$CLI_OUT" '.diagnostic.code' 'TGT_NO_DEFAULT'
cli_expect_json_query "$CLI_OUT" '.diagnostic.notes[0]' 'available targets: first'

test-step "plan usage and failure diagnostics"
(
	cd plan
	cli_run -- do plan ./nope.txt
	cli_expect_status 1
	cli_expect_stderr_contains "TGT_NO_RULE"

	cli_run -- do plan nope.txt
	cli_expect_status 1
	cli_expect_stderr_contains "TGT_NO_RULE" "note: did you mean ./nope.txt?"

	cli_run -- do plan --json ./nope.txt
	cli_expect_status 1
	cli_expect_jsonl "$CLI_OUT"
	cli_expect_stderr_empty
	cli_expect_json_query "$CLI_OUT" '.type' 'diagnostic'
	cli_expect_json_query "$CLI_OUT" '.diagnostic.code' 'TGT_NO_RULE'
	cli_expect_json_query "$CLI_OUT" '.diagnostic.severity' 'error'
)

test-end
