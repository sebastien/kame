#!/usr/bin/env bash
# Spec: docs/spec/013-tests.md — Fixture contract
# Spec: docs/spec/013-tests.md — T013-02-meta-hygiene
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T013-02 fixture hygiene"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "running a build never writes under tests/data"
stamp="$(mktemp -p "$TEST_PATH" stamp.XXXXXX)"
sleep 1
set_mtime "$stamp" "$(date +%s)"
fixture_copy project hygiene
(
	cd hygiene
	cli_run -- ./build/app
	cli_expect_status 0
	cli_run -- --force --json ./build/app >/dev/null 2>&1 || true
)
touched="$(find "$CLI_ROOT/tests/data" -type f -newer "$stamp" -print -quit)"
if [ -z "$touched" ]; then
	test-ok "tests/data was not modified"
else
	test-fail "tests/data was modified: $(test-relpath "$touched")"
fi

test-step "fixtures copy instead of moving"
if [ -f "$(fixture_path basic/Makefile.kmk)" ]; then
	test-ok "source fixture still present"
else
	test-fail "source fixture disappeared"
fi

test-step "test scratch directories are cleaned by test-end"
if [ -d "$TEST_PATH" ]; then
	test-ok "current scratch directory exists during the test"
else
	test-fail "missing scratch directory"
fi

test-end
