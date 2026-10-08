#!/bin/sh
set -eu

destination=${1:-build/tools/cosmocc}
archive_url=${COSMOCC_URL:-https://cosmo.zip/pub/cosmocc/cosmocc-4.0.2.zip}
expected_sha256=${COSMOCC_SHA256:-85b8c37a406d862e656ad4ec14be9f6ce474c1b436b9615e91a55208aced3f44}

case "$expected_sha256" in
	*[!0123456789abcdefABCDEF]*|'')
		printf 'error: COSMOCC_SHA256 must contain 64 hexadecimal digits\n' >&2
		exit 1
		;;
esac
if [ "${#expected_sha256}" -ne 64 ]; then
	printf 'error: COSMOCC_SHA256 must contain 64 hexadecimal digits\n' >&2
	exit 1
fi
expected_sha256=$(printf '%s' "$expected_sha256" | tr 'ABCDEF' 'abcdef')

if [ -x "$destination/bin/cosmocc" ]; then
	if [ -f "$destination/.archive-sha256" ] && [ "$(cat "$destination/.archive-sha256")" = "$expected_sha256" ]; then
		exit 0
	fi
	printf 'error: %s is not a verified cosmocc %s installation; remove it and retry\n' "$destination" "$expected_sha256" >&2
	exit 1
fi

if [ -e "$destination" ]; then
	printf 'error: %s exists but does not contain bin/cosmocc; remove it and retry\n' "$destination" >&2
	exit 1
fi

command -v curl >/dev/null 2>&1 || {
	printf 'error: curl is required to provision cosmocc\n' >&2
	exit 1
}
command -v unzip >/dev/null 2>&1 || {
	printf 'error: unzip is required to provision cosmocc\n' >&2
	exit 1
}
if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	printf 'error: sha256sum or shasum is required to verify cosmocc\n' >&2
	exit 1
fi

parent=$(dirname "$destination")
mkdir -p "$parent"
temporary=$(mktemp -d "$parent/.cosmocc.XXXXXX")
trap 'rm -rf "$temporary"' EXIT HUP INT TERM

curl --fail --location --retry 3 --silent --show-error "$archive_url" -o "$temporary/cosmocc.zip"
actual_sha256=$(sha256 "$temporary/cosmocc.zip")
if [ "$actual_sha256" != "$expected_sha256" ]; then
	printf 'error: cosmocc archive checksum mismatch (expected %s, got %s)\n' "$expected_sha256" "$actual_sha256" >&2
	exit 1
fi
mkdir "$temporary/unpacked"
unzip -q "$temporary/cosmocc.zip" -d "$temporary/unpacked"

if [ ! -x "$temporary/unpacked/bin/cosmocc" ]; then
	printf 'error: downloaded archive does not contain executable bin/cosmocc\n' >&2
	exit 1
fi

printf '%s\n' "$expected_sha256" >"$temporary/unpacked/.archive-sha256"
mv "$temporary/unpacked" "$destination"
