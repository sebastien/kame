#!/usr/bin/env bash
# Spec: docs/spec/006-runtime.md — scoped recipe environments
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T006-06 scoped recipe environments"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
wait_watch_idle() {
  local events="$1" target="$2"
  for attempt in {1..100}; do
    if jq -e -s --arg target "$target" 'any(.[]; .type == "target-completed" and .target == $target) and any(.[]; .type == "watch-idle" and .cycle == 1 and .status == "success")' "$events" >/dev/null 2>&1; then return; fi
    sleep 0.05
  done
  kill -TERM "$watch_pid" 2>/dev/null || true
  wait "$watch_pid" 2>/dev/null || true
  cat "$events" >&2
  test-fatal "$backend watch did not complete its initial cycle for $target"
}
for backend in native wasm; do
 project="$TMPDIR/$backend"
 mkdir -p "$project"
  # Bash supplies different '_' and SHLVL values for foreground/background commands.
  # Keep the child environment equal when comparing a warm build with a watch.
  if [ "$backend" = native ]; then runner=(env _=kame-environment-test SHLVL=1 "$CLI_BIN"); else runner=(env _=kame-environment-test SHLVL=1 node "$CLI_ROOT/dist/kame.js"); fi
 cat > "$project/Makefile.kmk" <<'KMK'
root : child local ; env "MODE=first" "MODE=debug" "MESSAGE=spaces; equal=ok"
child :
	printf '%s|%s' "$MODE" "$MESSAGE" > child-log
local : ; env "MODE=release"
	printf '%s|%s' "$MODE" "$MESSAGE" > local-log
plain :
	printf '%s' "$MODE" > plain-log
cache-root : cached ; env "MODE=debug"
task cached :
	printf %s "$MODE" > cache-log; printf x >> cache-runs
other-cache-root : cached ; env "MODE=release"
equivalent-root : equivalent-a equivalent-b
equivalent-a : equivalent-shared ; env "A=one" "B=two"
equivalent-b : equivalent-shared ; env "B=two" "A=one"
equivalent-shared :
	printf '%s|%s' "$A" "$B" > equivalent-log; printf x >> equivalent-runs
conflict-root : conflict-a conflict-b
conflict-a : shared ; env "MODE=debug"
conflict-b : shared ; env "MODE=release"
shared :
	sleep 0.1; printf %s "$MODE" > shared-log
dynamic-root : ; env "MODE=dynamic"
	printf '%s' @(text (read ./dynamic-input)) > dynamic-log
./dynamic-input : ./input
	printf '%s' "$MODE" > @>
retry : ; env "MODE=retry"
	printf %s "$MODE" >> retry-log; if [ ! -e retry-marker ]; then touch retry-marker; exit 1; fi
./output : ./input ; env "MODE=debug"
	printf %s "$MODE" > @>; printf x >> file-runs
SETTING = "authored"
NEXT = (env "NEXT")
MODE = (env "MODE")
(mode) = (str MODE)
VALUE = (mode)
definition-root : definition-cached ; env "MODE=debug" "KAME_SETTING=debug"
release-definition-root : definition-cached ; env "MODE=release" "KAME_SETTING=release"
task definition-cached :
	printf '%s|%s' '@(VALUE)' '@(SETTING)' > definition-log; printf x >> definition-runs
definition-select : @(NEXT) ; env "NEXT=definition-selected" "MODE=chosen"
definition-selected :
	printf %s @(VALUE) > definition-selected-log
definition-denied : ; env "MODE=secret"
	printf %s @(VALUE); touch forbidden-definition
GENERATED = (text (read ./definition-input))
definition-generated-root : ; env "MODE=generated"
	printf %s @(GENERATED) > definition-generated-log
./definition-input :
	printf %s "$MODE" > @>
COLLECTED = (shell "printf %s \"$MODE\"; printf captured-error >&2")
COLLECTED_VALUE = (text (get COLLECTED "stdout"))
collected-root : collected-cached ; env "MODE=debug"
collected-release-root : collected-cached ; env "MODE=release"
task collected-cached :
	printf %s @(COLLECTED_VALUE) > collected-log; printf x >> collected-runs
kash-collected : ; [shell: kash env: [MODE: "kash"]]
	value = (shell "printf %s \"$MODE\"")
	printf %s \@(text (get value "stdout"))
collected-denied : ; [shell: kash env: [MODE: "secret"]]
	value = (shell "touch forbidden-collected")
	\@(out (get value "status"))
