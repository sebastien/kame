#!/usr/bin/env bash
# Test-only release fixtures use ephemeral keys under the harness TMPDIR.

release_test_keys() {
	local root="$1"
	mkdir -p "$root"
	openssl genpkey -algorithm ED25519 -out "$root/signing.pem"
	openssl pkey -in "$root/signing.pem" -pubout -out "$root/verification.pem"
	RELEASE_TEST_KEY_B64="$(openssl pkey -pubin -in "$root/verification.pem" -outform DER | openssl base64 -A)"
}

release_test_stamp_launcher() {
	local source="$1" destination="$2" version="$3"
	sed -e "s|^KAME_STAMP=.*|KAME_STAMP=\"$version\"|" \
	    -e "s|^KAME_RELEASE_PUBKEY_B64=.*|KAME_RELEASE_PUBKEY_B64=\"$RELEASE_TEST_KEY_B64\"|" \
	    "$source" >"$destination"
	chmod +x "$destination"
}

release_test_sign() {
	local directory="$1" version="$2" revision="$3" keys="$4"
	python3 "$CLI_ROOT/tools/release_manifest.py" --directory "$directory" \
		--version "$version" --revision "$revision" \
		--public-key "$keys/verification.pem" --signing-key "$keys/signing.pem"
}
