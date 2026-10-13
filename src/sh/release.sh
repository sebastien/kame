#!/usr/bin/env bash
# Stage and publish a signed Kame release on GitHub (manual operator step).
#
# This wraps the tested `dist-release` staging with the publish step that no
# workflow performs: it requires the operator's Ed25519 key pair, builds
# `dist/release/`, creates a *draft* release for the `VERSION` tag, uploads the
# signed assets, then publishes. Nothing is published until every asset verifies
# against the signed manifest.
#
# The tag is `v$(cat VERSION)`. Existing GitHub releases are never overwritten;
# a published release can be resumed solely to finish npm publication.
set -euo pipefail

usage() {
	cat <<'USAGE'
Stage and publish a signed Kame release on GitHub (manual operator step).

Usage:
  src/sh/release.sh [--repo OWNER/REPO] [--notes-file FILE]

  Key paths may be supplied with KAME_RELEASE_PUBLIC_KEY and
  KAME_RELEASE_SIGNING_KEY; otherwise they are fetched from littlesecrets.
  Push the exact release commit to the target repository before running.
  Reruns of a published GitHub release retry only missing npm publication.

  --repo OWNER/REPO   publish to another repository (default: current)
  --notes-file FILE   use FILE as release notes instead of generated notes
  -h, --help          show this help
USAGE
}

root=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
caller_dir=$PWD
version=$(tr -d ' \t\r\n' <"$root/VERSION")
tag="v$version"
release_dir=$root/dist/release
repo_args=()
notes_args=(--generate-notes)

