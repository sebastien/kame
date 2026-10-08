#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — manual release and APE bootstrap helpers
# Spec: docs/spec/013-tests.md — T015-06-dist-release-scripts
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T015-06 release and APE helper argument handling"
cd "$CLI_ROOT"

release="$CLI_ROOT/src/sh/release.sh"
build_ape="$CLI_ROOT/src/sh/build-ape.sh"

# The helpers resolve the repository root from their own location, so their
# argument and prerequisite guards run before any provisioning or network work.

test-step "release help documents the operator contract"
if "$release" --help >"$TMPDIR/help" 2>&1 &&
	grep -q 'KAME_RELEASE_PUBLIC_KEY' "$TMPDIR/help" &&
	grep -q -- '--repo OWNER/REPO' "$TMPDIR/help" &&
	grep -q -- '--notes-file FILE' "$TMPDIR/help"; then
	test-ok "release --help exits zero with usage"
else
	test-fail "release --help: status=$? output=$(cat "$TMPDIR/help")"
fi

test-step "release rejects an unknown argument"
status=0
"$release" --not-a-flag >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
if [ "$status" = 2 ] && grep -q 'unknown argument: --not-a-flag' "$TMPDIR/err"; then
	test-ok "unknown release argument is a usage error"
else
	test-fail "unknown argument: status=$status err=$(cat "$TMPDIR/err")"
fi

test-step "release requires both keys before any staging"
status=0
env -u KAME_RELEASE_PUBLIC_KEY -u KAME_RELEASE_SIGNING_KEY \
	"$release" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
if [ "$status" = 1 ] && grep -q 'set KAME_RELEASE_PUBLIC_KEY' "$TMPDIR/err"; then
	test-ok "missing public key fails before staging"
else
	test-fail "missing public key: status=$status err=$(cat "$TMPDIR/err")"
fi
status=0
KAME_RELEASE_PUBLIC_KEY="$TMPDIR/none.pem" KAME_RELEASE_SIGNING_KEY="$TMPDIR/none.pem" \
	"$release" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
if [ "$status" = 1 ] && grep -q 'set KAME_RELEASE_PUBLIC_KEY' "$TMPDIR/err"; then
	test-ok "absent key file is rejected before staging"
else
	test-fail "absent key file: status=$status err=$(cat "$TMPDIR/err")"
fi

test-step "release parses --repo and --notes-file before validating keys"
# Both flags are accepted by the parser; the run then stops at the key guard
# rather than an unknown-argument error.
status=0
env -u KAME_RELEASE_PUBLIC_KEY -u KAME_RELEASE_SIGNING_KEY \
	"$release" --repo owner/repo --notes-file "$TMPDIR/notes" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
if [ "$status" = 1 ] && grep -q 'set KAME_RELEASE_PUBLIC_KEY' "$TMPDIR/err"; then
	test-ok "--repo and --notes-file reach the key guard"
else
	test-fail "flag parsing: status=$status err=$(cat "$TMPDIR/err")"
fi

test-step "build-ape reports a missing compiler driver"
tools="$TMPDIR/no-tools"
mkdir -p "$tools"
bash_path="$(command -v bash)"
status=0
PATH="$tools" "$bash_path" "$build_ape" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
if [ "$status" = 1 ] && grep -q 'the so compiler driver is required' "$TMPDIR/err"; then
	test-ok "build-ape fails clearly without the so driver"
else
	test-fail "build-ape missing driver: status=$status err=$(cat "$TMPDIR/err")"
fi

test-end
