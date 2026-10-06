#!/usr/bin/env bash
# Spec: docs/spec/006-runtime.md — newer input selector
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T006-07 newer input selection"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
for backend in native wasm; do
 project="$TMPDIR/$backend"
 mkdir -p "$project"
 if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
 printf a > "$project/a"
 printf b > "$project/b"
 printf ordered > "$project/order"
 cat > "$project/Makefile.kmk" <<'KMK'
(newer UNUSED) = @<?
./out : ./a ./b ./a | ./order ; env "MODE=stable"
	printf '%s\n' @(newer :nil) > selected; cat @<* > @>; printf x >> runs
./multi-a ./multi-b : ./a ./b
	printf '%s\n' @<? > multi-selected; touch ./multi-a ./multi-b
./empty : | ./order
	printf '%s' @(count @<?) > @>
./force : ./a ./b
	printf '%s' @(count @<?) > @>
./capture/{stem}.out : ./a
	printf '%s\n' @(stem) @(dirname @>) @(dirname @<) > @>
bad-task :
	printf '%s' @<?
KMK
 test-step "$backend selects unique normal file inputs for missing outputs"
 "${runner[@]}" -C "$project" ./out > "$project/log" 2> "$project/err"
 printf './a\n./b\n' > "$project/expected"
 if cmp -s "$project/selected" "$project/expected"; then test-ok "$backend first build selects both unique inputs"; else test-fail "$backend missing-output selection"; fi
 test-step "$backend keeps scoped file fresh after newer subset changes"
 "${runner[@]}" -C "$project" ./out > "$project/log" 2> "$project/err"
 if [ "$(cat "$project/runs")" = x ]; then test-ok "$backend selector transition preserves scoped freshness"; else test-fail "$backend selector caused redundant rebuild"; fi
  test-step "$backend selects only inputs strictly newer than output"
  printf changed > "$project/b"
 python3 - "$project" <<'PY'
import os, sys
root = sys.argv[1]
for name, stamp in [('a', 1000000000), ('out', 2000000000), ('b', 3000000000), ('order', 4000000000)]:
    os.utime(os.path.join(root, name), ns=(stamp, stamp))
PY
 "${runner[@]}" -C "$project" ./out > "$project/log" 2> "$project/err"
 printf './b\n' > "$project/expected"
 if cmp -s "$project/selected" "$project/expected" && [ "$(cat "$project/runs")" = xx ]; then test-ok "$backend newer subset excludes older and ordering inputs"; else test-fail "$backend newer subset"; fi
 test-step "$backend uses oldest output and all inputs when any output is absent"
 "${runner[@]}" -C "$project" ./multi-a > "$project/log" 2> "$project/err"
 rm "$project/multi-b"
 "${runner[@]}" -C "$project" ./multi-a > "$project/log" 2> "$project/err"
 printf './a\n./b\n' > "$project/expected"
 if cmp -s "$project/multi-selected" "$project/expected" && [ -f "$project/multi-b" ]; then test-ok "$backend missing sibling selects all inputs"; else test-fail "$backend multiple-output selection"; fi
  test-step "$backend compares against the oldest existing output"
  printf changed > "$project/a"
 python3 - "$project" <<'PYTIMES'
import os, sys
root = sys.argv[1]
for name, stamp in [('a', 3000000000), ('b', 1000000000), ('multi-a', 4000000000), ('multi-b', 2000000000)]:
    os.utime(os.path.join(root, name), ns=(stamp, stamp))
PYTIMES
 "${runner[@]}" -C "$project" ./multi-a > "$project/log" 2> "$project/err"
 printf './a\n' > "$project/expected"
 if cmp -s "$project/multi-selected" "$project/expected"; then test-ok "$backend oldest sibling determines newer inputs"; else test-fail "$backend oldest-output comparison"; fi
 test-step "$backend excludes equal timestamps even when forced"
 "${runner[@]}" -C "$project" ./force > "$project/log" 2> "$project/err"
 python3 - "$project" <<'PYTIMES'
import os, sys
root = sys.argv[1]
for name in ['a', 'b', 'force']:
    os.utime(os.path.join(root, name), ns=(5000000000, 5000000000))
PYTIMES
 "${runner[@]}" -C "$project" --force ./force > "$project/log" 2> "$project/err"
 if [ "$(cat "$project/force")" = 0 ]; then test-ok "$backend equal inputs remain absent during forced execution"; else test-fail "$backend force invented newer inputs"; fi
 test-step "$backend carries force through mixed runner sessions"
 printf session > "$project/force"
 "${runner[@]}" do run -C "$project" --allow-run --allow-read=. --allow-write=. --force -c 'ignored = :nil' Makefile.kmk ./force > "$project/log" 2> "$project/err"
 if [ "$(cat "$project/force")" = 0 ]; then test-ok "$backend mixed session forwards forced execution"; else test-fail "$backend mixed session lost force"; fi
 test-step "$backend handles empty normal inputs"
 "${runner[@]}" -C "$project" ./empty > "$project/log" 2> "$project/err"
 if [ "$(cat "$project/empty")" = 0 ]; then test-ok "$backend ordering-only file has an empty newer list"; else test-fail "$backend empty newer list"; fi
 test-step "$backend supports stem captures and directory expressions"
 "${runner[@]}" -C "$project" ./capture/demo.out > "$project/log" 2> "$project/err"
 printf 'demo\ncapture\n.\n' > "$project/expected"
 if cmp -s "$project/capture/demo.out" "$project/expected"; then test-ok "$backend capture and directory equivalents"; else test-fail "$backend capture/directory mapping"; fi
 test-step "$backend rejects newer selection outside file recipes"
 if "${runner[@]}" -C "$project" bad-task > "$project/log" 2> "$project/err"; then test-fail "$backend task accepted newer selector"; elif rg -q SEL_NO_CONTEXT "$project/err"; then test-ok "$backend task newer selector rejected"; else test-fail "$backend task newer diagnostic"; fi
 test-step "$backend rejects newer selection while resolving rule inputs"
 printf './invalid : @(@<?)\n\ttouch forbidden\n' > "$project/invalid.kmk"
 if "${runner[@]}" -C "$project" -f invalid.kmk ./invalid > "$project/log" 2> "$project/err"; then test-fail "$backend input phase accepted newer selector"; elif rg -q PHASE_INVALID "$project/err" && [ ! -e "$project/forbidden" ]; then test-ok "$backend input phase rejects before effects"; else test-fail "$backend input-phase diagnostic"; fi
 test-step "$backend preserves newer syntax during formatting"
 "${runner[@]}" do fmt --lang script "$project/Makefile.kmk" > "$project/fmt" 2> "$project/err"
 if rg -Fq '@<?' "$project/fmt"; then test-ok "$backend formatter preserves selector"; else test-fail "$backend selector formatting"; fi
done
test-end
