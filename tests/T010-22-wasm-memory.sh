#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — allocation-free exhaustion diagnostics
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T010-22 WASM memory exhaustion"
cd "$CLI_ROOT"
make wasm >/dev/null
test-step "allocator failures unwind without imports, preserve diagnostics and isolate instances"
if node --input-type=module <<'NODE'; then
import fs from 'node:fs';
import assert from 'node:assert/strict';
const module = new WebAssembly.Module(fs.readFileSync('build/wasm/kame.wasm'));
assert.deepEqual(WebAssembly.Module.imports(module), []);
const { exports: e } = new WebAssembly.Instance(module, {});
const alloc = (length, alignment = 1) => {
  const pointer = e.kame_wasm_alloc(length || 1, alignment);
  assert.notEqual(pointer, 0);
  return pointer;
};
const write = (text) => {
  const bytes = Buffer.from(text), pointer = alloc(bytes.length);
  new Uint8Array(e.memory.buffer, pointer, bytes.length).set(bytes);
  return [pointer, bytes.length];
};
// Reserve diagnostic scratch before exhausting either allocator.
const diagnostic = alloc(256), length = alloc(4, 4), output = alloc(128);
const instanceDiagnostic = (handle) => {
  const size = e.kame_wasm_instance_diagnostic_length(handle);
  assert.equal(e.kame_wasm_instance_diagnostic_copy(handle, diagnostic, 256), 0);
  return Buffer.from(e.memory.buffer, diagnostic, size).toString();
};
const healthy = e.kame_wasm_instance_create();
assert.equal(e.kame_wasm_instance_set_heap_limit(healthy, 0), 4);
assert.equal(e.kame_wasm_instance_set_heap_limit(healthy, 16 * 1024 * 1024 + 1), 4);
assert.equal(e.kame_wasm_source_compile(healthy, 0, 0), 0);
assert.equal(e.kame_wasm_instance_set_heap_limit(healthy, 512), 4);
// Repeatedly reuse the same slot; recovery must not leak handles or arena state.
for (let i = 0; i < 100; i++) {
  const handle = e.kame_wasm_instance_create();
  assert.notEqual(handle, 0n);
  assert.equal(e.kame_wasm_instance_set_heap_limit(handle, 512), 0);
  assert.equal(e.kame_wasm_source_compile(handle, 0, 0), 2);
  assert.match(instanceDiagnostic(handle), /^NO_MEMORY: instance heap exhausted$/);
  assert.equal(e.kame_wasm_source_compile(handle, 0, 0), 2);
  assert.equal(e.kame_wasm_step(handle), 2);
  assert.equal(e.kame_wasm_result_copy(handle, output, 128, length), 2);
  assert.equal(e.kame_wasm_instance_free(handle), 0);
  assert.equal(e.kame_wasm_instance_free(handle), 1);
}
assert.equal(e.kame_wasm_instance_diagnostic_length(healthy), 0);
assert.equal(e.kame_wasm_expression_begin(healthy, ...write('42')), 0);
assert.equal(e.kame_wasm_step(healthy), 2);
assert.equal(e.kame_wasm_result_copy(healthy, output, 128, length), 0);
assert.equal(Buffer.from(e.memory.buffer, output, 2).toString(), '42');
assert.equal(e.kame_wasm_instance_free(healthy), 0);
// Exhaust the engine while copying an actual asynchronous host completion.
const handle = e.kame_wasm_instance_create();
assert.equal(e.kame_wasm_instance_set_heap_limit(handle, 65536), 0);
assert.equal(e.kame_wasm_source_compile(handle, 0, 0), 0);
assert.equal(e.kame_wasm_expression_begin(handle, ...write('(read "./input")')), 0);
assert.equal(e.kame_wasm_step(handle), 1);
const header = alloc(48, 8);
assert.equal(e.kame_wasm_next_event_header(handle, header, 48), 0);
const request = new DataView(e.memory.buffer).getBigUint64(header + 20, true);
assert.equal(e.kame_wasm_complete_bytes(handle, request, alloc(1024 * 1024), 1024 * 1024), 2);
assert.match(instanceDiagnostic(handle), /^NO_MEMORY:/);
assert.equal(e.kame_wasm_complete_nil(handle, request), 2);
assert.equal(e.kame_wasm_instance_free(handle), 0);
// Optimized evaluator frames must reach the language guard before the C stack
// is exhausted. Destruction and slot reuse must remain valid after EVAL_DEPTH.
const recursiveSource = write('(walk TEXT) = (if (= (count TEXT) 0) 0 (character TEXT))\n(character TEXT) = (walk (slice 1 TEXT))\n');
for (const [expression, code] of [
  ['(walk "x,y\\n")', ''],
  ['(walk (reduce ([TEXT STEP] (str TEXT TEXT)) "x,y\\n" [1 2 3 4 5 6 7 8 9 10]))', 'EVAL_DEPTH'],
  ['(walk "x,y\\n")', ''],
]) {
  const bounded = e.kame_wasm_instance_create();
  assert.notEqual(bounded, 0n);
  assert.equal(e.kame_wasm_source_compile(bounded, ...recursiveSource), 0);
  assert.equal(e.kame_wasm_expression_begin(bounded, ...write(expression)), 0);
  let state = 0;
  for (let i = 0; i < 100 && state !== 2; i++) state = e.kame_wasm_step(bounded);
  assert.equal(state, 2);
  // Result retrieval materializes ordinary evaluation diagnostics at the ABI.
  const copied = e.kame_wasm_result_copy(bounded, output, 128, length);
  if (code) {
    assert.equal(copied, 5);
    assert.match(instanceDiagnostic(bounded), /^EVAL_DEPTH:/);
  } else {
    assert.equal(copied, 0);
    assert.equal(new DataView(e.memory.buffer).getUint32(length, true), 1);
    assert.equal(Buffer.from(e.memory.buffer, output, 1).toString(), '0');
    assert.equal(e.kame_wasm_instance_diagnostic_length(bounded), 0);
  }
  assert.equal(e.kame_wasm_instance_free(bounded), 0);
}
// Temporary module-heap exhaustion must release its failed query allocations.
const oversized = write('"' + 'x'.repeat(9 * 1024 * 1024) + '"');
const small = write('42');
for (let i = 0; i < 5; i++) {
  assert.equal(e.kame_wasm_eval_pure(...oversized, output, 128, length), 2);
  assert.equal(e.kame_wasm_diagnostic_copy(diagnostic, 256), 0);
  assert.match(Buffer.from(e.memory.buffer, diagnostic, e.kame_wasm_diagnostic_length()).toString(), /^NO_MEMORY: module heap exhausted$/);
  assert.equal(e.kame_wasm_eval_pure(...small, output, 128, length), 0);
}
// An unrelated out-of-bounds access remains a trap, rather than NO_MEMORY.
assert.throws(() => e.kame_wasm_copy(0xffffffff, 1, output, 1), WebAssembly.RuntimeError);
NODE
  test-ok "compile/completion/module exhaustion recovers; other traps remain traps"
else
  test-fail "memory exhaustion ABI assertions failed"
fi
test-step "stream publisher exhaustion kills and reaps shell and argv children"
if python3 "$CLI_ROOT/tests/wasm-memory.py" "$CLI_ROOT"; then
  test-ok "stdout/stderr failures preserve NO_MEMORY after process cleanup"
else
  test-fail "process publisher exhaustion assertions failed"
fi
test-end
