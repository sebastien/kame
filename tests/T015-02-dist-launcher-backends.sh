#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — backend selection, integrity, cache
# Spec: docs/spec/013-tests.md — T015-02-dist-launcher-backends
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-release.sh"

test-start "T015-02 distribution launcher backends and cache"
cd "$CLI_ROOT"

launcher="$CLI_ROOT/bin/kame"

sha() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

root="$TMPDIR/t015b"
mkdir -p "$root"
release_test_keys "$root/keys"

# build_release DIR APE_BODY writes a complete verified release tree.
build_release() {
	local dir="$1" ape="$2"
	mkdir -p "$dir/bin"
	printf '%s\n' "$ape" >"$dir/kame.com"
	chmod +x "$dir/kame.com"
	printf 'const a=process.argv.slice(2);process.stdout.write("wasm:"+a.join(",")+"\\n");\n' >"$dir/kame.js"
	printf 'dummy-wasm' >"$dir/kame.wasm"
	cp "$CLI_ROOT/Makefile.bootstrap" "$dir/Makefile.bootstrap"
	release_test_stamp_launcher "$launcher" "$dir/bin/kame" 9.9.9
	release_test_sign "$dir" 9.9.9 fixture "$root/keys"
}

good="$root/good"
build_release "$good" '#!/bin/sh
echo "ape:$*"'

broken="$root/broken"
build_release "$broken" '#!/bin/sh
exit 1'

export KAME_VERSION=9.9.9

test-step "auto falls back to wasm when the APE probe fails"
out="$(KAME_RELEASE_URL="file://$broken" KAME_HOME="$root/broken-cache" "$broken/bin/kame" a b)"
if [ "$out" = "wasm:a,b" ] && [ "$(cat "$root/broken-cache/9.9.9/.backend")" = "wasm" ]; then
	test-ok "a failed APE probe selects the wasm backend"
else
	test-fail "ape probe fallback: out=$out backend=$(cat "$root/broken-cache/9.9.9/.backend" 2>/dev/null)"
fi

test-step "KAME_BACKEND forces the APE strategy"
out="$(KAME_RELEASE_URL="file://$good" KAME_HOME="$root/ape-cache" KAME_BACKEND=ape "$good/bin/kame" a b)"
if [ "$out" = "ape:a b" ] && [ -x "$root/ape-cache/9.9.9/ape/kame.com" ]; then
	test-ok "KAME_BACKEND=ape provisions and runs the APE"
else
	test-fail "forced ape: out=$out"
fi

test-step "a missing manifest entry fails closed"
nomanifest="$root/nomanifest"
build_release "$nomanifest" '#!/bin/sh
echo "ape:$*"'
{
	echo "$(sha "$nomanifest/kame.com")  kame.com"
	echo "$(sha "$nomanifest/kame.wasm")  kame.wasm"
	echo "$(sha "$nomanifest/bin/kame")  bin/kame"
	echo "$(sha "$nomanifest/Makefile.bootstrap")  Makefile.bootstrap"
	echo "$(sha "$nomanifest/PROVENANCE.json")  PROVENANCE.json"
} | sort -k2 >"$nomanifest/SHA256SUMS"
openssl pkeyutl -sign -inkey "$root/keys/signing.pem" -rawin -in "$nomanifest/SHA256SUMS" -out "$nomanifest/SHA256SUMS.sig"
set +e
KAME_RELEASE_URL="file://$nomanifest" KAME_HOME="$root/nomanifest-cache" KAME_BACKEND=wasm "$nomanifest/bin/kame" x >"$root/out" 2>"$root/err"
status=$?
set -e
if [ "$status" = 1 ] && grep -q 'missing manifest entry: kame.js' "$root/err" && [ ! -e "$root/nomanifest-cache/9.9.9/wasm" ]; then
	test-ok "a missing manifest entry is fatal with no partial install"
else
	test-fail "missing manifest entry: status=$status err=$(cat "$root/err")"
fi

test-step "KAME_NO_DOWNLOAD fails when the artifact is absent"
set +e
KAME_RELEASE_URL="file://$good" KAME_HOME="$root/nodl-cache" KAME_BACKEND=wasm KAME_NO_DOWNLOAD=1 "$good/bin/kame" x >"$root/out" 2>"$root/err"
status=$?
set -e
if [ "$status" = 1 ] && grep -q 'download disabled' "$root/err" && [ ! -e "$root/nodl-cache/9.9.9/wasm" ]; then
	test-ok "downloads disabled with an empty cache is fatal"
else
	test-fail "no download: status=$status err=$(cat "$root/err")"
fi

test-step "concurrent first invocations install exactly one verified artifact"
conc="$root/conc-cache"
pids=""
for i in 1 2 3 4; do
	(
		KAME_RELEASE_URL="file://$good" KAME_HOME="$conc" KAME_BACKEND=wasm "$good/bin/kame" "t$i" >"$root/cout$i" 2>"$root/cerr$i"
		echo $? >"$root/cst$i"
	) &
	pids="$pids $!"
done
wait $pids
conc_ok=1
for i in 1 2 3 4; do
	[ "$(cat "$root/cst$i")" = 0 ] || conc_ok=0
	[ "$(cat "$root/cout$i")" = "wasm:t$i" ] || conc_ok=0
done
if [ "$conc_ok" = 1 ] && [ "$(sha "$conc/9.9.9/wasm/kame.js")" = "$(sha "$good/kame.js")" ] && [ ! -e "$conc/9.9.9/.staging.$$" ] && [ ! -e "$conc/9.9.9/.lock" ]; then
	test-ok "four concurrent launchers installed one verified artifact"
else
	test-fail "concurrent install: ok=$conc_ok err=$(cat "$root/cerr1" "$root/cerr2" "$root/cerr3" "$root/cerr4")"
fi

test-step "cache root selection honors XDG_DATA_HOME then HOME"
env -u KAME_HOME -u HOME KAME_RELEASE_URL="file://$good" XDG_DATA_HOME="$root/xdg" KAME_BACKEND=wasm "$good/bin/kame" x >/dev/null
xdg_ok=0
[ -f "$root/xdg/kame/9.9.9/wasm/kame.js" ] && xdg_ok=1
env -u KAME_HOME -u XDG_DATA_HOME KAME_RELEASE_URL="file://$good" HOME="$root/home" KAME_BACKEND=wasm "$good/bin/kame" x >/dev/null
home_ok=0
[ -f "$root/home/.local/share/kame/9.9.9/wasm/kame.js" ] && home_ok=1
set +e
env -u KAME_HOME -u XDG_DATA_HOME -u HOME KAME_RELEASE_URL="file://$good" KAME_BACKEND=wasm "$good/bin/kame" x >"$root/out" 2>"$root/err"
status=$?
set -e
if [ "$xdg_ok" = 1 ] && [ "$home_ok" = 1 ] && [ "$status" = 1 ] && grep -q 'no usable cache root' "$root/err"; then
	test-ok "XDG_DATA_HOME, HOME, and the no-root failure all behave"
else
	test-fail "cache root: xdg=$xdg_ok home=$home_ok no-root-status=$status err=$(cat "$root/err")"
fi

test-end
