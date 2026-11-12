#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — published release self-build
#
# Build this checkout's dist/kame with a kame obtained from a published
# channel: the npm/WASM CLI through bunx/npx, then the latest GitHub native.
# Network access, git and the Go toolchain are required. This is an opt-in
# release check, not part of the hermetic tests/harness.sh suite.
set -euo pipefail

root=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)

package=${KAME_PACKAGE:-@ssebastien/kame}
repository=${KAME_RELEASE_REPO:-sebastien/kame}
channel=${KAME_TEST_CHANNEL:-auto}
source=${KAME_TEST_SOURCE:-$root}
explicit_version=${KAME_TEST_RELEASE_VERSION:-}
runner_preference=${KAME_TEST_RUNNER:-auto}
timeout_seconds=${KAME_TEST_TIMEOUT:-1200}
public_key=${KAME_RELEASE_PUBLIC_KEY:-}
release_url=${KAME_RELEASE_URL:-}

keep=0
case ${KAME_TEST_KEEP:-0} in
1 | true | yes) keep=1 ;;
*) keep=0 ;;
esac

usage() {
	cat <<'USAGE'
Build this checkout's dist/kame with a published kame.

Usage:
  tests/platform/test-release-build.sh [OPTIONS]

Options:
  --channel auto|wasm|native   auto tries WASM, then the native (default: auto)
  --version VERSION         published version to use (default: latest)
  --source DIR              kame checkout to build (default: this repository)
  --keep                    keep temporary trees and logs
  -h, --help                show this help

Environment:
  KAME_TEST_RELEASE_VERSION  same as --version
  KAME_TEST_CHANNEL          same as --channel
  KAME_TEST_SOURCE           same as --source
  KAME_TEST_RUNNER           auto, bun, or npm (default: auto)
  KAME_TEST_TIMEOUT          per-command timeout in seconds (default: 1200)
  KAME_TEST_KEEP=1           keep temporary trees and logs
  KAME_PACKAGE               npm package (default: @ssebastien/kame)
  KAME_RELEASE_REPO          GitHub repository (default: sebastien/kame)
  KAME_RELEASE_URL           release asset base URL override
  KAME_RELEASE_PUBLIC_KEY    optional Ed25519 PEM for the release signature

Requires git, curl, tar, Python 3 and Go. The WASM channel also needs bun or npm;
the native channel supports Linux and macOS on x86-64 and ARM64.
USAGE
}

usage_error() {
	printf 'test-release-build: %s\n' "$1" >&2
	usage >&2
	exit 2
}

note() {
	printf '%s\n' "$*" >&2
}

warn() {
	if [ "${GITHUB_ACTIONS:-}" = true ]; then
		local message=${1//'%'/'%25'}
		message=${message//$'\r'/'%0D'}
		message=${message//$'\n'/'%0A'}
		printf '::warning::%s\n' "$message"
	else
		printf 'warning: %s\n' "$*" >&2
	fi
}

die() {
	printf 'test-release-build: %s\n' "$*" >&2
	exit 1
}

while [ $# -gt 0 ]; do
	case $1 in
	--channel)
		[ $# -ge 2 ] || usage_error '--channel requires a value'
		channel=$2
		shift 2
		;;
	--version)
		[ $# -ge 2 ] || usage_error '--version requires a value'
		explicit_version=$2
		shift 2
		;;
	--source)
		[ $# -ge 2 ] || usage_error '--source requires a value'
		source=$2
		shift 2
		;;
	--keep)
		keep=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		usage_error "unknown argument: $1"
		;;
	esac
done

case $channel in
auto | wasm | native) ;;
*) usage_error "invalid channel: $channel" ;;
esac

[[ $timeout_seconds =~ ^[1-9][0-9]*$ ]] || usage_error "invalid KAME_TEST_TIMEOUT: $timeout_seconds"

case $runner_preference in
auto | bun | npm) ;;
*) usage_error "invalid KAME_TEST_RUNNER: $runner_preference" ;;
esac

