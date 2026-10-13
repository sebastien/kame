#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — signed manifests and provenance
# Spec: docs/spec/013-tests.md — T015-04-dist-signed-release
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-release.sh"

test-start "T015-04 signed release and provenance"
cd "$CLI_ROOT"

root="$TMPDIR/t015-signing"
release_test_keys "$root/keys"

test-step "stage actual release artifacts with operator supplied keys"
make dist-native >/dev/null
# Assemble in a copied tree: neither missing-artifact probes nor fixture signing
# may move operator artifacts or replace the real dist/release directory.
staging="$root/staging"
mkdir -p "$staging/dist" "$staging/src/sh" "$staging/bin"
cp Makefile VERSION Makefile.bootstrap "$staging/"
cp bin/kame "$staging/bin/"
cp src/sh/release_package_manifests.py src/sh/release_manifest.py "$staging/src/sh/"
for artifact in kame.com kame.wasm kame.js kame-windows-x64.zip kame-windows-x64.exe; do
	cp "dist/$artifact" "$staging/dist/"
done
if [ -d dist/native ]; then cp -R dist/native "$staging/dist/"; fi
assemble_release() {
	make -C "$staging" --old-file=dist/kame.com --old-file=dist/kame.wasm \
		--old-file=dist/kame.js --old-file=dist/kame-windows-x64.zip \
		--old-file=dist/kame-windows-x64.exe KAME_PLATFORM=test-staging \
		WASM_SOURCES= KAME_SOURCES= dist-release "$@"
}
missing_platform="openbsd-x86_64"
missing_artifact="$staging/dist/native/$missing_platform/kame"
rm -f "$missing_artifact"
if assemble_release KAME_RELEASE_PUBLIC_KEY="$root/keys/verification.pem" \
	KAME_RELEASE_SIGNING_KEY="$root/keys/signing.pem" >"$root/missing-native.out" 2>&1; then
	missing_status=0
else
	missing_status=$?
fi
if [ "$missing_status" -ne 0 ] && grep -q 'missing native release artifact:' "$root/missing-native.out"; then
	test-ok "release assembly refuses an incomplete cross-host native set"
else
	test-fail "release assembly accepted a missing native artifact: status=$missing_status output=$(cat "$root/missing-native.out")"
fi
if [ -f "$CLI_ROOT/dist/native/$missing_platform/kame" ]; then
	cp "$CLI_ROOT/dist/native/$missing_platform/kame" "$missing_artifact"
fi
for platform in linux-x86_64 linux-arm64 darwin-x86_64 darwin-arm64 freebsd-x86_64 netbsd-x86_64 openbsd-x86_64; do
	if [ ! -f "$staging/dist/native/$platform/kame" ]; then
		mkdir -p "$staging/dist/native/$platform"
		printf 'fixture artifact for %s\n' "$platform" >"$staging/dist/native/$platform/kame"
	fi
done
assemble_release KAME_RELEASE_PUBLIC_KEY="$root/keys/verification.pem" \
	KAME_RELEASE_SIGNING_KEY="$root/keys/signing.pem" KAME_RELEASE_REVISION=test-fixture >/dev/null
release="$staging/dist/release"
if grep -qx "KAME_VERSION ?= $(cat VERSION)" "$release/Makefile.bootstrap" && \
	grep -qx "KAME_RELEASE_PUBKEY_B64 ?= $RELEASE_TEST_KEY_B64" "$release/Makefile.bootstrap"; then
	test-ok "release bootstrap pins the version and trusted verification key"
else
	test-fail "release bootstrap is not stamped with its version and verification key"
fi
if python3 - "$release" <<'PY'; then test-ok "staged assets have signed-manifest provenance"; else test-fail "release provenance is incomplete"; fi
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
rows = {}
for line in (root / "SHA256SUMS").read_text().splitlines():
    digest, name = line.split("  ", 1)
    rows[name] = digest
platforms = ("linux-x86_64", "linux-arm64", "darwin-x86_64", "darwin-arm64", "freebsd-x86_64", "netbsd-x86_64", "openbsd-x86_64")
natives = {f"kame-{platform}" for platform in platforms}
assert set(rows) == {"PROVENANCE.json", "VERSION", "Makefile.bootstrap", "kame.rb", "kame", "kame.com", "kame.js", "kame.json", "kame.wasm", "kame-windows-x64.exe", "kame-windows-x64.zip", *natives}
assert all(path.is_file() for path in root.iterdir()), "release assets must be flat"
provenance = json.loads((root / "PROVENANCE.json").read_text())
assert provenance["schema"] == 1 and provenance["sourceRevision"] == "test-fixture"
assert all(rows[item["name"]] == item["sha256"] for item in provenance["subjects"])
assert hashlib.sha256((root / "PROVENANCE.json").read_bytes()).hexdigest() == rows["PROVENANCE.json"]
formula = (root / "kame.rb").read_text()
scoop = json.loads((root / "kame.json").read_text())
version = (root / "VERSION").read_text().strip()
assert f'version "{version}"' in formula and rows["kame.com"] in formula
assert scoop["version"] == version and scoop["hash"] == rows["kame-windows-x64.exe"]
assert scoop["url"].endswith(f"/v{version}/kame-windows-x64.exe") and scoop["bin"] == [["kame-windows-x64.exe", "kame"]]
assert all(rows[name] == hashlib.sha256((root / name).read_bytes()).hexdigest() for name in natives)
PY
project="$root/project"
mkdir -p "$project"
printf 'task signed-release :\n\t@(out! "signed-release-ok")\n' >"$project/Makefile.kmk"
out="$(cd "$project" && KAME_HOME="$root/cache" KAME_RELEASE_URL="file://$release" KAME_BACKEND=wasm "$release/kame" signed-release)"
if [ "$out" = "signed-release-ok" ]; then test-ok "release launcher verifies and runs signed WASM assets"; else test-fail "signed release output: $out"; fi

