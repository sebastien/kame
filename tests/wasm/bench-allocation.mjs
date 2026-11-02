// Run: node tests/wasm/bench-allocation.mjs [path/to/kame.wasm]
// Report scaling rather than asserting machine-dependent timing thresholds.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { performance } from 'node:perf_hooks';

const module = await WebAssembly.compile(await readFile(process.argv[2] ?? 'build/wasm/kame.wasm'));
for (const count of [4000, 8000, 16000]) {
  const { exports: e } = await WebAssembly.instantiate(module, {});
  const pointers = [];
  const start = performance.now();
  for (let i = 0; i < count; i++) {
    const pointer = e.kame_wasm_alloc(32, 8);
    assert.notEqual(pointer, 0);
    pointers.push(pointer);
  }
  const allocated = performance.now();
  for (let i = count - 1; i >= 0; i--) assert.equal(e.kame_wasm_free(pointers[i], 32), 0);
  assert.equal(e.kame_wasm_host_heap_used(), 0);
  console.log(JSON.stringify({ benchmark: 'host-buffers', count, allocMS: allocated - start, freeMS: performance.now() - allocated }));
}

for (const count of [128, 256, 512, 1024]) {
  const { exports: e } = await WebAssembly.instantiate(module, {});
  const source = Buffer.from(Array.from({ length: count }, (_, i) => `value${i} = "value${i}"`).join('\n') + '\n');
  const pointer = e.kame_wasm_alloc(source.length, 1);
  assert.notEqual(pointer, 0);
  new Uint8Array(e.memory.buffer, pointer, source.length).set(source);
  const handle = e.kame_wasm_instance_create();
  assert.notEqual(handle, 0n);
  const start = performance.now();
  assert.equal(e.kame_wasm_source_compile(handle, pointer, source.length), 0);
  const elapsed = performance.now() - start;
  const peak = e.kame_wasm_instance_heap_peak(handle);
  assert.equal(e.kame_wasm_instance_free(handle), 0);
  assert.equal(e.kame_wasm_free(pointer, source.length), 0);
  assert.equal(e.kame_wasm_host_heap_used(), 0);
  console.log(JSON.stringify({ benchmark: 'compile-definitions', count, elapsedMS: elapsed, instancePeak: peak }));
}
