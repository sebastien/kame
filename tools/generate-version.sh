#!/usr/bin/env bash
set -euo pipefail

root="$(realpath "$(dirname "${BASH_SOURCE[0]}")/..")"
version="$(cat "$root/VERSION")"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
	echo "invalid version in VERSION: $version" >&2
	exit 1
fi

output="$root/src/go/kame/cmd/kame/version_generated.go"
temporary="$output.tmp"
trap 'rm -f "$temporary"' EXIT
cat >"$temporary" <<EOF
// Code generated from the repository VERSION file; DO NOT EDIT.
package main

const version = "$version"
EOF
if ! cmp -s "$temporary" "$output" 2>/dev/null; then
	mv "$temporary" "$output"
fi
