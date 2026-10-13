#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — manual release and APE bootstrap helpers
# Spec: docs/spec/013-tests.md — T015-06-dist-release-scripts
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T015-06 release preflight, publication and APE helper arguments"
cd "$CLI_ROOT"

release="$CLI_ROOT/src/sh/release.sh"
build_ape="$CLI_ROOT/src/sh/build-ape.sh"

# The helpers resolve the repository root from their own location, so their
# argument and prerequisite guards run before any provisioning or network work.

test-step "release help documents the operator contract"
if "$release" --help >"$TMPDIR/help" 2>&1 &&
	grep -q 'KAME_RELEASE_PUBLIC_KEY' "$TMPDIR/help" &&
	grep -q -- '--repo OWNER/REPO' "$TMPDIR/help" &&
	grep -q -- '--notes-file FILE' "$TMPDIR/help"; then
	test-ok "release --help exits zero with usage"
else
	test-fail "release --help: status=$? output=$(cat "$TMPDIR/help")"
fi

test-step "release rejects an unknown argument"
status=0
"$release" --not-a-flag >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
if [ "$status" = 2 ] && grep -q 'unknown argument: --not-a-flag' "$TMPDIR/err"; then
	test-ok "unknown release argument is a usage error"
else
	test-fail "unknown argument: status=$status err=$(cat "$TMPDIR/err")"
fi

test-step "release rejects missing option values"
for option in --repo --notes-file; do
	status=0
	"$release" "$option" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
	if [ "$status" = 2 ] && grep -q 'requires' "$TMPDIR/err"; then
		test-ok "$option without a value is a usage error"
	else
		test-fail "$option: status=$status err=$(cat "$TMPDIR/err")"
	fi
done

# Exercise the real script in a committed, detached checkout, with only external
# publishing/build commands mocked. No real keys, builds or remote writes.
fixture="$TMPDIR/repository"
mock_bin="$TMPDIR/mock-bin"
mkdir -p "$fixture/src/sh" "$mock_bin"
cp "$release" "$fixture/src/sh/release.sh"
cp VERSION package.json "$fixture/"
git -C "$fixture" init -q
git -C "$fixture" add .
git -C "$fixture" -c user.name=ReleaseTest -c user.email=release@example.invalid commit -qm fixture
git -C "$fixture" checkout -q --detach
revision=$(git -C "$fixture" rev-parse HEAD)
release="$fixture/src/sh/release.sh"
export MOCK_REVISION="$revision" MOCK_REPO=owner/repo MOCK_ROOT="$fixture"
export MOCK_LOG="$TMPDIR/commands.log" MOCK_ASSETS="$TMPDIR/assets.log"
export LITTLESECRETS_LOG="$TMPDIR/secrets.log" MOCK_MODE=ok
cat >"$mock_bin/gh" <<'SH'
#!/usr/bin/env bash
set -eu
[ "$PWD" = "$MOCK_ROOT" ] || { echo 'wrong working directory' >&2; exit 1; }
printf 'gh %s\n' "$*" >>"$MOCK_LOG"
case "$*" in
repo\ view*)
	# repo view takes a positional repository, not release commands' --repo.
	[ "$*" = "repo view --json nameWithOwner --jq .nameWithOwner" ] ||
		[ "$*" = "repo view $MOCK_REPO --json nameWithOwner --jq .nameWithOwner" ] || exit 1
	printf '%s\n' "$MOCK_REPO"
	;;
"api repos/$MOCK_REPO/commits/$MOCK_REVISION --jq .sha")
	[ "$MOCK_MODE" != missing_commit ] || { echo 'No commit found (HTTP 422)' >&2; exit 1; }
	printf '%s\n' "$MOCK_REVISION"
	;;
"api --paginate repos/$MOCK_REPO/releases "*)
	[ "$MOCK_MODE" != api_failure ] || { echo 'HTTP 500' >&2; exit 1; }
	if [ "$MOCK_MODE" = existing_release ]; then echo 123; fi
	;;
"api repos/$MOCK_REPO/git/matching-refs/tags/"*)
	if [ "$MOCK_MODE" = existing_tag ]; then echo refs/tags/existing; fi
	;;
