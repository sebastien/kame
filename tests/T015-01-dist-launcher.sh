#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — launcher, provisioning, bootstrap
# Spec: docs/spec/013-tests.md — T015-01-dist-launcher
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-release.sh"

test-start "T015-01 distribution launcher"
cd "$CLI_ROOT"

launcher="$CLI_ROOT/bin/kame"

sha() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

root="$TMPDIR/t015"
release="$root/release"
cache="$root/cache"
mkdir -p "$release"
release_test_keys "$root/keys"
export KAME_RELEASE_PUBKEY_B64_OVERRIDE="$RELEASE_TEST_KEY_B64"

cat >"$release/kame-linux-x86_64" <<'SH'
#!/bin/sh
if [ "${1:-}" = "--version" ] || [ "${1:-}" = "-V" ]; then
	echo "kame 9.9.9"
	exit 0
fi
echo "native:$*"
SH
chmod +x "$release/kame-linux-x86_64"
printf 'import process from "node:process";const a=process.argv.slice(2);process.stdout.write("wasm:"+a.join(",")+"\\n");\n' >"$release/kame.js"
printf 'dummy-wasm' >"$release/kame.wasm"
printf '9.9.9\n' >"$release/VERSION"
release_test_sign "$release" 9.9.9 fixture "$root/keys"

export KAME_VERSION=9.9.9
export KAME_RELEASE_URL="file://$release"
export KAME_HOME="$cache"

test-step "a lone version request provisions nothing"
for flag in --version -V; do
	out="$(KAME_NO_DOWNLOAD=1 KAME_BACKEND=invalid KAME_JS=/nonexistent "$launcher" "$flag")"
	if [ "$out" = "kame 9.9.9 (runtime=unknown)" ] && [ ! -e "$cache" ]; then
		test-ok "$flag answers without backend selection or provisioning"
	else
		test-fail "$flag: out=$out cache-exists=$([ -e "$cache" ] && echo yes || echo no)"
	fi
done

test-step "the explicit latest channel resolves a release version without provisioning"
out="$(KAME_VERSION=latest KAME_LATEST_URL="file://$release/VERSION" KAME_HOME="$root/latest-version-cache" "$launcher" --version)"
if [ "$out" = "kame 9.9.9 (runtime=unknown)" ] && [ ! -e "$root/latest-version-cache" ]; then
	test-ok "latest resolves its signed-release version asset before --version"
else
	test-fail "latest version lookup: out=$out"
fi
set +e
latest_offline_out="$(KAME_VERSION=latest KAME_NO_DOWNLOAD=1 KAME_HOME="$root/latest-offline-cache" "$launcher" --version 2>&1)"
latest_offline_status=$?
set -e
if [ "$latest_offline_status" = 1 ] && grep -q 'latest channel selection requires a download' <<<"$latest_offline_out" && [ ! -e "$root/latest-offline-cache" ]; then
	test-ok "latest selection fails clearly when downloads are disabled"
else
	test-fail "offline latest lookup: status=$latest_offline_status out=$latest_offline_out"
fi
out="$(KAME_VERSION=latest KAME_LATEST_URL="file://$release/VERSION" KAME_RELEASE_URL="file://$release" KAME_HOME="$root/latest-cache" KAME_BACKEND=wasm "$launcher" latest)"
if [ "$out" = "wasm:latest" ] && [ -f "$root/latest-cache/9.9.9/wasm/kame.js" ] && \
	[ ! -e "$root/latest-cache/9.9.9/launcher" ]; then
	test-ok "latest provisions runtime assets without downloading a shell launcher"
else
	test-fail "latest backend: out=$out"
fi

set +e
invalid_version_out="$(KAME_VERSION='../9.9.9' KAME_HOME="$root/invalid-version-cache" "$launcher" --version 2>&1)"
invalid_version_status=$?
set -e
if [ "$invalid_version_status" = 1 ] && grep -q 'invalid version' <<<"$invalid_version_out" && [ ! -e "$root/invalid-version-cache" ]; then
	test-ok "version overrides cannot escape the cache namespace"
