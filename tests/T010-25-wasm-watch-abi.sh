#!/usr/bin/env bash
# Spec: docs/spec/020-watch.md — public watch ABI
# Spec: docs/spec/013-tests.md — T010-25-wasm-watch-abi
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T010-25 WASM watch ABI"
cd "$CLI_ROOT"
test-step "build the freestanding WASM module"
make wasm >/dev/null
test-step "watch snapshots, validation, allocation failure, isolation and disposal"
if node --input-type=module - <<'NODE'
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const bytes = await readFile('./build/wasm/kame.wasm');
const { instance } = await WebAssembly.instantiate(bytes, {});
const { exports: e } = instance;
const alloc = (size, alignment = 1) => {
  const pointer = e.kame_wasm_alloc(size, alignment);
  if (pointer === 0) throw new Error(`allocation failed: ${size}`);
  return pointer;
};
const write = (text) => {
  const bytes = new TextEncoder().encode(text);
  const pointer = alloc(bytes.length || 1);
  new Uint8Array(e.memory.buffer, pointer, bytes.length).set(bytes);
  return [pointer, bytes.length];
};
const decode = (pointer, length) => new TextDecoder().decode(new Uint8Array(e.memory.buffer, pointer, length));
const diagnostic = (handle) => {
  const length = e.kame_wasm_instance_diagnostic_length(handle);
  const pointer = alloc(length || 1);
  assert.equal(e.kame_wasm_instance_diagnostic_copy(handle, pointer, length), 0);
  return decode(pointer, length);
};
const state = (handle) => {
  const lengthPointer = alloc(4, 4);
  for (let attempt = 0; attempt < 8; attempt++) {
    assert.equal(e.kame_wasm_watch_state(handle, 0, 0, lengthPointer), 3);
    const length = new DataView(e.memory.buffer, lengthPointer, 4).getUint32(0, true);
    assert.ok(length > 0);
    const output = alloc(length);
    const copied = e.kame_wasm_watch_state(handle, output, length, lengthPointer);
    if (copied === 0) return JSON.parse(decode(output, length));
    assert.equal(copied, 4, 'snapshot copy failed for a reason other than a concurrent size change');
  }
  throw new Error('watch state changed size during every copy attempt');
};
const compile = (handle, source) => {
  const [pointer, length] = write(source);
  assert.equal(e.kame_wasm_source_compile(handle, pointer, length), 0);
};
const advanceWatch = (handle) => {
  for (let i = 0; i < 128; i++) {
    const result = e.kame_wasm_step(handle);
    if (result === 0 || result === 2) {
      if (!state(handle).busy) return;
      continue;
    }
    assert.equal(result, 1);
    const kind = e.kame_wasm_next_request_kind(handle);
    const header = alloc(48, 8);
    assert.equal(e.kame_wasm_next_event_header(handle, header, 48), 0);
    const request = new DataView(e.memory.buffer, header, 48).getBigUint64(20, true);
    if (kind === 1 || kind === 27) {
      const [data, length] = write(fileContent);
      assert.equal(e.kame_wasm_complete_bytes(handle, request, data, length), 0);
    } else if (kind === 7) {
      const [data, length] = write('true');
      assert.equal(e.kame_wasm_complete_json(handle, request, data, length), 0);
    } else {
      assert.fail(`unexpected watch request kind: ${kind}`);
    }
  }
  throw new Error(`watch did not settle: ${JSON.stringify(state(handle))}; ${diagnostic(handle)}`);
};

const constrained = e.kame_wasm_instance_create();
assert.notEqual(constrained, 0n);
assert.equal(e.kame_wasm_instance_set_heap_limit(constrained, 15500), 0);
compile(constrained, 'VALUE = 1\n');
const [constrainedTargets, constrainedTargetsLength] = write('["VALUE"]');
assert.equal(e.kame_wasm_watch_begin(constrained, constrainedTargets, constrainedTargetsLength), 0);
for (let i = 0; i < 8; i++) e.kame_wasm_step(constrained);
assert.equal(e.kame_wasm_watch_state(constrained, 0, 0, alloc(4, 4)), 2);
assert.match(diagnostic(constrained), /^NO_MEMORY: instance heap exhausted$/);
assert.equal(e.kame_wasm_instance_free(constrained), 0);

const first = e.kame_wasm_instance_create();
const second = e.kame_wasm_instance_create();
let fileContent = 'first';
assert.notEqual(first, 0n);
assert.notEqual(second, 0n);
compile(first, 'VALUE = (text (read ./input))\n');
const [cwd, cwdLength] = write('.');
assert.equal(e.kame_wasm_set_directory(first, cwd, cwdLength), 0);
assert.equal(e.kame_wasm_set_forwarding(first, 1), 0);
const [targets, targetsLength] = write('["VALUE"]');
assert.equal(e.kame_wasm_watch_begin(first, targets, targetsLength), 0);
advanceWatch(first);

const initial = state(first);
assert.equal(initial.busy, false, JSON.stringify(initial));
assert.equal(initial.roots.length, 1);
assert.equal(initial.roots[0].value, '"first"');
const observedInput = initial.resources.find((item) => item.kind === 'file' && item.name.includes('input'));
assert.ok(observedInput);
assert.equal(observedInput.missing, false);
assert.deepEqual(state(first), initial, 'repeated state queries changed or dropped root state');

fileContent = 'second';
const [change, changeLength] = write('[{"kind":"file","name":"input"}]');
assert.equal(e.kame_wasm_watch_invalidate(first, change, changeLength), 0);
advanceWatch(first);
const updated = state(first);
assert.equal(updated.roots[0].value, '"second"');
assert.ok(updated.roots[0].revision > initial.roots[0].revision);

for (const invalid of ['[{', '[{"kind":"process","name":"bad"}]']) {
  const before = state(first);
  const [payload, payloadLength] = write(invalid);
  assert.equal(e.kame_wasm_watch_invalidate(first, payload, payloadLength), 5);
  assert.match(diagnostic(first), /^PARSE_ERR:/);
  assert.deepEqual(state(first), before, 'rejected invalidation changed retained graph state');
}

compile(second, 'VALUE = "isolated"\n');
const [secondTargets, secondTargetsLength] = write('["VALUE"]');
assert.equal(e.kame_wasm_watch_begin(second, secondTargets, secondTargetsLength), 0);
advanceWatch(second);
const isolated = state(second);
assert.equal(isolated.roots[0].value, '"isolated"');
assert.equal(state(first).roots[0].value, '"second"');

const malformed = e.kame_wasm_instance_create();
assert.notEqual(malformed, 0n);
compile(malformed, 'VALUE = 1\n');
const [badTargets, badTargetsLength] = write('[');
assert.equal(e.kame_wasm_watch_begin(malformed, badTargets, badTargetsLength), 5);
assert.match(diagnostic(malformed), /^PARSE_ERR:/);
assert.equal(e.kame_wasm_watch_state(malformed, alloc(1), 1, alloc(4, 4)), 5);

assert.equal(e.kame_wasm_watch_cancel(first), 0);
assert.equal(e.kame_wasm_watch_cancel(second), 0);
assert.equal(e.kame_wasm_instance_free(first), 0);
assert.equal(e.kame_wasm_instance_free(second), 0);
assert.equal(e.kame_wasm_instance_free(malformed), 0);
assert.equal(e.kame_wasm_watch_state(first, 0, 0, alloc(4, 4)), 1);
NODE
then
	test-ok "watch ABI snapshots, rejection, exhaustion, isolation and disposal are stable"
else
	test-fail "raw WASM watch ABI assertions failed"
fi
test-end
