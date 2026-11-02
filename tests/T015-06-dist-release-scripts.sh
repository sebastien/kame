#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — manual release and APE bootstrap helpers
# Spec: docs/spec/013-tests.md — T015-06-dist-release-scripts
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-release.sh"

test-start "T015-06 release preflight, publication and APE helper arguments"
cd "$CLI_ROOT"

release="$CLI_ROOT/src/sh/release.sh"
build_ape="$CLI_ROOT/src/sh/build-ape.sh"

test-step "the curated WASM gate retains topology and Kame recipe coverage"
wasm_tests=$(sed -n '/^TEST_WASM =/,/^$/p' Makefile.kmk)
for script in tests/T042-*.sh tests/T044-01-*.sh; do
	if grep -Fq "./$script" <<<"$wasm_tests"; then
		test-ok "$script is in TEST_WASM"
	else
		test-fail "$script is missing from TEST_WASM"
	fi
done

test-step "distribution metadata agrees with the project license and version"
if python3 - "$CLI_ROOT" <<'PYLICENSE'
import json
from pathlib import Path
import sys
import tomllib

root = Path(sys.argv[1])
assert json.loads((root / 'package.json').read_text())['license'] == 'BSD-3-Clause'
assert json.loads((root / 'package.json').read_text())['version'] == (root / 'VERSION').read_text().strip()
assert tomllib.loads((root / 'pyproject.toml').read_text())['project']['license']['file'] == 'LICENSE.md'
assert (root / 'LICENSE.md').read_text().startswith('# Revised BSD License')
PYLICENSE
then
	test-ok "npm version matches VERSION and package licenses preserve Revised BSD"
else
	test-fail "distribution metadata differs from VERSION or LICENSE.md"
fi

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

test-step "kame release delegates to the publication wrapper"
if grep -Eq '^release \{repo=\} \{notes=\} : ./dist/kame.com' Makefile.kmk &&
	grep -Fq ':KAME_RELEASE_PREBUILT "1" ./src/sh/release.sh $flags' Makefile.kmk; then
	test-ok "release target prebuilds assets and forwards repo and notes"
else
	test-fail "release target is missing or not wired to release.sh"
fi

# Exercise the real script in a committed, detached checkout, with only external
# publishing/build commands mocked. No real keys, builds or remote writes.
fixture="$TMPDIR/repository"
mock_bin="$TMPDIR/mock-bin"
mkdir -p "$fixture/src/sh" "$mock_bin"
cp "$release" "$fixture/src/sh/release.sh"
cp VERSION package.json Makefile "$fixture/"
git -C "$fixture" init -q
git -C "$fixture" add .
git -C "$fixture" -c user.name=ReleaseTest -c user.email=release@example.invalid commit -qm fixture
git -C "$fixture" checkout -q --detach
revision=$(git -C "$fixture" rev-parse HEAD)
release="$fixture/src/sh/release.sh"
export MOCK_REVISION="$revision" MOCK_REPO=owner/repo MOCK_ROOT="$fixture"
export MOCK_LOG="$TMPDIR/commands.log" MOCK_ASSETS="$TMPDIR/assets.log" MOCK_NPM_MARKER="$TMPDIR/npm-published"
export MOCK_BRANCH="$TMPDIR/release-branch" MOCK_NPM_CHECKS="$TMPDIR/npm-checks"
MOCK_VERSION=$(cat VERSION)
MOCK_PACKAGE=$(node -p "require('./package.json').name")
export MOCK_VERSION MOCK_PACKAGE MOCK_NPM_STATE=missing
export DISPLAY=:test
unset WAYLAND_DISPLAY
export LITTLESECRETS_LOG="$TMPDIR/secrets.log" MOCK_MODE=ok
REAL_GIT=$(command -v git)
REAL_MAKE=$(command -v make)
export REAL_GIT REAL_MAKE
cat >"$mock_bin/gh" <<'SH'
#!/usr/bin/env bash
set -eu
[ "$PWD" = "$MOCK_ROOT" ] || { echo 'wrong working directory' >&2; exit 1; }
printf 'gh %s\n' "$*" >>"$MOCK_LOG"
case "$*" in
"config get git_protocol") printf 'https\n' ;;
repo\ view*)
	case "$*" in
	"repo view --json nameWithOwner --jq .nameWithOwner" | \
		"repo view $MOCK_REPO --json nameWithOwner --jq .nameWithOwner")
		printf '%s\n' "$MOCK_REPO"
		;;
	"repo view $MOCK_REPO --json sshUrl --jq .sshUrl")
		printf 'git@github.com:%s.git\n' "$MOCK_REPO"
		;;
	"repo view $MOCK_REPO --json url --jq .url")
		printf 'https://github.com/%s\n' "$MOCK_REPO"
		;;
	*) echo "unexpected repository query: $*" >&2; exit 1 ;;
	esac
	;;