release\ create*)
	[[ "$*" == *"--repo $MOCK_REPO"* && "$*" == *"--target $MOCK_REVISION"* && "$*" == *--draft* ]] || exit 1
	[ "$MOCK_MODE" != create_failure ] || { echo 'HTTP 500' >&2; exit 1; }
	;;
release\ upload*)
	shift 3
	while [ "$#" -gt 0 ] && [ "$1" != --repo ]; do
		[ -f "$1" ] || exit 1
		name=${1##*/}
		if [ "$MOCK_MODE" != missing_asset ] || [ "$name" != kame ]; then
			printf '%s\n' "$name" >>"$MOCK_ASSETS"
		fi
		shift
	done
	[ "$*" = "--repo $MOCK_REPO" ] || exit 1
	;;
release\ view*) cat "$MOCK_ASSETS" ;;
release\ edit*) [[ "$*" == *"--repo $MOCK_REPO"* && "$*" == *--draft=false* ]] ;;
*) echo "unexpected GitHub call: $*" >&2; exit 1 ;;
esac
SH
cat >"$mock_bin/make" <<'SH'
#!/bin/sh
set -eu
printf 'make %s\n' "$*" >>"$MOCK_LOG"
mkdir -p "$MOCK_ROOT/dist/release"
printf 'launcher fixture\n' >"$MOCK_ROOT/dist/release/kame"
cd "$MOCK_ROOT/dist/release"
sha256sum kame >SHA256SUMS
printf 'signature fixture\n' >SHA256SUMS.sig
SH
cat >"$mock_bin/npm" <<'SH'
#!/bin/sh
printf 'npm %s\n' "$*" >>"$MOCK_LOG"
SH
cat >"$mock_bin/littlesecrets" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >>"$LITTLESECRETS_LOG"
exit 1
SH
chmod +x "$mock_bin/gh" "$mock_bin/make" "$mock_bin/npm" "$mock_bin/littlesecrets"
export PATH="$mock_bin:$PATH"
unset KAME_RELEASE_PUBLIC_KEY KAME_RELEASE_SIGNING_KEY
run_release() {
	: >"$MOCK_LOG"
	: >"$MOCK_ASSETS"
	: >"$LITTLESECRETS_LOG"
	status=0
	"$release" "$@" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
}

test-step "an unpushed detached commit fails before secrets or staging"
MOCK_MODE=missing_commit
run_release --repo "$MOCK_REPO"
if [ "$status" = 1 ] && grep -q "cannot resolve commit $revision in $MOCK_REPO" "$TMPDIR/err" &&
	grep -q 'HEAD:refs/heads/release-' "$TMPDIR/err" && [ ! -s "$LITTLESECRETS_LOG" ] &&
	! grep -Eq '^(make|npm|gh release)' "$MOCK_LOG"; then
	test-ok "missing remote commit stops before any build or publication"
else
	test-fail "commit preflight: status=$status err=$(cat "$TMPDIR/err")"
fi

test-step "remote conflicts and API failures stop before secrets or staging"
for mode in api_failure existing_release existing_tag; do
	MOCK_MODE=$mode
	run_release
	if [ "$status" = 1 ] && [ ! -s "$LITTLESECRETS_LOG" ] && ! grep -Eq '^(make|npm|gh release)' "$MOCK_LOG"; then
		test-ok "$mode stops publication preflight"
	else
		test-fail "$mode: status=$status err=$(cat "$TMPDIR/err")"
	fi
done
MOCK_MODE=ok

test-step "dirty tracked sources and local tags stop before GitHub or keys"
printf '\n' >>"$fixture/VERSION"
run_release
if [ "$status" = 1 ] && grep -q 'uncommitted tracked changes' "$TMPDIR/err" &&
	[ ! -s "$MOCK_LOG" ] && [ ! -s "$LITTLESECRETS_LOG" ]; then
	test-ok "dirty checkout fails locally"
else
	test-fail "dirty checkout: status=$status err=$(cat "$TMPDIR/err")"
fi
cp "$CLI_ROOT/VERSION" "$fixture/VERSION"
git -C "$fixture" tag "v$(cat "$fixture/VERSION")"
run_release
if [ "$status" = 1 ] && grep -q 'tag .* already exists' "$TMPDIR/err" && [ ! -s "$MOCK_LOG" ]; then
	test-ok "existing local tag fails locally"
