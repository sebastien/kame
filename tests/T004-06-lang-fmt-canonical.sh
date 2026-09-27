#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — Formatting
# Spec: docs/spec/009-cli.md — Format
# Spec: docs/spec/013-tests.md — T004-06-lang-fmt-canonical
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T004-06 format canonical form and idempotence"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "fixture formatting matches the canonical fixture"
fixture_copy fmt fmtcheck
(
	cd fmtcheck
	cli_run -- do fmt --lang script canonical.kmk
	cli_expect_status 0
	cli_expect_stdout_file canonical.kmk

	cli_run -- do fmt --lang script unformatted.kmk
	cli_expect_status 0
	cli_expect_stdout_file canonical.kmk

	cli_run -- do fmt --lang script -n canonical.kmk
	cli_expect_status 0
	cli_expect_stdout_empty

	cli_run -- do fmt --lang script -n unformatted.kmk
	cli_expect_status 1
	cli_expect_stdout "unformatted.kmk
"
)

# Function: fmt_stable DIR LANG
# Formatting any fixture twice must be a fixed point, and -n must then be clean.
fmt_stable() {
	local dir="$1"
	local lang="$2"
	local file
	for file in "$dir"/*.km; do
		[ -e "$file" ] || continue
		cli_run -- do fmt --lang "$lang" "$file"
		if [ "$CLI_STATUS" != 0 ]; then
			test-fail "fmt $(test-relpath "$file") exited $CLI_STATUS: $(test_fmt_line "$(cat "$CLI_ERR")")"
			continue
		fi
		cp "$CLI_OUT" once.out
		cli_run --stdin once.out -- do fmt --lang "$lang"
		if [ "$CLI_STATUS" != 0 ]; then
			test-fail "reformat $(test-relpath "$file") exited $CLI_STATUS"
			continue
		fi
		if cmp -s once.out "$CLI_OUT"; then
			test-ok "idempotent $(basename "$file")"
		else
			test-fail "not idempotent: $(test-relpath "$file")"
		fi
		cli_run -- do fmt --lang "$lang" -n once.out
		if [ "$CLI_STATUS" = 0 ]; then
			test-ok "check clean $(basename "$file")"
		else
			test-fail "fmt -n reported changes after formatting: $(test-relpath "$file")"
		fi
	done
}

test-step "expressions"
fmt_stable "$(lang_fixture expr)" expr

test-step "string templates"
fmt_stable "$(lang_fixture template/string)" template

test-step "definitions and rules"
fmt_stable "$(lang_fixture rule/definition)" script
fmt_stable "$(lang_fixture rule/rules)" script

test-step "scripts"
fmt_stable "$(lang_fixture script)" script

test-end
