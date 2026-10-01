#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — freestanding host ABI
# Spec: docs/spec/013-tests.md — T010-02-wasm-abi
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-02 wasm ABI hardening"
cd "$CLI_ROOT"

test-step "freestanding module imports nothing"
make wasm >/dev/null
if import_output="$(node tools/wasm/check-imports.mjs build/wasm/kame.wasm)"; then
	test-ok "$import_output"
else
	test-fail "freestanding module imported a hosted symbol"
fi

marker="$(mktemp)"
printf 'x' >"$marker"
trap 'rm -f "$marker"' EXIT

test-step "async host services exists, stat, and glob distinctly"
if exists="$(node tools/kame-wasm.mjs do expr --async -c "(exists? \"$marker\")")" && [ "$exists" = ':true' ]; then
	test-ok "exists? on a present path is true"
else
	test-fail "exists? on a present path = ${exists:-<error>}"
fi
if missing="$(node tools/kame-wasm.mjs do expr --async -c '(exists? "/nonexistent-kame-path")')" && [ "$missing" = ':false' ]; then
	test-ok "exists? on an absent path is false"
else
	test-fail "exists? on an absent path = ${missing:-<error>}"
fi
if size="$(node tools/kame-wasm.mjs do expr --async -c "(let [s (stat \"$marker\")] s.size)")" && [ "$size" = "1" ]; then
	test-ok "stat returns a record with a size field"
else
	test-fail "stat size = ${size:-<error>}"
fi
if glob="$(node tools/kame-wasm.mjs do expr --async -c '(wildcard "*.md")' 2>&1)"; then
	test-fail "unimplemented glob capability unexpectedly succeeded: $glob"
elif printf '%s' "$glob" | grep -q 'FEATURE_UNSUP'; then
	test-ok "glob reports FEATURE_UNSUP honestly"
else
	test-fail "glob diagnostic = $glob"
fi

test-step "handles are generational and completion is validated"
if node --input-type=module - <<'NODE'; then
const { readFile } = await import('node:fs/promises');
const bytes = await readFile('./build/wasm/kame.wasm');
const { instance } = await WebAssembly.instantiate(bytes, {});
const { exports } = instance;
const alloc = (size, alignment = 1) => {
  const pointer = exports.kame_wasm_alloc(size, alignment);
  if (pointer === 0) throw new Error(`allocation failed: ${size}`);
  return pointer;
};
const write = (text) => {
  const data = new TextEncoder().encode(text);
  const pointer = alloc(data.length || 1);
  new Uint8Array(exports.memory.buffer, pointer, data.length).set(data);
  return [pointer, data.length];
};

// A stale instance handle is rejected after free.
const stale = exports.kame_wasm_instance_create();
if (stale === 0n || exports.kame_wasm_instance_free(stale) !== 0 || exports.kame_wasm_instance_free(stale) !== 1) {
  throw new Error('instance handles are not generational');
}

// Distinct kinds: stat=5, glob=6, exists=7.
const kinds = [
  ['(stat "x")', 5],
  ['(wildcard "*.x")', 6],
  ['(exists? "x")', 7],
];
for (const [expression, expected] of kinds) {
  const handle = exports.kame_wasm_instance_create();
  if (exports.kame_wasm_source_compile(handle, 0, 0) !== 0) throw new Error('compile failed');
  const [text, length] = write(expression);
  if (exports.kame_wasm_expression_begin(handle, text, length) !== 0) throw new Error(`begin failed: ${expression}`);
  if (exports.kame_wasm_step(handle) !== 1) throw new Error(`no request: ${expression}`);
  const kind = exports.kame_wasm_next_request_kind(handle);
  if (kind !== expected) throw new Error(`request kind ${kind}, want ${expected}: ${expression}`);
  if (exports.kame_wasm_expression_cancel(handle) !== 0) throw new Error('cancel failed');
  if (exports.kame_wasm_instance_free(handle) !== 0) throw new Error('free failed');
}

// Structured completions cross the ABI as canonical JSON.
const jsonInstance = exports.kame_wasm_instance_create();
if (exports.kame_wasm_source_compile(jsonInstance, 0, 0) !== 0) throw new Error('compile failed');
const [existsExpr, existsLen] = write('(exists? "x")');
if (exports.kame_wasm_expression_begin(jsonInstance, existsExpr, existsLen) !== 0) throw new Error('begin failed');
if (exports.kame_wasm_step(jsonInstance) !== 1) throw new Error('no exists request');
const header = alloc(48, 8);
if (exports.kame_wasm_next_event_header(jsonInstance, header, 48) !== 0) throw new Error('header unavailable');
const request = new DataView(exports.memory.buffer, header, 48).getBigUint64(20, true);
const [json, jsonLength] = write('true');
if (exports.kame_wasm_complete_json(jsonInstance, request, json, jsonLength) !== 0) throw new Error('json completion failed');
let state = 0;
for (let i = 0; i < 5 && state !== 2; i++) state = exports.kame_wasm_step(jsonInstance);
if (state !== 2) throw new Error('json expression did not complete');
const outLength = alloc(4, 4);
if (exports.kame_wasm_result_copy(jsonInstance, 0, 0, outLength) !== 3) throw new Error('result length was not queried');
const resultLength = new DataView(exports.memory.buffer, outLength, 4).getUint32(0, true);
const output = alloc(resultLength || 1);
if (exports.kame_wasm_result_copy(jsonInstance, output, resultLength, outLength) !== 0) throw new Error('result copy failed');
if (new TextDecoder().decode(new Uint8Array(exports.memory.buffer, output, resultLength)) !== ':true') throw new Error('json completion value was not preserved');
if (exports.kame_wasm_instance_free(jsonInstance) !== 0) throw new Error('json instance did not free');

// Instance diagnostics are isolated to their instance.
const instanceA = exports.kame_wasm_instance_create();
const instanceB = exports.kame_wasm_instance_create();
const [badSource, badLength] = write('result = (');
if (exports.kame_wasm_source_compile(instanceA, badSource, badLength) !== 5) throw new Error('malformed source did not diagnose');
if (exports.kame_wasm_instance_diagnostic_length(instanceA) === 0) throw new Error('instance A had no diagnostic');
if (exports.kame_wasm_instance_diagnostic_length(instanceB) !== 0) throw new Error('instance B observed instance A diagnostic');
const diagnosticLength = exports.kame_wasm_instance_diagnostic_length(instanceA);
const diagnosticPointer = alloc(diagnosticLength || 1);
if (exports.kame_wasm_instance_diagnostic_copy(instanceA, diagnosticPointer, diagnosticLength) !== 0) throw new Error('instance diagnostic copy failed');
if (!new TextDecoder().decode(new Uint8Array(exports.memory.buffer, diagnosticPointer, diagnosticLength)).includes('PARSE_ERR')) throw new Error('unexpected instance diagnostic');
exports.kame_wasm_instance_free(instanceA);
exports.kame_wasm_instance_free(instanceB);
NODE
	test-ok "handles, kinds, JSON completion, and diagnostics behave"
else
	test-fail "raw ABI assertions failed"
fi

test-end
