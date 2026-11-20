#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Format
# Spec: docs/spec/013-tests.md — T004-07-lang-fmt-modes
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T004-07 format modes"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "stdin formats per language"
printf 'value=42\n' >definition.km
cli_run --stdin definition.km -- do fmt
cli_expect_status 0
cli_expect_stdout "value = 42
"
cli_expect_stderr_empty

printf 'rule ./x\n  true\n' >rule.km
cli_run --stdin rule.km -- do fmt --lang km
cli_expect_status 0
cli_expect_stdout "rule ./x
	true
"

cli_run --stdin rule.km -- do fmt --lang km --indent spaces
cli_expect_status 0
cli_expect_stdout "rule ./x
    true
"

cli_run --stdin rule.km -- do fmt --lang km --indent spaces --indent-width 2
cli_expect_status 0
cli_expect_stdout "rule ./x
  true
"

printf '( join "," [a,b] )' >expression.km
cli_run --stdin expression.km -- do fmt --lang expr
cli_expect_status 0
cli_expect_stdout '(join "," [a, b])
'

printf 'echo ${count [a, b]}' >template.km
cli_run --stdin template.km -- do fmt --lang template
cli_expect_status 0
cli_expect_stdout 'echo ${count [a, b]}'

test-step "-n lists changed files and exits 1"
fixture_copy fmt fmt-modes
(
	cd fmt-modes
	cli_run -- do fmt --lang km -n canonical.km
	cli_expect_status 0
	cli_expect_stdout_empty

	cli_run -- do fmt --lang km -n unformatted.km
	cli_expect_status 1
	cli_expect_stdout "unformatted.km
"
)

test-step "-i replaces files atomically"
(
	cd fmt-modes
	cp unformatted.km replace.km
	cli_run -- do fmt --lang km -i replace.km
	cli_expect_status 0
	cli_expect_stdout_empty
	if cmp -s replace.km canonical.km; then
		test-ok "in-place formatting matches the canonical fixture"
	else
		test-fail "in-place formatting differs from the canonical fixture"
	fi
	if [ -z "$(find . -name '*.kame-fmt.tmp' -print -quit)" ]; then
		test-ok "no temporary formatter files left behind"
	else
		test-fail "formatter temporary file left behind"
	fi
)

test-step "format usage errors"
(
	cd fmt-modes
	cli_run -- do fmt -i -n canonical.km
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_CONFLICT"

	cli_run -- do fmt --lang bogus
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_VALUE_INVALID"

	cli_run -- do fmt --lang
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_NO_VALUE"

	cli_run -- do fmt --bogus
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_UNKNOWN"

	cli_run -- do fmt --indent invalid
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_VALUE_INVALID"

	cli_run -- do fmt --indent-width 0
	cli_expect_status 2
	cli_expect_stderr_contains "OPT_VALUE_INVALID"
)

test-step "invalid sources fail without partial output"
(
	cd fmt-modes
	printf '[unclosed' >invalid.km
	cli_run -- do fmt --lang expr invalid.km
	cli_expect_status 1
	cli_expect_stderr_contains "PARSE_ERR"
	cli_expect_stdout_empty
)

test-end