"api repos/$MOCK_REPO/commits/$MOCK_REVISION --jq .sha")
	case "$MOCK_MODE" in
	missing_commit | branch_failure)
		[ -f "$MOCK_BRANCH" ] || { echo 'No commit found (HTTP 422)' >&2; exit 1; }
		;;
	esac
	printf '%s\n' "$MOCK_REVISION"
	;;
"api --paginate repos/$MOCK_REPO/releases "*)
	[ "$MOCK_MODE" != api_failure ] || { echo 'HTTP 500' >&2; exit 1; }
	if [ "$MOCK_MODE" = existing_release ]; then echo draft; fi
	if [ "$MOCK_MODE" = published_release ]; then echo published; fi
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
		if [ "$MOCK_MODE" != missing_asset ] || [ "$name" != kame.wasm ]; then
			printf '%s\n' "$name" >>"$MOCK_ASSETS"
		fi
		shift
	done
	[ "$*" = "--repo $MOCK_REPO" ] || exit 1
	;;
release\ view*) cat "$MOCK_ASSETS" ;;
release\ edit*) [[ "$*" == *"--repo $MOCK_REPO"* && "$*" == *--draft=false* ]] ;;
release\ download*)
	[[ "$*" == *"--repo $MOCK_REPO"* ]] || exit 1
	while [ "$1" != --dir ]; do shift; done
	mkdir -p "$2"
	printf 'released JS\n' >"$2/kame.js"
	printf 'released WASM\n' >"$2/kame.wasm"
	;;
"api -H Accept: application/vnd.github.raw+json repos/$MOCK_REPO/contents/package.json?ref="*)
	if [ "$MOCK_NPM_STATE" = metadata_mismatch ]; then
		printf '{"name":"wrong-package","version":"0.0.0"}\n'
	else
		cat "$MOCK_ROOT/package.json"
	fi
	;;
"api -H Accept: application/vnd.github.raw+json repos/$MOCK_REPO/contents/VERSION?ref="*)
	printf '%s\n' "$MOCK_VERSION"
	;;
"api -H Accept: application/vnd.github.raw+json repos/$MOCK_REPO/contents/README.md?ref="* | \
"api -H Accept: application/vnd.github.raw+json repos/$MOCK_REPO/contents/LICENSE.md?ref="*) echo 'tagged fixture' ;;
*) echo "unexpected GitHub call: $*" >&2; exit 1 ;;
esac
SH
cat >"$mock_bin/git" <<'SH'
#!/usr/bin/env bash
for arg in "$@"; do
	if [ "$arg" = push ]; then
		printf 'git %s\n' "$*" >>"$MOCK_LOG"
		[ "$MOCK_MODE" != branch_failure ] || { echo 'push rejected' >&2; exit 1; }
		touch "$MOCK_BRANCH"
		exit 0
	fi
done
exec "$REAL_GIT" "$@"
SH
cat >"$mock_bin/make" <<'SH'
#!/bin/sh
set -eu
printf 'make %s\n' "$*" >>"$MOCK_LOG"
mkdir -p "$MOCK_ROOT/dist/release"
cd "$MOCK_ROOT/dist/release"
for name in kame.com kame.js kame.wasm VERSION PROVENANCE.json; do
	printf 'fixture\n' >"$name"
