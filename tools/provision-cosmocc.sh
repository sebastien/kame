#!/bin/sh
set -eu

destination=${1:-build/tools/cosmocc}
archive_url=${COSMOCC_URL:-https://cosmo.zip/pub/cosmocc/cosmocc.zip}

if [ -x "$destination/bin/cosmocc" ]; then
	exit 0
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

parent=$(dirname "$destination")
mkdir -p "$parent"
temporary=$(mktemp -d "$parent/.cosmocc.XXXXXX")
trap 'rm -rf "$temporary"' EXIT HUP INT TERM

curl --fail --location --retry 3 "$archive_url" -o "$temporary/cosmocc.zip"
mkdir "$temporary/unpacked"
unzip -q "$temporary/cosmocc.zip" -d "$temporary/unpacked"

if [ ! -x "$temporary/unpacked/bin/cosmocc" ]; then
	printf 'error: downloaded archive does not contain executable bin/cosmocc\n' >&2
	exit 1
fi

mv "$temporary/unpacked" "$destination"
