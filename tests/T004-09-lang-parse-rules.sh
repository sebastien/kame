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

# Standalone rule parsing covers single rules without a leading comment.
# indent.lm carries a leading script comment and is covered as a script fixture.
for name in cached-task file inputs outputs recipe service task; do
	path="$(lang_fixture "rule/rules/$name.lm")"
	cli_run -- do parse --lang rule "$path"
	cli_expect_status 0 "$name"
	cli_expect_json_query "$CLI_OUT" '.lang' 'rule'
	cli_expect_json_query "$CLI_OUT" '.diagnostics | length' '0'
done

test-step "rule parse excludes the script-level leading comment fixture"
test_log_message "rule/rules/indent.lm parses as a script; kept out of the standalone rule matrix"

test-end
