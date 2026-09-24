#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Parse
# Spec: docs/spec/013-tests.md — T004-03-lang-parse-io
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T004-03 parse input handling"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "missing file reads stdin and reports <stdin>"
printf ':true\n' >expr.lm
cli_run --stdin expr.lm -- do parse --lang expr
cli_expect_status 0
cli_expect_json_query "$CLI_OUT" '.source' '<stdin>'
cli_expect_json_query "$CLI_OUT" '.ast.value' 'true'
cli_expect_stderr_empty

test-step "empty stdin is a parse error with a null AST"
: >empty.lm
cli_run --stdin empty.lm -- do parse --lang expr
cli_expect_status 1
cli_expect_json_query "$CLI_OUT" '.ast' 'null'
cli_expect_json_query "$CLI_OUT" '.diagnostics[0].code' 'PARSE_ERR'
cli_expect_json_query "$CLI_OUT" '.diagnostics[0].severity' 'error'

test-step "a missing explicit file is FS_ERR"
cli_run -- do parse --lang expr missing.lm
cli_expect_status 1
cli_expect_stderr_contains "FS_ERR" "cannot read source"

test-step "parse accepts at most one file"
cli_run -- do parse --lang expr expr.lm expr.lm
cli_expect_status 2
cli_expect_stderr_contains "OPT_VALUE_INVALID"

test-end
