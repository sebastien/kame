#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — self-build inputs and generated metadata
# Spec: docs/spec/013-tests.md — T015-03-dist-self-build
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T015-03 self-build incremental"

test-step "toolchain and existing CLI"
cli_require_tools
if [ ! -x "$CLI_BIN" ]; then
	test-fail "CLI binary is missing: $CLI_BIN"
fi

test-step "version metadata is stable across invocations"
version_file="$CLI_ROOT/src/go/kame/cmd/kame/version_generated.go"
before="$(sha256sum "$version_file")"
"$CLI_ROOT/tools/generate-version.sh"
"$CLI_ROOT/tools/generate-version.sh"
after="$(sha256sum "$version_file")"
if [ "$before" = "$after" ]; then
	test-ok "version metadata did not change"
else
	test-fail "version metadata changed: $before -> $after"
fi

test-step "second build invocation skips generated version source"
cli_run --dir "$CLI_ROOT" --env "HOME=$HOME" --env TMPDIR=/tmp -- --force --json ./src/go/kame/cmd/kame/version_generated.go
cli_expect_status 0
if ! grep -q '"type":"process-started"' "$CLI_OUT"; then
	test-fail "forced version generation did not run: $(cat "$CLI_OUT")"
fi

cli_run --dir "$CLI_ROOT" --env "HOME=$HOME" --env TMPDIR=/tmp -- --json ./src/go/kame/cmd/kame/version_generated.go
cli_expect_status 0
if grep -q '"type":"process-started"' "$CLI_OUT"; then
	test-fail "fresh version source ran again: $(cat "$CLI_OUT")"
else
	test-ok "fresh version source was skipped"
fi

test-step "self-build source prerequisites are declared"
if grep -Fq '(wildcard ./src/go/kame/**/*.go)' "$CLI_ROOT/Makefile.kmk" && grep -Fq './src/go/kame/cmd/kame/version_generated.go :' "$CLI_ROOT/Makefile.kmk"; then
	test-ok "build rules declare Go sources and generated version input"
else
	test-fail "build rules are missing source or generated version inputs"
fi

test-end
