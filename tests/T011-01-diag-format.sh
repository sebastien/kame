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
stream_codes="$(awk -F'`' '/^\| `KASH_[A-Z_]+` \|/ { print $2 }' "$ORIGINAL_PATH/docs/spec/042-kash-streaming.md")"
for code in $stream_codes KASH_TERMINAL_UNSUPPORTED OUTPUT_CONFLICT INPUT_UNSTABLE INPUT_CHANGE_CANCELLED PICK_UNAVAILABLE; do
	if printf '%s\n' "$registry_codes" | grep -Fxq "$code"; then
		test-ok "$code is registered"
	else
		test-fail "$code is missing from the diagnostic registry"
	fi
done

fixture_copy errors diag-format

test-step "CLI diagnostics use the documented layout"
cli_run -- do run --lang expr --bogus
cli_expect_status 2
	cli_expect_stderr 'error[OPT_UNKNOWN]: unknown option: --bogus
'

cli_run -- do plan -f
cli_expect_status 2
	cli_expect_stderr 'error[OPT_NO_VALUE]: missing value for -f
'

test-step "build failures report the stable code and a message"
(
	cd diag-format
	cli_run -- ./failing.out
	cli_expect_status 1
	cli_expect_stderr_contains 'error[RECIPE_FAIL]: [./failing.out] recipe exited unsuccessfully'
)

test-step "source-qualified parse diagnostics have a separate location block"
printf 'value = (unclosed\n' >broken.km
cli_run -- --diagnostic-format plain -f broken.km anything
cli_expect_status 1
	if grep -Eq '^  --> [^:]+:[0-9]+:[0-9]+$' "$CLI_ERR" && grep -Eq '^error\[PARSE_ERR\]: .+' "$CLI_ERR"; then
	test-ok "parse diagnostic is source-qualified"
else
	test-fail "parse diagnostic layout: $(test_fmt_line "$(cat "$CLI_ERR")")"
fi

test-step "suggestions are separate note lines"
(
	cli_run -- do plan -c 'task first'
	cli_expect_status 1
	cli_expect_stderr_contains 'TGT_NO_DEFAULT' 'note: available targets: first'

	cli_run --dir diag-format -- do plan unknown
	cli_expect_status 1
	cli_expect_stderr_contains 'TGT_NO_RULE'
	if grep -q 'note:' "$CLI_ERR"; then
		test-fail "unknown logical targets must not suggest ./"
	else
		test-ok "no path suggestion for unknown logical targets"
	fi
)

test-step "human and JSON diagnostics describe the same failure"
(
	cd diag-format
	cli_run -- do plan unknown
	human="$(cat "$CLI_ERR")"
	cli_run -- do plan --json unknown
	cli_expect_status 1
	cli_expect_json_query "$CLI_OUT" '.diagnostic.code' 'TGT_NO_RULE'
	cli_expect_json_query "$CLI_OUT" '.diagnostic.target' 'unknown'
	message="$(jq -r '.diagnostic.message' "$CLI_OUT")"
	if printf '%s' "$human" | grep -Fq "$message"; then
		test-ok "JSON message appears in the human diagnostic"
	else
		test-fail "human and JSON messages differ"
	fi
)

test-end