done
sha256sum kame.com kame.js kame.wasm VERSION PROVENANCE.json >SHA256SUMS
printf 'signature fixture\n' >SHA256SUMS.sig
SH
cat >"$mock_bin/npm" <<'SH'
#!/usr/bin/env bash
set -eu
printf 'npm %s\n' "$*" >>"$MOCK_LOG"
case "$*" in
"whoami --registry https://registry.npmjs.org/")
	[ "$MOCK_NPM_STATE" != unauthenticated ] || { echo 'npm error code E401' >&2; exit 1; }
	printf 'release-operator\n'
	;;
"view $MOCK_PACKAGE@$MOCK_VERSION version --json --registry https://registry.npmjs.org/")
	if [ "$MOCK_NPM_STATE" = api_failure ]; then
		echo '{"error":{"code":"E401"}}'
		echo 'registry authentication failure' >&2
		exit 1
	fi
	if [ "$MOCK_NPM_STATE" = verification_failure ] && [ -f "$MOCK_NPM_MARKER" ]; then
		echo '{"error":{"code":"E404"}}'
		exit 1
	fi
	if [ "$MOCK_NPM_STATE" = delayed ] && [ -f "$MOCK_NPM_MARKER" ]; then
		echo check >>"$MOCK_NPM_CHECKS"
		if [ "$(wc -l <"$MOCK_NPM_CHECKS")" -lt 3 ]; then
			echo '{"error":{"code":"E404"}}'
			exit 1
		fi
	fi
	if [ "$MOCK_NPM_STATE" = verification_auth_failure ] && [ -f "$MOCK_NPM_MARKER" ]; then
		echo '{"error":{"code":"E401"}}'
		echo 'registry authentication failure' >&2
		exit 1
	fi
	if [ "$MOCK_NPM_STATE" = present ] || [ -f "$MOCK_NPM_MARKER" ]; then
		printf '"%s"\n' "$MOCK_VERSION"
	else
		echo '{"error":{"code":"E404"}}'
		exit 1
	fi
	;;
"publish --access public --registry https://registry.npmjs.org/" | "publish --access public --registry https://registry.npmjs.org/ --browser=false")
	[ "${MOCK_REQUIRE_TTY:-0}" = 0 ] || { [ -t 0 ] && [ -t 1 ] && [ -t 2 ]; } || exit 1
	[ "${MOCK_REQUIRE_TTY:-0}" = 0 ] || printf 'Authenticate at https://example.invalid/approve\n'
	[ "$MOCK_NPM_STATE" != publish_failure ] || { echo 'npm authentication expired' >&2; exit 1; }
	[ "$PWD" != "$MOCK_ROOT" ] && [ -f package.json ] || exit 1
	[ "$(cat VERSION)" = "$MOCK_VERSION" ] || exit 1
	grep -qx 'released JS' dist/kame.js && grep -qx 'released WASM' dist/kame.wasm || exit 1
	touch "$MOCK_NPM_MARKER"
	;;
*) echo "unexpected npm call: $*" >&2; exit 1 ;;
esac
SH
cat >"$mock_bin/sleep" <<'SH'
#!/bin/sh
printf 'sleep %s\n' "$*" >>"$MOCK_LOG"
SH
cat >"$mock_bin/littlesecrets" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >>"$LITTLESECRETS_LOG"
exit 1
SH
chmod +x "$mock_bin/gh" "$mock_bin/git" "$mock_bin/make" "$mock_bin/npm" "$mock_bin/littlesecrets" "$mock_bin/sleep"
export PATH="$mock_bin:$PATH"
unset KAME_RELEASE_PUBLIC_KEY KAME_RELEASE_SIGNING_KEY
run_release() {
	: >"$MOCK_LOG"
	: >"$MOCK_ASSETS"
	: >"$LITTLESECRETS_LOG"
	: >"$MOCK_NPM_CHECKS"
	rm -f "$MOCK_NPM_MARKER" "$MOCK_BRANCH"
	status=0
	"$release" "$@" >"$TMPDIR/out" 2>"$TMPDIR/err" || status=$?
}

test-step "an unpushed detached commit is published to a release branch"
MOCK_MODE=missing_commit
run_release --repo "$MOCK_REPO"
if [ "$status" = 1 ] && grep -q 'publishing release-v' "$TMPDIR/err" &&
	grep -q "^git .* push https://github.com/$MOCK_REPO .*:refs/heads/release-v$MOCK_VERSION$" "$MOCK_LOG" &&
	grep -qx -- '-q get package.verification.pem' "$LITTLESECRETS_LOG" &&
	! grep -Eq '^(make|npm|gh release)' "$MOCK_LOG"; then
	test-ok "unpushed commit is published before key retrieval and staging"
