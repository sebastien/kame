#!/usr/bin/env bash
# Stage and publish a signed Kame release on GitHub (manual operator step).
#
# This wraps the tested `dist-release` staging with the publish step that no
# workflow performs: it requires the operator's Ed25519 key pair, builds
# `dist/release/`, creates a *draft* release for the `VERSION` tag, uploads the
# signed assets, then publishes. Nothing is published until every asset verifies
# against the signed manifest.
#
# The tag is `v$(cat VERSION)`. The script refuses to reuse an existing tag or
# release; bump VERSION and re-tag instead of overwriting a published release.
set -euo pipefail

usage() {
	cat <<'USAGE'
Stage and publish a signed Kame release on GitHub (manual operator step).

Usage:
  src/sh/release.sh [--repo OWNER/REPO] [--notes-file FILE]

  Key paths may be supplied with KAME_RELEASE_PUBLIC_KEY and
  KAME_RELEASE_SIGNING_KEY; otherwise they are fetched from littlesecrets.

  --repo OWNER/REPO   publish to another repository (default: current)
  --notes-file FILE   use FILE as release notes instead of generated notes
  -h, --help          show this help
USAGE
}

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
version=$(tr -d ' \t\r\n' <"$root/VERSION")
tag="v$version"
release_dir=$root/dist/release
repo_args=()
repo_slug=""
notes_args=(--generate-notes)

while [ $# -gt 0 ]; do
	case "$1" in
	--repo)
		repo_args=(--repo "$2")
		repo_slug=$2
		shift 2
		;;
	--notes-file)
		notes_args=(--notes-file "$2")
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		printf 'release: unknown argument: %s\n' "$1" >&2
		usage >&2
		exit 2
		;;
	esac
done

public_key=${KAME_RELEASE_PUBLIC_KEY:-}
signing_key=${KAME_RELEASE_SIGNING_KEY:-}
if [ -z "$public_key" ] || [ -z "$signing_key" ]; then
	command -v littlesecrets >/dev/null 2>&1 || {
		printf 'release: set both key paths or install littlesecrets to retrieve package signing keys\n' >&2
		exit 1
	}
	umask 077
	key_dir=$(mktemp -d "${TMPDIR:-/tmp}/kame-release-keys.XXXXXX")
	chmod 700 "$key_dir"
	trap 'rm -rf "$key_dir"' EXIT
	if [ -z "$public_key" ]; then
		public_key=$key_dir/verification.pem
		littlesecrets -q get package.verification.pem >"$public_key"
	fi
	if [ -z "$signing_key" ]; then
		signing_key=$key_dir/signing.pem
		littlesecrets -q get package.signing.pem >"$signing_key"
	fi
fi
[ -n "$public_key" ] && [ -f "$public_key" ] || {
	printf 'release: verification key file is missing\n' >&2
	exit 1
}
[ -n "$signing_key" ] && [ -f "$signing_key" ] || {
	printf 'release: signing key file is missing\n' >&2
	exit 1
}
command -v gh >/dev/null 2>&1 || {
	printf 'release: the GitHub CLI (gh) is required to publish\n' >&2
	exit 1
}
command -v openssl >/dev/null 2>&1 || {
	printf 'release: openssl is required to sign the manifest\n' >&2
	exit 1
}

# Refuse to clobber a published tag or release.
if git -C "$root" rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	printf 'release: tag %s already exists; bump VERSION instead of re-releasing\n' "$tag" >&2
	exit 1
fi
if gh release view "$tag" ${repo_args[@]+"${repo_args[@]}"} >/dev/null 2>&1; then
	printf 'release: release %s already exists; refusing to overwrite\n' "$tag" >&2
	exit 1
fi

# A signed release must correspond to a committed revision; staged artifacts
# from uncommitted sources would not match the tagged tree.
if ! git -C "$root" diff --quiet || ! git -C "$root" diff --cached --quiet; then
	printf 'release: working tree has uncommitted tracked changes; commit or stash first\n' >&2
	exit 1
fi

revision=$(git -C "$root" rev-parse HEAD 2>/dev/null || printf 'unknown')
printf 'release: staging %s (revision %s)\n' "$tag" "$revision"

# dist-release stages and signs every asset and requires the full native set.
make -C "$root" dist-release \
	KAME_RELEASE_PUBLIC_KEY="$public_key" \
	KAME_RELEASE_SIGNING_KEY="$signing_key" \
	KAME_RELEASE_REVISION="$revision"

[ -f "$release_dir/SHA256SUMS" ] && [ -f "$release_dir/SHA256SUMS.sig" ] || {
	printf 'release: staging did not produce a signed manifest\n' >&2
	exit 1
}

# The launcher lives at bin/kame so its download URL is .../vX.Y.Z/bin/kame.
# Upload it separately: GitHub may rename an asset whose name contains a slash.
launcher=$release_dir/bin/kame
assets=()
while IFS= read -r asset; do
	[ "$asset" = "$launcher" ] && continue
	assets+=("$asset")
done < <(find "$release_dir" -type f | sort)

printf 'release: creating draft release %s\n' "$tag"
# ${arr[@]+...} keeps empty-array expansion safe under `set -u` on bash < 4.4.
gh release create "$tag" --draft --target "$revision" --title "Kame $version" ${notes_args[@]+"${notes_args[@]}"} ${repo_args[@]+"${repo_args[@]}"}
gh release upload "$tag" ${assets[@]+"${assets[@]}"} ${repo_args[@]+"${repo_args[@]}"}
gh release upload "$tag" "$launcher" ${repo_args[@]+"${repo_args[@]}"}

# GitHub renames asset filenames containing special characters. Force the
# launcher back to bin/kame, then verify every manifest asset is present.
asset_names=$(gh release view "$tag" --json assets --jq '.assets[].name' ${repo_args[@]+"${repo_args[@]}"})
if ! grep -qx 'bin/kame' <<<"$asset_names"; then
	renamed=$(grep -E '(^|/)kame$' <<<"$asset_names" | head -n 1 || true)
	[ -n "$renamed" ] || {
		printf 'release: launcher asset was not uploaded\n' >&2
		exit 1
	}
	asset_id=$(gh release view "$tag" --json assets \
		--jq ".assets[] | select(.name == \"$renamed\") | .id" ${repo_args[@]+"${repo_args[@]}"})
	printf 'release: launcher uploaded as %s; renaming to bin/kame\n' "$renamed" >&2
	# gh api resolves {owner}/{repo} from the current repository; GH_REPO points
	# it at --repo when the release was created elsewhere.
	if [ -n "$repo_slug" ]; then
		GH_REPO="$repo_slug" gh api -X PATCH "repos/{owner}/{repo}/releases/assets/$asset_id" -f name=bin/kame >/dev/null
	else
		gh api -X PATCH "repos/{owner}/{repo}/releases/assets/$asset_id" -f name=bin/kame >/dev/null
	fi
	asset_names=$(gh release view "$tag" --json assets --jq '.assets[].name' ${repo_args[@]+"${repo_args[@]}"})
fi

missing=0
while read -r _hex name; do
	[ -n "$name" ] || continue
	if ! grep -qxF "$name" <<<"$asset_names"; then
		printf 'release: manifest asset missing from release: %s\n' "$name" >&2
		missing=1
	fi
done <"$release_dir/SHA256SUMS"
[ "$missing" = 0 ] || {
	printf 'release: refusing to publish %s with missing assets\n' "$tag" >&2
	exit 1
}

printf 'release: publishing %s\n' "$tag"
gh release edit "$tag" --draft=false --latest ${repo_args[@]+"${repo_args[@]}"}
printf 'release: published %s\n' "$tag"
