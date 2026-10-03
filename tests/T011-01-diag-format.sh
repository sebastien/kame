#!/usr/bin/env bash
# Spec: docs/spec/011-diagnostics.md — Codes, Rules
# Spec: docs/spec/009-cli.md — Diagnostics
# Spec: docs/spec/013-tests.md — T011-01-diag-format
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T011-01 diagnostic formatting"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "registry codes are unique and stable-shaped"
registry_codes="$(awk -F'`' '/^\| `[A-Z]+(_[A-Z]+)*` \|/ { print $2 }' "$ORIGINAL_PATH/docs/spec/011-diagnostics.md")"
if printf '%s\n' "$registry_codes" | grep -Ev '^[A-Z]+(_[A-Z]+)*$' >/dev/null || [ "$(printf '%s\n' "$registry_codes" | sort -u | wc -l)" -ne "$(printf '%s\n' "$registry_codes" | wc -l)" ]; then
	test-fail "diagnostic registry has malformed or duplicate codes"
else
	test-ok "diagnostic registry codes are unique and stable-shaped"
fi

fixture_copy errors diag-format

test-step "CLI diagnostics use the documented layout"
cli_run -- do run --lang expr --bogus
cli_expect_status 2
	cli_expect_stderr 'error OPT_UNKNOWN: unknown option: --bogus
'

cli_run -- do plan -f
cli_expect_status 2
	cli_expect_stderr 'error OPT_NO_VALUE: missing value for -f
'

test-step "build failures report the stable code and a message"
(
	cd diag-format
	cli_run -- ./failing.out
	cli_expect_status 1
	cli_expect_stderr_contains 'error RECIPE_FAIL: recipe exited unsuccessfully'
)

test-step "source-qualified parse diagnostics keep the source prefix"
printf 'value = (unclosed\n' >broken.kmk
cli_run -- -f broken.kmk anything
cli_expect_status 1
	if grep -Eq '^[^:]+:[0-9]+:[0-9]+: error PARSE_ERR: .+' "$CLI_ERR"; then
	test-ok "parse diagnostic is source-qualified"
else
	test-fail "parse diagnostic layout: $(test_fmt_line "$(cat "$CLI_ERR")")"
fi

test-step "suggestions are separate note lines"
(
	cd diag-format
	cli_run -- do plan nope.txt
	cli_expect_status 1
	cli_expect_stderr_contains 'TGT_NO_RULE' 'note: did you mean ./nope.txt?'

	cli_run -- do plan ./nope.txt
	cli_expect_status 1
	cli_expect_stderr_contains 'TGT_NO_RULE'
	if grep -q 'note:' "$CLI_ERR"; then
		test-fail "explicit ./ targets must not suggest ./"
	else
		test-ok "no suggestion for explicit ./ targets"
	fi
)

test-step "human and JSON diagnostics describe the same failure"
(
	cd diag-format
	cli_run -- do plan nope.txt
	human="$(cat "$CLI_ERR")"
	cli_run -- do plan --json nope.txt
	cli_expect_status 1
	cli_expect_json_query "$CLI_OUT" '.diagnostic.code' 'TGT_NO_RULE'
	cli_expect_json_query "$CLI_OUT" '.diagnostic.notes[0]' 'did you mean ./nope.txt?'
	message="$(jq -r '.diagnostic.message' "$CLI_OUT")"
	if printf '%s' "$human" | grep -Fq "$message"; then
		test-ok "JSON message appears in the human diagnostic"
	else
		test-fail "human and JSON messages differ"
	fi
)

test-end