else
	test-fail "commit preflight: status=$status err=$(cat "$TMPDIR/err") log=$(cat "$MOCK_LOG")"
fi

test-step "a failed release branch publication stops before keys"
MOCK_MODE=branch_failure
run_release
if [ "$status" = 1 ] && grep -q 'cannot publish release-v' "$TMPDIR/err" &&
	[ ! -s "$LITTLESECRETS_LOG" ] && ! grep -Eq '^(make|npm|gh release)' "$MOCK_LOG"; then
	test-ok "branch publication failure stops before secrets or staging"
else
	test-fail "branch failure: status=$status err=$(cat "$TMPDIR/err")"
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

test-step "dirty tracked sources and local tags stop before builds or keys"
printf '\n' >>"$fixture/VERSION"
run_release
if [ "$status" = 1 ] && grep -q 'uncommitted tracked changes' "$TMPDIR/err" &&
	! grep -Eq '^(make|npm|gh release)' "$MOCK_LOG" && [ ! -s "$LITTLESECRETS_LOG" ]; then
	test-ok "dirty checkout fails locally"
else
	test-fail "dirty checkout: status=$status err=$(cat "$TMPDIR/err")"
fi
cp "$CLI_ROOT/VERSION" "$fixture/VERSION"
git -C "$fixture" tag "v$(cat "$fixture/VERSION")"
run_release
if [ "$status" = 1 ] && grep -q 'tag .* already exists' "$TMPDIR/err" && ! grep -Eq '^(make|npm|gh release)' "$MOCK_LOG"; then
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

test-step "successful publication uploads minimal assets and explains usage"
touch "$TMPDIR/verification.pem" "$TMPDIR/signing.pem"
export KAME_RELEASE_PUBLIC_KEY="$TMPDIR/verification.pem" KAME_RELEASE_SIGNING_KEY="$TMPDIR/signing.pem"
run_release --repo "$MOCK_REPO" --notes-file "$TMPDIR/notes"
if [ "$status" = 0 ] && grep -qx kame.wasm "$MOCK_ASSETS" && ! grep -qx kame "$MOCK_ASSETS" &&
	grep -q 'node ./kame.js --help' "$MOCK_LOG" &&
	grep -q "npm x --yes $MOCK_PACKAGE@$MOCK_VERSION -- --help" "$MOCK_LOG" &&
	grep -q 'release notes' "$MOCK_LOG" &&
	! grep -q -- '--generate-notes' "$MOCK_LOG" &&
	grep -q '^gh release edit .*--draft=false' "$MOCK_LOG" &&
	grep -qx 'npm publish --access public --registry https://registry.npmjs.org/' "$MOCK_LOG" &&
	! grep -q 'bin/kame\|PATCH' "$MOCK_LOG"; then
	test-ok "flat assets verify before GitHub and npm publication"
else
	test-fail "publication: status=$status err=$(cat "$TMPDIR/err") log=$(cat "$MOCK_LOG")"
fi

test-step "relative operator paths work when invoked outside the checkout"
(cd "$TMPDIR" && KAME_RELEASE_PUBLIC_KEY=verification.pem KAME_RELEASE_SIGNING_KEY=signing.pem \
	run_release --notes-file notes)
if grep -q 'release notes' "$MOCK_LOG" &&
	grep -q "KAME_RELEASE_PUBLIC_KEY=$TMPDIR/verification.pem" "$MOCK_LOG" &&
	grep -qx 'npm publish --access public --registry https://registry.npmjs.org/' "$MOCK_LOG"; then
	test-ok "relative key and note paths remain relative to the caller"
else
	test-fail "relative paths: err=$(cat "$TMPDIR/err") log=$(cat "$MOCK_LOG")"
fi

