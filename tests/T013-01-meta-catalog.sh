#!/usr/bin/env bash
# Spec: docs/spec/013-tests.md — Test catalog
# Spec: docs/spec/013-tests.md — T013-01-meta-catalog
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T013-01 test catalog consistency"

test-step "every test script is catalogued"
catalog="$CLI_ROOT/tests/CATALOG.tsv"
if [ -f "$catalog" ]; then
	test-ok "catalog exists"
else
	test-fail "missing catalog: $catalog"
	exit 1
fi

test-step "catalog names follow T<spec>-<seq>-<category>-<topic>.sh"
while IFS=$'\t' read -r file spec category covers; do
	if [ -z "$file" ] || [ "${file#\#}" != "$file" ]; then
		continue
	fi
	if printf '%s' "$file" | grep -Eq '^tests/T[0-9]{3}-[0-9]{2}-(host|lang|eval|runtime|lib|cache|cli|diag|streams|meta)-[a-z0-9-]+\.sh$'; then
		test-ok "name $(basename "$file")"
	else
		test-fail "invalid catalog name: $file"
	fi
	if [ "${file#tests/T$spec-}" != "$file" ]; then
		test-ok "spec prefix $spec"
	else
		test-fail "$file does not match spec $spec"
	fi
	if [ -n "$covers" ]; then
		test-ok "coverage text present"
	else
		test-fail "$file has no coverage text"
	fi
	if [ -f "$CLI_ROOT/$file" ]; then
		test-ok "file exists"
	else
		test-fail "catalogued file missing: $file"
	fi
done <"$catalog"

test-step "every test script has a catalog row"
missing=0
for test_file in "$CLI_ROOT"/tests/T*.sh; do
	relative="tests/$(basename "$test_file")"
	if grep -q "^$relative	" "$catalog"; then
		continue
	fi
	test-fail "not catalogued: $relative"
	missing=1
done
if [ "$missing" = 0 ]; then
	test-ok "no uncatalogued tests"
fi

test-end
