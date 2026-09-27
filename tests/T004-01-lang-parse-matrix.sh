#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — Expression Language, Templates, Rules, Scripts
# Spec: docs/spec/009-cli.md — Parse
# Spec: docs/spec/013-tests.md — T004-01-lang-parse-matrix
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T004-01 language parse matrix"

test-step "toolchain and binary"
cli_require_tools
cli_build

# Function: parse_matrix DIR LANG
# Every fixture in DIR parses to a schema-1 AST with in-bounds spans.
parse_matrix() {
	local dir="$1"
	local lang="$2"
	local file size
	for file in "$dir"/*.km; do
		[ -e "$file" ] || continue
		size="$(wc -c <"$file")"
		cli_run -- do parse --lang "$lang" "$file"
		if [ "$CLI_STATUS" != 0 ]; then
			test-fail "parse $(test-relpath "$file") exited $CLI_STATUS: $(test_fmt_line "$(cat "$CLI_ERR")")"
			continue
		fi
		if jq -e --arg lang "$lang" --arg src "$file" --argjson size "$size" '
			(.schema == 1) and (.lang == $lang) and (.source == $src)
			and ((.diagnostics | length) == 0)
			and ((.ast.kind | type) == "string")
			and (([.ast | .. | objects | select(has("span")) | .span
				| select(.start < 0 or .end < .start or .end > $size)] | length) == 0)
		' "$CLI_OUT" >/dev/null 2>&1; then
			test-ok "parsed $(basename "$file")"
		else
			test-fail "AST shape: $(test-relpath "$file")"
		fi
	done
}

test-step "expressions"
parse_matrix "$(lang_fixture expr)" expr

test-step "string templates"
parse_matrix "$(lang_fixture template/string)" template

test-step "definitions and rules parse as scripts"
parse_matrix "$(lang_fixture rule/definition)" script
parse_matrix "$(lang_fixture rule/rules)" script

test-step "scripts"
parse_matrix "$(lang_fixture script)" script

test-step "target templates are excluded from the CLI parse matrix"
test_log_message "tests/data/lang/template/target has no do parse language; Go fixture tests cover target parsing"

test-end