while [ $# -gt 0 ]; do
	case "$1" in
	--repo)
		[ $# -ge 2 ] && [ -n "$2" ] || { printf 'release: --repo requires OWNER/REPO\n' >&2; exit 2; }
		repo_args=("$2")
		shift 2
		;;
	--notes-file)
		[ $# -ge 2 ] && [ -f "$2" ] || { printf 'release: --notes-file requires an existing file\n' >&2; exit 2; }
		notes_args=(--notes-file "$(CDPATH='' cd -- "$(dirname -- "$2")" && pwd)/$(basename -- "$2")")
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

cd "$root"
command -v gh >/dev/null 2>&1 || {
	printf 'release: the GitHub CLI (gh) is required to publish\n' >&2
	exit 1
}
command -v npm >/dev/null 2>&1 || {
	printf 'release: npm is required to publish the runtime package\n' >&2
	exit 1
}

package_version=$(cd "$root" && node -p "require('./package.json').version")
package_name=$(node -p "require('./package.json').name")
[ "$package_version" = "$version" ] || {
	printf 'release: package.json version (%s) does not match VERSION (%s)\n' "$package_version" "$version" >&2
	exit 1
}

ensure_npm() {
	local registry=https://registry.npmjs.org/ result published_version name attempt
	npm_dir=$(mktemp -d "${TMPDIR:-/tmp}/kame-npm-release.XXXXXX")
	trap 'rm -rf "$npm_dir"; if [ -n "${key_dir:-}" ]; then rm -rf "$key_dir"; fi' EXIT
	if result=$(npm view "$package_name@$version" version --json --registry "$registry" 2>"$npm_dir/view.err"); then
		published_version=$(printf '%s' "$result" | node -e 'process.stdout.write(JSON.parse(require("fs").readFileSync(0, "utf8")))')
		[ "$published_version" = "$version" ] || { printf 'release: npm returned an unexpected version\n' >&2; return 1; }
		printf 'release: %s@%s is already published on npm\n' "$package_name" "$version"
		return 0
	fi
	# Only a missing package/version permits publishing; outages/auth errors do not.
	if ! printf '%s' "$result" | node -e 'try { process.exit(JSON.parse(require("fs").readFileSync(0, "utf8")).error?.code === "E404" ? 0 : 1) } catch { process.exit(1) }'; then
		cat "$npm_dir/view.err" >&2
		printf 'release: cannot check npm publication; refusing to publish\n' >&2
		return 1
	fi
	printf 'release: publishing missing npm package %s@%s from %s\n' "$package_name" "$version" "$tag"
	# Use the immutable release, never possibly newer local dist/package files.
	mkdir -p "$npm_dir/dist"
	gh release download "$tag" --pattern kame.js --pattern kame.wasm --dir "$npm_dir/dist" "${repo_args[@]}" || return 1
	for name in package.json README.md LICENSE.md; do
		gh api -H 'Accept: application/vnd.github.raw+json' "repos/$repo_slug/contents/$name?ref=$tag" >"$npm_dir/$name" || return 1
	done
	if ! node -e 'const p = require(process.argv[1]); process.exit(p.name === process.argv[2] && p.version === process.argv[3] ? 0 : 1)' "$npm_dir/package.json" "$package_name" "$version"; then
		printf 'release: tagged npm package metadata does not match %s@%s\n' "$package_name" "$version" >&2
		return 1
	fi
	if ! (cd "$npm_dir" && npm publish --access public --registry "$registry"); then
		printf 'release: GitHub release %s is unchanged; fix npm authentication and rerun src/sh/release.sh to retry npm only\n' "$tag" >&2
		return 1
	fi
	# npm can accept the upload before its processing makes the version readable.
	for ((attempt = 1; attempt <= 30; attempt++)); do
		if result=$(npm view "$package_name@$version" version --json --registry "$registry" 2>"$npm_dir/view.err"); then
			published_version=$(printf '%s' "$result" | node -e 'process.stdout.write(JSON.parse(require("fs").readFileSync(0, "utf8")))')
			[ "$published_version" = "$version" ] || { printf 'release: npm returned an unexpected version after accepting the upload\n' >&2; return 1; }
			printf 'release: verified %s@%s on npm\n' "$package_name" "$version"
			return 0
		fi
		if ! printf '%s' "$result" | node -e 'try { process.exit(JSON.parse(require("fs").readFileSync(0, "utf8")).error?.code === "E404" ? 0 : 1) } catch { process.exit(1) }'; then
			cat "$npm_dir/view.err" >&2
			printf 'release: npm accepted the upload, but registry verification failed; do not republish\n' >&2
			return 1
		fi
		if [ "$attempt" = 1 ]; then
			printf 'release: npm accepted the upload; waiting up to five minutes for processing\n'
		fi
		if [ "$attempt" -lt 30 ]; then sleep 10; fi
	done
	printf 'release: npm accepted %s@%s but it is still processing; do not republish\n' "$package_name" "$version" >&2
	printf 'release: check later with npm view %s@%s version --registry %s\n' "$package_name" "$version" "$registry" >&2
	return 3
}

# Resolve once so --repo, GH_REPO and the checkout all use the same target.
repo_slug=$(gh repo view ${repo_args[@]+"${repo_args[@]}"} --json nameWithOwner --jq '.nameWithOwner')
repo_args=(--repo "$repo_slug")
# Successful list requests distinguish absence from authentication/API failures.
existing_release=$(gh api --paginate "repos/$repo_slug/releases" --jq ".[] | select(.tag_name == \"$tag\") | if .draft then \"draft\" else \"published\" end")
if [ "$existing_release" = published ]; then
	printf 'release: GitHub release %s is already published; checking npm only\n' "$tag"
	ensure_npm
	exit 0
fi
[ -z "$existing_release" ] || { printf 'release: release %s already exists as a draft; refusing to overwrite\n' "$tag" >&2; exit 1; }

command -v openssl >/dev/null 2>&1 || {
	printf 'release: openssl is required to sign the manifest\n' >&2
	exit 1
}

# Refuse to clobber a published tag or release.
if git -C "$root" rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	printf 'release: tag %s already exists; bump VERSION instead of re-releasing\n' "$tag" >&2
	exit 1
fi
# A signed release must correspond to a committed revision; staged artifacts
# from uncommitted sources would not match the tagged tree.
if ! git -C "$root" diff --quiet || ! git -C "$root" diff --cached --quiet; then
	printf 'release: working tree has uncommitted tracked changes; commit or stash first\n' >&2
	exit 1
fi

revision=$(git -C "$root" rev-parse HEAD)
if ! remote_revision=$(gh api "repos/$repo_slug/commits/$revision" --jq '.sha'); then
	printf 'release: cannot resolve commit %s in %s; check GitHub access and push this exact commit before releasing\n' "$revision" "$repo_slug" >&2
	printf 'release: from a detached HEAD, push it to a branch in %s (for example HEAD:refs/heads/release-%s)\n' "$repo_slug" "$tag" >&2
	exit 1
fi
[ "$remote_revision" = "$revision" ] || { printf 'release: GitHub returned a different release revision\n' >&2; exit 1; }
existing_tag=$(gh api "repos/$repo_slug/git/matching-refs/tags/$tag" --jq ".[] | select(.ref == \"refs/tags/$tag\") | .ref")
[ -z "$existing_tag" ] || { printf 'release: remote tag %s already exists; refusing to overwrite\n' "$tag" >&2; exit 1; }

public_key=${KAME_RELEASE_PUBLIC_KEY:-}
signing_key=${KAME_RELEASE_SIGNING_KEY:-}
# Supplied paths, like --notes-file, are relative to the caller, not the checkout.
case $public_key in "" | /*) ;; *) public_key=$caller_dir/$public_key ;; esac
case $signing_key in "" | /*) ;; *) signing_key=$caller_dir/$signing_key ;; esac
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
[ -n "$public_key" ] && [ -f "$public_key" ] || { printf 'release: verification key file is missing\n' >&2; exit 1; }
[ -n "$signing_key" ] && [ -f "$signing_key" ] || { printf 'release: signing key file is missing\n' >&2; exit 1; }

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

# GitHub release assets have flat filenames, including the stamped launcher.
assets=()
while IFS= read -r asset; do
	assets+=("$asset")
done < <(find "$release_dir" -type f | sort)

printf 'release: creating draft release %s\n' "$tag"
# ${arr[@]+...} keeps empty-array expansion safe under `set -u` on bash < 4.4.
gh release create "$tag" --draft --target "$revision" --title "Kame $version" ${notes_args[@]+"${notes_args[@]}"} ${repo_args[@]+"${repo_args[@]}"}
gh release upload "$tag" ${assets[@]+"${assets[@]}"} ${repo_args[@]+"${repo_args[@]}"}
# Verify every signed manifest asset is present before publishing.
asset_names=$(gh release view "$tag" --json assets --jq '.assets[].name' ${repo_args[@]+"${repo_args[@]}"})

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
printf 'release: published %s; checking npm publication\n' "$tag"
ensure_npm
