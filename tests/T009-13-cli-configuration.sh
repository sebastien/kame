#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — Definitions
# Spec: docs/spec/009-cli.md — Build configuration
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T009-13 build definition configuration"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/configuration"
mkdir -p "$project"
shorthand="$TMPDIR/shorthand"
mkdir -p "$shorthand"
cat >"$shorthand/Makefile.kmk" <<'KMK'
port ?= "3000"
default :
	printf '%s:%s' "$HOST" '@{port}'
serve {address=localhost} :
	printf '%s:%s:%s' "$HOST" '@{port}' '@{address}'
KMK
arguments="$TMPDIR/default-arguments"
mkdir -p "$arguments"
cat >"$arguments/Makefile.kmk" <<'KMK'
default {port=3000} :
	printf '%s:%s' "$HOST" '@{port}'
KMK
cat >"$project/Makefile.kmk" <<'KMK'
SDK ?= "./default"
SDK ?= (read "must-not-be-read")
normal = "authored"
default :
	@(out! SDK)
KMK
typed="$TMPDIR/typed"
mkdir -p "$typed"
cat >"$typed/Makefile.kmk" <<'KMK'
FLAG = :true
SDK_MODULES = []
VALUE = :nil
(if FLAG (include ./must-not-be-loaded.kmk))
default : ; [shell: kash env: [KAME_SDK_MODULES: "ambient" MODE: "per-rule"]]
	(out! (json [flag: FLAG modules: SDK_MODULES member: (has? "py" SDK_MODULES) blank: (blank? "") path: (joinpath ./sdk "src")]))
@(joinpath ./build (join "-" SDK_MODULES)) : ; [shell: kash]
	printf artifact > @>
@(joinpath ./build "joined") : @(joinpath ./ "input") ; [shell: kash]
	cat @< > @>
./typed-output : ./input ; [shell: kash]
	(yield! (json VALUE))
KMK
printf input >"$typed/input"
for backend in native wasm; do
 test-step "$backend build defaults and literal overrides"
  if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
  HOST=inherited "${runner[@]}" -C "$shorthand" env.HOST=0.0.0.0 port=8000 >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = '0.0.0.0:8000' ]; then test-ok "$backend assignment-only default and environment replacement"; else test-fail "$backend shorthand default"; fi
  "${runner[@]}" -C "$shorthand" serve env.HOST=local port=8001 address=remote >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = 'local:8001:remote' ]; then test-ok "$backend environment, global definition and target argument"; else test-fail "$backend mixed assignments"; fi
  "${runner[@]}" -C "$shorthand" env.HOST= port=first port='@(missing)=literal' >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = ':@(missing)=literal' ]; then test-ok "$backend empty environment and repeated literal parameters"; else test-fail "$backend literal assignments"; fi
  "${runner[@]}" -C "$arguments" env.HOST=default port=8002 >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = 'default:8002' ]; then test-ok "$backend implicit default target argument"; else test-fail "$backend default argument"; fi
  "${runner[@]}" -C "$shorthand" --dry-run env.HOST=dry port=8003 >"$project/value" 2>"$project/error"
  if [ ! -s "$project/value" ]; then test-ok "$backend shorthand dry-run has no effects"; else test-fail "$backend shorthand dry-run"; fi
  status=0
  "${runner[@]}" -C "$shorthand" env.=invalid >"$project/value" 2>"$project/error" || status=$?
  if [ "$status" = 2 ] && grep -q OPT_VALUE_INVALID "$project/error"; then test-ok "$backend invalid explicit environment name"; else test-fail "$backend invalid env assignment"; fi
  status=0
  "${runner[@]}" -C "$shorthand" env.HOST=local unknown=value >"$project/value" 2>"$project/error" || status=$?
  if [ "$status" = 1 ] && [ ! -s "$project/value" ]; then test-ok "$backend unknown parameter before default effects"; else test-fail "$backend unknown parameter"; fi
  "${runner[@]}" -C "$shorthand" -f "$shorthand/Makefile.kmk" env.HOST=session port=8004 >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = 'session:8004' ]; then test-ok "$backend explicit source parameter shorthand"; else test-fail "$backend explicit source shorthand"; fi
  "${runner[@]}" do run --print-result -c 'port = "authored"' -c 'port' port=literal.km >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = '"literal.km"' ]; then test-ok "$backend value-source literal parameter preserves statement execution"; else test-fail "$backend value-source parameter"; fi
 "${runner[@]}" -C "$project" default >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = ./default ]; then test-ok "$backend lazy first default"; else test-fail "$backend default"; fi
 "${runner[@]}" do run --print-result --lang km -c 'SDK ?= "default"' -c 'SDK' --define SDK=session >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = '"session"' ]; then test-ok "$backend unified session override"; else test-fail "$backend session override"; fi
 KAME_SDK=environment "${runner[@]}" -C "$project" default >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = environment ]; then test-ok "$backend environment convention"; else test-fail "$backend environment"; fi
 KAME_SDK=environment "${runner[@]}" -C "$project" --define SDK=first --define='SDK=@(missing)=literal' default >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = '@(missing)=literal' ]; then test-ok "$backend repeated CLI precedence and literal data"; else test-fail "$backend precedence"; fi
 "${runner[@]}" do cat -C "$project" --define normal=override normal >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = 'override' ]; then test-ok "$backend ordinary definition override"; else test-fail "$backend value override"; fi
  "${runner[@]}" do plan --json -C "$project" --define SDK=/opt/wasi-sdk default >"$project/plan" 2>"$project/error"
 if python3 - "$project/plan" <<'PY'
