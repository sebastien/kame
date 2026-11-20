#!/usr/bin/env bash
# Spec: docs/spec/055-unified-kame-kash-language.md — composed inputs
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T004-19 rule header composition"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
project="$TMPDIR/project"
mkdir -p "$project/src" "$project/src dir"
mkdir -p "$project/repos/alpha" "$project/repos/beta"
printf a >"$project/src/a.go"
printf b >"$project/src/b.c"
printf c >"$project/src/extra.h"
printf literal >"$project/literal"
printf spaced >"$project/src dir/space.txt"
cat >"$project/Makefile.km" <<'KAME'
root = "./src"
left = (wildcard ./src/*.go)
rule ./out : ${left} ./literal ${wildcard ./src/*.c} ${root}/extra.h
	cat $...inputs > $outputs.0
task quoted : "./src dir/space.txt" "${root}/a.go"
REPOS = ./repos/*
task repositories : ${(dirs REPOS)}
KAME
for backend in native wasm; do
	test-step "$backend mixes expression values, literal paths and interpolated paths"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	"${runner[@]}" do plan --json -C "$project" -f Makefile.km ./out >"$project/$backend.plan" 2>"$project/error"
	if jq -e '[.resources[] | select(.key.kind == "file" and (.roles | index("configuration") | not) and (.roles | index("input"))) | .display] == ["./src/a.go","./literal","./src/b.c","./src/extra.h"]' "$project/$backend.plan" >/dev/null; then test-ok "$backend plan resolves every input expression"; else test-fail "$backend plan inputs"; fi
	"${runner[@]}" do span --json --expand -C "$project" -f Makefile.km ./out >"$project/$backend.inputs" 2>"$project/error"
	if grep -q './src/a.go' "$project/$backend.inputs" && grep -q './src/b.c' "$project/$backend.inputs" && grep -q './src/extra.h' "$project/$backend.inputs"; then test-ok "$backend expands each header expression"; else test-fail "$backend missing expression input"; fi
	"${runner[@]}" do plan --json -C "$project" -f Makefile.km quoted >"$project/quoted.plan" 2>"$project/error"
	if jq -e '[.resources[] | select(.key.kind == "file" and (.roles | index("configuration") | not) and (.roles | index("input"))) | .display] == ["./src dir/space.txt","./src/a.go"]' "$project/quoted.plan" >/dev/null; then test-ok "$backend quoted input tokens render without quotes"; else test-fail "$backend quoted paths"; fi
	"${runner[@]}" do plan --json -C "$project" -f Makefile.km repositories >"$project/dirs.plan" 2>"$project/error"
	if jq -e '[.resources[] | select(.key.kind == "file" and (.roles | index("configuration") | not) and (.roles | index("input"))) | .display] | sort == ["./repos/alpha","./repos/beta"]' "$project/dirs.plan" >/dev/null; then
		test-ok "$backend substitutes a computed directory list"
	else
		test-fail "$backend computed directory list"
	fi
done
if cmp -s "$project/native.plan" "$project/wasm.plan" && cmp -s "$project/native.inputs" "$project/wasm.inputs"; then test-ok "native/WASM composed header inspection bytes match"; else test-fail "composed header parity"; fi

# Compare inspection on the same unbuilt tree before either backend publishes.
test-step "both backends execute flattened inputs in order"
for backend in native wasm; do
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	rm -f "$project/out"
	"${runner[@]}" -C "$project" ./out >"$project/value" 2>"$project/error"
	if [ "$(cat "$project/out")" = aliteralbc ]; then test-ok "$backend executes flattened inputs in order"; else test-fail "$backend input order"; fi
done
test-end
