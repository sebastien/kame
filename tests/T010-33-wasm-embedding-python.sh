#!/usr/bin/env bash
# Spec: docs/spec/028-embedding-apis.md — asynchronous Python embedding lifecycle
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-33 Python embedding API lifecycle"
test-step "Python client compiles, evaluates, builds and watches through the CLI"
cd "$CLI_ROOT"
cli_require_tools
cli_build
if KAME_TEST_CLI="$CLI_BIN" PYTHONPATH="$CLI_ROOT/python" python3 -m unittest discover -s python/tests -v; then
	test-ok "Python embedding API passes parse, grants, build, watch, cancellation, and 100 lifecycle cycles"
else
	test-fail "Python embedding API conformance failed"
fi

test-end
