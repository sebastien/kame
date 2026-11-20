#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — forwarded declarative build writes
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T010-21 declarative build effect publication"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
wasm_runner="$CLI_ROOT/src/js/kame.js"
if [ -z "${KAME_WASM:-}" ]; then
	make dist-wasm >/dev/null
	wasm_runner="$CLI_ROOT/dist/kame.js"
fi
project="$TMPDIR/project"
mkdir -p "$project"
printf 'bytes\000\377\ntail' >"$project/input.bin"
cat >"$project/Kamefile" <<'KAME'
ROOT = "."
rule ./staged/input.copy : ./input.bin = read inputs.0
rule ./nested/out.bin : ${ROOT}/staged/input.copy
	out! "before\n"
	(write! ./side.txt "side")
	out! "after\n"
	return read inputs.0
task default : ./nested/out.bin
	cat (first inputs)
rule ./empty = ""
rule ./blocked = "failure"
rule ./shell/nested.out
	/bin/sh -c 'printf shell > "$1"' sh (first outputs)
rule ./missing
	true
rule ./no-shell
rule ./written
	(write! ./written "written")
rule ./bad-parent/output
	/bin/sh -c 'printf blocked > "$1"' sh (first outputs)
KAME
cp "$project/input.bin" "$project/expected.bin"
for backend in native wasm; do
	test-step "$backend publishes bytes before dependent recipes"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_runner"); fi
	rm -rf "$project/nested" "$project/staged" "$project/side.txt"
	"${runner[@]}" -C "$project" --allow-read --allow-write --allow-run default >"$project/$backend.out" 2>"$project/error"
	if cmp -s "$project/nested/out.bin" "$project/expected.bin" && cmp -s "$project/staged/input.copy" "$project/expected.bin" && [ "$(cat "$project/side.txt")" = side ]; then test-ok "$backend generated input, return and write reach real filesystem"; else test-fail "$backend missing declarative output"; fi
	printf 'before\nafter\n' >"$project/expected.out"
	cat "$project/expected.bin" >>"$project/expected.out"
	if cmp -s "$project/$backend.out" "$project/expected.out"; then test-ok "$backend effect and dependent output order"; else test-fail "$backend reordered or repeated effects"; fi
	rm -rf "$project/nested" "$project/staged" "$project/side.txt"
	"${runner[@]}" do cat -C "$project" --allow-read --allow-write --allow-run -f Kamefile ./nested/out.bin >"$project/$backend.cat" 2>"$project/error"
	if cmp -s "$project/nested/out.bin" "$project/expected.bin"; then test-ok "$backend cat builds its own artifact"; else test-fail "$backend cat relies on another backend's output"; fi
	rm -f "$project/empty"
	"${runner[@]}" -C "$project" --allow-read --allow-write --allow-run ./empty >"$project/value" 2>"$project/error"
	if [ -f "$project/empty" ] && [ ! -s "$project/empty" ]; then test-ok "$backend publishes zero-byte return value"; else test-fail "$backend empty artifact missing"; fi
	mkdir -p "$project/blocked"
	status=0
	"${runner[@]}" -C "$project" --allow-read --allow-write --allow-run ./blocked >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q FS_ERR "$project/error"; then test-ok "$backend host publication failure fails target"; else test-fail "$backend write error was ignored"; fi
 rm -rf "$project/shell" "$project/missing" "$project/no-shell" "$project/written" "$project/bad-parent"
 "${runner[@]}" -C "$project" --allow-read --allow-write --allow-run ./shell/nested.out >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/shell/nested.out")" = shell ]; then test-ok "$backend creates shell output parent directories"; else test-fail "$backend shell parent directories"; fi
 for target in ./missing ./no-shell; do
  status=0
  "${runner[@]}" -C "$project" --allow-read --allow-write --allow-run "$target" >"$project/value" 2>"$project/error" || status=$?
  if [ "$status" = 1 ] && grep -q OUTPUT_MISSING "$project/error"; then test-ok "$backend rejects omitted output $target"; else test-fail "$backend omitted output was accepted"; cat "$project/error"; fi
 done
 "${runner[@]}" -C "$project" --allow-read --allow-write --allow-run ./written >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/written")" = written ]; then test-ok "$backend verifies explicit write output without shell"; else test-fail "$backend explicit write output"; fi
 printf file >"$project/bad-parent"
 status=0
 "${runner[@]}" -C "$project" --allow-read --allow-write --allow-run ./bad-parent/output >"$project/value" 2>"$project/error" || status=$?
 if [ "$status" = 1 ] && grep -q FS_ERR "$project/error"; then test-ok "$backend parent directory failure fails before shell"; else test-fail "$backend parent directory error"; fi
	if find "$project" -name '.kame-write-*' -print -quit | grep -q .; then test-fail "$backend leaked atomic staging"; else test-ok "$backend cleans staging after failed publication"; fi
done
if cmp -s "$project/native.out" "$project/wasm.out" && cmp -s "$project/native.cat" "$project/wasm.cat"; then test-ok "isolated native/WASM bytes match"; else test-fail "isolated build effect parity"; fi
test-end