import json,sys
assert json.load(open(sys.argv[1]))['producers'][0]['configuration']['SDK'] == '/opt/wasi-sdk'
PY
 then test-ok "$backend effective plan value"; else test-fail "$backend plan configuration"; fi
 "${runner[@]}" do cat -C "$project" --define SDK= SDK >"$project/value" 2>"$project/error"
 if [ ! -s "$project/value" ]; then test-ok "$backend empty string override"; else test-fail "$backend empty override"; fi
 status=0
 "${runner[@]}" -C "$project" --define missing=value default >"$project/value" 2>"$project/error" || status=$?
 if [ "$status" = 1 ] && grep -q DEF_INVALID "$project/error" && [ ! -s "$project/value" ]; then test-ok "$backend unknown override before effects"; else test-fail "$backend unknown override"; fi
 "${runner[@]}" do fmt --lang script "$project/Makefile.kmk" >"$project/formatted" 2>"$project/error"
  if grep -q 'SDK ?=' "$project/formatted"; then test-ok "$backend formatter retains defaults"; else test-fail "$backend formatting"; fi
  test-step "$backend typed overrides, per-rule Kash metadata, and SDK primitives"
  KAME_FLAG=ambient KAME_SDK_MODULES=ambient "${runner[@]}" -C "$typed" --set FLAG=:false --set 'SDK_MODULES=["py" "js"]' default >"$project/value" 2>"$project/error"
  if jq -e '.flag == false and .modules == ["py", "js"] and .member == true and .blank == true and .path == "./sdk/src"' "$project/value" >/dev/null; then test-ok "$backend typed values survive scoped environments and conditional includes"; else test-fail "$backend typed configuration"; cat "$project/value" "$project/error"; fi
  "${runner[@]}" do plan --json -C "$typed" --set FLAG=:false --set 'SDK_MODULES=["py" "js"]' ./build/py-js >"$project/plan" 2>"$project/error"
  if jq -e '.producers[0].kind == "file" and .producers[0].configuration.FLAG == false and .producers[0].configuration.SDK_MODULES == ["py", "js"]' "$project/plan" >/dev/null; then test-ok "$backend typed planning retains native JSON and computed file targets"; else test-fail "$backend typed planning"; cat "$project/plan" "$project/error"; fi
  "${runner[@]}" -C "$typed" --set=FLAG=:false ./build/joined >"$project/value" 2>"$project/error"
  if [ "$(cat "$typed/build/joined")" = input ]; then test-ok "$backend joinpath outputs and prerequisites materialize as concrete files"; else test-fail "$backend joined file rule"; fi
  "${runner[@]}" -C "$typed" --set FLAG=:false --define VALUE=:false ./typed-output >"$project/value" 2>"$project/error"
  if [ "$(cat "$typed/typed-output")" = '":false"' ]; then test-ok "$backend literal configuration produces a string file value"; else test-fail "$backend initial configuration output"; fi
  "${runner[@]}" -C "$typed" --set FLAG=:false --set VALUE=:false ./typed-output >"$project/value" 2>"$project/error"
  if [ "$(cat "$typed/typed-output")" = 'false' ]; then test-ok "$backend changing only override type invalidates file freshness"; else test-fail "$backend typed configuration reused a stale string"; fi
  "${runner[@]}" -C "$typed" --set FLAG=:false --define VALUE=:false ./typed-output >"$project/value" 2>"$project/error"
  if [ "$(cat "$typed/typed-output")" = '":false"' ]; then test-ok "$backend changing back to literal configuration invalidates freshness"; else test-fail "$backend literal configuration reused a stale boolean"; fi
  "${runner[@]}" do run --lang km -c 'value = :nil' value --set 'value=[a: :false b: [1 1.5 :nil ""]]' >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = '[a: :false b: [1 1.5 :nil ""]]' ]; then test-ok "$backend typed record and nested scalar literals"; else test-fail "$backend typed record"; cat "$project/value" "$project/error"; fi
  "${runner[@]}" do run --lang km -c 'value = :nil' value --set value=:false --define value=:true >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = '":true"' ]; then test-ok "$backend later literal option retains string semantics"; else test-fail "$backend mixed override order"; fi
  "${runner[@]}" do run --lang km -c 'value = :nil' value --define value=:true --set value=:false >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = ':false' ]; then test-ok "$backend later typed option retains boolean semantics"; else test-fail "$backend mixed typed override order"; fi
  "${runner[@]}" do run --lang km -c 'value = :nil' value --set 'value=[]' >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = '[]' ]; then test-ok "$backend empty typed list"; else test-fail "$backend empty typed value"; fi
  "${runner[@]}" do run --lang km -c 'value = :nil' value --set 'value="""$(touch forbidden) @(missing)"""' >"$project/value" 2>"$project/error"
  if grep -Fq '$(touch forbidden) @(missing)' "$project/value" && [ ! -e "$CLI_ROOT/forbidden" ]; then test-ok "$backend typed verbatim data remains inert"; else test-fail "$backend typed data was reparsed"; fi
  for value in '(out! "forbidden")' '$(printf forbidden)' '[1 (env "HOME")]' 'missing' '"@(missing)"' './*' '[x: 1 x: 2]' ''; do
    status=0
    "${runner[@]}" do run --lang km -c '(out! "forbidden")' --set "value=$value" >"$project/value" 2>"$project/error" || status=$?
    if [ "$status" = 2 ] && grep -q OPT_VALUE_INVALID "$project/error" && [ ! -s "$project/value" ]; then test-ok "$backend rejects executable typed input before effects"; else test-fail "$backend typed validation ($status)"; cat "$project/value" "$project/error"; fi
  done
   test-step "$backend removed typed option recovery"
  for removed in --define-value --define-value=VALUE=:false; do
    for format in human plain json; do
      status=0
      options=(--color=never --diagnostic-format="$format")
      report="$project/error"
      if [ "$format" = json ]; then options=(--json); report="$project/value"; fi
      "${runner[@]}" "${options[@]}" do run --lang km -c '(out! "forbidden")' "$removed" >"$project/value" 2>"$project/error" || status=$?
      if [ "$status" = 2 ] && grep -q OPT_UNKNOWN "$report" && grep -q -- '--set NAME=LITERAL' "$report" && { [ "$format" = json ] || [ ! -s "$project/value" ]; }; then test-ok "$backend removed option gives $format recovery before effects"; else test-fail "$backend removed option recovery ($format/$status)"; cat "$project/value" "$project/error"; fi
   done
   test-step "$backend authored setting checks are lazy, pure and name constraints without disclosing values"
   checks=$'ITEMS ?= []\n(checked-items!) = (if (list? ITEMS) (if (= (count (filter-out string? ITEMS)) 0) ITEMS (fail! "ITEMS must contain strings")) (fail! "ITEMS must be a list"))\n'
   "${runner[@]}" do run --print-result --lang km -c "$checks" -c '(checked-items!)' --set 'ITEMS=["two words" ""]' >"$project/value" 2>"$project/error"
   if [ "$(cat "$project/value")" = '["two words" ""]' ]; then test-ok "$backend checked list retains element boundaries"; else test-fail "$backend checked list"; fi
   "${runner[@]}" do run --print-result --lang km -c "$checks" -c ITEMS --set ITEMS=42 >"$project/value" 2>"$project/error"
   if [ "$(cat "$project/value")" = 42 ]; then test-ok "$backend valid literal is not an inferred setting schema"; else test-fail "$backend validation became eager"; fi
   for value in '"validation-secret"' '[1]'; do
     status=0
     "${runner[@]}" do run --lang km -c "$checks" -c '(checked-items!)' --set "ITEMS=$value" >"$project/value" 2>"$project/error" || status=$?
     if [ "$status" = 1 ] && grep -q USER_ERROR "$project/error" && grep -q 'ITEMS must' "$project/error" && ! grep -q validation-secret "$project/error"; then test-ok "$backend demanded check reports constraint, not rejected data"; else test-fail "$backend authored constraint ($value)"; cat "$project/error"; fi
   done
   for registration in $'(if (> (count (checked-items!)) 0) (include ./must-not-be-loaded.kmk))\ndefault :\n' $'@(joinpath ./build (join "-" (checked-items!))) :\n'; do
     status=0
     "${runner[@]}" do plan --json -c "$checks$registration" --set 'ITEMS="validation-secret"' default >"$project/plan" 2>"$project/error" || status=$?
     if [ "$status" = 1 ] && grep -q USER_ERROR "$project/plan" && grep -q 'ITEMS must be a list' "$project/plan" && ! grep -q validation-secret "$project/plan"; then test-ok "$backend pure registration demands authored check before work"; else test-fail "$backend pure authored check"; cat "$project/plan" "$project/error"; fi
   done
done
done
test-end
