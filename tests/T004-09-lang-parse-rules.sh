#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — Rules
# Spec: docs/spec/009-cli.md — Parse
# Spec: docs/spec/013-tests.md — T004-09-lang-parse-rules
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T004-09 rule-language parse matrix"

test-step "toolchain and binary"
cli_require_tools
cli_build

for name in cached-task file indent inputs outputs recipe service task; do
	path="$(lang_fixture "rule/rules/$name.km")"
	cli_run -- do parse --json --lang km "$path"
	cli_expect_status 0 "$name"
	cli_expect_json_query "$CLI_OUT" '.lang' 'km'
	cli_expect_json_query "$CLI_OUT" '.ast.items | any(.kind == "rule")' 'true'
	cli_expect_json_query "$CLI_OUT" '.diagnostics | length' '0'
done

test-end
