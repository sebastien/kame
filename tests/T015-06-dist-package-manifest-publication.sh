#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — automated package manifest publication
# Spec: docs/spec/013-tests.md — T015-06-dist-package-manifest-publication
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T015-06 package manifest publication"
cd "$CLI_ROOT"

root="$TMPDIR/t015-package-publication"
release="$root/release"
tap="$root/homebrew-tap"
bucket="$root/scoop-bucket"
mkdir -p "$release" "$tap/Formula" "$bucket/bucket"
git -C "$tap" init -q
git -C "$bucket" init -q
printf 'existing formula\n' >"$tap/Formula/other.rb"
printf '{"keep":true}\n' >"$bucket/bucket/other.json"
printf 'APE cli\n' >"$release/kame.com"
printf 'Windows cli\n' >"$release/kame-windows-x64.exe"
python3 tools/release_package_manifests.py --directory "$release" --version 1.2.3

test-step "validate release digests and update only package files"
python3 tools/publish_package_manifests.py \
	--release-directory "$release" --version v1.2.3 \
	--homebrew-tap "$tap" --scoop-bucket "$bucket"
if cmp -s "$release/kame.rb" "$tap/Formula/kame.rb" && \
	cmp -s "$release/kame.json" "$bucket/bucket/kame.json" && \
	[ "$(cat "$tap/Formula/other.rb")" = "existing formula" ] && \
	[ "$(cat "$bucket/bucket/other.json")" = '{"keep":true}' ]; then
	test-ok "release-pinned manifests staged without disturbing sibling files"
else
	test-fail "package publication changed unexpected files"
fi

test-step "reject manifest tampering before target mutation"
printf 'corrupt manifest\n' >"$release/kame.json"
published_formula="$tap/Formula/kame.rb"
published_scoop="$bucket/bucket/kame.json"
cp "$published_formula" "$root/published-kame.rb"
cp "$published_scoop" "$root/published-kame.json"
if python3 tools/publish_package_manifests.py \
	--release-directory "$release" --version 1.2.3 \
	--homebrew-tap "$tap" --scoop-bucket "$bucket" \
	>"$root/tamper.out" 2>&1; then
	test-fail "package publication accepted a corrupt Scoop manifest"
elif cmp -s "$release/kame.rb" "$tap/Formula/kame.rb" && \
	cmp -s "$root/published-kame.rb" "$published_formula" && \
	cmp -s "$root/published-kame.json" "$published_scoop" && \
	[ "$(cat "$bucket/bucket/other.json")" = '{"keep":true}' ]; then
	test-ok "invalid package assets leave repositories untouched"
else
	test-fail "package publication mutated repositories after validation failed"
fi
