#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — composed inputs and alternative rule separator
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T004-09 rule header composition"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/project"
mkdir -p "$project/src" "$project/src dir"
printf a >"$project/src/a.go"
printf b >"$project/src/b.c"
printf c >"$project/src/extra.h"
printf literal >"$project/literal"
printf spaced >"$project/src dir/space.txt"
cat >"$project/Makefile.kmk" <<'KMK'
root = "./src"
left = (wildcard ./src/*.go)
./out <- @(left) ./literal @(wildcard ./src/*.c) @(root)/extra.h
	cat @<* > @>
quoted : "./src dir/space.txt" "@(root)/a.go"
KMK
for backend in native wasm; do
	test-step "$backend mixes expression values, literal paths and interpolated paths"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	"${runner[@]}" do plan -C "$project" -f Makefile.kmk ./out >"$project/$backend.plan" 2>"$project/error"
	if jq -e '.inputs == ["./literal","./src/extra.h"]' "$project/$backend.plan" >/dev/null; then test-ok "$backend plan renders pure path token"; else test-fail "$backend plan inputs"; fi
	"${runner[@]}" do span --expand -C "$project" -f Makefile.kmk ./out >"$project/$backend.inputs" 2>"$project/error"
	if grep -q './src/a.go' "$project/$backend.inputs" && grep -q './src/b.c' "$project/$backend.inputs" && grep -q './src/extra.h' "$project/$backend.inputs"; then test-ok "$backend expands each header expression"; else test-fail "$backend missing expression input"; fi
	rm -f "$project/out"
	"${runner[@]}" -C "$project" ./out >"$project/value" 2>"$project/error"
	if [ "$(cat "$project/out")" = aliteralbc ]; then test-ok "$backend executes flattened inputs in order"; else test-fail "$backend input order"; fi
	"${runner[@]}" do plan -C "$project" -f Makefile.kmk quoted >"$project/quoted.plan" 2>"$project/error"
	if jq -e '.inputs == ["./src dir/space.txt","./src/a.go"]' "$project/quoted.plan" >/dev/null; then test-ok "$backend quoted input tokens render without quotes"; else test-fail "$backend quoted paths"; fi
done
if cmp -s "$project/native.plan" "$project/wasm.plan" && cmp -s "$project/native.inputs" "$project/wasm.inputs"; then test-ok "native/WASM composed header inspection bytes match"; else test-fail "composed header parity"; fi
test-end
