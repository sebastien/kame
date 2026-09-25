#!/usr/bin/env bash
# Spec: docs/spec/011-diagnostics.md — Codes
# Spec: docs/spec/013-tests.md — T011-02-diag-integrity
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T011-02 diagnostic integrity sweep"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy errors diag-sweep
fixture_copy json diag-json

# Function: sweep_dir DIR CODE ARGS...
# Runs ARGS in DIR and requires CODE with a non-empty printable message.
sweep_dir() {
	local dir="$1"
	local code="$2"
	shift 2
	cli_run --dir "$dir" -- "$@"
	cli_expect_status_nonzero "$code"
	if ! grep -Fq "$code" "$CLI_OUT" && ! grep -Fq "$code" "$CLI_ERR"; then
		test-fail "$code absent from diagnostics"
		return 1
	fi
	cli_expect_printable "$CLI_OUT"
	cli_expect_printable "$CLI_ERR"
	if grep -Eq "[A-Z_]+: .+" "$CLI_ERR" || grep -Eq "[A-Z_]+: .+" "$CLI_OUT" || grep -Eq '"message":"[^"]+' "$CLI_OUT"; then
		test-ok "$code message non-empty"
	else
		test-fail "$code message empty"
	fi
}

test-step "usage diagnostics"
sweep_dir . OPT_UNKNOWN do expr --bogus
sweep_dir . OPT_VALUE_INVALID do expr -c '(count)' --allow-read=
sweep_dir . OPT_NO_VALUE do plan
sweep_dir . CMD_UNKNOWN do bogus
sweep_dir . BUILD_NO_SOURCE --force

test-step "selection and planning diagnostics"
sweep_dir diag-sweep TGT_NO_RULE ./no-such.txt
sweep_dir diag-sweep TGT_AMBIG ./amb-q.c
sweep_dir diag-sweep DEP_CYCLE ./cycle.out
sweep_dir diag-sweep FEATURE_UNSUP serve

test-step "execution diagnostics"
sweep_dir diag-sweep RECIPE_FAIL ./failing.out
sweep_dir diag-sweep OUTPUT_MISSING ./missing.out
sweep_dir diag-sweep OUTPUT_CONFLICT ./conflict.out
sweep_dir diag-sweep RECIPE_TIMEOUT --timeout 100 ./timeout.out

test-step "language and capability diagnostics"
sweep_dir . PARSE_ERR do parse --lang expr
sweep_dir . REF_MISSING do expr -c 'unknown-name'
sweep_dir . EXPR_INVALID do expr -c '(first 1)'
sweep_dir . CAP_DENIED do expr -c '(read "x")'
sweep_dir diag-sweep NO_ARTIFACT do cat noart
sweep_dir diag-sweep FS_ERR -f missing-build.lmk anything

test-end
