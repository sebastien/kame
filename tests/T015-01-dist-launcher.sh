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
mkdir -p "$release/bin"
release_test_keys "$root/keys"
export KAME_RELEASE_PUBKEY_B64_OVERRIDE="$RELEASE_TEST_KEY_B64"

cat >"$release/kame.com" <<'SH'
#!/bin/sh
if [ "${1:-}" = "--version" ] || [ "${1:-}" = "-V" ]; then
	echo "kame 9.9.9"
	exit 0
fi
echo "ape:$*"
SH
chmod +x "$release/kame.com"
printf 'const a=process.argv.slice(2);process.stdout.write("wasm:"+a.join(",")+"\\n");\n' >"$release/kame.js"
printf 'dummy-wasm' >"$release/kame.wasm"
printf '9.9.9\n' >"$release/VERSION"
cp "$CLI_ROOT/Makefile.bootstrap" "$release/Makefile.bootstrap"
release_test_stamp_launcher "$launcher" "$release/bin/kame" 9.9.9
release_test_sign "$release" 9.9.9 fixture "$root/keys"

export KAME_VERSION=9.9.9
export KAME_RELEASE_URL="file://$release"
export KAME_HOME="$cache"

test-step "a lone version request provisions nothing"
out="$("$launcher" --version)"
if [ "$out" = "kame 9.9.9" ] && [ ! -e "$cache" ]; then
	test-ok "--version answers without provisioning"
else
	test-fail "--version: out=$out cache-exists=$([ -e "$cache" ] && echo yes || echo no)"
fi

test-step "the explicit latest channel resolves a release version without provisioning"
out="$(KAME_VERSION=latest KAME_LATEST_URL="file://$release/VERSION" KAME_HOME="$root/latest-version-cache" "$launcher" --version)"
if [ "$out" = "kame 9.9.9" ] && [ ! -e "$root/latest-version-cache" ]; then
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
	[ -x "$root/latest-cache/9.9.9/launcher/kame" ]; then
	test-ok "latest updates to its verified pinned launcher and provisions the selected release"
else
	test-fail "latest backend: out=$out"
fi

test-step "latest revalidates its cached launcher before handoff"
printf 'tampered launcher\n' >"$root/latest-cache/9.9.9/launcher/kame"
set +e
latest_tamper_out="$(KAME_VERSION=latest KAME_LATEST_URL="file://$release/VERSION" KAME_RELEASE_URL="file://$release" KAME_HOME="$root/latest-cache" KAME_BACKEND=wasm "$launcher" latest 2>&1)"
latest_tamper_status=$?
set -e
if [ "$latest_tamper_status" = 1 ] && grep -q 'checksum mismatch: cached bin/kame' <<<"$latest_tamper_out"; then
	test-ok "tampered latest launcher is rejected before execution"
else
	test-fail "latest launcher tamper: status=$latest_tamper_status out=$latest_tamper_out"
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

