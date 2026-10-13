#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — Acceptance Tests
# Spec: docs/spec/011-diagnostics.md — PARSE_ERR
# Spec: docs/spec/013-tests.md — T004-02-lang-parse-invalid
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T004-02 invalid language fixtures"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "every invalid fixture yields its manifest diagnostic"
manifest="$(lang_fixture invalid/MANIFEST.tsv)"
count=0
while IFS=$'\t' read -r name code severity recovery; do
	if [ "$name" = "file" ] || [ -z "$name" ]; then
		continue
	fi
	lang=script
	case "$name" in
	rule-recipe-*)
		# Script loading defers recipe diagnostics until the interpreter is known.
		lang=rule
		;;
	expr-*)
		lang=expr
		;;
	template-string-*)
		lang=template
		;;
	template-target-*)
		test_log_message "excluded from CLI parse: $name"
		continue
		;;
	esac
	path="$(lang_fixture "invalid/$name")"
	cli_run -- do parse --json --lang "$lang" "$path"
	count=$((count + 1))
	want_status=1
	if [ "$severity" = "warning" ]; then
		want_status=0
	fi
	if [ "$CLI_STATUS" != "$want_status" ]; then
		test-fail "$name: exit $CLI_STATUS, wanted $want_status"
		continue
	fi
	if jq -e --arg code "$code" --arg sev "$severity" '
		(if type == "array" then .[0].diagnostic elif has("diagnostic") then .diagnostic else .diagnostics[0] end) as $d |
		($d.code == $code) and ($d.severity == $sev) and (($d.message | length) > 0)
	' "$CLI_OUT" >/dev/null 2>&1; then
		test-ok "$name ($code/$severity)"
	else
		test-fail "$name diagnostic mismatch: $(test_fmt_line "$(cat "$CLI_OUT")")"
	fi
done <"$manifest"
if [ "$count" -gt 0 ]; then
	test-ok "checked $count invalid fixtures"
else
	test-fail "manifest yielded no invalid fixtures"
fi

test-step "invalid diagnostics are printable"
cli_run -- do parse --json --lang expr "$(lang_fixture invalid/expr-empty-application.km)"
cli_expect_status 1
cli_expect_printable "$CLI_OUT"

test-step "EOF list errors report the missing delimiter once"
eof_source="$(mktemp "${TMPDIR:-/tmp}/kame-list-eof.XXXXXX")"
printf '%s' '[1 2' >"$eof_source"
cli_run --stdin "$eof_source" -- do parse --json --lang expr
rm -f "$eof_source"
cli_expect_status 1
if jq -se --arg message "unclosed list; expected ']'" 'length == 1 and .[0].diagnostic.code == "PARSE_ERR" and .[0].diagnostic.message == $message' "$CLI_OUT" >/dev/null; then
	test-ok "one actionable list EOF diagnostic"
else
	test-fail "list EOF diagnostics were misleading or duplicated"
fi

test-end
