#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Parse, Primary Output
# Spec: docs/spec/013-tests.md — T004-05-lang-parse-goldens
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T004-05 parse AST goldens"

test-step "toolchain and binary"
cli_require_tools
cli_build

cp -a "$CLI_ROOT/src/go/kame/cmd/kame/testdata/parse" parse

test-step "CLI AST goldens are byte-stable apart from the source path"
(
	cd parse
	for entry in expr:expr template:template rule:rule script:script invalid-expr:expr; do
		name="${entry%%:*}"
		lang="${entry##*:}"
		cli_run -- do parse --json --lang "$lang" "$name.km"
		if [ "$name" = "invalid-expr" ]; then
			cli_expect_status 1
		else
			cli_expect_status 0
		fi
		if [ "$name" = "invalid-expr" ]; then
			jq -S '.diagnostic | del(.source)' "$CLI_OUT" >got.json
			jq -S '.diagnostics[0]' "$name.json" >want.json
		else
			jq -S 'del(.source)' "$CLI_OUT" >got.json
			jq -S 'del(.source)' "$name.json" >want.json
		fi
		if cmp -s got.json want.json; then
			test-ok "$name golden"
		else
			test-fail "$name golden differs"
		fi
	done
)

test-end
