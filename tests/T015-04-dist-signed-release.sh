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

test-step "stage the Go platform binaries, Windows bundle and WASM pair"
staging="$root/staging"
mkdir -p "$staging/dist" "$staging/src/sh"
cp VERSION "$staging/"
cp VERSION "$staging/dist/"
for artifact in kame-linux-x86_64 kame-linux-arm64 kame-darwin-x86_64 kame-darwin-arm64 kame-windows-x64.zip kame.wasm kame.js; do
	cp "dist/$artifact" "$staging/dist/"
done
release_manifest "$staging/dist/release" "$(cat VERSION)" test-fixture "$root/keys" "$staging/dist"
release="$staging/dist/release"
if python3 - "$release" <<'PY'; then test-ok "all platform runtime assets and verification metadata"; else test-fail "unexpected release contents or provenance"; fi
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
assets = {"VERSION", "kame-linux-x86_64", "kame-linux-arm64", "kame-darwin-x86_64", "kame-darwin-arm64", "kame-windows-x64.zip", "kame.js", "kame.wasm"}
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
cp "$release/PROVENANCE.json" "$root/manual/PROVENANCE.json"
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
KAME_VERSION=unrelated-selector node "$root/manual/kame.mjs" --json --version >"$root/manual/version.json"
if jq -e --arg version "$(cat VERSION)" '.version == $version and .buildID == "test-fixture" and .buildTime == "unknown" and .runtime == "WASM"' "$root/manual/version.json" >/dev/null; then
	test-ok "staged provenance identifies the renamed wrapper and module, not an environment selector"
else
	test-fail "staged WASM identity does not match its provenance"
fi
cp "$release/SHA256SUMS" "$root/tampered"
printf 'tampered\n' >>"$root/tampered"
if openssl pkeyutl -verify -pubin -inkey "$root/keys/verification.pem" -rawin \
	-in "$root/tampered" -sigfile "$release/SHA256SUMS.sig" >/dev/null 2>&1; then
	test-fail "tampered manifest accepted"
else
	test-ok "manifest tampering is rejected"
fi

test-step "package definitions select matching Go binaries and the Windows bundle"
"$CLI_BIN" do run --allow-read --allow-run --allow-write="$staging/etc" src/kash/package-manifests.kash -- \
	directory="$release" output="$staging/etc" version="$(cat VERSION)"
if python3 - "$staging" <<'PY'; then test-ok "Homebrew and Scoop select versioned platform assets"; else test-fail "invalid package definitions"; fi
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
version = (root / "VERSION").read_text().strip()
formula = (root / "etc/kame.rb").read_text()
scoop = json.loads((root / "etc/kame.json").read_text())
assert f'version "{version}"' in formula
for platform in ("linux-x86_64", "linux-arm64", "darwin-x86_64", "darwin-arm64"):
    name = f"kame-{platform}"
    digest = hashlib.sha256((root / f"dist/release/{name}").read_bytes()).hexdigest()
    assert digest in formula and f'/v{version}/{name}' in formula
assert scoop["version"] == version
assert scoop["hash"] == hashlib.sha256((root / "dist/release/kame-windows-x64.zip").read_bytes()).hexdigest()
assert scoop["url"].endswith(f'/v{version}/kame-windows-x64.zip')
assert scoop["depends"] == "nodejs" and scoop["bin"] == [["kame.exe", "kame"]]
assert not (root / "dist/release/kame.rb").exists()
PY

test-step "signing refuses unexpected or missing assets"
printf 'placeholder\n' >"$release/kame-unknown"
sign_release() {
	release_manifest "$release" "$(cat VERSION)" test-fixture "$root/keys"
}
if sign_release >"$root/out" 2>&1; then
	test-fail "unexpected native artifact accepted"
elif grep -q 'unexpected release asset: kame-unknown' "$root/out"; then
	test-ok "unexpected native artifacts cannot enter the signed release"
else
	test-fail "unexpected signing error: $(cat "$root/out")"
fi
rm "$release/kame-unknown" "$release/kame.wasm"
if sign_release >"$root/out" 2>&1; then
	test-fail "missing WASM accepted"
elif grep -q 'missing release asset: kame.wasm' "$root/out"; then
	test-ok "missing runtime assets block signing"
else
	test-fail "unexpected signing error: $(cat "$root/out")"
fi
test-end
