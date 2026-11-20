#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — continued declarations retain authored spans
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T004-10 line continuations"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
project="$TMPDIR/project"
mkdir -p "$project"
printf a >"$project/a"
printf b >"$project/b"
cat >"$project/Makefile.km" <<'KAME'
# A comment ending with backslash stays one physical line \
words = ["one", \
  "two", \
  "three"]
paths = (list ./a \
  ./b)
extra = \
  ./a
raw = """first
second"""
rule ./out : ${paths} \
  ${extra}
	cat $...inputs \
	 > $outputs.0
task default : ./out
	(out! (join "," words))
KAME
for backend in native wasm; do
	test-step "$backend continued definitions, expressions and headers"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
	rm -f "$project/out"
	"${runner[@]}" -C "$project" default >"$project/$backend.value" 2>"$project/error"
	if [ "$(cat "$project/$backend.value")" = one,two,three ] && [ "$(cat "$project/out")" = aba ]; then test-ok "$backend continuation values and shell-owned recipe backslashes"; else test-fail "$backend continuation execution"; fi
	"${runner[@]}" do plan --json -C "$project" -f Makefile.km ./out >"$project/$backend.plan" 2>"$project/error"
	if jq -e '[.resources[] | select(.key.kind == "file" and (.roles | index("configuration") | not) and (.roles | index("input"))) | .display] == ["./a","./b"]' "$project/$backend.plan" >/dev/null; then test-ok "$backend continued header plan deduplicates resources"; else test-fail "$backend header split"; fi
	"${runner[@]}" do cat -C "$project" -f Makefile.km raw >"$project/raw" 2>"$project/error"
	if [ "$(cat "$project/raw")" = "$(printf 'first\nsecond')" ]; then test-ok "$backend existing multiline verbatim definition"; else test-fail "$backend multiline raw definition"; fi
	"${runner[@]}" do fmt "$project/Makefile.km" >"$project/$backend.fmt" 2>"$project/error"
	"${runner[@]}" do fmt --lang km "$project/$backend.fmt" >"$project/$backend.fmt.again" 2>"$project/error"
	if cmp -s "$project/$backend.fmt" "$project/$backend.fmt.again"; then test-ok "$backend continued format is idempotent"; else test-fail "$backend continuation format"; fi
done
if cmp -s "$project/native.plan" "$project/wasm.plan" && cmp -s "$project/native.fmt" "$project/wasm.fmt"; then test-ok "native/WASM continuation plan and format parity"; else test-fail "continuation parity"; fi
test-end
