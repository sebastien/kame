#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — computed record lookup
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T007-10 computed record lookup"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/lookup"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'KMK'
mode = "release"
flags = [debug: "-O0" release: "-O2"]
selected = (get flags mode)
inputs = [debug: [./debug.txt] release: [./release.txt]]
default : @(get inputs mode)
	@(out selected)
KMK
printf debug >"$project/debug.txt"
printf release >"$project/release.txt"
for backend in native wasm; do
 test-step "$backend computed lookup and defaults"
 if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
 for sample in '(get [one: 1 two: 2] (cat "t" "wo"))|2' '(get [one: 1] "missing")|:nil' '(get [one: 1] "missing" :false)|:false' '(get [one: [a: 3]] "one")|[a: 3]' '(apply (get [used: ([x] (cat "hello " x)) unused: ([x] x)] "used" ([x] x)) ["Ada"])|"hello Ada"'; do
  "${runner[@]}" do run --lang expr -c "${sample%|*}" >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = "${sample##*|}" ]; then test-ok "$backend ${sample%|*}"; else test-fail "$backend record lookup"; fi
 done
 status=0
 "${runner[@]}" do run --lang expr -c '(get [one: 1] 1)' >"$project/value" 2>"$project/error" || status=$?
 if [ "$status" = 1 ] && grep -q EXPR_INVALID "$project/error"; then test-ok "$backend invalid key contract"; else test-fail "$backend invalid key accepted"; fi
 "${runner[@]}" -C "$project" default >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = -O2 ]; then test-ok "$backend computed configuration reaches recipe"; else test-fail "$backend computed definition"; fi
  "${runner[@]}" do plan --json -C "$project" default >"$project/plan" 2>"$project/error"
 if grep -q release.txt "$project/plan" && ! grep -q debug.txt "$project/plan"; then test-ok "$backend computed input reaches planning"; else test-fail "$backend computed input plan"; fi
done
test-end