BAD = (shell "touch forbidden-phase")
definition-phase : @(BAD) ; env "MODE=debug"
CYCLE = (str CYCLE)
definition-cycle :
	printf %s @(CYCLE); touch forbidden-cycle
read-root : read-cached ; env "MODE=debug"
release-read-root : read-cached ; env "MODE=release"
task read-cached :
	printf %s @(env "MODE") > read-log; printf x >> read-runs
select-root : @(env "NEXT") ; env "NEXT=selected" "MODE=chosen"
selected :
	printf %s @(env "MODE") > select-log
ambient-selected :
	touch forbidden-selection
read-denied : ; env "MODE=secret"
	printf %s @(env "MODE"); touch forbidden-read
KMK
 test-step "$backend inherits values and applies local overrides"
 MODE=ambient "${runner[@]}" -C "$project" root plain > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/child-log")" = 'debug|spaces; equal=ok' ] && [ "$(cat "$project/local-log")" = 'release|spaces; equal=ok' ] && [ "$(cat "$project/plain-log")" = ambient ]; then test-ok "$backend inheritance, override and root isolation"; else test-fail "$backend scoped values"; fi
 test-step "$backend fingerprints inherited values in cached tasks"
 MODE=ambient "${runner[@]}" -C "$project" cache-root > "$project/out" 2> "$project/err"
 MODE=ambient "${runner[@]}" -C "$project" cache-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/cache-runs")" = x ] && [ "$(cat "$project/cache-log")" = debug ]; then test-ok "$backend unchanged inherited environment hits cache"; else test-fail "$backend inherited cache reuse"; fi
 MODE=ambient "${runner[@]}" -C "$project" other-cache-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/cache-runs")" = xx ] && [ "$(cat "$project/cache-log")" = release ]; then test-ok "$backend changed inherited environment invalidates cache"; else test-fail "$backend inherited cache identity"; fi
 test-step "$backend scopes expression environment reads and cache identity"
 MODE=ambient "${runner[@]}" --allow-run --allow-env=MODE -C "$project" -f Makefile.kmk read-root > "$project/out" 2> "$project/err"
 MODE=ambient "${runner[@]}" --allow-run --allow-env=MODE -C "$project" -f Makefile.kmk read-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/read-log")" = debug ] && [ "$(cat "$project/read-runs")" = x ]; then test-ok "$backend expression reads inherit and reuse the scoped cache"; else test-fail "$backend expression environment or cache reuse"; fi
 MODE=ambient "${runner[@]}" --allow-run --allow-env=MODE -C "$project" -f Makefile.kmk release-read-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/read-log")" = release ] && [ "$(cat "$project/read-runs")" = xx ]; then test-ok "$backend scoped expression cache invalidates on changed values"; else test-fail "$backend expression cache leaked earlier scoped values"; fi
 test-step "$backend resolves dynamic prerequisites using scoped expression reads"
 NEXT=ambient-selected MODE=ambient "${runner[@]}" --allow-run --allow-env=MODE --allow-env=NEXT -C "$project" -f Makefile.kmk select-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/select-log")" = chosen ] && [ ! -e "$project/forbidden-selection" ]; then test-ok "$backend dynamic prerequisite selection uses target environment"; else test-fail "$backend dynamic prerequisite used ambient environment"; fi
 test-step "$backend preserves grants for scoped expression reads"
 if MODE=ambient "${runner[@]}" -C "$project" read-denied > "$project/out" 2> "$project/err"; then test-fail "$backend scoped expression bypassed environment grants"; elif rg -q CAP_DENIED "$project/err" && [ ! -e "$project/forbidden-read" ]; then test-ok "$backend denied scoped reads have no recipe effects"; else test-fail "$backend scoped expression grant diagnostic"; fi
 test-step "$backend isolates lazy definitions, functions and KAME_NAME configuration"
 KAME_SETTING=ambient MODE=ambient "${runner[@]}" --allow-run --allow-env=MODE -C "$project" -f Makefile.kmk definition-root > "$project/out" 2> "$project/err"
 KAME_SETTING=ambient MODE=ambient "${runner[@]}" --allow-run --allow-env=MODE -C "$project" -f Makefile.kmk definition-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/definition-log")" = 'debug|debug' ] && [ "$(cat "$project/definition-runs")" = x ]; then test-ok "$backend scoped definitions reuse equivalent snapshots"; else test-fail "$backend scoped definition cache or value"; fi
 KAME_SETTING=ambient MODE=ambient "${runner[@]}" --allow-run --allow-env=MODE -C "$project" -f Makefile.kmk release-definition-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/definition-log")" = 'release|release' ] && [ "$(cat "$project/definition-runs")" = xx ]; then test-ok "$backend scoped definitions invalidate changed snapshots"; else test-fail "$backend scoped definition changed value"; fi
 "${runner[@]}" --allow-run --allow-env=MODE --define 'SETTING=@(missing)=literal' -C "$project" -f Makefile.kmk definition-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/definition-log")" = 'debug|@(missing)=literal' ]; then test-ok "$backend explicit literal definitions win over scoped KAME_NAME"; else test-fail "$backend scoped definition precedence"; fi
 NEXT=ambient-selected MODE=ambient "${runner[@]}" --allow-run --allow-env=NEXT --allow-env=MODE -C "$project" -f Makefile.kmk definition-select > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/definition-selected-log")" = chosen ]; then test-ok "$backend dynamic inputs traverse scoped definitions"; else test-fail "$backend dynamic definition context"; fi
 if "${runner[@]}" -C "$project" definition-denied > "$project/out" 2> "$project/err"; then test-fail "$backend scoped definitions bypassed grants"; elif rg -q CAP_DENIED "$project/err" && [ ! -e "$project/forbidden-definition" ]; then test-ok "$backend scoped definitions retain grants before effects"; else test-fail "$backend definition denial"; fi
 "${runner[@]}" -C "$project" definition-generated-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/definition-generated-log")" = generated ]; then test-ok "$backend produced files reached through definitions inherit environment"; else test-fail "$backend generated definition prerequisite context"; fi
 if "${runner[@]}" --allow-run -C "$project" -f Makefile.kmk definition-phase > "$project/out" 2> "$project/err"; then test-fail "$backend launched definition during input resolution"; elif rg -q PHASE_INVALID "$project/err" && [ ! -e "$project/forbidden-phase" ]; then test-ok "$backend scoped definitions keep dynamic inputs read-only"; else test-fail "$backend scoped definition phase"; fi
 if "${runner[@]}" -C "$project" definition-cycle > "$project/out" 2> "$project/err"; then test-fail "$backend accepted scoped definition cycle"; elif rg -q DEP_CYCLE "$project/err" && [ ! -e "$project/forbidden-cycle" ]; then test-ok "$backend scoped definition cycles fail without effects"; else test-fail "$backend scoped cycle diagnostic"; fi
 test-step "$backend forwards target environments into collected shell requests"
 MODE=ambient "${runner[@]}" --allow-run -C "$project" -f Makefile.kmk collected-root > "$project/out" 2> "$project/err"
 MODE=ambient "${runner[@]}" --allow-run -C "$project" -f Makefile.kmk collected-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/collected-log")" = debug ] && [ "$(cat "$project/collected-runs")" = x ]; then test-ok "$backend collected shell inherits target snapshot and cache identity"; else test-fail "$backend collected shell scoped cache"; fi
 MODE=ambient "${runner[@]}" --allow-run -C "$project" -f Makefile.kmk collected-release-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/collected-log")" = release ] && [ "$(cat "$project/collected-runs")" = xx ]; then test-ok "$backend collected shell changes with inherited environment"; else test-fail "$backend collected shell changed context"; fi
 MODE=ambient "${runner[@]}" --allow-run -C "$project" -f Makefile.kmk kash-collected > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/out")" = kash ]; then test-ok "$backend collected shell in structured recipes inherits metadata"; else test-fail "$backend structured collected shell snapshot"; fi
 if "${runner[@]}" --allow-env=MODE -C "$project" -f Makefile.kmk collected-denied > "$project/out" 2> "$project/err"; then test-fail "$backend collected shell bypassed run grant"; elif rg -q CAP_DENIED "$project/err" && [ ! -e "$project/forbidden-collected" ]; then test-ok "$backend collected shell retains run policy before effects"; else test-fail "$backend collected shell denial"; fi
 test-step "$backend shares equivalent environments regardless of assignment order"
 MODE=ambient "${runner[@]}" -C "$project" equivalent-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/equivalent-log")" = 'one|two' ] && [ "$(cat "$project/equivalent-runs")" = x ]; then test-ok "$backend equivalent environments share a prerequisite"; else test-fail "$backend assignment ordering changed environment identity"; fi
 test-step "$backend refuses conflicting active prerequisite contexts"
 if MODE=ambient "${runner[@]}" --json -C "$project" conflict-root > "$project/out" 2> "$project/err"; then test-fail "$backend accepted conflicting environments"; elif rg -q ENV_CONFLICT "$project/out"; then test-ok "$backend diagnoses shared environment conflict"; else test-fail "$backend environment conflict diagnostic"; fi
 test-step "$backend carries scoped values to file recipes"
 printf source > "$project/input"
 MODE=ambient "${runner[@]}" -C "$project" ./output > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/output")" = debug ]; then test-ok "$backend file recipe environment"; else test-fail "$backend file environment"; fi
 test-step "$backend skips unchanged scoped file recipes"
 MODE=ambient "${runner[@]}" -C "$project" ./output > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/file-runs")" = x ]; then test-ok "$backend unchanged scoped file is fresh"; else test-fail "$backend unchanged scoped file rebuilt"; fi
 test-step "$backend rebuilds when its scoped environment changes"
 sed -i 's/env "MODE=debug"$/env "MODE=release"/' "$project/Makefile.kmk"
 MODE=ambient "${runner[@]}" -C "$project" ./output > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/file-runs")" = xx ] && [ "$(cat "$project/output")" = release ]; then test-ok "$backend changed file environment rebuilds"; else test-fail "$backend changed file environment stayed fresh"; fi
 test-step "$backend rebuilds after removing the scoped environment"
 sed -i 's/\(\.\/output : \.\/input\) ; env "MODE=release"/\1/' "$project/Makefile.kmk"
 MODE=ambient "${runner[@]}" -C "$project" ./output > "$project/out" 2> "$project/err"
 MODE=ambient "${runner[@]}" -C "$project" ./output > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/file-runs")" = xxx ] && [ "$(cat "$project/output")" = ambient ]; then test-ok "$backend scope removal rebuilds once"; else test-fail "$backend scope removal freshness"; fi
 test-step "$backend rebuilds externally replaced scoped output"
 printf replaced > "$project/output"
 MODE=ambient "${runner[@]}" -C "$project" ./output > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/file-runs")" = xxxx ] && [ "$(cat "$project/output")" = ambient ]; then test-ok "$backend replaced output invalidates context stamp"; else test-fail "$backend replacement context freshness"; fi
 test-step "$backend rebuilds with a corrupt context record"
 if [ "$backend" = native ]; then record_dir="$project/.kame/cache/file-context"; else record_dir="$project/.kame/cache/host"; fi
  for record in "$record_dir"/*; do if [ -f "$record" ]; then printf corrupt > "$record"; fi; done
 MODE=ambient "${runner[@]}" -C "$project" ./output > "$project/out" 2> "$project/err"
  if [ "$(cat "$project/file-runs")" = xxxxx ]; then test-ok "$backend corrupt context stamp rebuilds"; else test-fail "$backend corrupt context accepted"; fi
  test-step "$backend validates file bytes rather than timestamps"
  signatures="$project/signatures"
  mkdir -p "$signatures"
  cat > "$signatures/Makefile.kmk" <<'KMK'
SHELL = /bin/sh
VALUE ?= baseline
CONTENT = (text (read "./definition-input"))
./output : ./input
	cp @< @>; printf x >> runs
./override : ./input
	printf %s @(VALUE) > @>
./discovered : ./input ; [shell: kash]
	@(yield (text (read "./extra")))
./constant : ./input
	printf fixed > @>; printf x >> child-runs
./downstream : ./constant
	cp @< @>; printf x >> parent-runs
task read-task : ./input ; [shell: kash]
	printf %s \@(text (read "./extra")) > task-output
	printf x >> task-runs
task branch-task : ; [shell: kash]
	printf %s \@(text (read (text (read "./selection")))) > branch-output
	printf x >> branch-runs
./branch-file : ; [shell: kash]
	printf %s \@(text (read (text (read "./selection")))) > branch-file
	printf x >> branch-file-runs
task definition-task : ; [shell: kash]
	printf %s \@(CONTENT) > definition-output
	printf x >> definition-runs
./definition-file : ; [shell: kash]
	printf %s \@(CONTENT) > definition-file
	printf x >> definition-file-runs
KMK
  printf A > "$signatures/input"
  printf A > "$signatures/extra"
  "${runner[@]}" -C "$signatures" ./output > "$project/out" 2> "$project/err"
  touch "$signatures/input"
   "${runner[@]}" -C "$signatures" ./output > "$project/out" 2> "$project/err"
   if [ "$(cat "$signatures/runs")" = x ]; then test-ok "$backend metadata touches preserve reuse"; else test-fail "$backend metadata touch reran recipe"; fi
  cp -p "$signatures/input" "$signatures/stamp"
  printf B > "$signatures/input"
  touch -r "$signatures/stamp" "$signatures/input"
  "${runner[@]}" -C "$signatures" ./output > "$project/out" 2> "$project/err"
  if [ "$(cat "$signatures/runs")" = xx ] && [ "$(cat "$signatures/output")" = B ]; then test-ok "$backend preserved-metadata input edits rebuild"; else test-fail "$backend preserved-metadata input edit stayed fresh"; fi
  cp -p "$signatures/output" "$signatures/stamp"
  printf X > "$signatures/output"
  touch -r "$signatures/stamp" "$signatures/output"
  "${runner[@]}" -C "$signatures" ./output > "$project/out" 2> "$project/err"
  if [ "$(cat "$signatures/runs")" = xxx ] && [ "$(cat "$signatures/output")" = B ]; then test-ok "$backend preserved-metadata output tampering rebuilds"; else test-fail "$backend tampered output stayed fresh"; fi
  test-step "$backend reuses consumers when generated input results are equal"
  "${runner[@]}" -C "$signatures" ./downstream > "$project/out" 2> "$project/err"
  printf D > "$signatures/input"
  "${runner[@]}" -C "$signatures" ./downstream > "$project/out" 2> "$project/err"
  if [ "$(cat "$signatures/child-runs")" = xx ] && [ "$(cat "$signatures/parent-runs")" = x ]; then test-ok "$backend equal generated results suppress downstream recipe execution"; else test-fail "$backend consumer tracked producer inputs instead of results"; fi
  test-step "$backend watch suppresses unchanged downstream generations"
   "${runner[@]}" --output json --watch -C "$signatures" ./downstream > "$project/equal-watch-out" 2> "$project/equal-watch-err" &
   watch_pid=$!
   wait_watch_idle "$project/equal-watch-out" ./downstream
  printf F > "$signatures/input"
  for attempt in {1..100}; do
     if [ "$(cat "$signatures/child-runs")" = xxx ] && jq -e -s '[.[] | select(.type == "target-completed" and .target == "./constant")] | length >= 2' "$project/equal-watch-out" >/dev/null 2>&1; then break; fi
    sleep 0.05
  done
  kill -TERM "$watch_pid" 2>/dev/null || true
  wait "$watch_pid" 2>/dev/null || true
     if [ "$(cat "$signatures/child-runs")" = xxx ] && [ "$(cat "$signatures/parent-runs")" = x ] && jq -e -s '([.[] | select(.type == "target-started" and .target == "./downstream")] | length == 1) and ([.[] | select(.type == "target-completed" and .target == "./constant")] | length >= 2)' "$project/equal-watch-out" >/dev/null; then test-ok "$backend equal result preserved the downstream generation"; else
      cat "$project/equal-watch-out" >&2
     cat "$project/equal-watch-err" >&2
     test-fail "$backend equal result restarted a downstream generation (child=$(cat "$signatures/child-runs"), parent=$(cat "$signatures/parent-runs"))"
   fi
  test-step "$backend validates consumed override values"
  KAME_VALUE=first "${runner[@]}" -C "$signatures" ./override > "$project/out" 2> "$project/err"
  KAME_VALUE=second "${runner[@]}" -C "$signatures" ./override > "$project/out" 2> "$project/err"
  if [ "$(cat "$signatures/override")" = second ]; then test-ok "$backend consumed override changes rebuild"; else test-fail "$backend consumed override stayed fresh"; fi
  test-step "$backend restores execution-time read observations"
  "${runner[@]}" -C "$signatures" ./discovered > "$project/out" 2> "$project/err"
  cp -p "$signatures/extra" "$signatures/stamp"
  printf B > "$signatures/extra"
  touch -r "$signatures/stamp" "$signatures/extra"
  "${runner[@]}" -C "$signatures" ./discovered > "$project/out" 2> "$project/err"
  if [ "$(cat "$signatures/discovered")" = B ]; then test-ok "$backend execution-time read bytes invalidate reuse"; else test-fail "$backend execution-time read stayed fresh"; fi
  test-step "$backend warm watch restores discovered edges and notices byte edits"
   "${runner[@]}" --output json --watch -C "$signatures" ./discovered > "$project/watch-out" 2> "$project/watch-err" &
   watch_pid=$!
   wait_watch_idle "$project/watch-out" ./discovered
  cp -p "$signatures/extra" "$signatures/stamp"
  printf C > "$signatures/extra"
  touch -r "$signatures/stamp" "$signatures/extra"
  for attempt in {1..100}; do
    if [ "$(cat "$signatures/discovered")" = C ]; then break; fi
    sleep 0.05
  done
  kill -TERM "$watch_pid" 2>/dev/null || true
  wait "$watch_pid" 2>/dev/null || true
  if [ "$(cat "$signatures/discovered")" = C ]; then test-ok "$backend warm watch tracks restored execution-time reads"; else test-fail "$backend warm watch lost discovered dependency"; fi
  test-step "$backend cached tasks validate post-execution observations"
  "${runner[@]}" -C "$signatures" read-task > "$project/out" 2> "$project/err"
  touch "$signatures/input"
   "${runner[@]}" -C "$signatures" read-task > "$project/out" 2> "$project/err"
   if [ "$(cat "$signatures/task-runs")" = x ]; then test-ok "$backend task reuse ignores metadata touches"; else test-fail "$backend task reuse hashed metadata"; fi
  cp -p "$signatures/extra" "$signatures/stamp"
  printf D > "$signatures/extra"
  touch -r "$signatures/stamp" "$signatures/extra"
  "${runner[@]}" -C "$signatures" read-task > "$project/out" 2> "$project/err"
  if [ "$(cat "$signatures/task-runs")" = xx ] && [ "$(cat "$signatures/task-output")" = D ]; then test-ok "$backend task execution-time read invalidates cache"; else test-fail "$backend task execution-time read was not persisted"; fi
  test-step "$backend warm task watch restores execution-time dependencies"
   "${runner[@]}" --output json --watch -C "$signatures" read-task > "$project/task-watch-out" 2> "$project/task-watch-err" &
   watch_pid=$!
   wait_watch_idle "$project/task-watch-out" read-task
  cp -p "$signatures/extra" "$signatures/stamp"
  printf E > "$signatures/extra"
  touch -r "$signatures/stamp" "$signatures/extra"
  for attempt in {1..100}; do
    if [ "$(cat "$signatures/task-output")" = E ] && [ "$(cat "$signatures/task-runs")" = xxx ]; then break; fi
    sleep 0.05
  done
  kill -TERM "$watch_pid" 2>/dev/null || true
  wait "$watch_pid" 2>/dev/null || true
  if [ "$(cat "$signatures/task-output")" = E ] && [ "$(cat "$signatures/task-runs")" = xxx ]; then test-ok "$backend warm task watch tracks accepted execution reads"; else test-fail "$backend warm task watch lost execution reads"; fi
  test-step "$backend cache misses discard obsolete branch observations"
  for target in branch-task ./branch-file; do
    rm -f "$signatures/left"
    printf L > "$signatures/left"
    printf R > "$signatures/right"
    printf ./left > "$signatures/selection"
    "${runner[@]}" -C "$signatures" "$target" > "$project/out" 2> "$project/err"
    rm "$signatures/left"
    printf ./right > "$signatures/selection"
    "${runner[@]}" -C "$signatures" "$target" > "$project/out" 2> "$project/err"
    "${runner[@]}" -C "$signatures" "$target" > "$project/out" 2> "$project/err"
    printf obsolete > "$signatures/left"
    "${runner[@]}" -C "$signatures" "$target" > "$project/out" 2> "$project/err"
  done
  if [ "$(cat "$signatures/branch-output")" = R ] && [ "$(cat "$signatures/branch-file")" = R ] && [ "$(cat "$signatures/branch-runs")" = xx ] && [ "$(cat "$signatures/branch-file-runs")" = xx ]; then test-ok "$backend file and task records retain only the executed branch"; else test-fail "$backend rejected reuse retained obsolete branch reads"; fi
  test-step "$backend restores execution-time definitions in the current snapshot"
  printf one > "$signatures/definition-input"
  for target in definition-task ./definition-file; do
    "${runner[@]}" -C "$signatures" "$target" > "$project/out" 2> "$project/err"
     "${runner[@]}" -C "$signatures" "$target" > "$project/out" 2> "$project/err"
  done
   if [ "$(cat "$signatures/definition-runs")" = x ] && [ "$(cat "$signatures/definition-file-runs")" = x ]; then test-ok "$backend unchanged execution-time definitions reuse their records"; else test-fail "$backend execution-time definition could not be reused"; fi
  printf two > "$signatures/definition-input"
  for target in definition-task ./definition-file; do
    "${runner[@]}" -C "$signatures" "$target" > "$project/out" 2> "$project/err"
    "${runner[@]}" -C "$signatures" "$target" > "$project/out" 2> "$project/err"
  done
  if [ "$(cat "$signatures/definition-output")" = two ] && [ "$(cat "$signatures/definition-file")" = two ] && [ "$(cat "$signatures/definition-runs")" = xx ] && [ "$(cat "$signatures/definition-file-runs")" = xx ]; then test-ok "$backend definition reads invalidate and then reuse"; else test-fail "$backend definition observations did not follow current bytes"; fi
  test-step "$backend warm watch restores definition resource edges"
   "${runner[@]}" --output json --watch -C "$signatures" definition-task > "$project/definition-watch-out" 2> "$project/definition-watch-err" &
   watch_pid=$!
   wait_watch_idle "$project/definition-watch-out" definition-task
  printf three > "$signatures/definition-input"
  for attempt in {1..100}; do
    if [ "$(cat "$signatures/definition-output")" = three ] && [ "$(cat "$signatures/definition-runs")" = xxx ]; then break; fi
    sleep 0.05
  done
  kill -TERM "$watch_pid" 2>/dev/null || true
  wait "$watch_pid" 2>/dev/null || true
  if [ "$(cat "$signatures/definition-output")" = three ] && [ "$(cat "$signatures/definition-runs")" = xxx ]; then test-ok "$backend warm definition cache hit retains file reads"; else test-fail "$backend warm definition watch lost resource interest"; fi
  test-step "$backend restores consumed execution-time definition overrides"
  for value in first second; do
    KAME_CONTENT="$value" "${runner[@]}" -C "$signatures" definition-task > "$project/out" 2> "$project/err"
     KAME_CONTENT="$value" "${runner[@]}" -C "$signatures" definition-task > "$project/out" 2> "$project/err"
  done
  if [ "$(cat "$signatures/definition-output")" = second ] && [ "$(cat "$signatures/definition-runs")" = xxxxx ]; then test-ok "$backend execution-time overrides invalidate only when consumed values change"; else test-fail "$backend execution-time override restoration was stale or nonreusable"; fi
  test-step "$backend fingerprints binary environment reads for shell and Kash"
  child_env="$project/child-environment"
  mkdir -p "$child_env"
  cat > "$child_env/Makefile.kmk" <<'KMK'
./shell-file : ./input
	printf %s "$ENV_SIGNATURE_BINARY" > @>; printf x >> shell-file-runs
task shell-task : ./input
	printf %s "$ENV_SIGNATURE_BINARY" > shell-task; printf x >> shell-task-runs
./kash-file : ./input ; [shell: kash]
	sh ./binary-read kash-file kash-file-runs
task kash-task : ./input ; [shell: kash]
	sh ./binary-read kash-task kash-task-runs
task pure-task : ./input
	@(out "pure")
./pure-file : ./input
	@(yield "pure")
./overridden : ./input ; env "ENV_SIGNATURE_BINARY=fixed"
	printf %s "$ENV_SIGNATURE_BINARY" > @>; printf x >> overridden-runs
READ_ENV = (env "ENV_SIGNATURE_BINARY")
(indirect-env) = (str READ_ENV)
task pure-read-task : ./input
	@(out (indirect-env))
./pure-read-file : ./input
	@(yield (indirect-env))
KMK
  cat > "$child_env/binary-read" <<'SH'
printf %s "$ENV_SIGNATURE_BINARY" > "$1"
printf x >> "$2"
SH
  printf input > "$child_env/input"
  for target in ./shell-file shell-task ./kash-file kash-task; do
    for value in first first second second; do
      ENV_SIGNATURE_BINARY="$value" "${runner[@]}" -C "$child_env" "$target" > "$project/out" 2> "$project/err"
    done
    output="${target#./}"
    if [ "$(cat "$child_env/$output")" = second ] && [ "$(cat "$child_env/$output-runs")" = xx ]; then test-ok "$backend $target tracks binary getenv without a Kame read"; else test-fail "$backend $target ignored or failed to reuse its child snapshot"; fi
  done
  test-step "$backend tracks loader, PATH and locale inputs"
  for assignment in "PATH=$PATH:/unused-signature-path" "LD_LIBRARY_PATH=/unused-signature-library" "LC_ALL=C" "LANG=C"; do
    for repeat in 1 2; do
      env ENV_SIGNATURE_BINARY=second "$assignment" "${runner[@]}" -C "$child_env" ./shell-file > "$project/out" 2> "$project/err"
    done
  done
  if [ "$(cat "$child_env/shell-file-runs")" = xxxxxx ]; then test-ok "$backend execution environment changes invalidate once each"; else test-fail "$backend loader, locale or PATH snapshot was missing or unstable"; fi
  test-step "$backend pure/declarative rules ignore unread environment changes"
  for value in first second; do
    ENV_SIGNATURE_BINARY="$value" "${runner[@]}" --json -C "$child_env" pure-task > "$project/pure-task-json" 2> "$project/err"
    ENV_SIGNATURE_BINARY="$value" "${runner[@]}" --json -C "$child_env" ./pure-file > "$project/pure-file-json" 2> "$project/err"
    ENV_SIGNATURE_BINARY="$value" "${runner[@]}" -C "$child_env" ./overridden > "$project/out" 2> "$project/err"
  done
  if jq -e -s 'any(.[]; .cached == true)' "$project/pure-task-json" >/dev/null && ! jq -e -s 'any(.[]; .type == "effect")' "$project/pure-file-json" >/dev/null && [ "$(cat "$child_env/overridden-runs")" = x ]; then test-ok "$backend selective pure reuse and effective overrides"; else test-fail "$backend fingerprinted an unconsumed or overridden environment"; fi
  test-step "$backend pure rules track indirect named environment reads"
  for value in first second; do
    for target in pure-read-task ./pure-read-file; do
      ENV_SIGNATURE_BINARY="$value" "${runner[@]}" --allow-env=ENV_SIGNATURE_BINARY --allow-read --allow-write -C "$child_env" -f Makefile.kmk "$target" > "$project/out" 2> "$project/err"
      if [ "$target" = pure-read-task ] && [ "$(cat "$project/out")" != "$value" ]; then test-fail "$backend indirect read task returned stale value"; fi
      ENV_SIGNATURE_BINARY="$value" UNREAD_SIGNATURE_ENV=changed "${runner[@]}" --allow-env=ENV_SIGNATURE_BINARY --allow-read --allow-write --json -C "$child_env" -f Makefile.kmk "$target" > "$project/pure-read-json" 2> "$project/err"
      if [ "$target" = pure-read-task ]; then
        if jq -e -s 'any(.[]; .cached == true)' "$project/pure-read-json" >/dev/null; then test-ok "$backend indirect read task reuses $value despite unread change"; else test-fail "$backend pure indirect read task widened environment tracking"; fi
      else
        if [ "$(cat "$child_env/pure-read-file")" = "$value" ] && ! jq -e -s 'any(.[]; .type == "effect")' "$project/pure-read-json" >/dev/null; then test-ok "$backend indirect read file tracks $value and ignores unread change"; else test-fail "$backend pure indirect read file was stale or nonreusable"; fi
      fi
    done
  done
  test-step "$backend inherits through dynamically discovered file producers"
 MODE=ambient "${runner[@]}" -C "$project" dynamic-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/dynamic-log")" = dynamic ]; then test-ok "$backend dynamic file producer inherits environment"; else test-fail "$backend dynamic producer environment"; fi
 test-step "$backend preserves scoped values through process retries"
 MODE=ambient "${runner[@]}" --retry 1 -C "$project" retry > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/retry-log")" = retryretry ]; then test-ok "$backend retried recipe keeps its environment"; else test-fail "$backend retry environment"; fi
 test-step "$backend reports authored environment metadata"
 "${runner[@]}" do plan --json -C "$project" root > "$project/plan" 2> "$project/err"
  "${runner[@]}" do parse --json --lang script "$project/Makefile.kmk" > "$project/ast" 2> "$project/err"
 "${runner[@]}" do fmt --lang script "$project/Makefile.kmk" > "$project/fmt" 2> "$project/err"
  if jq -e '.producers[0].environment == ["MODE=first", "MODE=debug", "MESSAGE=spaces; equal=ok"]' "$project/plan" >/dev/null && jq -e '.. | objects | select(.environment? == ["MODE=first", "MODE=debug", "MESSAGE=spaces; equal=ok"])' "$project/ast" >/dev/null && rg -q '; env "MODE=release"' "$project/fmt"; then test-ok "$backend plan, AST and format environment metadata"; else test-fail "$backend environment inspection"; fi
 test-step "$backend validates assignments before effects"
 printf 'bad : ; env "1MODE=debug"\n\ttouch forbidden\n' > "$project/bad.kmk"
 if "${runner[@]}" -C "$project" -f bad.kmk bad > "$project/out" 2> "$project/err"; then test-fail "$backend accepted invalid environment"; elif rg -q PARSE_ERR "$project/err" && [ ! -e "$project/forbidden" ]; then test-ok "$backend invalid assignment has no effects"; else test-fail "$backend assignment validation"; fi
done
test-end
