#!/usr/bin/env bash
# Test-only release fixtures use ephemeral keys under the harness TMPDIR.

release_test_keys() {
	local root="$1"
	mkdir -p "$root"
	openssl genpkey -algorithm ED25519 -out "$root/signing.pem"
	openssl pkey -in "$root/signing.pem" -pubout -out "$root/verification.pem"
	RELEASE_TEST_KEY_B64="$(openssl pkey -pubin -in "$root/verification.pem" -outform DER | openssl base64 -A)"
}

release_test_sign() {
	local directory="$1" version="$2" revision="$3" keys="$4"
	# Shell fixtures model a platform binary; fill the other mandatory assets.
	for asset in kame-linux-arm64 kame-darwin-x86_64 kame-darwin-arm64 kame-windows-x64.zip; do
		if [ ! -e "$directory/$asset" ] && [ -f "$directory/kame-linux-x86_64" ]; then cp "$directory/kame-linux-x86_64" "$directory/$asset"; fi
	done
	release_manifest "$directory" "$version" "$revision" "$keys"
}

# Function: release_manifest DIRECTORY VERSION REVISION KEYS [SOURCE_DIRECTORY]
# Stages (optionally), validates and signs DIRECTORY with the Kash release script.
release_manifest() {
	local directory="$1" version="$2" revision="$3" keys="$4" source="${5:-}"
	"$CLI_BIN" do run --allow-read --allow-run --allow-write="$directory" \
		"$CLI_ROOT/src/kash/release-manifest.kash" -- directory="$directory" version="$version" \
		revision="$revision" public-key="$keys/verification.pem" signing-key="$keys/signing.pem" \
		${source:+source-directory="$source"}
}