test-step "latest updates an older launcher using the actual signed release"
old_launcher="$root/old-kame"
sed 's/^KAME_STAMP=.*/KAME_STAMP="0.0.0"/' "$release/kame" >"$old_launcher"
chmod +x "$old_launcher"
out="$(cd "$project" && KAME_VERSION=latest KAME_LATEST_URL="file://$release/VERSION" \
	KAME_HOME="$root/latest-cache" KAME_RELEASE_URL="file://$release" KAME_BACKEND=wasm \
	"$old_launcher" signed-release)"
version=$(cat VERSION)
if [ "$out" = "signed-release-ok" ] && [ -x "$root/latest-cache/$version/launcher/kame" ]; then
	test-ok "latest installs and executes the actual manifest-verified launcher"
else
	test-fail "actual signed launcher update: out=$out"
fi

init_project="$root/init"
mkdir -p "$init_project"
init_out="$(cd "$init_project" && KAME_HOME="$root/init-cache" KAME_RELEASE_URL="file://$release" "$release/kame" init)"
if [ "$init_out" = "Created Makefile.bootstrap for Kame $(cat VERSION). Use make -f Makefile.bootstrap [targets]." ] &&
	grep -qx "KAME_VERSION ?= $(cat VERSION)" "$init_project/Makefile.bootstrap"; then
	test-ok "actual signed release initializes a pinned bootstrap sidecar"
else
	test-fail "signed release init: $init_out"
fi

test-step "tampered and wrong-key manifests fail closed"
tampered="$root/tampered"
cp -R "$release" "$tampered"
printf '\n# tampered\n' >>"$tampered/SHA256SUMS"
version=$(cat VERSION)
set +e
KAME_HOME="$root/tamper-cache" KAME_RELEASE_URL="file://$tampered" KAME_BACKEND=wasm "$release/kame" signed-release >"$root/out" 2>"$root/err"
tamper_status=$?
KAME_RELEASE_PUBKEY_B64_OVERRIDE="$(openssl genpkey -algorithm ED25519 | openssl pkey -pubout -outform DER | openssl base64 -A)" \
KAME_HOME="$root/wrong-key-cache" KAME_RELEASE_URL="file://$release" KAME_BACKEND=wasm \
	"$release/kame" signed-release >"$root/out-wrong" 2>"$root/err-wrong"
wrong_status=$?
set -e
if [ "$tamper_status" = 1 ] && grep -q 'manifest signature is invalid' "$root/err" && [ ! -d "$root/tamper-cache/$version/wasm" ]; then
	test-ok "manifest tampering is rejected before artifact installation"
else
	test-fail "tampered manifest status=$tamper_status err=$(cat "$root/err")"
fi
if [ "$wrong_status" = 1 ] && grep -q 'manifest signature is invalid' "$root/err-wrong" && [ ! -d "$root/wrong-key-cache/$version/wasm" ]; then
	test-ok "a wrong release key is rejected before artifact installation"
else
	test-fail "wrong-key manifest status=$wrong_status err=$(cat "$root/err-wrong")"
fi

test-step "cached provenance is checked against the signed manifest"
cached="$root/provenance-cache"
(cd "$project" && KAME_HOME="$cached" KAME_RELEASE_URL="file://$release" KAME_BACKEND=wasm "$release/kame" signed-release >/dev/null)
printf 'tampered' >"$cached/$version/PROVENANCE.json"
set +e
(cd "$project" && KAME_HOME="$cached" KAME_RELEASE_URL="file://$release" KAME_BACKEND=wasm "$release/kame" signed-release >"$root/out-prov" 2>"$root/err-prov")
prov_status=$?
set -e
if [ "$prov_status" = 1 ] && grep -q 'checksum mismatch: PROVENANCE.json' "$root/err-prov"; then
	test-ok "cached provenance tampering is rejected"
else
	test-fail "cached provenance status=$prov_status err=$(cat "$root/err-prov")"
fi

test-end
