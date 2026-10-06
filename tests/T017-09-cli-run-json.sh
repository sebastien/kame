#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-09 runner JSON parity"
test-step "build artifacts"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/json"
mkdir -p "$work"
printf 'name = "Ada"\n' >"$work/values.km"
printf 'include ./values.km\nname\n' >"$work/includes.km"
printf 'build :\n\tprintf rule\n' >"$work/rules.kmk"
printf 'name = [\n' >"$work/invalid.km"

parity() {
	local expected="$1"; shift
	local native=0 wasm=0
	"$CLI_BIN" do run --json "$@" >"$work/native" 2>"$work/native.err" || native=$?
	node "$CLI_ROOT/dist/kame.js" do run --json "$@" >"$work/wasm" 2>"$work/wasm.err" || wasm=$?
	jq -cS 'del(.node,.request,.generation,.attempt,.runtimeMS)' "$work/native" >"$work/native.norm"
	jq -cS 'del(.node,.request,.generation,.attempt,.runtimeMS)' "$work/wasm" >"$work/wasm.norm"
	if [ "$native" = "$expected" ] && [ "$wasm" = "$expected" ] && [ ! -s "$work/native.err" ] && [ ! -s "$work/wasm.err" ] && cmp -s "$work/native.norm" "$work/wasm.norm"; then test-ok "$*"; else test-fail "JSON parity: native=$native wasm=$wasm"; diff -u "$work/native.norm" "$work/wasm.norm" || true; cat "$work/native.err" "$work/wasm.err"; fi
	if jq -e -s 'all(.schema == 1 and (.type | type == "string"))' "$work/native" >/dev/null; then test-ok "schema-1 JSON Lines only"; else test-fail "raw output escaped the event stream"; fi
}

test-step "values, selected definitions, output effects and process streams"
parity 0 -c 'name = "Ada"' -c '(out "notice")' -c '(err "warning")' -c 'name'
parity 0 "$work/values.km" name name
parity 0 -l kash -c 'printf bytes' -l km -c '42'
parity 0 -l kash -c 'printf "\\377"' -l expr -c ':nil'
if jq -e -s 'any(.type == "stdout" and .encoding == "base64" and .data == "/w==") and any(.type == "target-value" and .value.kind == "nil")' "$work/native" >/dev/null; then test-ok "binary streams and nil values are represented structurally"; else test-fail "binary/nil JSON encoding"; fi
parity 0 "$work/rules.kmk" build -c '42'
parity 0 "$work/includes.km"

test-step "runtime failures preserve metadata without fabricated values"
parity 1 -l kash -c '/bin/sh -c "printf secret; exit 7"' -l expr -c '42'
if jq -e -s 'any(.type == "target-failed" and .diagnostic.cause.status == 7) and (all(.type != "target-value")) and (all((.diagnostic.cause | has("stdout")) | not))' "$work/native" >/dev/null; then test-ok "failure retains exit status without captured diagnostic output"; else test-fail "failure event policy"; fi
parity 1 -c '$(printf denied)'
parity 1 --allow-run --capture-limit 1 -c '$(printf abc)'

test-step "preflight errors stay on stdout and run no earlier effects"
parity 1 -c '(out "must-not-run")' -l expr -c '['
parity 1 -c '(out "must-not-run")' -c 'name = 1' -c 'name = 2'
parity 1 -c '(out "must-not-run")' "$work/invalid.km"
parity 1 -c '(out "must-not-run")' "$work/missing.km"
test-end
