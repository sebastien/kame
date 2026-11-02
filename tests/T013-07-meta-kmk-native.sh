#!/usr/bin/env bash
# Regression: Makefile.kmk compiler configuration and native artifact ownership.
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T013-07 Kame native build recipes"
[ -x "$CLI_BIN" ] || cli_build

test-step "extract the authored recipes into an isolated stub project"
work="$TMPDIR/project"
mkdir -p "$work/bin" "$work/src/go/kame" "$work/src/sh"
sed -n '/^SHELL =/,/^# --- Source sets/p; /^# --- Native binaries/,/^# --- WebAssembly/p' \
	"$CLI_ROOT/Makefile.kmk" | sed 's/@{KAME_INPUTS}/.\/seed/g' >"$work/Makefile.kmk"
# Toolchain provisioning is tested separately; this fixture stubs the compiler.
printf '\ntoolchain :\n' >>"$work/Makefile.kmk"
printf seed >"$work/seed"
printf unused >"$work/src/sh/platform_id.py"
cat >"$work/src/sh/provision-cosmocc.sh" <<'SH'
#!/bin/sh
mkdir -p "$1/bin"
touch "$1/bin/cosmocc"
chmod +x "$1/bin/cosmocc"
SH
cat >"$work/bin/so" <<'SH'
#!/bin/sh
set -eu
while [ "$1" != -o ]; do shift; done
printf '%s|%s\n' "${CC-<unset>}" "$CFLAGS" >"$2"
SH
cat >"$work/bin/python3" <<'SH'
#!/bin/sh
printf x >> platform-runs
printf 'test-host\n'
SH
cat >"$work/bin/cp" <<'SH'
#!/bin/sh
printf x >> copy-runs
exec /bin/cp "$@"
SH
chmod +x "$work/bin/so" "$work/bin/python3" "$work/bin/cp" "$work/src/sh/provision-cosmocc.sh"
export PATH="$work/bin:$PATH"
unset CC KAME_CC KAME_CFLAGS KAME_PLATFORM KAME_KAME_PLATFORM
run() {
	if ! timeout 20 "$CLI_BIN" -C "$work" "$@" >"$work/out" 2>"$work/err"; then
		cat "$work/out" "$work/err"
		test-fail "recipe invocation: $*"
		exit 1
	fi
}
expect_compiler() {
	if [ "$(cut -d '|' -f 1 "$work/$1")" = "$2" ]; then test-ok "$1 compiler: $2"; else test-fail "$1 compiler: $(cat "$work/$1")"; fi
}

test-step "debug and release honor configuration without blanking inherited CC"
for target in build/kame.debug dist/kame; do
	run --force "./$target"
	expect_compiler "$target" '<unset>'
	CC=raw KAME_CC=alias run --force "./$target"
	expect_compiler "$target" alias
	CC=raw KAME_CC=alias run --force "./$target" 'CC=assignment compiler'
	expect_compiler "$target" 'assignment compiler'
	CC=raw KAME_CC=alias run --force "./$target" CC=
	expect_compiler "$target" raw
done
test-step "sanitizer and APE compilers remain pinned"
CC=raw KAME_CC=alias run --force ./build/kame.sanitize CC=assignment
expect_compiler build/kame.sanitize clang
CC=raw KAME_CC=alias run --force ./dist/kame.com CC=assignment
expect_compiler dist/kame.com "$work/build/tools/cosmocc-4.0.2/bin/cosmocc"

test-step "planning and dry-run never execute platform detection or copies"
for command in plan inputs; do
	timeout 20 "$CLI_BIN" 'do' "$command" -C "$work" dist-native >"$work/out" 2>"$work/err"
done
run -n dist-native
if [ ! -e "$work/platform-runs" ] && [ ! -e "$work/copy-runs" ] && [ ! -d "$work/dist/native" ]; then test-ok "inspection has no process effects"; else test-fail "inspection ran a process"; fi

test-step "dist-native requests the pattern producer and reuses unchanged copies"
run dist-native
run dist-native
if [ "$(cat "$work/copy-runs")" = x ] && [ -x "$work/dist/native/test-host/kame" ] && cmp -s "$work/dist/kame" "$work/dist/native/test-host/kame"; then test-ok "host copy is executable and incremental"; else test-fail "host artifact was absent or recopied"; fi
printf changed >"$work/seed"
run dist-native
if [ "$(cat "$work/copy-runs")" = x ]; then test-ok "byte-identical rebuilt release does not recopy"; else test-fail "identical release recopied"; fi
run dist-native CC=changed
if [ "$(cat "$work/copy-runs")" = xx ] && cmp -s "$work/dist/kame" "$work/dist/native/test-host/kame"; then test-ok "changed release refreshes host copy"; else test-fail "changed release did not refresh copy"; fi
rm "$work/dist/native/test-host/kame"
run dist-native CC=changed
if [ "$(cat "$work/copy-runs")" = xxx ]; then test-ok "missing copy is rebuilt"; else test-fail "missing copy was not rebuilt"; fi

test-step "automatic clean recognizes the dynamically requested platform output"
run dist-native
timeout 20 "$CLI_BIN" 'do' clean -C "$work" >"$work/out" 2>"$work/err"
if [ ! -e "$work/dist/native/test-host/kame" ] && [ -f "$work/seed" ]; then test-ok "owned native copy is cleaned; source is preserved"; else
	test-fail "native copy was not owned by the pattern rule"
	cat "$work/out" "$work/err"
fi

test-step "configured platforms bypass runtime detection"
platform_runs="$(cat "$work/platform-runs")"
KAME_PLATFORM=alias-platform run dist-native KAME_PLATFORM=explicit-platform
KAME_PLATFORM=alias-platform run dist-native
if [ -x "$work/dist/native/explicit-platform/kame" ] && [ -x "$work/dist/native/alias-platform/kame" ] && [ "$(cat "$work/platform-runs")" = "$platform_runs" ]; then test-ok "platform assignment wins over alias without detection"; else test-fail "configured platform selection"; fi
test-step "the WASM gate passes prebuilt artifacts to nested Make without relocking"
gate="$TMPDIR/wasm-gate"
mkdir -p "$gate/dist"
cp "$CLI_ROOT/Makefile" "$gate/Makefile"
touch "$gate/dist/kame.js" "$gate/dist/kame.wasm"
printf 'SHELL = kash\nTEST_WASM = [./probe.sh]\n' >"$gate/Makefile.kmk"
sed -n '/^test-wasm :/,/^$/p' "$CLI_ROOT/Makefile.kmk" |
	sed 's/^test-wasm :.*/test-wasm :/' >>"$gate/Makefile.kmk"
cat >"$gate/probe.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
test "$KAME_TEST_PREBUILT" = "$PWD"
make KAME="$GATE_CLI" dist-wasm
touch nested-make-passed
SH
chmod +x "$gate/probe.sh"
if GATE_CLI="$CLI_BIN" timeout 20 "$CLI_BIN" -C "$gate" test-wasm >"$gate/out" 2>"$gate/err" && [ -f "$gate/nested-make-passed" ]; then
	test-ok "nested Make reused artifacts while the outer gate held the project lock"
else
	cat "$gate/out" "$gate/err"
	test-fail "WASM gate attempted a nested build or dropped its prebuilt environment"
fi

test-end
