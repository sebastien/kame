#!/usr/bin/env bash
# Spec: docs/spec/008-cache.md — portable in-memory backend
# Spec: docs/spec/013-tests.md — T008-04-cache-memory-backend
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T008-04 portable in-memory cache backend"
test-step "runtime cache identity, publication, and hit validation"
(
	cd "$CLI_ROOT/src/go/kame"
	so test -run TestCachedTaskUsesPortableMemoryBackend -check=warn ./program
)
test-ok "portable memory backend publishes and reuses a complete cached task record"
test-end
