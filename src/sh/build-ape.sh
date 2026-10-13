#!/usr/bin/env bash
# Build the release Cosmopolitan APE (dist/kame.com) without GNU Make.
#
# The APE is a single multi-OS binary that runs the full native CLI on Linux,
# macOS, Windows and the BSDs with no Node, no WASM runtime and no make. It is
# what `bin/kame` and `Makefile.bootstrap` provision as the `ape` backend, so
# `-f Makefile.kmk TARGET` drives a project without any other tool.
#
# This mirrors the `dist/kame.com` rule in Makefile/Makefile.kmk: provision the
# pinned Cosmocc, refresh version metadata, then build stripped (-s). Prefer
# this over the Makefiles only when bootstrapping from a checkout that has no
# working `make`.
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
kame_dir=$root/src/go/kame
cosmocc_dir=$root/build/tools/cosmocc-4.0.2
cosmocc=$cosmocc_dir/bin/cosmocc
out=$root/dist/kame.com

command -v so >/dev/null 2>&1 || {
	printf 'build-ape: the so compiler driver is required\n' >&2
	exit 1
}

# Provision Cosmocc once, unless a verified install is already present.
[ -x "$cosmocc" ] || "$root/src/sh/provision-cosmocc.sh" "$cosmocc_dir"

# Refresh the embedded version metadata so the APE reports the current tree.
"$root/src/sh/generate-version.sh"

mkdir -p "$root/dist"
cd "$kame_dir"
CC=$cosmocc \
	CFLAGS="-O3 -s -DKAME_BUILD_MODE_RELEASE -DKAME_RUNTIME_APE" \
	so build -assert=off -panic=exit -o "$out" ./cmd/kame

printf 'built %s (%s bytes)\n' "$out" "$(wc -c <"$out" | tr -d ' ')"
