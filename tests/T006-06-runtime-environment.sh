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
