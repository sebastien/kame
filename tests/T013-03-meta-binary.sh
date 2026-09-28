#!/usr/bin/env bash
# Spec: docs/spec/013-tests.md — Binary contract, Suite acceptance
# Spec: docs/spec/013-tests.md — T013-03-meta-binary
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T013-03 binary contract and determinism"

test-step "host tools are available"
cli_require_tools

test-step "the CLI under test is an executable debug build"
if [ -x "$KAME" ]; then
	test-ok "CLI is executable: $(test-relpath "$KAME")"
else
	test-fail "CLI is not executable: $KAME"
fi
if printf '%s' "$KAME" | grep -q 'build/kame.debug$'; then
	test-ok "tests run the debug build"
else
	test-fail "unexpected binary path: $KAME"
fi
cli_run -- --version
cli_expect_status 0
if [ "$(cat "$CLI_OUT")" = "kame $(<"$CLI_ROOT/VERSION")" ]; then
	test-ok "version matches kame VERSION"
else
	test-fail "version output: $(cat -A "$CLI_OUT")"
fi

test-step "cli_build is a no-op when the binary is current"
cli_build
before="$(stat -c %Y "$KAME")"
cli_build
after="$(stat -c %Y "$KAME")"
if [ "$before" = "$after" ]; then
	test-ok "binary was not rebuilt"
else
	test-fail "binary mtime changed without source changes"
fi

test-step "the same build is deterministic across fresh copies"
fixture_copy project deterministic-a
fixture_copy project deterministic-b
cli_run --dir deterministic-a -- --json ./build/app
cli_expect_status 0
cp "$CLI_OUT" events-a.jsonl
cli_run --dir deterministic-b -- --json ./build/app
cli_expect_status 0
cp "$CLI_OUT" events-b.jsonl
if diff -u <(jq -cS 'del(.node, .request, .generation, .attempt) | {type, target, data}' events-a.jsonl) \
	<(jq -cS 'del(.node, .request, .generation, .attempt) | {type, target, data}' events-b.jsonl) >/dev/null; then
	test-ok "normalized event streams match"
else
	test-fail "event streams differ between fresh copies"
fi
if cmp -s deterministic-a/build/app deterministic-b/build/app; then
	test-ok "artifacts are byte-identical"
else
	test-fail "artifacts differ between fresh copies"
fi

test-end