else
	test-fail "invalid version: status=$invalid_version_status out=$invalid_version_out"
fi

test-step "the wasm backend provisions, verifies, and dispatches"
out="$(KAME_BACKEND=wasm "$launcher" alpha beta)"
if [ "$out" = "wasm:alpha,beta" ] && [ -f "$cache/9.9.9/wasm/kame.js" ] && \
	[ -f "$cache/9.9.9/wasm/kame.wasm" ] && [ ! -e "$cache/9.9.9/launcher" ]; then
	test-ok "pinned wasm artifacts installed and executed without launcher self-update"
else
	test-fail "wasm backend: out=$out"
fi
out="$(KAME_BACKEND=wasm KAME_NO_DOWNLOAD=1 "$launcher" gamma)"
if [ "$out" = "wasm:gamma" ]; then
	test-ok "a populated cache is reused with downloads disabled"
else
	test-fail "cache reuse: out=$out"
fi

test-step "auto selects the native when the host and probe succeed"
out="$("$launcher" a b)"
if [ "$out" = "native:a b" ] && [ "$(cat "$cache/9.9.9/.backend")" = "native" ]; then
	test-ok "auto provisioned and selected the native"
else
	test-fail "auto backend: out=$out"
fi

test-step "KAME_BIN bypasses detection and download"
out="$(KAME_BIN=/bin/echo "$launcher" hi there)"
if [ "$out" = "hi there" ]; then
	test-ok "KAME_BIN is executed directly"
else
	test-fail "KAME_BIN: out=$out"
fi

test-step "a checksum mismatch fails closed and installs nothing"
bad="$root/bad"
mkdir -p "$bad"
cp "$release/"* "$bad/"
cp "$release/PROVENANCE.json" "$bad/"
{
	echo "$(sha "$bad/kame-linux-x86_64")  kame-linux-x86_64"
	printf '%064d  kame.js\n' 0
echo "$(sha "$bad/kame.wasm")  kame.wasm"
echo "$(sha "$bad/VERSION")  VERSION"
} | sort -k2 >"$bad/SHA256SUMS"
echo "$(sha "$bad/PROVENANCE.json")  PROVENANCE.json" >>"$bad/SHA256SUMS"
sort -k2 "$bad/SHA256SUMS" -o "$bad/SHA256SUMS"
openssl pkeyutl -sign -inkey "$root/keys/signing.pem" -rawin -in "$bad/SHA256SUMS" -out "$bad/SHA256SUMS.sig"
set +e
KAME_HOME="$root/badcache" KAME_RELEASE_URL="file://$bad" KAME_BACKEND=wasm "$launcher" x >"$root/out" 2>"$root/err"
status=$?
set -e
if [ "$status" = 1 ] && grep -q 'checksum mismatch' "$root/err" && [ ! -e "$root/badcache/9.9.9/wasm" ]; then
	test-ok "checksum mismatch is fatal with no partial install"
else
	test-fail "checksum mismatch: status=$status err=$(cat "$root/err")"
fi

test-step "a missing verifier fails closed"
tools="$root/tools"
mkdir -p "$tools"
for tool in dirname tr mkdir cat cut rm sleep kill uname mv chmod sort rmdir curl mktemp; do
	if path=$(command -v "$tool" 2>/dev/null); then
		ln -sf "$path" "$tools/$tool"
	fi
done
set +e
PATH="$tools" KAME_HOME="$root/nocache" KAME_RELEASE_URL="file://$release" KAME_BACKEND=wasm "$launcher" x >"$root/out" 2>"$root/err"
status=$?
set -e
if [ "$status" = 1 ] && grep -q 'no signature verifier' "$root/err"; then
	test-ok "missing verifier is fatal"
else
	test-fail "missing verifier: status=$status err=$(cat "$root/err")"
fi

test-end
