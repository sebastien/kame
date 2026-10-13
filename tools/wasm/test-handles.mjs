// Focused long-lived raw ABI check. All loop inputs/outputs reuse host buffers:
// system-heap growth must not be confused with the separate no-op-free bump.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const bytes = await readFile(process.argv[2] ?? 'build/wasm/kame.wasm');
const { instance } = await WebAssembly.instantiate(bytes, {});
const e = instance.exports;
const encoder = new TextEncoder();
const decoder = new TextDecoder();
const input = e.kame_wasm_alloc(4096, 8);
const output = e.kame_wasm_alloc(65536, 8);
const header = e.kame_wasm_alloc(48, 8);
const length = e.kame_wasm_alloc(4, 4);
const write = (text) => {
  const data = encoder.encode(text);
  assert.ok(data.length <= 4096);
  new Uint8Array(e.memory.buffer, input, data.length).set(data);
  return [input, data.length];
};
const handle = e.kame_wasm_instance_create();
assert.notEqual(handle, 0n);
const requestHeader = () => {
  assert.equal(e.kame_wasm_next_event_header(handle, header, 48), 0);
  const view = new DataView(e.memory.buffer, header, 48);
  const request = view.getBigUint64(20, true);
  assert.ok(Number(request & 0xffffffffn) <= 10, 'handle history must stay bounded');
  return { request, root: view.getBigUint64(4, true), node: view.getBigUint64(12, true), generation: view.getBigInt64(28, true), id: e.kame_wasm_next_request_id(handle) };
};
const awaitRequest = () => {
  for (let i = 0; i < 128; i++) if (e.kame_wasm_step(handle) === 1) return requestHeader();
  assert.fail('host request was not issued');
};

let systemBaseline;
let prior;
for (let i = 0; i < 2000; i++) {
  assert.equal(e.kame_wasm_source_compile(handle, 0, 0), 0);
  if (prior) {
    assert.equal(e.kame_wasm_complete_nil(handle, prior.request), 1, 'recompile retires old requests');
    for (const child of [prior.root, prior.node]) assert.equal(e.kame_wasm_instance_free(child), 1);
  }
  assert.equal(e.kame_wasm_expression_begin(handle, ...write('(exists? "x")')), 0);
  const next = awaitRequest();
  if (prior) {
    assert.notEqual(next.root, prior.root, 'replacement root must change generation');
    assert.notEqual(next.node, prior.node);
  }
  assert.equal(e.kame_wasm_complete_json(handle, next.request, ...write('true')), 0);
  assert.equal(e.kame_wasm_complete_nil(handle, next.request), 0, 'immediate duplicate remains accepted');
  for (let step = 0; step < 128 && e.kame_wasm_step(handle) !== 2; step++) {}
  assert.equal(e.kame_wasm_step(handle), 2);
  prior = next;
  if (i === 10) systemBaseline = e.kame_wasm_system_heap_used();
  if (i > 10) assert.equal(e.kame_wasm_system_heap_used(), systemBaseline, 'recompile system budget grew');
}

assert.equal(e.kame_wasm_source_compile(handle, ...write('A = (text (read ./x))\nB = (text (read ./y))\n')), 0);
assert.equal(e.kame_wasm_set_forwarding(handle, 1), 0);
assert.equal(e.kame_wasm_watch_begin(handle, ...write('["A","B"]')), 0);
const first = awaitRequest();
assert.equal(e.kame_wasm_request_detach(handle, first.request), 0);
const second = awaitRequest();
assert.notEqual(first.id, second.id, 'raw request identity differs despite reusable slots');
assert.equal(e.kame_wasm_source_compile(handle, 0, 0), 4, 'parked/active requests block recompile');
// Completing a parked request must preserve the active request by parking it.
assert.equal(e.kame_wasm_complete_bytes(handle, first.request, ...write('first')), 0);
assert.equal(e.kame_wasm_complete_nil(handle, first.request), 0);
assert.equal(e.kame_wasm_request_attach(handle, second.request), 0);
assert.equal(e.kame_wasm_complete_bytes(handle, second.request, ...write('second')), 0);
assert.equal(e.kame_wasm_complete_nil(handle, first.request), 1, 'superseded duplicate is stale');

const watchState = () => {
  assert.equal(e.kame_wasm_watch_state(handle, output, 65536, length), 0);
  const size = new DataView(e.memory.buffer, length, 4).getUint32(0, true);
  return JSON.parse(decoder.decode(new Uint8Array(e.memory.buffer, output, size)));
};
let last;
const settle = () => {
  for (let i = 0; i < 256; i++) {
    const step = e.kame_wasm_step(handle);
    if (step === 1) {
      const next = requestHeader();
      if (last) assert.equal(e.kame_wasm_complete_nil(handle, last.request), 0, 'late duplicate must not disturb a new active request');
      const kind = e.kame_wasm_next_request_kind(handle);
      assert.ok(kind === 1 || kind === 27, `unexpected read kind ${kind}`);
      assert.equal(e.kame_wasm_complete_bytes(handle, next.request, ...write('value')), 0);
      if (last) assert.equal(e.kame_wasm_complete_nil(handle, last.request), 1);
      last = next;
    } else if (!watchState().busy) return;
  }
  assert.fail('watch did not settle');
};
settle();
const bumpBaseline = e.kame_wasm_alloc(1, 1);
systemBaseline = e.kame_wasm_system_heap_used();
let generation = 0n;
for (let i = 0; i < 2000; i++) {
  assert.equal(e.kame_wasm_watch_invalidate(handle, ...write('[{"kind":"file","name":"x"},{"kind":"file","name":"y"}]')), 0);
  settle();
  const snapshot = watchState();
  assert.equal(snapshot.roots.length, 2, 'watch retains both roots');
  assert.equal(snapshot.busy, false);
  assert.ok(snapshot.roots.every((root) => root.value === '"value"'));
  assert.ok(last.generation > generation, 'invalidation advances request generation');
  generation = last.generation;
  assert.equal(e.kame_wasm_system_heap_used(), systemBaseline, 'watch exhausted fixed system budget');
}
assert.equal(e.kame_wasm_alloc(1, 1), bumpBaseline + 1, 'loop must not grow host input bump');
assert.ok(systemBaseline < 8 * 1024 * 1024);
assert.equal(e.kame_wasm_watch_invalidate(handle, ...write('[{"kind":"file","name":"x"},{"kind":"file","name":"y"}]')), 0);
const cancelledParked = awaitRequest();
assert.equal(e.kame_wasm_request_detach(handle, cancelledParked.request), 0);
const cancelledActive = awaitRequest();
assert.equal(e.kame_wasm_watch_cancel(handle), 0);
assert.equal(e.kame_wasm_complete_nil(handle, cancelledActive.request), 0, 'cancelled active request keeps its late-completion window');
assert.equal(e.kame_wasm_request_attach(handle, cancelledParked.request), 0, 'cancellation must preserve detached host identity until host finishes');
assert.equal(e.kame_wasm_complete_nil(handle, cancelledParked.request), 0);
assert.equal(e.kame_wasm_complete_nil(handle, cancelledActive.request), 1);
assert.equal(e.kame_wasm_instance_free(handle), 0);
console.log(`PASS bounded handles: 2000 recompiles, 2000 two-root watch invalidations; system heap ${systemBaseline}/8388608 bytes; host bump +1 probe byte`);
