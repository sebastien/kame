#!/usr/bin/env bash
# Spec: docs/spec/016-templates.md — portable document rendering CLI
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T016-01 render command"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
work="$TMPDIR/render"
mkdir -p "$work"
printf 'Hello @(name)!' >"$work/hello.md"
printf '@(text (read ./value.txt))' >"$work/read.md"
printf 'read-value' >"$work/value.txt"
printf '@if(:true)\nmissing end' >"$work/broken.md"
for backend in native wasm; do
 test-step "$backend raw output, payloads, sources and styles"
 if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
 "${runner[@]}" do render "$work/hello.md" --define name=World >"$work/out" 2>"$work/err"
 printf 'Hello World!' >"$work/expected"
 if cmp -s "$work/out" "$work/expected"; then test-ok "$backend file payload raw bytes"; else test-fail "$backend file rendering"; fi
 "${runner[@]}" do render -c 'Hello @(name)!' --define name=old --define 'name=new "value"' >"$work/out" 2>"$work/err"
 printf 'Hello new "value"!' >"$work/expected"
 if cmp -s "$work/out" "$work/expected"; then test-ok "$backend inline repeated definitions preserve literals"; else test-fail "$backend inline definitions"; fi
 printf 'stdin @(name)' | "${runner[@]}" do render --define name=OK >"$work/out" 2>"$work/err"
 if [ "$(cat "$work/out")" = 'stdin OK' ]; then test-ok "$backend default stdin"; else test-fail "$backend stdin"; fi
 printf 'literal' >"$work/no-extension"
 "${runner[@]}" do render "$work/no-extension" --comment plain >"$work/out" 2>"$work/err"
 if [ "$(cat "$work/out")" = literal ]; then test-ok "$backend explicit style overrides extension"; else test-fail "$backend style"; fi
 "${runner[@]}" do render -c '@(missing)' --check >"$work/out" 2>"$work/err"
 if [ ! -s "$work/out" ]; then test-ok "$backend check does not evaluate or output"; else test-fail "$backend check output"; fi
 status=0
 "${runner[@]}" do render "$work/broken.md" --comment plain --check >"$work/out" 2>"$work/err" || status=$?
 if [ "$status" = 1 ] && grep -q TPL_BLOCK "$work/err" && grep -q broken.md "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend authored parse diagnostic"; else test-fail "$backend parse diagnostic"; cat "$work/err"; fi
 status=0
 (cd "$work" && "${runner[@]}" do render read.md) >"$work/out" 2>"$work/err" || status=$?
 if [ "$status" = 1 ] && grep -q CAP_DENIED "$work/err" && [ ! -s "$work/out" ]; then test-ok "$backend template read denied by default"; else test-fail "$backend read denial"; cat "$work/err"; fi
 (cd "$work" && "${runner[@]}" do render read.md --allow-read) >"$work/out" 2>"$work/err"
 if [ "$(cat "$work/out")" = read-value ]; then test-ok "$backend explicit read grant"; else test-fail "$backend read grant"; fi
 printf 'nul\0control\001\n' >"$work/binary.md"
 "${runner[@]}" do render "$work/binary.md" >"$work/out" 2>"$work/err"
 if cmp -s "$work/binary.md" "$work/out"; then test-ok "$backend raw control bytes"; else test-fail "$backend control bytes"; fi
 status=0
 "${runner[@]}" do render -c x --define broken >"$work/out" 2>"$work/err" || status=$?
 if [ "$status" = 2 ] && grep -q OPT_VALUE_INVALID "$work/err"; then test-ok "$backend define usage status"; else test-fail "$backend usage"; fi
done
test-end