source=$(CDPATH='' cd -- "$source" && pwd) || die "source directory does not exist"
[ -f "$source/Kamefile" ] || die "$source is not a kame checkout (missing Kamefile)"
[ -f "$source/VERSION" ] || die "$source is not a kame checkout (missing VERSION)"
command -v git >/dev/null 2>&1 || die 'git is required to copy the checkout'
command -v tar >/dev/null 2>&1 || die 'tar is required to copy the checkout'
command -v go >/dev/null 2>&1 || die 'Go is required to build kame'

top=$(git -C "$source" rev-parse --show-toplevel 2>/dev/null) || die "$source is not a git checkout"
[ "$(CDPATH='' cd -- "$top" && pwd -P)" = "$(CDPATH='' cd -- "$source" && pwd -P)" ] ||
	die '--source and KAME_TEST_SOURCE must name the repository root'

normalize_version() {
	local value=${1#v}
	[[ $value =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] || return 1
	printf '%s\n' "$value"
}

source_version=$(tr -d ' \t\r\n' <"$source/VERSION")
source_version=$(normalize_version "$source_version") || die "invalid VERSION in $source"

case $explicit_version in
'' | latest) explicit_version='' ;;
*) explicit_version=$(normalize_version "$explicit_version") || usage_error "invalid version: $explicit_version" ;;
esac

tmp=$(mktemp -d "${TMPDIR:-/tmp}/kame-release-self-build.XXXXXX") || die 'cannot create a temporary directory'
cleanup() {
	local status=$?
	trap - EXIT INT TERM HUP
	if [ "$keep" = 1 ]; then
		printf 'kept temporary directory: %s\n' "$tmp" >&2
	else
		rm -rf "$tmp"
	fi
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

run_limited() {
	if command -v timeout >/dev/null 2>&1; then
		timeout "$timeout_seconds" "$@"
	else
		"$@"
	fi
}

copy_tree() {
	local destination=$1
	mkdir -p "$destination" || return 1
	{
		git -C "$source" ls-files -z
		git -C "$source" ls-files -z --others --exclude-standard
	} | tar --null -T - -cf - | tar -xf - -C "$destination" || return 1
}

npm_latest() {
	command -v npm >/dev/null 2>&1 || return 1
	local value
	value=$(run_limited npm view "$package" version --json 2>/dev/null) || return 1
	value=${value//\"/}
	value=${value//[[:space:]]/}
	normalize_version "$value"
}

github_latest() {
	command -v curl >/dev/null 2>&1 || return 1
	local url
	url=$(run_limited curl -fsSL -o /dev/null -w '%{url_effective}' \
		"https://github.com/$repository/releases/latest" 2>/dev/null) || return 1
	url=${url##*/tag/}
	normalize_version "$url"
}

resolve_wasm_version() {
	local version
	if [ -n "$explicit_version" ]; then
		normalize_version "$explicit_version"
		return
	fi
	if version=$(npm_latest); then
		printf '%s\n' "$version"
		return 0
	fi
	github_latest
}

resolve_native_version() {
	local version
	if [ -n "$explicit_version" ]; then
		normalize_version "$explicit_version"
		return
	fi
	if version=$(github_latest); then
		printf '%s\n' "$version"
		return 0
	fi
	npm_latest
}

release_base() {
	local version=$1
	if [ -n "$release_url" ]; then
		printf '%s\n' "${release_url%/}"
	else
		printf 'https://github.com/%s/releases/download/v%s\n' "$repository" "$version"
	fi
}

select_wasm_runner() {
	case $runner_preference in
	bun)
		command -v bun >/dev/null 2>&1 || return 1
		printf 'bun\n'
		;;
	npm)
		command -v npx >/dev/null 2>&1 || command -v npm >/dev/null 2>&1 || return 1
		printf 'npm\n'
		;;
	auto)
		if command -v bun >/dev/null 2>&1; then
			printf 'bun\n'
		elif command -v npx >/dev/null 2>&1 || command -v npm >/dev/null 2>&1; then
			printf 'npm\n'
		else
			return 1
		fi
		;;
	esac
}

write_wasm_runner() {
	local runner=$1 directory=$2 version=$3
	mkdir -p "$directory" || return 1
	case $runner in
	bun)
		cat >"$directory/kame-dist" <<SH
#!/usr/bin/env bash
set -euo pipefail
here=\$(CDPATH='' cd -- "\$(dirname -- "\$0")" && pwd)
cd "\$here"
exec bun x '$package@$version' "\$@"
SH
		;;
	npm)
		cat >"$directory/kame-dist" <<SH
