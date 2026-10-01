#!/usr/bin/env bash
set -euo pipefail

root="$(realpath "$(dirname "${BASH_SOURCE[0]}")/..")"
version="$(cat "$root/VERSION")"
build_id="$(git -C "$root" rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')"
build_time="${KAME_BUILD_TIME:-development}"
build_mode="${KAME_BUILD_MODE:-development}"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
	echo "invalid version in VERSION: $version" >&2
	exit 1
fi

output="$root/src/go/kame/cmd/kame/version_generated.go"
temporary="$output.tmp"
trap 'rm -f "$temporary"' EXIT
cat >"$temporary" <<EOF
// Code generated from repository/build metadata; DO NOT EDIT.
package main

const version = "$version"
const buildID = "$build_id"
const buildTime = "$build_time"
const buildMode = "$build_mode"
EOF
if ! cmp -s "$temporary" "$output" 2>/dev/null; then
	mv "$temporary" "$output"
fi
