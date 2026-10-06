#!/usr/bin/env bash
# Spec: docs/spec/006-runtime.md — order-only prerequisites
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T006-04 order-only prerequisites"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
for backend in native wasm; do
 project="$TMPDIR/$backend"
 mkdir -p "$project"
 if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
 printf source > "$project/input"
 printf ordered > "$project/ordered-source"
 cat > "$project/Makefile.kmk" <<'KMK'
./out : ./input | prepare ./ordered
	cat @<* > @>; printf x >> runs
prepare :
	printf p >> prepares
./ordered : ./ordered-source
	cat @< > @>
task cached : ./input | prepare ./ordered
	printf c >> cache-runs
./read-out : ./input | ./ordered
	printf '%s' @(text (read ./ordered)) > @>; printf r >> read-runs
task read-cached : ./input | ./ordered
	printf %s @(text (read ./ordered)) > cached-read; printf q >> cached-read-runs
./duplicate : ./ordered | ./ordered
	cat @<* > @>; printf d >> duplicate-runs
broken :
	false
./blocked : ./input | broken
	touch forbidden
./computed : ./input | @([./ordered])
	cat @<* > @>; printf e >> computed-runs
KMK
 test-step "$backend schedules order-only producers and omits them from selectors"
 "${runner[@]}" -C "$project" ./out > "$project/log" 2> "$project/err"
 "${runner[@]}" -C "$project" ./out > "$project/log" 2> "$project/err"
 if cmp -s "$project/input" "$project/out" && [ "$(cat "$project/runs")" = x ] && [ "$(cat "$project/prepares")" = pp ] && [ -f "$project/ordered" ]; then test-ok "$backend order-only work runs before a fresh artifact"; else test-fail "$backend order-only scheduling or selector"; fi
 test-step "$backend ignores newer order-only timestamps"
 printf changed > "$project/ordered-source"
 "${runner[@]}" -C "$project" ./out > "$project/log" 2> "$project/err"
 if [ "$(cat "$project/runs")" = x ]; then test-ok "$backend order-only changes leave file fresh"; else test-fail "$backend order-only timestamp rebuilt artifact"; fi
 test-step "$backend excludes order-only values and bare tasks from cache identity"
 "${runner[@]}" -C "$project" cached > "$project/log" 2> "$project/err"
 printf another > "$project/ordered-source"
 "${runner[@]}" -C "$project" cached > "$project/log" 2> "$project/err"
 if [ "$(cat "$project/cache-runs")" = c ]; then test-ok "$backend order-only tasks and content preserve cache"; else test-fail "$backend order-only cache identity"; fi
 test-step "$backend upgrades explicit reads to content dependencies"
 "${runner[@]}" -C "$project" ./read-out > "$project/log" 2> "$project/err"
	printf consumed > "$project/ordered-source"
 "${runner[@]}" -C "$project" ./read-out > "$project/log" 2> "$project/err"
 if [ "$(cat "$project/read-runs")" = rr ] && [ "$(cat "$project/read-out")" = consumed ]; then test-ok "$backend explicit read dominates ordering"; else test-fail "$backend order-only read upgrade"; fi
 test-step "$backend upgrades cached explicit reads to content dependencies"
 "${runner[@]}" -C "$project" read-cached > "$project/log" 2> "$project/err"
 printf cached-consumed > "$project/ordered-source"
 "${runner[@]}" -C "$project" read-cached > "$project/log" 2> "$project/err"
 if [ "$(cat "$project/cached-read-runs")" = qq ] && [ "$(cat "$project/cached-read")" = cached-consumed ]; then test-ok "$backend cached read dominates ordering"; else test-fail "$backend cached order-only read upgrade"; fi
 test-step "$backend normal duplicates dominate order-only occurrences"
 "${runner[@]}" -C "$project" ./duplicate > "$project/log" 2> "$project/err"
 printf duplicate > "$project/ordered-source"
 "${runner[@]}" -C "$project" ./duplicate > "$project/log" 2> "$project/err"
  if [ "$(cat "$project/duplicate-runs")" = dd ] && [ "$(cat "$project/duplicate")" = duplicate ]; then test-ok "$backend normal duplicate remains content input"; else test-fail "$backend duplicate purpose"; fi
  test-step "$backend repairs generated input tampering before comparing consumed bytes"
  printf tampered > "$project/ordered"
  "${runner[@]}" -C "$project" ./duplicate > "$project/log" 2> "$project/err"
  if [ "$(cat "$project/ordered")" = duplicate ] && [ "$(cat "$project/duplicate-runs")" = dd ]; then test-ok "$backend repaired equal artifact preserves downstream reuse"; else test-fail "$backend generated tampering was consumed before repair"; fi
 test-step "$backend blocks on failed order-only prerequisites"
 if "${runner[@]}" -C "$project" ./blocked > "$project/log" 2> "$project/err"; then test-fail "$backend accepted failed ordering work"; elif [ ! -e "$project/forbidden" ]; then test-ok "$backend order-only failure blocks recipe"; else test-fail "$backend blocked recipe executed"; fi
 test-step "$backend resolves expression order-only prerequisites"
 "${runner[@]}" -C "$project" ./computed > "$project/log" 2> "$project/err"
 "${runner[@]}" -C "$project" ./computed > "$project/log" 2> "$project/err"
 if [ "$(cat "$project/computed-runs")" = e ] && cmp -s "$project/input" "$project/computed"; then test-ok "$backend computed order-only edges preserve freshness"; else test-fail "$backend computed order-only inputs"; fi
 test-step "$backend reports and formats ordering metadata"
 "${runner[@]}" do plan --json -C "$project" ./out > "$project/plan" 2> "$project/err"
 "${runner[@]}" do parse --lang script "$project/Makefile.kmk" > "$project/ast" 2> "$project/err"
 "${runner[@]}" do fmt --lang script "$project/Makefile.kmk" > "$project/fmt" 2> "$project/err"
 if jq -e '.orderOnlyInputs == ["prepare", "./ordered"]' "$project/plan" >/dev/null && jq -e '.. | objects | select(.orderOnly? == true)' "$project/ast" >/dev/null && rg -q '^./out : ./input \| prepare ./ordered$' "$project/fmt"; then test-ok "$backend ordering metadata preserved"; else test-fail "$backend ordering inspection"; fi
done
test-end
