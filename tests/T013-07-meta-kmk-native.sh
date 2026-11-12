#!/usr/bin/env bash
# Regression: Kamefile compiler configuration and native artifact ownership.
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T013-07 Kame native build recipes"
[ -x "$CLI_BIN" ] || cli_build

test-step "extract the authored recipes into an isolated stub project"
work="$TMPDIR/project"
mkdir -p "$work/bin" "$work/src/go/kame"
sed -n '/^SHELL =/,/^# --- Source sets/p; /^# --- Native binaries/,/^# --- WebAssembly/p' \
	"$CLI_ROOT/Kamefile" | sed 's/@{KAME_INPUTS}/.\/seed/g' >"$work/Makefile.kmk"
printf seed >"$work/seed"
# The extracted header includes the repository's Kash libraries.
cp -R "$CLI_ROOT/src/kash" "$work/src/kash"
cat >"$work/bin/go" <<'SH'
#!/bin/sh
set -eu
arguments="$*"
while [ "$1" != -o ]; do shift; done
printf '%s|%s\n' "${CGO_ENABLED-<unset>}" "$arguments" >"$2"
SH
cat >"$work/bin/uname" <<'SH'
#!/bin/sh
printf x >> platform-runs
case "$1" in -s) printf 'Linux\n' ;; -m) printf 'X86_64\n' ;; esac
SH
cat >"$work/bin/cp" <<'SH'
#!/bin/sh
printf x >> copy-runs
exec /bin/cp "$@"
SH
chmod +x "$work/bin/go" "$work/bin/uname" "$work/bin/cp"
export PATH="$work/bin:$PATH"
unset CC KAME_CC KAME_CFLAGS KAME_PLATFORM KAME_KAME_PLATFORM
run() {
	if ! timeout 20 "$CLI_BIN" -C "$work" "$@" >"$work/out" 2>"$work/err"; then
		cat "$work/out" "$work/err"
		test-fail "recipe invocation: $*"
		exit 1
	fi
}
test-step "native recipes disable cgo and select Go artifact modes"
for pair in build/kame.debug:debug dist/kame:release build/kame.sanitize:sanitize; do
	target=${pair%:*}; mode=${pair#*:}
	CC=invalid run --force "./$target"
	if [ "$(cut -d '|' -f 1 "$work/$target")" = 0 ] && grep -q "main.artifactMode=$mode" "$work/$target"; then test-ok "$target uses Go with cgo disabled"; else test-fail "$target build arguments: $(cat "$work/$target")"; fi
done
if grep -q -- '-d=checkptr=2' "$work/build/kame.sanitize"; then test-ok "sanitizer artifact enables Go pointer checks"; else test-fail "sanitizer flags missing"; fi

test-step "planning and dry-run never execute platform detection or copies"
for command in plan inputs; do
	timeout 20 "$CLI_BIN" 'do' "$command" -C "$work" dist-native >"$work/out" 2>"$work/err"
done
run -n dist-native
if [ ! -e "$work/platform-runs" ] && [ ! -e "$work/copy-runs" ] && [ ! -d "$work/dist/native" ]; then test-ok "inspection has no process effects"; else test-fail "inspection ran a process"; fi

test-step "dist-native requests the pattern producer and reuses unchanged copies"
run dist-native
run dist-native
if [ "$(cat "$work/copy-runs")" = x ] && [ -x "$work/dist/native/linux-x86_64/kame" ] && cmp -s "$work/dist/kame" "$work/dist/native/linux-x86_64/kame"; then test-ok "host copy is executable and incremental"; else test-fail "host artifact was absent or recopied"; fi
printf changed >"$work/seed"
run dist-native
if [ "$(cat "$work/copy-runs")" = x ]; then test-ok "byte-identical rebuilt release does not recopy"; else test-fail "identical release recopied"; fi
sed -i 's/main.artifactMode=release/main.artifactMode=changed/' "$work/Makefile.kmk"
run dist-native
if [ "$(cat "$work/copy-runs")" = xx ] && cmp -s "$work/dist/kame" "$work/dist/native/linux-x86_64/kame"; then test-ok "changed release refreshes host copy"; else test-fail "changed release did not refresh copy"; fi
rm "$work/dist/native/linux-x86_64/kame"
run dist-native
if [ "$(cat "$work/copy-runs")" = xxx ]; then test-ok "missing copy is rebuilt"; else test-fail "missing copy was not rebuilt"; fi

test-step "automatic clean recognizes the dynamically requested platform output"
run dist-native
timeout 20 "$CLI_BIN" 'do' clean -C "$work" >"$work/out" 2>"$work/err"
if [ ! -e "$work/dist/native/linux-x86_64/kame" ] && [ -f "$work/seed" ]; then test-ok "owned native copy is cleaned; source is preserved"; else
	test-fail "native copy was not owned by the pattern rule"
	cat "$work/out" "$work/err"
fi

test-step "configured platforms bypass runtime detection"
platform_runs="$(cat "$work/platform-runs")"
KAME_PLATFORM=alias-platform run dist-native KAME_PLATFORM=explicit-platform
KAME_PLATFORM=alias-platform run dist-native
if [ -x "$work/dist/native/explicit-platform/kame" ] && [ -x "$work/dist/native/alias-platform/kame" ] && [ "$(cat "$work/platform-runs")" = "$platform_runs" ]; then test-ok "platform assignment wins over alias without detection"; else test-fail "configured platform selection"; fi
test-step "the WASM gate's nested Make reaches Kame without deadlocking on the project lease"
gate="$TMPDIR/wasm-gate"
mkdir -p "$gate/dist"
cp "$CLI_ROOT/Makefile" "$gate/Makefile"
touch "$gate/dist/kame.js" "$gate/dist/kame.wasm"
printf 'SHELL = kash\nTEST_WASM = [./probe.sh]\ndist-wasm : ./dist/kame.js ./dist/kame.wasm\n\n' >"$gate/Makefile.kmk"
sed -n '/^test-wasm :/,/^$/p' "$CLI_ROOT/Kamefile" |
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
	test-ok "nested Make forwarded to Kame while the outer gate held the project lease"
else
	cat "$gate/out" "$gate/err"
	test-fail "WASM gate attempted a nested build or dropped its prebuilt environment"
fi

test-end
