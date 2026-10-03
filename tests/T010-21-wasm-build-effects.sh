#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — forwarded declarative build writes
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T010-21 declarative build effect publication"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/project"
mkdir -p "$project"
printf 'bytes\000\377\n' >"$project/input.bin"
cat >"$project/Makefile.kmk" <<'KMK'
./nested/out.bin : ./input.bin
	@(out "before\n")
	@(write "./side.txt" "side")
	@(out "after\n")
	@(yield (read ./input.bin))
	@(yield "tail")
default : ./nested/out.bin
	cat @<
./empty :
	@(yield "")
./blocked :
	@(yield "failure")
KMK
cat "$project/input.bin" >"$project/expected.bin"
printf tail >>"$project/expected.bin"
for backend in native wasm; do
	test-step "$backend publishes bytes before dependent recipes"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	rm -rf "$project/nested" "$project/side.txt"
	"${runner[@]}" -C "$project" default >"$project/$backend.out" 2>"$project/error"
	if cmp -s "$project/nested/out.bin" "$project/expected.bin" && [ "$(cat "$project/side.txt")" = side ]; then test-ok "$backend yield and write reach real filesystem"; else test-fail "$backend missing declarative output"; fi
	printf 'before\nafter\n' >"$project/expected.out"
	cat "$project/expected.bin" >>"$project/expected.out"
	if cmp -s "$project/$backend.out" "$project/expected.out"; then test-ok "$backend effect and dependent output order"; else test-fail "$backend reordered or repeated effects"; fi
	rm -rf "$project/nested" "$project/side.txt"
	"${runner[@]}" do cat -C "$project" -f Makefile.kmk ./nested/out.bin >"$project/$backend.cat" 2>"$project/error"
	if cmp -s "$project/nested/out.bin" "$project/expected.bin"; then test-ok "$backend cat builds its own artifact"; else test-fail "$backend cat relies on another backend's output"; fi
	rm -f "$project/empty"
	"${runner[@]}" -C "$project" ./empty >"$project/value" 2>"$project/error"
	if [ -f "$project/empty" ] && [ ! -s "$project/empty" ]; then test-ok "$backend publishes zero-byte yield"; else test-fail "$backend empty yield missing"; fi
	mkdir -p "$project/blocked"
	status=0
	"${runner[@]}" -C "$project" ./blocked >"$project/value" 2>"$project/error" || status=$?
	if [ "$status" = 1 ] && grep -q FS_ERR "$project/error"; then test-ok "$backend host publication failure fails target"; else test-fail "$backend write error was ignored"; fi
	if find "$project" -name '.kame-write-*' -print -quit | grep -q .; then test-fail "$backend leaked atomic staging"; else test-ok "$backend cleans staging after failed publication"; fi
done
if cmp -s "$project/native.out" "$project/wasm.out" && cmp -s "$project/native.cat" "$project/wasm.cat"; then test-ok "isolated native/WASM bytes match"; else test-fail "isolated build effect parity"; fi
test-end
