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
	cli_run -- do parse --lang "$lang" "$path"
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
		((.diagnostics | length) > 0)
		and (.diagnostics[0].code == $code)
		and (.diagnostics[0].severity == $sev)
		and ((.diagnostics[0].message | length) > 0)
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
cli_run -- do parse --lang expr "$(lang_fixture invalid/expr-empty-application.lm)"
cli_expect_status 1
cli_expect_printable "$CLI_OUT"

test-end
