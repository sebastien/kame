#!/usr/bin/env bash
# Spec: docs/spec/037-build-inspection.md
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T037-01 recursive build inspection"
test-step "build both hosts"
cli_require_tools
cli_build
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make -C "$CLI_ROOT" dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
native_bin="$CLI_BIN"
wasm_bin="$TEST_PATH/kame-wasm"
printf '#!/usr/bin/env bash\nexec node %q "$@"\n' "$wasm_wrapper" >"$wasm_bin"
chmod +x "$wasm_bin"

# Use the same controlled environment and safety deadline for both hosts.
inspection_run() {
  local environment=()
  if [ "$backend" = wasm ] && [ -n "${KAME_WASM:-}" ]; then
    environment+=(--env "KAME_WASM=$KAME_WASM" --env "NODE_NO_WARNINGS=1")
  fi
  cli_run "${environment[@]}" -- "$@"
  cat "$CLI_OUT"
  cat "$CLI_ERR" >&2
  return "$CLI_STATUS"
}

project="$TMPDIR/inspection"
mkdir -p "$project/src" "$project/rules"
printf 'one' >"$project/src/one.c"
printf 'two' >"$project/src/two.c"
printf 'version' >"$project/seed"
cat >"$project/Makefile.km" <<'KM'
include ./rules/build.km
task default : build
task build : ./bin/one ./bin/two cache-entry daemon
task cached cache-entry :
	touch forbidden-cache
service daemon :
	touch forbidden-service
task unrelated :
	touch forbidden-unrelated
