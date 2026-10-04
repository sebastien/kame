#!/usr/bin/env bash
# Spec: docs/spec/021-cosmocc-provisioning.md — pinned toolchain archive
# Spec: docs/spec/013-tests.md — T015-03-dist-cosmocc-integrity
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T015-03 pinned cosmocc provisioning integrity"
cd "$CLI_ROOT"
fixture="$TMPDIR/cosmocc-fixture"
mkdir -p "$fixture/bin"
printf '#!/bin/sh\nprintf cosmocc-fixture\n' >"$fixture/bin/cosmocc"
chmod +x "$fixture/bin/cosmocc"
(cd "$fixture" && zip -q -r "$TMPDIR/cosmocc.zip" bin)
sha() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}
archive_hash=$(sha "$TMPDIR/cosmocc.zip")

test-step "a verified archive installs atomically and reuses its pin"
COSMOCC_URL="file://$TMPDIR/cosmocc.zip" COSMOCC_SHA256="$archive_hash" tools/provision-cosmocc.sh "$fixture/verified"
if [ -x "$fixture/verified/bin/cosmocc" ] && [ "$(cat "$fixture/verified/.archive-sha256")" = "$archive_hash" ]; then
	test-ok "verified cosmocc archive installed with its digest marker"
else
	test-fail "verified archive was not installed"
fi
COSMOCC_URL="file://$TMPDIR/does-not-exist.zip" COSMOCC_SHA256="$archive_hash" tools/provision-cosmocc.sh "$fixture/verified"
test-ok "verified installation is reused without another download"

test-step "a mismatch fails before extraction or installation"
if COSMOCC_URL="file://$TMPDIR/cosmocc.zip" COSMOCC_SHA256="$(printf '%064d' 0)" tools/provision-cosmocc.sh "$fixture/rejected" >"$TMPDIR/out" 2>"$TMPDIR/err"; then
	status=0
else
	status=$?
fi
if [ "$status" = 1 ] && grep -q 'archive checksum mismatch' "$TMPDIR/err" && [ ! -e "$fixture/rejected" ]; then
	test-ok "checksum mismatch leaves no installed compiler"
else
	test-fail "checksum mismatch status=$status err=$(cat "$TMPDIR/err")"
fi

test-end