test-step "generated change notes also include download instructions"
run_release --repo "$MOCK_REPO"
if [ "$status" = 0 ] && grep -q -- '--generate-notes' "$MOCK_LOG" &&
	grep -q 'Download \*\*both into the same directory\*\*' "$MOCK_LOG" &&
	grep -q "npm x --yes $MOCK_PACKAGE@$MOCK_VERSION -- --help" "$MOCK_LOG"; then
	test-ok "default notes prepend versioned usage to generated changes"
else
	test-fail "default release notes: status=$status err=$(cat "$TMPDIR/err")"
fi

test-step "failed creation and missing runtime never publish"
for mode in create_failure missing_asset; do
	MOCK_MODE=$mode
	run_release
	if [ "$status" = 1 ] && ! grep -Eq '^(gh release edit|npm (view|publish)) ' "$MOCK_LOG"; then
		test-ok "$mode leaves publication blocked"
	else
		test-fail "$mode: status=$status err=$(cat "$TMPDIR/err")"
	fi
done

test-step "a published GitHub release retries only missing npm publication"
MOCK_MODE=published_release
MOCK_NPM_STATE=missing
unset KAME_RELEASE_PUBLIC_KEY KAME_RELEASE_SIGNING_KEY
# Local tags and changed tracked sources must not block recovery of the already
# published version; the npm payload comes from GitHub, not this checkout.
git -C "$fixture" tag "v$MOCK_VERSION"
printf '\n' >>"$fixture/package.json"
run_release --repo "$MOCK_REPO"
if [ "$status" = 0 ] && grep -q '^gh release download ' "$MOCK_LOG" &&
	grep -q '^npm publish ' "$MOCK_LOG" && [ ! -s "$LITTLESECRETS_LOG" ] &&
	! grep -Eq '^(make|gh release (create|upload|edit)|gh api repos/.*/commits/)' "$MOCK_LOG"; then
	test-ok "npm-only retry works without rebuilding, signing or modifying GitHub"
else
	test-fail "npm recovery: status=$status err=$(cat "$TMPDIR/err") log=$(cat "$MOCK_LOG")"
fi

test-step "headless interactive publication reaches npm's terminal"
: >"$MOCK_LOG"
rm -f "$MOCK_NPM_MARKER"
status=0
env -u DISPLAY -u WAYLAND_DISPLAY MOCK_REQUIRE_TTY=1 python3 - "$REAL_MAKE" -C "$fixture" release KAME=/bin/true repo=owner/repo >"$TMPDIR/tty-out" 2>&1 <<'PY' || status=$?
import errno, os, pty, sys
pid, fd = pty.fork()
if pid == 0:
    os.execv(sys.argv[1], sys.argv[1:])
try:
    while data := os.read(fd, 4096):
        sys.stdout.buffer.write(data)
except OSError as e:
    if e.errno != errno.EIO:
        raise
sys.exit(os.waitstatus_to_exitcode(os.waitpid(pid, 0)[1]))
PY
if [ "$status" = 0 ] && grep -qx 'npm publish --access public --registry https://registry.npmjs.org/ --browser=false' "$MOCK_LOG" &&
	grep -q 'https://example.invalid/approve' "$TMPDIR/tty-out" &&
	grep -q '^gh repo view owner/repo ' "$MOCK_LOG" && ! grep -q '^make ' "$MOCK_LOG"; then
	test-ok "make release retries npm without building or obscuring terminal approval"
else
	test-fail "interactive npm: status=$status output=$(cat "$TMPDIR/tty-out") log=$(cat "$MOCK_LOG")"
fi

test-step "an already published npm version is a successful no-op"
MOCK_NPM_STATE=present
run_release
if [ "$status" = 0 ] && grep -q 'already published on npm' "$TMPDIR/out" &&
	! grep -Eq '^(make|npm publish|gh release)' "$MOCK_LOG"; then
	test-ok "published npm version is never republished"
else
	test-fail "npm no-op: status=$status err=$(cat "$TMPDIR/err")"
fi

