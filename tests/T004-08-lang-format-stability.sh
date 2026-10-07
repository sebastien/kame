#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — Formatting
# Spec: docs/spec/013-tests.md — T004-08-lang-format-stability
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T004-08 format preserves the AST"

test-step "toolchain and binary"
cli_require_tools
cli_build

# Function: fmt_stable_ast DIR LANG
# Formatting must preserve the AST apart from spans and the source path.
fmt_stable_ast() {
	local dir="$1"
	local lang="$2"
	local file
	for file in "$dir"/*.km; do
		[ -e "$file" ] || continue
		cli_run -- do parse --json --lang "$lang" "$file"
		if [ "$CLI_STATUS" != 0 ]; then
			test-fail "parse $(test-relpath "$file") exited $CLI_STATUS"
			continue
		fi
		jq -S 'walk(if type == "object" then del(.span, .start, .end) else . end) | del(.source)' "$CLI_OUT" >before.json
		cli_run -- do fmt --lang "$lang" "$file"
		if [ "$CLI_STATUS" != 0 ]; then
			test-fail "fmt $(test-relpath "$file") exited $CLI_STATUS"
			continue
		fi
		cp "$CLI_OUT" formatted.km
		cli_run -- do parse --json --lang "$lang" formatted.km
		if [ "$CLI_STATUS" != 0 ]; then
			test-fail "parse formatted $(test-relpath "$file") exited $CLI_STATUS"
			continue
		fi
		jq -S 'walk(if type == "object" then del(.span, .start, .end) else . end) | del(.source)' "$CLI_OUT" >after.json
		if cmp -s before.json after.json; then
			test-ok "AST stable $(basename "$file")"
		else
			test-fail "AST changed after formatting: $(test-relpath "$file")"
		fi
	done
}

test-step "expressions"
fmt_stable_ast "$(lang_fixture expr)" expr

test-step "string templates"
fmt_stable_ast "$(lang_fixture template/string)" template

test-step "definitions and rules"
fmt_stable_ast "$(lang_fixture rule/definition)" script
fmt_stable_ast "$(lang_fixture rule/rules)" script

test-step "scripts"
fmt_stable_ast "$(lang_fixture script)" script

test-end