KM
cat >"$project/rules/build.km" <<'KM'
SOURCES = (wildcard ./src/*.c)
rule ./bin/one ./bin/one.map : ${SOURCES} ./version | prepare
	touch forbidden-recipe
rule ./bin/two : ./version
	touch forbidden-recipe
rule ./version : ./seed
	touch forbidden-recipe
task prepare :
	touch forbidden-recipe
KM
for backend in native wasm; do
  if [ "$backend" = native ]; then CLI_BIN="$native_bin"; else CLI_BIN="$wasm_bin"; fi
  runner=(inspection_run)
  test-step "$backend keeps producer details concise unless verbose"
  "${runner[@]}" do plan --output text -c 'rule ./out : ./in' ./out >"$project/concise" 2>"$project/error"
  "${runner[@]}" do plan --output text --verbose -c 'rule ./out : ./in' ./out >"$project/verbose" 2>"$project/error"
  if ! grep -Eq 'freshness: unknown|captures|tools' "$project/concise" && grep -q 'source: <command>' "$project/concise" && grep -q 'freshness: unknown' "$project/verbose" && grep -q captures "$project/verbose" && ! grep -q tools "$project/verbose"; then test-ok "$backend producer verbosity"; else test-fail "$backend producer verbosity"; fi
  test-step "$backend inventories concrete inputs, products and persistent intermediates"
  for kind in plan inputs outputs; do
    "${runner[@]}" do "$kind" --json -C "$project" >"$project/$backend.$kind.json" 2>"$project/error"
    if jq -e '.schema == 2 and .depth == -1 and .scope == "declared-and-read-only" and (.truncated | not)' "$project/$backend.$kind.json" >/dev/null; then test-ok "$backend $kind schema"; else test-fail "$backend $kind schema"; fi
  done
  if jq -e '
    [.resources[] | select(.roles | index("artifact")) | .display] == ["./bin/one", "./bin/two", "./bin/one.map", "./version"] and
    any(.resources[]; .display == "./version" and .lifetime == "persistent" and (.roles | index("intermediate")) and (.roles | index("input"))) and
    all(.resources[] | select(.key.kind != "file"); (.roles | index("artifact")) == null) and
    ([.producers[] | select(.target == "./bin/one")] | length == 1) and
    all(.producers[]; .target != "unrelated") and
    any(.dependencies[]; .orderOnly) and
    any(.resources[]; .display == "./src/one.c") and any(.resources[]; .display == "./src/two.c") and
    any(.resources[]; .display == "./seed") and
    any(.resources[]; .display == "./rules/build.km" and (.roles | index("configuration")))
  ' "$project/$backend.plan.json" >/dev/null; then test-ok "$backend classification, sibling sharing and computed traversal"; else test-fail "$backend incomplete graph"; fi
  if [ ! -e "$project/bin" ] && [ ! -e "$project/version" ] && [ ! -e "$project/.kame" ] && ! compgen -G "$project/forbidden-*" >/dev/null; then test-ok "$backend inspection is effect-free"; else test-fail "$backend inspection executed work"; fi
  "${runner[@]}" do plan --output text -C "$project" >"$project/$backend.text" 2>"$project/error"
  if [ "$(grep -c '^    ./version · runtime-discovery · opaque-process-io$' "$project/$backend.text")" = 1 ]; then test-ok "$backend boundaries group attributes by path"; else test-fail "$backend verbose discovery boundaries"; fi
   if grep -q '^    ./src/one.c \[1\]$' "$project/$backend.text" && grep -q '^    ./version \[3\] · missing$' "$project/$backend.text" && grep -q 'parallel eligible' "$project/$backend.text" && ! grep -Eq '^      (producer|consumer):' "$project/$backend.text"; then test-ok "$backend compact human resource/stage report"; else test-fail "$backend human facts missing"; fi
   if grep -F -x -q '    SOURCES = (wildcard ./src/*.c)' "$project/$backend.text" && ! grep -F -q 'SOURCES · definition' "$project/$backend.text"; then test-ok "$backend definition symbols are one compact line"; else test-fail "$backend definition symbol layout"; fi
  "${runner[@]}" do outputs --output text -C "$project" >"$project/$backend.outputs.text" 2>"$project/error"
  if jq -e '.direction == "downstream" and (.items | length == 0)' "$project/$backend.outputs.json" >/dev/null; then test-ok "$backend default has no downstream artifacts"; else test-fail "$backend outputs walked upstream"; fi
  for kind in inputs; do
    "${runner[@]}" do "$kind" --output text -C "$project" >"$project/$backend.$kind.text" 2>"$project/error"
    if grep -q '^    ./version · runtime-discovery · opaque-process-io$' "$project/$backend.$kind.text"; then test-ok "$backend $kind path-first boundaries"; else test-fail "$backend $kind boundary layout"; fi
    if grep -q '^    ./version \[3\] · missing$' "$project/$backend.$kind.text" && ! grep -Eq '^      (producer|consumer):| · (artifact|source file|generated input)' "$project/$backend.$kind.text"; then test-ok "$backend $kind one resource per line"; else test-fail "$backend verbose $kind inventory"; fi
  done
  "${runner[@]}" do plan --output text --verbose -C "$project" ./version >"$project/detail" 2>"$project/error"
  if grep -q 'source' "$project/detail" && grep -q 'freshness' "$project/detail"; then test-ok "$backend selected target plan retains details"; else test-fail "$backend selected target details missing"; fi
  test-step "$backend shared roots, depth, and local sequence gates"
  "${runner[@]}" do plan --json -C "$project" ./bin/one ./bin/one.map >"$project/shared" 2>"$project/error"
  if jq -e '(.targets | length == 2) and ([.producers[] | select(.kind == "file")] | length == 2)' "$project/shared" >/dev/null; then test-ok "$backend multi-output producer shared across roots"; else test-fail "$backend repeated producer"; fi
  for depth in 0 1 2; do
    "${runner[@]}" do inputs --json --depth "$depth" -C "$project" >"$project/depth" 2>"$project/error"
    if jq -e --argjson depth "$depth" '.depth == $depth and .truncated and (if $depth == 0 then (.items | length == 0) and (.dependencies | length == 0) else true end)' "$project/depth" >/dev/null; then test-ok "$backend depth $depth"; else test-fail "$backend depth $depth"; fi
  done
  "${runner[@]}" do plan --json -c $'task default : first, second\ntask first :\ntask second :\n' >"$project/sequence" 2>"$project/error"
  if jq -e '[.dependencies[].group] == [1,2] and [.stages[].producers] == [[2],[3],[1]]' "$project/sequence" >/dev/null; then test-ok "$backend sequence stages"; else test-fail "$backend sequence order"; fi
  test-step "$backend reports selected tools once at their earliest stage"
  tool_plan=$'task default : middle\n\t(capture! {stdout: true} "git" "--version")\ntask middle : early\n\tprintf middle\n\tcat /dev/null\ntask early :\n\tprintf early\ntask unrelated :\n\tsleep 5\n'
  "${runner[@]}" do plan --json -C "$project" -c "$tool_plan" >"$project/$backend.tool-plan.json"
  if jq -e '[.tools[] | [.name, .stage]] == [["printf", 1], ["cat", 2], ["git", 3]] and all(.tools[]; .path | startswith("/")) and all(.producers[]; has("tools") | not)' "$project/$backend.tool-plan.json" >/dev/null; then
    test-ok "$backend tools follow stage order and selected producers"
  else
    test-fail "$backend incorrect tool stages: $(jq -c .tools "$project/$backend.tool-plan.json")"
  fi
  "${runner[@]}" do plan --output text --verbose -C "$project" -c "$tool_plan" >"$project/$backend.tool-plan.text"
  if grep -Fxq '  tools · first required stage' "$project/$backend.tool-plan.text" && grep -q '^    stage 1 · printf → /' "$project/$backend.tool-plan.text" && ! grep -Fq ' · tools:' "$project/$backend.tool-plan.text"; then
    test-ok "$backend renders tool requirements outside producer rows"
  else
    test-fail "$backend tool section layout"
  fi
  test-step "$backend diagnoses cycles and unknown descendants without partial documents"
  for kind in plan inputs; do
    status=0
    "${runner[@]}" do "$kind" --json -C "$project" -c $'rule ./collision/{left:*}x.o = "left"\nrule ./collision/x{right:*}.o = "right"\n' ./collision/xx.o >"$project/ambiguous" 2>"$project/error" || status=$?
    if [ "$status" = 1 ] && jq -es 'length == 1 and .[0].type == "diagnostic" and .[0].diagnostic.code == "TGT_AMBIG"' "$project/ambiguous" >/dev/null && [ ! -e "$project/collision" ]; then
      test-ok "$backend $kind diagnoses overlapping producers without effects"
    else
      test-fail "$backend $kind hid an ambiguous producer"
      cat "$project/ambiguous" "$project/error"
    fi
  done
  for source in $'task default : child\ntask child : default\n' $'task default : unknown\n'; do
    status=0
    "${runner[@]}" do plan --json -c "$source" >"$project/failure" 2>"$project/error" || status=$?
    if [ "$status" = 1 ] && jq -e '.type == "diagnostic" and (.diagnostic.code == "DEP_CYCLE" or .diagnostic.code == "TGT_NO_RULE") and (.diagnostic.targetStack | length >= 2)' "$project/failure" >/dev/null; then test-ok "$backend descendant failure"; else test-fail "$backend lost descendant diagnostic"; fi
  done
  test-step "$backend reports unavailable generated discovery without executing its producer"
  printf './manifest' >"$project/selection"
  cat >"$project/deferred.km" <<'KM'
FILES = (text (read (text (read ./selection))))
task default : ${FILES}
rule ./manifest : ./seed
	touch forbidden-manifest
KM
  "${runner[@]}" do inputs --json -C "$project" -f deferred.km >"$project/$backend.deferred" 2>"$project/error"
  if jq -e 'any(.deferred[]; .reason == "generated-input-unavailable") and any(.producers[]; .target | endswith("/manifest")) and any(.resources[]; .display == "./seed")' "$project/$backend.deferred" >/dev/null && [ ! -e "$project/manifest" ] && [ ! -e "$project/forbidden-manifest" ]; then test-ok "$backend generated read boundary"; else test-fail "$backend generated read boundary missing"; fi
  "${runner[@]}" do inputs --output text -C "$project" -f deferred.km >"$project/deferred-text" 2>"$project/error"
  if grep -q '^    ./manifest · generated-input-unavailable' "$project/deferred-text"; then test-ok "$backend generated boundary names the affected file"; else test-fail "$backend missing generated boundary path"; fi
  if ! grep -q 'no inputs' "$project/deferred-text"; then test-ok "$backend partial is not empty"; else test-fail "$backend misleading empty inventory"; fi
  test-step "$backend read denials and invalid phases remain errors"
  for expression in '(text (read ../blocked))' '(out! "forbidden-effect")'; do
    status=0
    "${runner[@]}" do plan --json -C "$project" -c "task default : \${nop $expression}" >"$project/failure" 2>"$project/error" || status=$?
    if [ "$status" = 1 ] && jq -e '.type == "diagnostic" and (.diagnostic.code == "CAP_DENIED" or .diagnostic.code == "PHASE_INVALID")' "$project/failure" >/dev/null; then test-ok "$backend read-only authority"; else test-fail "$backend authority failure hidden"; fi
  done
  cat >"$project/failed-read.km" <<'KM'
FILES = (list (exists? ./manifest) (text (read ./missing-external)))
task default : ${FILES}
rule ./manifest : ./seed
	touch forbidden-manifest
KM
  status=0
  "${runner[@]}" do inputs --json -C "$project" -f failed-read.km >"$project/failure" 2>"$project/error" || status=$?
  if [ "$status" = 1 ] && jq -e '.type == "diagnostic" and .diagnostic.code == "FS_ERR"' "$project/failure" >/dev/null; then test-ok "$backend missing generated input does not hide unrelated read failures"; else test-fail "$backend unrelated read failure hidden"; fi
  test-step "$backend artifact status verifies signatures without rebuilding"
  status_project="$TMPDIR/status-$backend"
  mkdir -p "$status_project"
  printf one >"$status_project/seed"
  printf 'rule ./result : ./seed = read ./seed\n' >"$status_project/Makefile.km"
  "${runner[@]}" do plan --json -C "$status_project" ./result >"$project/status" 2>"$project/error"
  if jq -e 'any(.resources[]; .display == "./result" and .status == "missing")' "$project/status" >/dev/null; then test-ok "$backend absent artifact is missing"; else test-fail "$backend missing status"; fi
  "${runner[@]}" -C "$status_project" ./result >"$project/value" 2>"$project/error"
  "${runner[@]}" do plan --output text -C "$status_project" ./result >"$project/status" 2>"$project/error"
  if grep -q '^    ./result \[1\] · built$' "$project/status"; then test-ok "$backend accepted artifact is built"; else test-fail "$backend built status: $(cat "$project/status")"; fi
  metadata_project="$TMPDIR/status-metadata-$backend"
  mkdir -p "$metadata_project"
  printf one >"$metadata_project/seed"
  printf 'rule ./result : ./seed = str (get "size" (stat ./seed))\n' >"$metadata_project/Makefile.km"
  "${runner[@]}" -C "$metadata_project" ./result >"$project/metadata-value" 2>"$project/error"
  "${runner[@]}" do plan --json -C "$metadata_project" ./result >"$project/metadata-status" 2>"$project/error"
  if jq -e 'any(.resources[]; .display == "./result" and .status == "built")' "$project/metadata-status" >/dev/null; then test-ok "$backend metadata identity survives inspection"; else test-fail "$backend metadata status: $(cat "$project/metadata-status")"; fi
  cp -p "$status_project/seed" "$status_project/stamp"
  printf two >"$status_project/seed"
  touch -r "$status_project/stamp" "$status_project/seed"
  "${runner[@]}" do plan --json -C "$status_project" ./result >"$project/status" 2>"$project/error"
  if jq -e 'any(.resources[]; .display == "./result" and .status == "outdated")' "$project/status" >/dev/null && [ "$(cat "$status_project/result")" = one ]; then test-ok "$backend preserved-metadata input change is outdated without rebuilding"; else test-fail "$backend input signature status"; fi
  "${runner[@]}" -C "$status_project" ./result >"$project/value" 2>"$project/error"
  printf bad >"$status_project/result"
  "${runner[@]}" do plan --json -C "$status_project" ./result >"$project/status" 2>"$project/error"
  if jq -e 'any(.resources[]; .display == "./result" and .status == "outdated")' "$project/status" >/dev/null && [ "$(cat "$status_project/result")" = bad ]; then test-ok "$backend tampered artifact is outdated without repair"; else test-fail "$backend output signature status"; fi
  "${runner[@]}" do plan --json --allow-read="$project" -C "$status_project" ./result >"$project/status" 2>"$project/error"
  if jq -e 'any(.resources[]; .display == "./result" and .status == "unknown")' "$project/status" >/dev/null; then test-ok "$backend denied status observation stays unknown"; else test-fail "$backend denied artifact observation"; fi
  printf 'rule ./result : ./seed\n\tx/sh -c "cat ./seed > ./result"\n' >"$status_project/Makefile.km"
  "${runner[@]}" -C "$status_project" ./result >"$project/value" 2>"$project/error"
  "${runner[@]}" do plan --json -C "$status_project" ./result >"$project/status" 2>"$project/error"
  if jq -e 'any(.resources[]; .display == "./result" and .status == "built")' "$project/status" >/dev/null; then test-ok "$backend verifies tool metadata without treating it as file content"; else test-fail "$backend tool artifact status"; fi
  printf '# source changed\n' >>"$status_project/Makefile.km"
  "${runner[@]}" do plan --json -C "$status_project" ./result >"$project/status" 2>"$project/error"
  if jq -e 'any(.resources[]; .display == "./result" and .status == "unknown")' "$project/status" >/dev/null; then test-ok "$backend changed source guard needs reevaluation, not a freshness guess"; else test-fail "$backend source guard status"; fi
  test-step "$backend downstream source impact, patterns and absent glob members"
  impact="$TMPDIR/impact-$backend"
  mkdir -p "$impact/src"
  printf seed >"$impact/src/one.txt"
  cat >"$impact/Makefile.km" <<'KM'
SOURCES = (wildcard ./src/*.txt)
PAGES = (wildcard ./build/**/*.json)
task default : site
task site : ./site/one.html ./catalogue ./assets
rule ./build/{name}.json ./build/{name}.map : ./src/{name}.txt
	touch forbidden
