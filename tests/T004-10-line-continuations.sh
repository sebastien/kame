#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — continued declarations retain authored spans
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T004-10 line continuations"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/project"
mkdir -p "$project"
printf a >"$project/a"
printf b >"$project/b"
cat >"$project/Makefile.kmk" <<'KMK'
# A comment ending with backslash stays one physical line \
words = one \
  two \
  three
paths = (list ./a \
  ./b)
dir = \
  "./"
raw = """first
second"""
./out : @(paths) \
  @(dir)a
	cat @<* \
	 > @>
default : ./out
	@(out (join words ","))
KMK
for backend in native wasm; do
	test-step "$backend continued definitions, expressions and headers"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	rm -f "$project/out"
	"${runner[@]}" -C "$project" default >"$project/$backend.value" 2>"$project/error"
	if [ "$(cat "$project/$backend.value")" = one,two,three ] && [ "$(cat "$project/out")" = aba ]; then test-ok "$backend continuation values and shell-owned recipe backslashes"; else test-fail "$backend continuation execution"; fi
	"${runner[@]}" do plan -C "$project" -f Makefile.kmk ./out >"$project/$backend.plan" 2>"$project/error"
	if jq -e '.inputs == ["./a","./b","./a"]' "$project/$backend.plan" >/dev/null; then test-ok "$backend continued header plan"; else test-fail "$backend header split"; fi
	"${runner[@]}" do cat -C "$project" -f Makefile.kmk raw >"$project/raw" 2>"$project/error"
	if [ "$(cat "$project/raw")" = '"first\nsecond"' ]; then test-ok "$backend existing multiline verbatim definition"; else test-fail "$backend multiline raw definition"; fi
	"${runner[@]}" do fmt "$project/Makefile.kmk" >"$project/$backend.fmt" 2>"$project/error"
	"${runner[@]}" do fmt --lang script "$project/$backend.fmt" >"$project/$backend.fmt.again" 2>"$project/error"
	if cmp -s "$project/$backend.fmt" "$project/$backend.fmt.again"; then test-ok "$backend continued format is idempotent"; else test-fail "$backend continuation format"; fi
done
if cmp -s "$project/native.plan" "$project/wasm.plan" && cmp -s "$project/native.fmt" "$project/wasm.fmt"; then test-ok "native/WASM continuation plan and format parity"; else test-fail "continuation parity"; fi
test-end
