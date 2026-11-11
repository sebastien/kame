import { instantiateReactor } from './instantiate-reactor.mjs';
// Read-only presentation ABI: query/copy, lifetime, and bounded scratch memory.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const { instance } = await instantiateReactor(await readFile(process.argv[2] ?? 'dist/kame.wasm'));
const e = instance.exports;
const handle = e.kame_wasm_instance_create();
assert.notEqual(handle, 0n);
const encoder = new TextEncoder(), decoder = new TextDecoder();
const input = e.kame_wasm_alloc(4096, 1);
const output = e.kame_wasm_alloc(4096, 1);
const length = e.kame_wasm_alloc(4, 4);
assert.ok(input && output && length);
for (const invalid of ['{', '[]', '{"type":"unknown"}', '{"type":"process-started","node":"invalid"}']) {
  const rejected = e.kame_wasm_instance_create();
  assert.notEqual(rejected, 0n);
  const bytes = encoder.encode(invalid);
  new Uint8Array(e.memory.buffer, input, bytes.length).set(bytes);
  const hostUsed = e.kame_wasm_host_heap_used();
  for (let attempt = 0; attempt < 2; attempt++) {
    assert.equal(e.kame_wasm_present_observe(rejected, 1n, 0n, input, bytes.length), 5);
    assert.equal(e.kame_wasm_host_heap_used(), hostUsed, 'rejected observation retained an arena');
  }
  assert.equal(e.kame_wasm_source_compile(rejected, 0, 0), 0, 'rejected observation switched instance mode');
  assert.equal(e.kame_wasm_instance_free(rejected), 0);
}
const render = (snapshot) => {
  const bytes = encoder.encode(JSON.stringify(snapshot));
  new Uint8Array(e.memory.buffer, input, bytes.length).set(bytes);
  const query = (dst, capacity) => e.kame_wasm_present(handle, input, bytes.length, dst, capacity, length);
  assert.equal(query(0, 0), 3);
  const size = new DataView(e.memory.buffer, length, 4).getUint32(0, true);
  assert.ok(size < 4096);
  assert.equal(query(output, size), 0);
  const text = decoder.decode(new Uint8Array(e.memory.buffer, output, size));
  assert.equal(query(output, size), 0);
  assert.equal(decoder.decode(new Uint8Array(e.memory.buffer, output, size)), text);
  return text;
};

assert.equal(render({ op: 'outcome', target: 'dist', outcome: 'complete', ansi: true, elapsedMS: 123, hasTiming: true }), '[dist] 0.1s - ✓\n');
assert.equal(render({ op: 'summary', command: 'build', status: 0, ansi: true, elapsedMS: 123, counts: { completed: 1 } }), '1 complete · 0 failed  0.1s - ✓\n');
const frame = { op: 'frame', command: 'build', subject: 'dist', width: 80, height: 12, elapsedMS: 123, counts: { completed: 1 }, workers: [{ target: './out', program: 'cc', elapsedMS: 123 }] };
const text = render(frame);
assert.ok(text.startsWith('── build · workers: 1\n'));
assert.ok(text.includes('target [dist]\n'));
assert.ok(text.endsWith('0.1s - building\n'));
e.kame_wasm_collect();
const used = e.kame_wasm_system_heap_used();
for (let i = 0; i < 100; i++) assert.equal(render(frame), text);
e.kame_wasm_collect();
assert.ok(e.kame_wasm_system_heap_used() < used + 1048576, 'presentation queries retain scratch history');
const invalid = encoder.encode('{"op":"frame","width":"80","workers":[]}');
new Uint8Array(e.memory.buffer, input, invalid.length).set(invalid);
assert.equal(e.kame_wasm_present(handle, input, invalid.length, 0, 0, length), 5);
assert.equal(render(frame), text, 'bad snapshots do not poison the instance');
const observe = (event, scope = 1n, now = 1000000000n) => {
  const bytes = encoder.encode(JSON.stringify(event));
  new Uint8Array(e.memory.buffer, input, bytes.length).set(bytes);
  return e.kame_wasm_present_observe(handle, scope, now, input, bytes.length);
};
assert.equal(observe({ type: 'reset' }), 0);
const event = { type: 'target-started', node: 7, generation: 1, target: 'dist', resource: { kind: 'task', name: 'dist' } };
assert.equal(observe(event), 0);
assert.equal(observe(event), 0);
assert.deepEqual(JSON.parse(render({ op: 'counts' })), { active: 1, completed: 0, failed: 0, cancelled: 0 });
event.type = 'target-completed';
assert.equal(observe(event, 1n, 1123000000n), 0);
assert.equal(observe(event, 1n, 1123000000n), 0);
assert.equal(observe(event, 2n), 0, 'independent execution namespaces do not collide');
assert.deepEqual(JSON.parse(render({ op: 'counts' })), { active: 0, completed: 2, failed: 0, cancelled: 0 });
const trackedOutcome = { op: 'outcome', target: 'dist', outcome: 'complete', ansi: true, tracked: true, scope: '1', event };
assert.equal(render(trackedOutcome), '[dist] 0.1s - ✓\n');
assert.equal(observe({ type: 'process-started', node: 'invalid' }), 5);
assert.equal(render(trackedOutcome), '[dist] 0.1s - ✓\n', 'malformed observations do not mutate state');
assert.equal(observe({ type: 'reset' }), 0);
for (let i = 0; i < 100; i++) {
  assert.equal(observe({ ...event, type: 'target-started' }), 0);
  assert.equal(observe(event), 0);
  assert.equal(observe({ type: 'cycle' }), 0);
}
assert.equal(e.kame_wasm_source_compile(handle, 0, 0), 4, 'execution cannot overwrite a live presentation arena');
assert.equal(e.kame_wasm_instance_free(handle), 0);
assert.equal(e.kame_wasm_present_observe(handle, 1n, 0n, input, 0), 1);
assert.equal(e.kame_wasm_present(handle, input, 0, 0, 0, length), 1);
for (const [pointer, size] of [[input, 4096], [output, 4096], [length, 4]]) e.kame_wasm_free(pointer, size);
console.log('presentation ABI passed');
