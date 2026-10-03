#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — launcher, provisioning, bootstrap
# Spec: docs/spec/013-tests.md — T015-01-dist-launcher
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

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
sed 's|^KAME_STAMP=.*|KAME_STAMP="9.9.9"|' "$launcher" >"$release/bin/kame"
chmod +x "$release/bin/kame"
{
	echo "$(sha "$release/kame.com")  kame.com"
	echo "$(sha "$release/kame.js")  kame.js"
	echo "$(sha "$release/kame.wasm")  kame.wasm"
	echo "$(sha "$release/bin/kame")  bin/kame"
} | sort -k2 >"$release/SHA256SUMS"

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

test-step "the wasm backend provisions, verifies, and dispatches"
out="$(KAME_BACKEND=wasm "$launcher" alpha beta)"
if [ "$out" = "wasm:alpha,beta" ] && [ -f "$cache/9.9.9/wasm/kame.js" ] && [ -f "$cache/9.9.9/wasm/kame.wasm" ]; then
	test-ok "wasm artifacts installed and executed"
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
cp "$release/kame.com" "$release/kame.js" "$release/kame.wasm" "$bad/"
cp "$release/bin/kame" "$bad/bin/kame"
{
	echo "$(sha "$bad/kame.com")  kame.com"
	printf '%064d  kame.js\n' 0
	echo "$(sha "$bad/kame.wasm")  kame.wasm"
	echo "$(sha "$bad/bin/kame")  bin/kame"
} | sort -k2 >"$bad/SHA256SUMS"
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
if [ "$status" = 1 ] && grep -q 'no SHA-256 verifier' "$root/err"; then
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

test-end