else
	test-fail "local tag: status=$status err=$(cat "$TMPDIR/err")"
fi
git -C "$fixture" tag -d "v$(cat "$fixture/VERSION")" >/dev/null

test-step "release retrieves keys only after successful preflight"
run_release
if [ "$status" = 1 ] && grep -qx -- '-q get package.verification.pem' "$TMPDIR/secrets.log"; then
	test-ok "release asks littlesecrets for the verification key before staging"
else
	test-fail "verification key lookup: status=$status err=$(cat "$TMPDIR/err")"
fi
KAME_RELEASE_PUBLIC_KEY="$TMPDIR/none.pem" KAME_RELEASE_SIGNING_KEY="$TMPDIR/none.pem" run_release
if [ "$status" = 1 ] && grep -q 'verification key file is missing' "$TMPDIR/err"; then
	test-ok "absent key file is rejected before staging"
else
	test-fail "absent key file: status=$status err=$(cat "$TMPDIR/err")"
fi

test-step "release parses --repo and --notes-file before validating keys"
# Both flags are accepted by the parser; successful preflight reaches keys.
printf 'release notes\n' >"$TMPDIR/notes"
run_release --repo owner/repo --notes-file "$TMPDIR/notes"
if [ "$status" = 1 ] && grep -qx -- '-q get package.verification.pem' "$TMPDIR/secrets.log"; then
	test-ok "--repo and --notes-file reach key retrieval"
else
	test-fail "flag parsing: status=$status err=$(cat "$TMPDIR/err")"
fi

test-step "successful publication uploads a flat launcher to the selected repo"
touch "$TMPDIR/verification.pem" "$TMPDIR/signing.pem"
export KAME_RELEASE_PUBLIC_KEY="$TMPDIR/verification.pem" KAME_RELEASE_SIGNING_KEY="$TMPDIR/signing.pem"
run_release --repo "$MOCK_REPO" --notes-file "$TMPDIR/notes"
if [ "$status" = 0 ] && grep -qx kame "$MOCK_ASSETS" &&
	grep -q '^gh release edit .*--draft=false' "$MOCK_LOG" &&
	grep -qx 'npm publish --access public' "$MOCK_LOG" &&
	! grep -q 'bin/kame\|PATCH' "$MOCK_LOG"; then
	test-ok "flat assets verify before GitHub and npm publication"
else
	test-fail "publication: status=$status err=$(cat "$TMPDIR/err") log=$(cat "$MOCK_LOG")"
fi

test-step "relative operator paths work when invoked outside the checkout"
(cd "$TMPDIR" && KAME_RELEASE_PUBLIC_KEY=verification.pem KAME_RELEASE_SIGNING_KEY=signing.pem \
	run_release --notes-file notes)
if grep -q -- "--notes-file $TMPDIR/notes" "$MOCK_LOG" &&
	grep -q "KAME_RELEASE_PUBLIC_KEY=$TMPDIR/verification.pem" "$MOCK_LOG" &&
	grep -qx 'npm publish --access public' "$MOCK_LOG"; then
	test-ok "relative key and note paths remain relative to the caller"
else
	test-fail "relative paths: err=$(cat "$TMPDIR/err") log=$(cat "$MOCK_LOG")"
fi

test-step "failed creation and missing launcher never publish"
for mode in create_failure missing_asset; do
	MOCK_MODE=$mode
	run_release
	if [ "$status" = 1 ] && ! grep -Eq '^(gh release edit|npm)' "$MOCK_LOG"; then
		test-ok "$mode leaves publication blocked"
	else
		test-fail "$mode: status=$status err=$(cat "$TMPDIR/err")"
	fi
done

test-step "build-ape reports a missing compiler driver"
tools="$TMPDIR/no-tools"
mkdir -p "$tools"
bash_path="$(command -v bash)"
status=0
PATH="$tools" "$bash_path" "$build_ape" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
if [ "$status" = 1 ] && grep -q 'the so compiler driver is required' "$TMPDIR/err"; then
	test-ok "build-ape fails clearly without the so driver"
else
	test-fail "build-ape missing driver: status=$status err=$(cat "$TMPDIR/err")"
fi

test-end
