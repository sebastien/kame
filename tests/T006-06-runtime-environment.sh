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
for backend in native wasm; do
 project="$TMPDIR/$backend"
 mkdir -p "$project"
 if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
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
 for record in "$record_dir"/*; do if [ "$(wc -c < "$record")" -eq 32 ]; then printf corrupt > "$record"; fi; done
 MODE=ambient "${runner[@]}" -C "$project" ./output > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/file-runs")" = xxxxx ]; then test-ok "$backend corrupt context stamp rebuilds"; else test-fail "$backend corrupt context accepted"; fi
 test-step "$backend inherits through dynamically discovered file producers"
 MODE=ambient "${runner[@]}" -C "$project" dynamic-root > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/dynamic-log")" = dynamic ]; then test-ok "$backend dynamic file producer inherits environment"; else test-fail "$backend dynamic producer environment"; fi
 test-step "$backend preserves scoped values through process retries"
 MODE=ambient "${runner[@]}" --retry 1 -C "$project" retry > "$project/out" 2> "$project/err"
 if [ "$(cat "$project/retry-log")" = retryretry ]; then test-ok "$backend retried recipe keeps its environment"; else test-fail "$backend retry environment"; fi
 test-step "$backend reports authored environment metadata"
 "${runner[@]}" do plan --json -C "$project" root > "$project/plan" 2> "$project/err"
 "${runner[@]}" do parse --lang script "$project/Makefile.kmk" > "$project/ast" 2> "$project/err"
 "${runner[@]}" do fmt --lang script "$project/Makefile.kmk" > "$project/fmt" 2> "$project/err"
 if jq -e '.environment == ["MODE=first", "MODE=debug", "MESSAGE=spaces; equal=ok"]' "$project/plan" >/dev/null && jq -e '.. | objects | select(.environment? == ["MODE=first", "MODE=debug", "MESSAGE=spaces; equal=ok"])' "$project/ast" >/dev/null && rg -q '; env "MODE=release"' "$project/fmt"; then test-ok "$backend plan, AST and format environment metadata"; else test-fail "$backend environment inspection"; fi
 test-step "$backend validates assignments before effects"
 printf 'bad : ; env "1MODE=debug"\n\ttouch forbidden\n' > "$project/bad.kmk"
 if "${runner[@]}" -C "$project" -f bad.kmk bad > "$project/out" 2> "$project/err"; then test-fail "$backend accepted invalid environment"; elif rg -q PARSE_ERR "$project/err" && [ ! -e "$project/forbidden" ]; then test-ok "$backend invalid assignment has no effects"; else test-fail "$backend assignment validation"; fi
done
test-end