rule ./site/{name}.html : ./build/{name}.json ./catalogue
	touch forbidden
rule ./catalogue : ${PAGES}
	touch forbidden
rule ./assets : ./catalogue
	touch forbidden
rule ./other : ./unrelated
	touch forbidden
rule ./ordered : | ./src/one.txt
	touch forbidden
rule ./all-sources : ${SOURCES}
	touch forbidden
task unrelated :
KM
  # Explicit paths select files; exact declared logical names still win.
  "${runner[@]}" do outputs --json -C "$impact" unrelated >"$project/logical" 2>"$project/error"
  if jq -e '.targets == ["unrelated"] and (.items | length == 0)' "$project/logical" >/dev/null; then test-ok "$backend exact logical name wins"; else test-fail "$backend logical/path collision"; fi
  "${runner[@]}" do plan --json -C "$impact" ./src/one.txt >"$project/source" 2>"$project/error"
  if jq -e '(.producers | length == 0) and any(.resources[]; .display == "./src/one.txt" and (.roles | index("input")))' "$project/source" >/dev/null; then test-ok "$backend external source root"; else test-fail "$backend requires source producer"; fi
  "${runner[@]}" do outputs --json -C "$impact" ./src/one.txt >"$project/$backend.impact" 2>"$project/error"
  if jq -e '
    .direction == "downstream" and
    ([.resources[] | select(.roles | index("artifact")) | .display] | sort) == ["./all-sources","./assets","./build/one.json","./build/one.map","./catalogue","./site/one.html"] and
    all(.producers[]; .target != "./other" and .target != "./ordered") and
    ([.producers[] | select(.target == "./build/one.json")] | length == 1) and
    any(.dependencies[]; .potential == true)
  ' "$project/$backend.impact" >/dev/null; then test-ok "$backend transitive consumers, sibling outputs, computed inputs and missing glob member"; else test-fail "$backend incomplete source impact"; fi
  "${runner[@]}" do outputs --json --depth 1 -C "$impact" ./src/one.txt >"$project/direct" 2>"$project/error"
  if jq -e '.truncated and ([.resources[] | select(.roles | index("artifact")) | .display] | sort) == ["./all-sources","./build/one.json","./build/one.map"]' "$project/direct" >/dev/null; then test-ok "$backend consumer-hop depth"; else test-fail "$backend downstream depth"; fi
  "${runner[@]}" do outputs --json --depth 0 -C "$impact" ./src/one.txt >"$project/zero" 2>"$project/error"
  if jq -e '.truncated and (.resources | length == 1) and (.dependencies | length == 0) and (.items | length == 0)' "$project/zero" >/dev/null; then test-ok "$backend downstream depth zero"; else test-fail "$backend depth zero leaked graph"; fi
  "${runner[@]}" do plan --affected --json -C "$impact" ./src/one.txt ./unrelated >"$project/$backend.affected" 2>"$project/error"
  if jq -e '.type == "plan" and .direction == "downstream" and (.targets | length == 2) and any(.producers[]; .target == "./other") and (.stages | length >= 3)' "$project/$backend.affected" >/dev/null; then test-ok "$backend shared affected plan and stages"; else test-fail "$backend affected plan"; fi
  "${runner[@]}" do outputs --json -C "$impact" ./assets >"$project/leaf" 2>"$project/error"
  if jq -e '(.items | length == 0) and all(.resources[]; (.roles | index("artifact")) == null)' "$project/leaf" >/dev/null; then test-ok "$backend outputs excludes queried artifact"; else test-fail "$backend outputs includes upstream artifacts"; fi
  "${runner[@]}" do outputs --json -C "$impact" src/one.txt >"$project/normalized" 2>"$project/error"
  "${runner[@]}" do outputs --json -C "$impact" ./src/one.txt >"$project/explicit" 2>"$project/error"
  if cmp -s "$project/normalized" "$project/explicit"; then test-ok "$backend omitted ./ is canonical"; else test-fail "$backend path normalization differs"; fi
  "${runner[@]}" do plan --json -C "$impact" build/one.json >"$project/normalized-plan" 2>"$project/error"
  if jq -e '.targets == ["./build/one.json"] and .direction == "upstream"' "$project/normalized-plan" >/dev/null; then test-ok "$backend missing output path normalization"; else test-fail "$backend missing path needs ./"; fi
  "${runner[@]}" do outputs --output text -C "$impact" src/one.txt >"$project/$backend.impact-text" 2>"$project/error"
  if grep -q './src/one.txt → ./build/one.json' "$project/$backend.impact-text" && grep -q './catalogue → ./assets' "$project/$backend.impact-text"; then test-ok "$backend explains impact chains"; else test-fail "$backend missing human provenance"; fi
  if [ ! -e "$impact/build" ] && [ ! -e "$impact/.kame" ] && [ ! -e "$impact/forbidden" ]; then test-ok "$backend impact queries have no effects"; else test-fail "$backend impact executed work"; fi
  test-step "$backend consumer discovery limits and relevant cycles"
  status=0
  "${runner[@]}" do outputs --json -c $'rule ./out : ./seed\n' unknown-target >"$project/unknown-impact-root" 2>"$project/error" || status=$?
  if [ "$status" = 1 ] && jq -e '.type == "diagnostic" and .diagnostic.code == "TGT_NO_RULE"' "$project/unknown-impact-root" >/dev/null; then test-ok "$backend unknown logical change root remains an error"; else test-fail "$backend unknown logical root accepted"; fi
  "${runner[@]}" do outputs --json -c $'rule ./out/{name} : ./src/{_0}\n' ./src/one >"$project/positional-impact" 2>"$project/error"
  if jq -e 'any(.producers[]; .target == "./out/one") and any(.resources[]; .display == "./out/one" and (.roles | index("artifact")))' "$project/positional-impact" >/dev/null; then test-ok "$backend positional input capture inversion"; else test-fail "$backend positional consumer lost"; fi
  "${runner[@]}" do plan --affected --json -c $'task build-{name} : seed-{name}\ntask seed-one :\nrule ./out : build-one\n' seed-one >"$project/logical-pattern-impact" 2>"$project/error"
  if jq -e 'any(.producers[]; .target == "build-one") and any(.producers[]; .target == "./out")' "$project/logical-pattern-impact" >/dev/null; then test-ok "$backend logical pattern consumers"; else test-fail "$backend logical pattern consumer lost"; fi
  for depth in -1 0; do
    "${runner[@]}" do outputs --json --depth "$depth" -c $'rule ./out : ./seed\ntask unrelated : unknown\nrule ./seed : ${text (read ./missing-review-input)}\n' ./seed >"$project/unrelated-failure" 2>"$project/error"
    if jq -e '.type == "outputs" and .direction == "downstream"' "$project/unrelated-failure" >/dev/null; then test-ok "$backend depth $depth ignores unrelated and excluded seed failures"; else test-fail "$backend unrelated resolution aborted impact query"; fi
  done
  status=0
  "${runner[@]}" do outputs --json -c $'rule ./out : ./seed unknown\n' ./seed >"$project/relevant-failure" 2>"$project/error" || status=$?
  if [ "$status" = 1 ] && jq -e '.type == "diagnostic" and .diagnostic.code == "TGT_NO_RULE"' "$project/relevant-failure" >/dev/null; then test-ok "$backend relevant consumer failure retained"; else test-fail "$backend affected diagnostic suppressed"; fi
  "${runner[@]}" do outputs --output text -c $'rule ./out : middle\ntask middle : ./seed\n' ./seed >"$project/$backend.logical-impact-text" 2>"$project/error"
  if grep -q './seed → middle' "$project/$backend.logical-impact-text" && grep -q 'middle → ./out' "$project/$backend.logical-impact-text"; then test-ok "$backend logical consumer impact chain"; else test-fail "$backend logical impact relationship omitted"; fi
  "${runner[@]}" do outputs --json -c $'rule ./out/{name} : ./seed\ntask deploy {mode} : ./seed\n' ./seed >"$project/boundary" 2>"$project/error"
  if jq -e '(.items | length == 0) and any(.deferred[]; .reason == "pattern-captures-unbound") and any(.deferred[]; .reason == "target-arguments-required")' "$project/boundary" >/dev/null; then test-ok "$backend unbound discovery is partial, not empty"; else test-fail "$backend missing unbound discovery boundaries"; fi
  "${runner[@]}" do outputs --output text --depth 0 -c $'rule ./out/{name} : ./seed\n' ./seed >"$project/boundary-text" 2>"$project/error"
  if grep -q 'consumer discovery · pattern-captures-unbound' "$project/boundary-text" && ! grep -q 'no declared artifacts' "$project/boundary-text"; then test-ok "$backend global boundaries have safe human labels"; else test-fail "$backend global boundary presentation"; fi
  "${runner[@]}" do outputs --json -c $'rule ./out : ./seed\nrule ./a : ./b\nrule ./b : ./a\n' ./seed >"$project/unrelated-cycle" 2>"$project/error"
  if jq -e '(.producers | length == 1) and .producers[0].target == "./out"' "$project/unrelated-cycle" >/dev/null; then test-ok "$backend unrelated cycles are outside affected work"; else test-fail "$backend unrelated cycle leaked"; fi
  status=0
  "${runner[@]}" do plan --affected --json -c $'rule ./a : ./b\nrule ./b : ./a\n' ./a >"$project/affected-cycle" 2>"$project/error" || status=$?
  if [ "$status" = 1 ] && jq -e '.type == "diagnostic" and .diagnostic.code == "DEP_CYCLE"' "$project/affected-cycle" >/dev/null; then test-ok "$backend affected cycle diagnosed"; else test-fail "$backend affected cycle was hidden"; fi
  "${runner[@]}" do outputs --json -C "$project" -f deferred.km ./seed >"$project/downstream-deferred" 2>"$project/error"
  if jq -e 'any(.deferred[]; .reason == "generated-input-unavailable") and any(.resources[]; .display == "./manifest" and (.roles | index("artifact")))' "$project/downstream-deferred" >/dev/null && [ ! -e "$project/manifest" ]; then test-ok "$backend downstream generated read stays deferred"; else test-fail "$backend generated read impact boundary"; fi
  test-step "$backend omitted generated prerequisites cannot prove downstream freshness"
  freshness="$TMPDIR/impact-freshness-$backend"
  mkdir -p "$freshness"
  printf trigger >"$freshness/trigger"
  printf one >"$freshness/source"
  cat >"$freshness/Makefile.km" <<'KM'
