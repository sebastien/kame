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
make dist-release KAME_RELEASE_PUBLIC_KEY="$root/keys/verification.pem" \
	KAME_RELEASE_SIGNING_KEY="$root/keys/signing.pem" KAME_RELEASE_REVISION=test-fixture >/dev/null
release="$CLI_ROOT/dist/release"
if python3 - "$release" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
rows = {}
for line in (root / "SHA256SUMS").read_text().splitlines():
    digest, name = line.split("  ", 1)
    rows[name] = digest
assert set(rows) == {"PROVENANCE.json", "Makefile.bootstrap", "bin/kame", "kame.com", "kame.js", "kame.wasm"}
provenance = json.loads((root / "PROVENANCE.json").read_text())
assert provenance["schema"] == 1 and provenance["sourceRevision"] == "test-fixture"
assert all(rows[item["name"]] == item["sha256"] for item in provenance["subjects"])
assert hashlib.sha256((root / "PROVENANCE.json").read_bytes()).hexdigest() == rows["PROVENANCE.json"]
PY
then test-ok "staged assets have signed-manifest provenance"; else test-fail "release provenance is incomplete"; fi

project="$root/project"
mkdir -p "$project"
printf 'task signed-release :\n\t@(out "signed-release-ok")\n' >"$project/Makefile.kmk"
out="$(cd "$project" && KAME_HOME="$root/cache" KAME_RELEASE_URL="file://$release" KAME_BACKEND=wasm "$release/bin/kame" signed-release)"
if [ "$out" = "signed-release-ok" ]; then test-ok "release launcher verifies and runs signed WASM assets"; else test-fail "signed release output: $out"; fi

init_project="$root/init"
mkdir -p "$init_project"
init_out="$(cd "$init_project" && KAME_HOME="$root/init-cache" KAME_RELEASE_URL="file://$release" "$release/bin/kame" init)"
if [ "$init_out" = "Created Makefile.bootstrap for Kame $(cat VERSION). Use make -f Makefile.bootstrap [targets]." ] && \
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
KAME_HOME="$root/tamper-cache" KAME_RELEASE_URL="file://$tampered" KAME_BACKEND=wasm "$release/bin/kame" signed-release >"$root/out" 2>"$root/err"
tamper_status=$?
KAME_RELEASE_PUBKEY_B64_OVERRIDE="$(openssl genpkey -algorithm ED25519 | openssl pkey -pubout -outform DER | openssl base64 -A)" \
	KAME_HOME="$root/wrong-key-cache" KAME_RELEASE_URL="file://$release" KAME_BACKEND=wasm \
	"$release/bin/kame" signed-release >"$root/out-wrong" 2>"$root/err-wrong"
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
(cd "$project" && KAME_HOME="$cached" KAME_RELEASE_URL="file://$release" KAME_BACKEND=wasm "$release/bin/kame" signed-release >/dev/null)
printf 'tampered' >"$cached/$version/PROVENANCE.json"
set +e
(cd "$project" && KAME_HOME="$cached" KAME_RELEASE_URL="file://$release" KAME_BACKEND=wasm "$release/bin/kame" signed-release >"$root/out-prov" 2>"$root/err-prov")
prov_status=$?
set -e
if [ "$prov_status" = 1 ] && grep -q 'checksum mismatch: PROVENANCE.json' "$root/err-prov"; then
	test-ok "cached provenance tampering is rejected"
else
	test-fail "cached provenance status=$prov_status err=$(cat "$root/err-prov")"
fi

test-end
