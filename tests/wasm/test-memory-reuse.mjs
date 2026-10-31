import assert from 'node:assert/strict';
import { readFile, mkdtemp, writeFile, rm, cp, open } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Kame } from '../../src/js/kame.js';

const wasmPath = process.env.KAME_WASM ?? 'build/wasm/kame.wasm';
const { instance: { exports: e } } = await WebAssembly.instantiate(await readFile(wasmPath), {});
const alloc = (size, alignment = 1) => {
  const pointer = e.kame_wasm_alloc(size, alignment);
  assert.notEqual(pointer, 0);
  assert.equal(pointer % alignment, 0);
  return pointer;
};
const first = alloc(1024 * 1024, 65536);
const second = alloc(1024 * 1024, 16);
const blocker = alloc(64, 8);
assert.equal(e.kame_wasm_free(first, 1024 * 1024 - 1), 4);
assert.equal(e.kame_wasm_free(first + 1, 1024 * 1024), 4);
assert.equal(e.kame_wasm_free(first, 1024 * 1024), 0);
assert.equal(e.kame_wasm_free(first, 1024 * 1024), 4);
assert.equal(e.kame_wasm_free(second, 1024 * 1024), 0);
const pages = e.memory.buffer.byteLength;
const merged = alloc(2 * 1024 * 1024, 16);
assert.equal(e.memory.buffer.byteLength, pages, 'adjacent frees must coalesce');
assert.equal(e.kame_wasm_free(merged, 2 * 1024 * 1024), 0);
assert.equal(e.kame_wasm_free(blocker, 64), 0);
assert.equal(e.kame_wasm_host_heap_used(), 0);
assert.equal(e.kame_wasm_alloc(0xffffffff, 16), 0, 'overflowing allocation was accepted');
assert.equal(e.kame_wasm_host_heap_used(), 0, 'failed allocation corrupted accounting');
for (let i = 0; i < 1000; i++) {
  const pointer = alloc(1024 * 1024, 64);
  assert.equal(e.kame_wasm_free(pointer, 1024 * 1024), 0);
  assert.equal(e.kame_wasm_host_heap_used(), 0);
  assert.equal(e.memory.buffer.byteLength, pages, 'freed host memory was not reused');
}
let arenaPages = 0;
for (let i = 0; i < 100; i++) {
  const handle = e.kame_wasm_instance_create();
  assert.notEqual(handle, 0n);
  assert.equal(e.kame_wasm_source_compile(handle, 0, 0), 0);
  assert.ok(e.kame_wasm_instance_heap_used(handle) > 0);
  assert.ok(e.kame_wasm_instance_heap_peak(handle) < 1024 * 1024);
  assert.equal(e.kame_wasm_instance_free(handle), 0);
  assert.equal(e.kame_wasm_host_heap_used(), 0, 'instance arena was retained');
  if (i === 0) arenaPages = e.memory.buffer.byteLength;
  assert.equal(e.memory.buffer.byteLength, arenaPages, 'instance creation did not reuse freed arenas');
}

const directory = await mkdtemp(join(tmpdir(), 'kame-memory-'));
const kame = await Kame.create({ wasmPath });
try {
  // Fingerprinting a file larger than the entire instance heap must not
  // materialize it. Native/Node SHA-256 equivalence is checked separately.
  await writeFile(join(directory, 'large'), Buffer.alloc(20 * 1024 * 1024, 65));
  const program = await kame.compile('./output : ./large ; [shell: kame]\n\t"ok"\n');
  const options = { cwd: directory, grants: [{ capability: 'read', names: [directory] }, { capability: 'write', names: [directory] }] };
  await program.build('./output', options);
  assert.equal((await readFile(join(directory, 'output'), 'utf8')), 'ok');
  await program.dispose();

  // Tool tracking has internal read authority, not a user read grant. A large
  // real executable must be hashed without transporting it into WASM.
  const tool = join(directory, 'large-node');
  await cp(process.execPath, tool);
  const executable = await open(tool, 'r+');
  try { await executable.truncate(Math.max(20 * 1024 * 1024, (await executable.stat()).size)); }
  finally { await executable.close(); }
  const probe = await kame.compile(`SHELL = kash\ntask probe :\n\t${JSON.stringify(tool)} --version\n`);
  await probe.build('probe', { cwd: directory, grants: [{ capability: 'run', names: [] }] });
  await probe.dispose();

  await writeFile(join(directory, 'input'), Buffer.alloc(128 * 1024, 66));
  const input = join(directory, 'input');
  const reads = await kame.compile(`VALUE = (count (read ${JSON.stringify(input)}))\n`);
  const watch = await reads.watch('VALUE', options);
  try {
    let expectedLength = 128 * 1024;
    const settle = async () => {
      for (let step = 0; step < 10000; step++) {
        const state = watch.module.drainWatch(watch.watch);
        if (state === 1) await watch.module.serviceWatchRequest(watch.watch);
        if (watch.watch.pending.size) await Promise.all([...watch.watch.pending]);
        const snapshot = watch.snapshot();
        watch.watch.context.events.splice(0);
        if (!snapshot.busy) {
          assert.equal(snapshot.roots[0].value, String(expectedLength), JSON.stringify(snapshot));
          return;
        }
      }
      assert.fail('watch did not settle');
    };
    await settle();
    // Grow and replace scratch slots, then alternate sizes without cumulative
    // growth. Freeing only the instance's payload would not pass this test.
    for (const size of [256, 64, 128]) {
      expectedLength = size * 1024;
      await writeFile(input, Buffer.alloc(expectedLength, 66));
      watch.invalidate([{ kind: 'file', name: input }]);
      await settle();
    }
    const exports = watch.module.exports;
    const baseline = exports.kame_wasm_host_heap_used();
    const linear = exports.memory.buffer.byteLength;
    for (let cycle = 0; cycle < 600; cycle++) {
      expectedLength = [64, 128, 256][cycle % 3] * 1024;
      await writeFile(input, Buffer.alloc(expectedLength, 66));
      watch.invalidate([{ kind: 'file', name: input }]);
      await settle();
      assert.equal(exports.kame_wasm_host_heap_used(), baseline, 'watch retained per-read host buffers');
      assert.equal(exports.memory.buffer.byteLength, linear, 'watch linear memory did not plateau');
    }
    assert.ok(exports.kame_wasm_instance_heap_peak(watch.watch.instance) < 2 * 1024 * 1024, '256 KiB reads exceeded the instance budget');
    console.log(JSON.stringify({ test: 'memory-reuse', cycles: 600, hostBytes: baseline, linearBytes: linear, instancePeak: exports.kame_wasm_instance_heap_peak(watch.watch.instance) }));
  } finally { await watch.close(); }
  await reads.dispose();
} finally {
  await kame.dispose();
  await rm(directory, { recursive: true, force: true });
}
