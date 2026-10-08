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
	printf '%s:%s' "$HOST" '@(port)'
serve {address=localhost} :
	printf '%s:%s:%s' "$HOST" '@(port)' '@(address)'
KMK
arguments="$TMPDIR/default-arguments"
mkdir -p "$arguments"
cat >"$arguments/Makefile.kmk" <<'KMK'
default {port=3000} :
	printf '%s:%s' "$HOST" '@(port)'
KMK
cat >"$project/Makefile.kmk" <<'KMK'
SDK ?= "./default"
SDK ?= (read "must-not-be-read")
normal = "authored"
default :
	@(out SDK)
KMK
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
  "${runner[@]}" -f "$shorthand/Makefile.kmk" env.HOST=session port=8004 >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = 'session:8004' ]; then test-ok "$backend explicit source parameter shorthand"; else test-fail "$backend explicit source shorthand"; fi
  "${runner[@]}" do run -c 'port = "authored"' -c 'port' port=literal.km >"$project/value" 2>"$project/error"
  if [ "$(cat "$project/value")" = '"literal.km"' ]; then test-ok "$backend value-source literal parameter preserves statement execution"; else test-fail "$backend value-source parameter"; fi
 "${runner[@]}" -C "$project" default >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = ./default ]; then test-ok "$backend lazy first default"; else test-fail "$backend default"; fi
 "${runner[@]}" do run --lang km -c 'SDK ?= "default"' -c 'SDK' --define SDK=session >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = '"session"' ]; then test-ok "$backend unified session override"; else test-fail "$backend session override"; fi
 KAME_SDK=environment "${runner[@]}" -C "$project" default >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = environment ]; then test-ok "$backend environment convention"; else test-fail "$backend environment"; fi
 KAME_SDK=environment "${runner[@]}" -C "$project" --define SDK=first --define='SDK=@(missing)=literal' default >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = '@(missing)=literal' ]; then test-ok "$backend repeated CLI precedence and literal data"; else test-fail "$backend precedence"; fi
 "${runner[@]}" do cat -C "$project" --define normal=override normal >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = '"override"' ]; then test-ok "$backend ordinary definition override"; else test-fail "$backend value override"; fi
  "${runner[@]}" do plan --json -C "$project" --define SDK=/opt/wasi-sdk default >"$project/plan" 2>"$project/error"
 if python3 - "$project/plan" <<'PY'
import json,sys
assert json.load(open(sys.argv[1]))['producers'][0]['configuration']['SDK'] == '/opt/wasi-sdk'
PY
 then test-ok "$backend effective plan value"; else test-fail "$backend plan configuration"; fi
 "${runner[@]}" do cat -C "$project" --define SDK= SDK >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = '""' ]; then test-ok "$backend empty string override"; else test-fail "$backend empty override"; fi
 status=0
 "${runner[@]}" -C "$project" --define missing=value default >"$project/value" 2>"$project/error" || status=$?
 if [ "$status" = 1 ] && grep -q DEF_INVALID "$project/error" && [ ! -s "$project/value" ]; then test-ok "$backend unknown override before effects"; else test-fail "$backend unknown override"; fi
 "${runner[@]}" do fmt --lang script "$project/Makefile.kmk" >"$project/formatted" 2>"$project/error"
 if grep -q 'SDK ?=' "$project/formatted"; then test-ok "$backend formatter retains defaults"; else test-fail "$backend formatting"; fi
done
test-end