test-step "npm errors and mismatched tagged metadata fail safely"
for state in unauthenticated api_failure publish_failure metadata_mismatch; do
	MOCK_NPM_STATE=$state
	run_release
	if [ "$status" = 1 ] && ! grep -Eq '^(make|gh release (create|upload|edit))' "$MOCK_LOG" &&
		{ [ "$state" = publish_failure ] || ! grep -q '^npm publish ' "$MOCK_LOG"; } &&
		{ [ "$state" != unauthenticated ] || { grep -q 'npm authentication is missing or expired' "$TMPDIR/err" && ! grep -q '^npm view ' "$MOCK_LOG" && ! grep -q '^gh release download ' "$MOCK_LOG"; }; }; then
		test-ok "$state does not modify GitHub or publish an unsafe npm payload"
	else
		test-fail "$state: status=$status err=$(cat "$TMPDIR/err") log=$(cat "$MOCK_LOG")"
	fi
done
MOCK_NPM_STATE=verification_failure
run_release
if [ "$status" = 3 ] && [ "$(grep -c '^npm publish ' "$MOCK_LOG")" = 1 ] &&
	[ "$(grep -c '^sleep ' "$MOCK_LOG")" = 29 ] && grep -q 'still processing; do not republish' "$TMPDIR/err" &&
	! grep -q 'verified .* on npm' "$TMPDIR/out"; then
	test-ok "bounded verification reports accepted/pending without repeating the upload"
else
	test-fail "npm verification: status=$status err=$(cat "$TMPDIR/err")"
fi
MOCK_NPM_STATE=delayed
run_release
if [ "$status" = 0 ] && grep -q 'verified .* on npm' "$TMPDIR/out" &&
	[ "$(grep -c '^npm publish ' "$MOCK_LOG")" = 1 ] && [ "$(grep -c '^sleep ' "$MOCK_LOG")" = 2 ] &&
	[ ! -s "$TMPDIR/err" ]; then
	test-ok "temporary post-publish E404s are retried quietly until verified"
else
	test-fail "delayed npm processing: status=$status err=$(cat "$TMPDIR/err")"
fi
MOCK_NPM_STATE=verification_auth_failure
run_release
if [ "$status" = 1 ] && grep -q 'npm accepted the upload, but registry verification failed' "$TMPDIR/err" &&
	! grep -q '^sleep ' "$MOCK_LOG"; then
	test-ok "post-publish auth errors fail immediately, not as processing delays"
else
	test-fail "post-publish auth failure: status=$status err=$(cat "$TMPDIR/err")"
fi
MOCK_NPM_STATE=missing
run_release
if [ "$status" = 0 ] && grep -q '^npm publish ' "$MOCK_LOG"; then
	test-ok "rerunning after failed npm authentication retries publication"
else
	test-fail "npm authentication retry: status=$status err=$(cat "$TMPDIR/err")"
fi

test-step "prebuilt release staging signs without re-entering Kame"
# The Kame `release` target holds the shared project lease and has built these
# assets. Staging must not call `make`, which would repeat that production and
# can wait on the same output locks.
cp VERSION package.json "$fixture/"
git -C "$fixture" tag -d "v$MOCK_VERSION" >/dev/null 2>&1 || true
mkdir -p "$fixture/dist" "$fixture/src/sh"
cp src/sh/release_manifest.py "$fixture/src/sh/"
for name in kame.com kame.wasm kame.js; do
	printf 'prebuilt %s\n' "$name" >"$fixture/dist/$name"
done
release_test_keys "$TMPDIR/prebuilt-keys"
MOCK_MODE=ok MOCK_NPM_STATE=present KAME_RELEASE_PREBUILT=1 \
	KAME_RELEASE_PUBLIC_KEY="$TMPDIR/prebuilt-keys/verification.pem" \
	KAME_RELEASE_SIGNING_KEY="$TMPDIR/prebuilt-keys/signing.pem" \
	run_release --repo "$MOCK_REPO"
if [ "$status" = 0 ] && ! grep -q '^make ' "$MOCK_LOG" &&
	openssl pkeyutl -verify -pubin -inkey "$TMPDIR/prebuilt-keys/verification.pem" \
		-rawin -in "$fixture/dist/release/SHA256SUMS" \
		-sigfile "$fixture/dist/release/SHA256SUMS.sig" >/dev/null; then
	test-ok "prebuilt assets are staged and signed without a nested Kame"
else
	test-fail "prebuilt staging: status=$status err=$(cat "$TMPDIR/err") log=$(cat "$MOCK_LOG")"
fi

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