test-step "auto selects the APE when the host and probe succeed"
out="$("$launcher" a b)"
if [ "$out" = "ape:a b" ] && [ "$(cat "$cache/9.9.9/.backend")" = "ape" ]; then
	test-ok "auto provisioned and selected the APE"
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
mkdir -p "$bad/bin"
cp "$release/kame.com" "$release/kame.js" "$release/kame.wasm" "$release/VERSION" "$bad/"
cp "$release/Makefile.bootstrap" "$bad/"
cp "$release/PROVENANCE.json" "$bad/"
cp "$release/bin/kame" "$bad/bin/kame"
{
	echo "$(sha "$bad/kame.com")  kame.com"
	printf '%064d  kame.js\n' 0
echo "$(sha "$bad/kame.wasm")  kame.wasm"
echo "$(sha "$bad/VERSION")  VERSION"
echo "$(sha "$bad/bin/kame")  bin/kame"
echo "$(sha "$bad/Makefile.bootstrap")  Makefile.bootstrap"
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
for tool in dirname tr mkdir cat cut rm sleep kill uname mv chmod sort rmdir curl; do
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

test-step "the bootstrap Makefile provisions the launcher and delegates"
project="$root/project"
mkdir -p "$project"
sed 's|@KAME_VERSION@|9.9.9|' "$CLI_ROOT/Makefile.bootstrap" >"$project/Makefile"
set +e
out="$(cd "$project" && KAME_HOME="$root/bootcache" KAME_RELEASE_URL="file://$release" KAME_BACKEND=wasm make --no-print-directory KAME="$project/bin/kame" hello 2>&1)"
status=$?
set -e
if [ "$status" = 0 ] && [ "$out" = "wasm:hello" ] && [ -x "$project/bin/kame" ]; then
	test-ok "bootstrap provisioned bin/kame and forwarded the goal"
else
	test-fail "bootstrap: status=$status out=$out"
fi

test-step "init creates a pinned sidecar and preserves project files"
init_project="$root/init-project"
mkdir -p "$init_project"
printf 'all:\n\t@echo project-makefile\n' >"$init_project/Makefile"
before_makefile=$(sha "$init_project/Makefile")
out="$(cd "$init_project" && KAME_HOME="$root/init-cache" KAME_RELEASE_URL="file://$release" "$release/bin/kame" init)"
if [ "$out" = "Created Makefile.bootstrap for Kame 9.9.9. Use make -f Makefile.bootstrap [targets]." ] && \
	grep -qx 'KAME_VERSION ?= 9.9.9' "$init_project/Makefile.bootstrap" && \
	[ "$(sha "$init_project/Makefile")" = "$before_makefile" ]; then
	test-ok "init writes a pinned bootstrap sidecar without changing Makefile"
else
	test-fail "init output or project preservation: out=$out"
fi
set +e
(cd "$init_project" && KAME_HOME="$root/init-cache" KAME_RELEASE_URL="file://$release" "$release/bin/kame" init >"$root/init-out" 2>"$root/init-err")
status=$?
set -e
if [ "$status" = 1 ] && grep -q 'refusing to overwrite Makefile.bootstrap' "$root/init-err" && \
	[ "$(sha "$init_project/Makefile")" = "$before_makefile" ] && grep -qx 'KAME_VERSION ?= 9.9.9' "$init_project/Makefile.bootstrap"; then
	test-ok "repeat init refuses to overwrite the existing sidecar"
else
	test-fail "repeat init: status=$status err=$(cat "$root/init-err")"
fi

race_project="$root/init-race"
mkdir -p "$race_project"
set +e
(cd "$race_project" && KAME_HOME="$root/init-race-cache" KAME_RELEASE_URL="file://$release" "$release/bin/kame" init >"$root/race1-out" 2>"$root/race1-err") &
race1=$!
(cd "$race_project" && KAME_HOME="$root/init-race-cache" KAME_RELEASE_URL="file://$release" "$release/bin/kame" init >"$root/race2-out" 2>"$root/race2-err") &
race2=$!
wait "$race1"
race1_status=$?
wait "$race2"
race2_status=$?
set -e
if { [ "$race1_status" = 0 ] && [ "$race2_status" = 1 ]; } || { [ "$race1_status" = 1 ] && [ "$race2_status" = 0 ]; }; then
	if grep -qx 'KAME_VERSION ?= 9.9.9' "$race_project/Makefile.bootstrap"; then
		test-ok "concurrent init creates one complete sidecar without replacement"
	else
		test-fail "concurrent init wrote an incomplete sidecar"
	fi
else
	test-fail "concurrent init statuses: $race1_status, $race2_status"
fi

offline_project="$root/init-offline"
mkdir -p "$offline_project"
set +e
(cd "$offline_project" && KAME_HOME="$root/init-offline-cache" KAME_RELEASE_URL="file://$release" KAME_NO_DOWNLOAD=1 "$release/bin/kame" init >"$root/offline-out" 2>"$root/offline-err")
status=$?
set -e
if [ "$status" = 1 ] && grep -q 'download disabled' "$root/offline-err" && \
	[ ! -e "$offline_project/Makefile.bootstrap" ] && ! compgen -G "$root/init-offline-cache/9.9.9/.bootstrap.*" >/dev/null; then
	test-ok "offline init fails without creating an unverified sidecar"
else
	test-fail "offline init: status=$status err=$(cat "$root/offline-err")"
fi

test-end