#!/usr/bin/env bash
set -euo pipefail
here=\$(CDPATH='' cd -- "\$(dirname -- "\$0")" && pwd)
cd "\$here"
if command -v npx >/dev/null 2>&1; then
	exec npx --yes '$package@$version' "\$@"
fi
exec npm exec --yes --package '$package@$version' -- "\$@"
SH
		;;
	*) return 1 ;;
	esac
	chmod +x "$directory/kame-dist" || return 1
}

assert_dist_version() {
	local file=$1 version=$2 output
	output=$(cat "$file") || return 1
	case $output in
	"kame $version "*) return 0 ;;
	"kame $version") return 0 ;;
	esac
	printf 'expected kame %s, got: %s\n' "$version" "$output" >&2
	return 1
}

assert_built() {
	local tree=$1 version=$2 binary output
	binary=$tree/dist/kame
	[ -f "$binary" ] || {
		printf 'missing %s\n' "$binary" >&2
		return 1
	}
	[ -x "$binary" ] || {
		printf 'not executable: %s\n' "$binary" >&2
		return 1
	}
	output=$(run_limited "$binary" --version 2>&1) || {
		printf '%s --version failed: %s\n' "$binary" "$output" >&2
		return 1
	}
	case $output in
	"kame $version "*) return 0 ;;
	"kame $version") return 0 ;;
	esac
	printf 'built binary reports %s, expected kame %s\n' "$output" "$version" >&2
	return 1
}

verify_checksum() {
	local directory=$1 line
	line=$(awk -v name="$asset" '$2 == name {print}' "$directory/SHA256SUMS")
	[ -n "$line" ] || return 1
	if command -v sha256sum >/dev/null 2>&1; then
		(cd "$directory" && printf '%s\n' "$line" | sha256sum -c -)
	elif command -v shasum >/dev/null 2>&1; then
		(cd "$directory" && printf '%s\n' "$line" | shasum -a 256 -c -)
	else
		return 1
	fi
}

verify_signature() {
	local directory=$1
	[ -n "$public_key" ] || return 0
	[ -f "$public_key" ] || return 1
	command -v openssl >/dev/null 2>&1 || return 1
	(cd "$directory" && openssl pkeyutl -verify -pubin -inkey "$public_key" -rawin \
		-in SHA256SUMS -sigfile SHA256SUMS.sig >/dev/null)
}

failure_reason() {
	local log=$1 line
	line=$(grep -E 'error\[' "$log" 2>/dev/null | tail -n 1 || true)
	[ -n "$line" ] || line=$(grep -E '^error ' "$log" 2>/dev/null | tail -n 1 || true)
	[ -n "$line" ] || line=$(tail -n 1 "$log" 2>/dev/null || true)
	[ -n "$line" ] || line='no diagnostic output'
	printf '%s\n' "$line"
}

dump_log() {
	local label=$1 log=$2
	printf '%s\n' "----- $label log -----" >&2
	tail -n 40 "$log" >&2 || true
	printf '%s\n' "----- end $label log -----" >&2
}

wasm_built_version=''
native_built_version=''

try_wasm() {
	local version runner tree log
	log=$tmp/wasm.log
	: >"$log"
	version=$(resolve_wasm_version) || {
		printf 'cannot resolve a published npm/WASM version\n' >>"$log"
		return 1
	}
	runner=$(select_wasm_runner) || {
		printf 'no JavaScript package runner (bun, npx or npm) is available\n' >>"$log"
		return 1
	}
	note "WASM channel: kame $version via $runner"
	write_wasm_runner "$runner" "$tmp/runner-wasm" "$version" >>"$log" 2>&1 || return 1
	tree=$tmp/tree-wasm
	copy_tree "$tree" >>"$log" 2>&1 || return 1

	run_limited "$tmp/runner-wasm/kame-dist" --version >"$tmp/wasm-version.out" 2>>"$log" || return 1
	assert_dist_version "$tmp/wasm-version.out" "$version" 2>>"$log" || return 1

	note "WASM channel: self-building dist/kame"
	run_limited "$tmp/runner-wasm/kame-dist" -C "$tree" dist/kame >>"$log" 2>&1 || return 1
	assert_built "$tree" "$source_version" >>"$log" 2>&1 || return 1
	wasm_built_version=$version
	note "WASM channel: built kame $source_version"
	return 0
}