rule ./generated : ./source = read ./source
rule ./out : ./trigger ./generated = read ./generated
KM
  "${runner[@]}" -C "$freshness" ./out >"$project/value" 2>"$project/error"
  printf two >"$freshness/source"
  "${runner[@]}" do outputs --json -C "$freshness" ./trigger >"$project/downstream-freshness" 2>"$project/error"
  if jq -e 'any(.resources[]; .display == "./out" and .status == "unknown") and all(.producers[]; .target != "./generated")' "$project/downstream-freshness" >/dev/null && [ "$(cat "$freshness/generated")" = one ] && [ "$(cat "$freshness/out")" = one ]; then test-ok "$backend omitted producer freshness remains unverified without effects"; else test-fail "$backend downstream artifact incorrectly proves reuse"; fi
done
test-step "native/WASM normalized documents and human facts agree"
for kind in plan inputs outputs; do
  if cmp -s "$project/native.$kind.json" "$project/wasm.$kind.json"; then test-ok "$kind parity"; else test-fail "$kind parity"; fi
  if [ "$kind" != plan ]; then
    if cmp -s "$project/native.$kind.text" "$project/wasm.$kind.text"; then test-ok "$kind human parity"; else test-fail "$kind human parity"; fi
  fi
done
if cmp -s "$project/native.deferred" "$project/wasm.deferred" && cmp -s "$project/native.text" "$project/wasm.text"; then test-ok "deferred and human parity"; else test-fail "deferred or human parity"; fi
for kind in impact affected impact-text logical-impact-text; do
  # Root-specific paths differ, so canonicalize only the controlled fixture root.
  sed "s|$TMPDIR/impact-native|IMPACT|g" "$project/native.$kind" >"$project/native.normalized"
  sed "s|$TMPDIR/impact-wasm|IMPACT|g" "$project/wasm.$kind" >"$project/wasm.normalized"
  if cmp -s "$project/native.normalized" "$project/wasm.normalized"; then test-ok "$kind parity"; else test-fail "$kind parity"; fi
done
test-end
