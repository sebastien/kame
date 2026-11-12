#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — Make bootstrap
# Spec: docs/spec/013-tests.md — T015-08-dist-make-bootstrap
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T015-08 Make bootstrap"

root="$TMPDIR/t015-08"
project="$root/project"
release="$root/release"
stubs="$root/stubs"
mkdir -p "$project" "$stubs"
# A copied Makefile has no Go sources, so it never selects the source build.
cp "$CLI_ROOT/Makefile" "$project/Makefile"
printf '9.9.9\n' >"$project/VERSION"

case "$(uname -s)" in Linux) os=linux ;; Darwin) os=darwin ;; *) os='' ;; esac
case "$(uname -m)" in x86_64 | amd64) arch=x86_64 ;; aarch64 | arm64) arch=arm64 ;; *) arch='' ;; esac
asset="kame-$os-$arch"

sha() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

# Arguments are logged one per line so quoting is observable.
cat >"$stubs/argv" <<'SH'
#!/bin/sh
printf '%s\n' "$@" >"$ARGV_LOG"
exit "${ARGV_STATUS:-0}"
SH
chmod +x "$stubs/argv"
export ARGV_LOG="$root/argv.log"

# Only system tools: no `kame` from the developer's PATH leaks in, and the
# test library's KAME must not act as an explicit executable selection.
unset KAME
base_path="/usr/bin:/bin"

make_suite() {
	local make=$1 label status output
	label=$(basename "$make")
	rm -rf "$project/.kame" "$stubs/kame" "$release"
	mkdir -p "$release"
	bootstrap() { (cd "$project" && PATH="$base_path" "$make" -s "$@"); }

	test-step "$label: goals, flags and assignments are forwarded in one invocation"
	if bootstrap KAME="$stubs/argv" -n -j3 dist/app task "msg=it's  spaced" >/dev/null &&
		[ "$(cat "$ARGV_LOG")" = $'-n\n--jobs=3\nmsg=it\'s  spaced\n./dist/app\ntask' ]; then
		test-ok "kame received -n, --jobs, the assignment and ./-prefixed file goal"
	else
		test-fail "unexpected argv: $(cat "$ARGV_LOG" 2>/dev/null)"
	fi
	if bootstrap KAME="$stubs/argv" >/dev/null && [ -z "$(cat "$ARGV_LOG")" ]; then
		test-ok "no goal leaves selection to Kame's default"
	else
		test-fail "default invocation passed arguments: $(cat "$ARGV_LOG")"
	fi
	status=0
	ARGV_STATUS=3 bootstrap KAME="$stubs/argv" build >/dev/null 2>&1 || status=$?
	if [ "$status" != 0 ] && [ ! -e "$project/.kame" ]; then
		test-ok "a failing Kame fails make; an explicit KAME is never installed"
	else
		test-fail "status=$status or unexpected install"
	fi
	output=$(printf 'typed\n' | bootstrap KAME="$stubs/stdin" 2>&1)
	if [ "$output" = "stdin:typed" ]; then
		test-ok "Kame inherits make's stdin"
	else
		test-fail "stdin was not forwarded: $output"
	fi

	if [ -z "$os" ] || [ -z "$arch" ]; then
		test-ok "release installation is unsupported on this host; skipped"
		return 0
	fi

	test-step "$label: a mismatched kame on PATH is ignored"
	cat >"$stubs/kame" <<'SH'
#!/bin/sh
[ "$1" = "--version" ] && { echo "kame 0.0.1 (stub)"; exit 0; }
echo "path:$*"
SH
	chmod +x "$stubs/kame"
	status=0
	output=$(cd "$project" && PATH="$stubs:$base_path" KAME_RELEASE_URL="file://$release" "$make" -s build 2>&1) || status=$?
	if [ "$status" != 0 ] && ! grep -q 'path:' <<<"$output"; then
		test-ok "version mismatch fell through to installation"
	else
		test-fail "mismatched PATH kame was used: $output"
	fi

	test-step "$label: a matching kame on PATH is used without installing"
	sed -i.bak 's/0\.0\.1/9.9.9/' "$stubs/kame" && rm -f "$stubs/kame.bak"
	output=$(cd "$project" && PATH="$stubs:$base_path" "$make" -s build 2>&1)
	if [ "$output" = "path:build" ] && [ ! -e "$project/.kame" ]; then
		test-ok "matching PATH kame received the goals"
	else
		test-fail "matching PATH kame: $output"
	fi

	test-step "$label: a tampered release is rejected"
	printf '#!/bin/sh\necho "release:$*"\n' >"$release/$asset"
	chmod +x "$release/$asset"
	(cd "$release" && printf '%s  %s\n' "$(sha "$asset")" "$asset" >SHA256SUMS)
	printf 'tampered\n' >>"$release/$asset"
	status=0
	output=$(cd "$project" && PATH="$base_path" KAME_RELEASE_URL="file://$release" "$make" -s build 2>&1) || status=$?
	if [ "$status" != 0 ] && grep -q 'checksum mismatch' <<<"$output" && [ ! -e "$project/.kame/bootstrap/9.9.9/kame" ]; then
		test-ok "checksum mismatch stops before installation"
	else
		test-fail "tampered asset: status=$status output=$output"
	fi

	test-step "$label: a verified release is installed once and reused offline"
	printf '#!/bin/sh\necho "release:$*"\n' >"$release/$asset"
	(cd "$release" && printf '%s  %s\n' "$(sha "$asset")" "$asset" >SHA256SUMS)
	output=$(cd "$project" && PATH="$base_path" KAME_RELEASE_URL="file://$release" "$make" -s build N=1 2>/dev/null)
	if [ "$output" = "release:N=1 build" ] && [ -x "$project/.kame/bootstrap/9.9.9/kame" ]; then
		test-ok "installed pinned release received the goals"
	else
		test-fail "installed release: $output"
	fi
	rm -rf "$release"
	output=$(cd "$project" && PATH="$base_path" KAME_RELEASE_URL="file://$release" "$make" -s test 2>&1)
	if [ "$output" = "release:test" ]; then
		test-ok "installed release is reused without downloading"
	else
		test-fail "reuse: $output"
	fi
}

cat >"$stubs/stdin" <<'SH'
#!/bin/sh
IFS= read -r line
echo "stdin:$line"
SH
chmod +x "$stubs/stdin"

# The bootstrap is POSIX make: exercise every implementation installed here.
for make in make bmake; do
	if command -v "$make" >/dev/null 2>&1; then make_suite "$(command -v "$make")"; fi
done

test-end