# Stable OS/architecture key used for native release artifacts.
native_platform() {
	local system machine
	case "$(uname -s)" in Linux) system=linux ;; Darwin) system=darwin ;; *) return 1 ;; esac
	case "$(uname -m)" in x86_64 | amd64) machine=x86_64 ;; aarch64 | arm64) machine=arm64 ;; *) return 1 ;; esac
	printf '%s-%s\n' "$system" "$machine"
}

try_native() {
	local version base directory tree log asset
	asset=kame-$(native_platform) || return 1
	log=$tmp/native.log
	: >"$log"
	version=$(resolve_native_version) || {
		printf 'cannot resolve a published native version\n' >>"$log"
		return 1
	}
	command -v curl >/dev/null 2>&1 || {
		printf 'curl is required to download the native release\n' >>"$log"
		return 1
	}
	note "native channel: kame $version"
	base=$(release_base "$version")
	directory=$tmp/native-$version
	mkdir -p "$directory" >>"$log" 2>&1 || return 1

	run_limited curl -fsSL "$base/$asset" -o "$directory/$asset" >>"$log" 2>&1 || return 1
	run_limited curl -fsSL "$base/SHA256SUMS" -o "$directory/SHA256SUMS" >>"$log" 2>&1 || return 1
	if [ -n "$public_key" ]; then
		run_limited curl -fsSL "$base/SHA256SUMS.sig" -o "$directory/SHA256SUMS.sig" >>"$log" 2>&1 || return 1
	else
		note "native channel: signature verification skipped (KAME_RELEASE_PUBLIC_KEY is not set)"
	fi

	verify_checksum "$directory" >>"$log" 2>&1 || {
		printf '$asset checksum verification failed\n' >>"$log"
		return 1
	}
	verify_signature "$directory" >>"$log" 2>&1 || {
		printf 'SHA256SUMS signature verification failed\n' >>"$log"
		return 1
	}
	chmod +x "$directory/$asset" >>"$log" 2>&1 || return 1

	run_limited "$directory/$asset" --version >"$tmp/native-version.out" 2>>"$log" || return 1
	assert_dist_version "$tmp/native-version.out" "$version" 2>>"$log" || return 1

	tree=$tmp/tree-native
	copy_tree "$tree" >>"$log" 2>&1 || return 1
	note "native channel: self-building dist/kame"
	run_limited "$directory/$asset" -C "$tree" dist/kame >>"$log" 2>&1 || return 1
	assert_built "$tree" "$source_version" >>"$log" 2>&1 || return 1
	native_built_version=$version
	note "native channel: built kame $source_version"
	return 0
}

winner=''
winner_version=''

if [ "$channel" = auto ] || [ "$channel" = wasm ]; then
	if try_wasm; then
		winner=wasm
		winner_version=$wasm_built_version
	else
		reason=$(failure_reason "$tmp/wasm.log")
		if [ "$channel" = wasm ]; then
			dump_log WASM "$tmp/wasm.log"
			die "WASM self-build failed: $reason"
		fi
		warn "WASM self-build unavailable: $reason"
	fi
fi

if [ -z "$winner" ] && { [ "$channel" = auto ] || [ "$channel" = native ]; }; then
	if try_native; then
		winner=native
		winner_version=$native_built_version
	else
		reason=$(failure_reason "$tmp/native.log")
		dump_log native "$tmp/native.log"
		die "native self-build failed: $reason"
	fi
fi

[ -n "$winner" ] || die 'no published channel could self-build kame'
printf 'release self-build passed: kame %s built kame %s via %s\n' \
	"$winner_version" "$source_version" "$winner"
