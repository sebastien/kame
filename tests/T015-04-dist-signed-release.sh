#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — minimal signed releases and package definitions
# Spec: docs/spec/013-tests.md — T015-04-dist-signed-release
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-release.sh"

test-start "T015-04 signed release and provenance"
cd "$CLI_ROOT"
root="$TMPDIR/t015-signing"
release_test_keys "$root/keys"

test-step "stage only runtime assets, ignoring stale native and installer outputs"
staging="$root/staging"
mkdir -p "$staging/dist/native/darwin-arm64" "$staging/src/sh"
mkdir -p "$staging/tools/wasm"
for directory in cli core diagnostic host lang operations plugins program; do
	mkdir -p "$staging/src/go/kame/$directory"
done
cp Makefile VERSION "$staging/"
cp src/sh/release_manifest.py "$staging/src/sh/"
for artifact in kame.com kame.wasm kame.js; do
	cp "dist/$artifact" "$staging/dist/"
done
printf 'fixture artifact for darwin-arm64\n' >"$staging/dist/native/darwin-arm64/kame"
printf 'stale launcher\n' >"$staging/dist/kame"
printf 'stale Windows bundle\n' >"$staging/dist/kame-windows-x64.zip"
make -C "$staging" --old-file=dist/kame.com --old-file=dist/kame.wasm \
	--old-file=dist/kame.js KAME_PLATFORM=test-staging WASM_SOURCES= KAME_SOURCES= dist-release \
	KAME_RELEASE_PUBLIC_KEY="$root/keys/verification.pem" \
	KAME_RELEASE_SIGNING_KEY="$root/keys/signing.pem" KAME_RELEASE_REVISION=test-fixture >/dev/null
release="$staging/dist/release"
if python3 - "$release" <<'PY'; then test-ok "exactly three runtime assets and verification metadata"; else test-fail "unexpected release contents or provenance"; fi
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
assets = {"VERSION", "kame.com", "kame.js", "kame.wasm"}
assert {p.name for p in root.iterdir()} == assets | {"PROVENANCE.json", "SHA256SUMS", "SHA256SUMS.sig"}
rows = dict(line.split("  ", 1)[::-1] for line in (root / "SHA256SUMS").read_text().splitlines())
assert set(rows) == assets | {"PROVENANCE.json"}
assert all(rows[name] == hashlib.sha256((root / name).read_bytes()).hexdigest() for name in rows)
provenance = json.loads((root / "PROVENANCE.json").read_text())
assert provenance["schema"] == 1 and provenance["sourceRevision"] == "test-fixture"
assert {s["name"] for s in provenance["subjects"]} == assets
assert all(rows[s["name"]] == s["sha256"] for s in provenance["subjects"])
PY

test-step "verify the signature and execute the staged WASM pair"
mkdir -p "$root/manual"
cp "$release/kame.js" "$root/manual/kame.mjs"
cp "$release/kame.wasm" "$root/manual/kame.wasm"
# The manual command must work even inside a CommonJS project.
printf '{"type":"commonjs"}\n' >"$root/manual/package.json"
if openssl pkeyutl -verify -pubin -inkey "$root/keys/verification.pem" -rawin \
	-in "$release/SHA256SUMS" -sigfile "$release/SHA256SUMS.sig" >/dev/null &&
	node "$root/manual/kame.mjs" --help >"$root/manual/help" &&
	grep -q '^Usage:' "$root/manual/help"; then
	test-ok "signed release includes a usable WASM CLI"
else
	test-fail "signature verification or WASM execution failed"
fi
cp "$release/SHA256SUMS" "$root/tampered"
printf 'tampered\n' >>"$root/tampered"
if openssl pkeyutl -verify -pubin -inkey "$root/keys/verification.pem" -rawin \
	-in "$root/tampered" -sigfile "$release/SHA256SUMS.sig" >/dev/null 2>&1; then
	test-fail "tampered manifest accepted"
else
	test-ok "manifest tampering is rejected"
fi

test-step "package definitions are separate and both use the APE"
python3 src/sh/release_package_manifests.py --directory "$release" \
	--output-directory "$staging/etc" --version "$(cat VERSION)"
if python3 - "$staging" <<'PY'; then test-ok "Homebrew and Scoop use versioned APE assets outside releases"; else test-fail "invalid package definitions"; fi
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
version = (root / "VERSION").read_text().strip()
digest = hashlib.sha256((root / "dist/release/kame.com").read_bytes()).hexdigest()
formula = (root / "etc/kame.rb").read_text()
scoop = json.loads((root / "etc/kame.json").read_text())
assert f'version "{version}"' in formula and digest in formula
assert f'/v{version}/kame.com' in formula
assert scoop["version"] == version and scoop["hash"] == digest
assert scoop["url"].endswith(f'/v{version}/kame.com#/kame.exe')
assert scoop["bin"] == [["kame.exe", "kame"]]
assert not (root / "dist/release/kame.rb").exists()
PY

test-step "signing refuses unexpected or missing assets"
printf 'placeholder\n' >"$release/kame-darwin-arm64"
sign_release() {
	python3 src/sh/release_manifest.py --directory "$release" --version "$(cat VERSION)" \
		--revision test-fixture --public-key "$root/keys/verification.pem" --signing-key "$root/keys/signing.pem"
}
if sign_release >"$root/out" 2>&1; then
	test-fail "unexpected native artifact accepted"
elif grep -q 'unexpected release asset: kame-darwin-arm64' "$root/out"; then
	test-ok "unexpected native artifacts cannot enter the signed release"
else
	test-fail "unexpected signing error: $(cat "$root/out")"
fi
rm "$release/kame-darwin-arm64" "$release/kame.wasm"
if sign_release >"$root/out" 2>&1; then
	test-fail "missing WASM accepted"
elif grep -q 'missing release asset: kame.wasm' "$root/out"; then
	test-ok "missing runtime assets block signing"
else
	test-fail "unexpected signing error: $(cat "$root/out")"
fi
test-end
