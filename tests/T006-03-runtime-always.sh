#!/usr/bin/env bash
# Spec: docs/spec/006-runtime.md — persistent always file rules
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T006-03 always file rules"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/project"
mkdir -p "$project"
printf source >"$project/input"
cat >"$project/Makefile.kmk" <<'KMK'
always ./output : ./input
	cp @< @>; printf x >> ./runs
left : ./output
right : ./output
root : left right
always ./one ./two : ./input
	cp @< ./one; cp @< ./two; printf m >> ./multi-runs
always ./capture/{name}.txt : ./input
	printf %s @(name) > @>; printf c >> ./capture-runs
always ./yielded : ./input
	@(yield "constant")
always ./missing : ./input
	true
always :
	printf ordinary
KMK
for backend in native wasm; do
 if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
 test-step "$backend reruns always files and deduplicates shared prerequisites"
 # Distinct public outputs and counters keep native artifacts from masking WASM.
 if [ -e "$project/runs" ]; then mv "$project/runs" "$project/native-runs.saved"; fi
 "${runner[@]}" -C "$project" root >"$project/out" 2>"$project/err"
 "${runner[@]}" -C "$project" root >"$project/out" 2>"$project/err"
 "${runner[@]}" -C "$project" ./output >"$project/out" 2>"$project/err"
 if [ "$(cat "$project/runs")" = xxx ] && cmp -s "$project/input" "$project/output"; then test-ok "$backend always output runs once per requested root"; else test-fail "$backend always file run count"; fi
 test-step "$backend keeps multiple outputs and cat artifact results"
 if [ -e "$project/multi-runs" ]; then mv "$project/multi-runs" "$project/native-multi-runs.saved"; fi
 "${runner[@]}" -C "$project" ./one >"$project/out" 2>"$project/err"
 "${runner[@]}" -C "$project" ./two >"$project/out" 2>"$project/err"
 "${runner[@]}" do cat -C "$project" ./one >"$project/cat" 2>"$project/err"
 if [ "$(cat "$project/multi-runs")" = mmm ] && cmp -s "$project/input" "$project/one" && cmp -s "$project/input" "$project/two" && cmp -s "$project/input" "$project/cat"; then test-ok "$backend multiple outputs remain artifacts"; else test-fail "$backend multi-output always rule"; fi
 test-step "$backend preserves capture binding in always outputs"
 if [ -e "$project/capture-runs" ]; then mv "$project/capture-runs" "$project/native-capture-runs.saved"; fi
 "${runner[@]}" -C "$project" ./capture/alpha.txt >"$project/out" 2>"$project/err"
 "${runner[@]}" -C "$project" ./capture/alpha.txt >"$project/out" 2>"$project/err"
 "${runner[@]}" -C "$project" ./capture/beta.txt >"$project/out" 2>"$project/err"
 if [ "$(cat "$project/capture-runs")" = ccc ] && [ "$(cat "$project/capture/alpha.txt")" = alpha ] && [ "$(cat "$project/capture/beta.txt")" = beta ]; then test-ok "$backend capture instances rerun with their bound names"; else test-fail "$backend always capture semantics"; fi
 test-step "$backend republishes content-equal always yields"
 "${runner[@]}" -C "$project" ./yielded >"$project/out" 2>"$project/err"
 python3 - "$project/yielded" <<'PYTIME'
import os, sys
os.utime(sys.argv[1], ns=(1000000000, 1000000000))
PYTIME
 "${runner[@]}" -C "$project" ./yielded >"$project/out" 2>"$project/err"
 if python3 - "$project/yielded" <<'PYTIME'
import os, sys
assert os.stat(sys.argv[1]).st_mtime_ns != 1000000000
assert open(sys.argv[1]).read() == 'constant'
PYTIME
 then test-ok "$backend always yields bypass content freshness"; else test-fail "$backend equal always yield was skipped"; fi
 test-step "$backend preserves planning and authored syntax"
 "${runner[@]}" do plan -C "$project" --json ./output >"$project/plan" 2>"$project/err"
  "${runner[@]}" do parse --json --lang script "$project/Makefile.kmk" >"$project/ast" 2>"$project/err"
 "${runner[@]}" do fmt --lang script "$project/Makefile.kmk" >"$project/formatted" 2>"$project/err"
 if jq -e '.producers[0].always == true and [.resources[] | select(.roles | index("artifact")) | .display] == ["./output"]' "$project/plan" >/dev/null && jq -e '.. | objects | select(.always? == true)' "$project/ast" >/dev/null && rg -q '^always ./one ./two :' "$project/formatted"; then test-ok "$backend plan, AST and formatter preserve always"; else test-fail "$backend always inspection"; fi
 test-step "$backend still verifies output publication"
 if "${runner[@]}" --json -C "$project" ./missing >"$project/out" 2>"$project/err"; then test-fail "$backend accepted missing output"; elif rg -q OUTPUT_MISSING "$project/out"; then test-ok "$backend missing output is diagnosed"; else test-fail "$backend missing-output diagnostic"; fi
 test-step "$backend preserves the ordinary target named always"
 "${runner[@]}" -C "$project" always >"$project/out" 2>"$project/err"
 if [ "$(cat "$project/out")" = ordinary ]; then test-ok "$backend ordinary always target works"; else test-fail "$backend reserved name regression"; fi
 test-step "$backend rejects always on named tasks before effects"
 printf 'always named :\n\ttouch forbidden\n' >"$project/bad.kmk"
 if "${runner[@]}" -C "$project" -f bad.kmk named >"$project/out" 2>"$project/err"; then test-fail "$backend accepted invalid always task"; elif rg -q PARSE_ERR "$project/err" && [ ! -e "$project/forbidden" ]; then test-ok "$backend invalid always declaration has no effects"; else test-fail "$backend always declaration preflight"; fi
done
test-end
