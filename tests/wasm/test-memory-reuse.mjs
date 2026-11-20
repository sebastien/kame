import { instantiateReactor } from './instantiate-reactor.mjs';
import assert from 'node:assert/strict';
import { readFile, mkdtemp, writeFile, rm, cp, open } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { Kame } from '../../src/js/kame.js';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const wasmPath = process.env.KAME_WASM ?? resolve(root, 'build/wasm/kame.wasm');
const { instance: { exports: e } } = await instantiateReactor(await readFile(wasmPath));
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
e.kame_wasm_collect();
assert.equal(e.kame_wasm_free(merged, 2 * 1024 * 1024), 0);
assert.equal(e.kame_wasm_free(blocker, 64), 0);
assert.equal(e.kame_wasm_host_heap_used(), 0);
assert.equal(e.kame_wasm_alloc(0xffffffff, 16), 0, 'overflowing allocation was accepted');
assert.equal(e.kame_wasm_host_heap_used(), 0, 'failed allocation corrupted accounting');
for (let i = 0; i < 1000; i++) {
  const pointer = alloc(1024 * 1024, 64);
  assert.equal(e.kame_wasm_free(pointer, 1024 * 1024), 0);
  assert.equal(e.kame_wasm_host_heap_used(), 0);
  if (i % 100 === 0) e.kame_wasm_collect();
  assert.ok(e.memory.buffer.byteLength < 128 * 1024 * 1024, 'buffer backing storage grew without bound');
}
let arenaPages = 0;
for (let i = 0; i < 100; i++) {
  const handle = e.kame_wasm_instance_create();
  assert.notEqual(handle, 0n);
  assert.equal(e.kame_wasm_source_compile(handle, 0, 0), 0);
  assert.equal(e.kame_wasm_instance_free(handle), 0);
  assert.equal(e.kame_wasm_host_heap_used(), 0, 'instance arena was retained');
  if (i === 0) arenaPages = e.memory.buffer.byteLength;
  if (i % 10 === 0) e.kame_wasm_collect();
  assert.ok(e.memory.buffer.byteLength < 128 * 1024 * 1024, 'retired instances retain their state');
}

// Many live buffers and arbitrary free order exercise indexed exact frees and
// both neighbors of split/coalesced blocks, not just one recycled scratch slot.
const blocks = Array.from({ length: 4096 }, (_, i) => {
  const size = 1 + i % 257;
  const pointer = alloc(size, 2 ** (i % 7));
  new Uint8Array(e.memory.buffer, pointer, size).fill(i % 251);
  return { pointer, size };
});
assert.equal(e.kame_wasm_free(0xffffffff, 1), 4);
assert.equal(e.kame_wasm_free(1, 1), 4);
for (let i = 0; i < blocks.length; i++) {
  const index = i * 97 % blocks.length;
  const { pointer, size } = blocks[index];
  assert.ok(new Uint8Array(e.memory.buffer, pointer, size).every((byte) => byte === index % 251), 'live host buffer was corrupted');
  assert.equal(e.kame_wasm_free(pointer, size + 1), 4);
  assert.equal(e.kame_wasm_free(pointer + 1, size), 4);
  assert.equal(e.kame_wasm_free(pointer, size), 0);
  assert.equal(e.kame_wasm_free(pointer, size), 4);
}
assert.equal(e.kame_wasm_host_heap_used(), 0);

// Caller-owned payload can forge a plausible header and back-reference, but
// an interior pointer must never free part of a live allocation.
const live = alloc(256, 16);
const forgedBlock = live, interior = forgedBlock + 32;
const metadata = new DataView(e.memory.buffer);
for (const [offset, value] of [[0, 64], [4, 0], [8, interior], [12, 16], [16, 0], [20, 0], [28, forgedBlock], [72, 1]]) {
  metadata.setUint32(forgedBlock + offset, value, true);
}
const retained = e.kame_wasm_host_heap_used();
assert.equal(e.kame_wasm_free(interior, 16), 4, 'payload-forged allocation header was accepted');
assert.equal(e.kame_wasm_host_heap_used(), retained, 'interior free changed heap accounting');
const neighbor = alloc(16, 1);
assert.ok(neighbor + 16 <= live || neighbor >= live + 256, 'allocation overlapped a live buffer');
assert.equal(e.kame_wasm_free(neighbor, 16), 0);
assert.equal(e.kame_wasm_free(live, 256), 0);
assert.equal(e.kame_wasm_host_heap_used(), 0);

// Formatter errors leave no partial output and repeated diagnostics release
// their parsed document trees while the same instance stays live.
{
  const encoded = ['template', 'view.kmt', 'x'.repeat(64 * 1024) + '\n@if true\nunclosed', 'tab']
    .map((value) => new TextEncoder().encode(value));
  const fields = encoded.map((data) => {
    const pointer = alloc(data.length);
    new Uint8Array(e.memory.buffer, pointer, data.length).set(data);
    return [pointer, data.length];
  });
  const length = alloc(4, 4);
  const instance = e.kame_wasm_instance_create();
  assert.notEqual(instance, 0n);
  const format = () => e.kame_wasm_format(instance, ...fields.flat(), 4, 0, 0, length);
  assert.equal(format(), 5);
  e.kame_wasm_collect();
  const baseline = e.kame_wasm_system_heap_used();
  for (let i = 0; i < 128; i++) {
    assert.equal(format(), 5);
    assert.equal(new DataView(e.memory.buffer).getUint32(length, true), 0);
  }
  e.kame_wasm_collect();
  assert.ok(e.kame_wasm_system_heap_used() < baseline + 4 * 1024 * 1024, 'formatter retained failed parse trees');
  assert.equal(e.kame_wasm_instance_free(instance), 0);
  for (const [pointer, size] of fields) assert.equal(e.kame_wasm_free(pointer, size), 0);
  assert.equal(e.kame_wasm_free(length, 4), 0);
  assert.equal(e.kame_wasm_host_heap_used(), 0);
}

const directory = await mkdtemp(join(tmpdir(), 'kame-memory-'));
const kame = await Kame.create({ wasmPath });
try {
  // Fingerprinting a file larger than the entire instance heap must not
  // materialize it. Native/Node SHA-256 equivalence is checked separately.
  await writeFile(join(directory, 'large'), Buffer.alloc(20 * 1024 * 1024, 65));
  const program = await kame.compile('rule ./output : ./large = "ok"\n');
  const options = { cwd: directory, grants: [{ capability: 'read', names: [directory] }, { capability: 'write', names: [directory] }] };
  assert.equal(await program.evaluate('count [1, 2]', options), '2');
  await program.build('./output', options);
  assert.equal((await readFile(join(directory, 'output'), 'utf8')), 'ok');
  await program.dispose();

  let wildcardRequests = 0;
  const observeWildcard = { hostRequest: async () => {
    wildcardRequests++;
    return { type: 'json', value: [] };
  } };
  const wildcardSource = 'unused = files ./watched/*.txt\nselected = if false then dirs ./watched/* else []\n';
  const parsedWildcard = await kame.parse('km', wildcardSource, observeWildcard);
  assert.deepEqual(parsedWildcard.diagnostics, []);
  assert.match(await kame.format('km', wildcardSource, observeWildcard), /files \.\/watched\/\*\.txt/);
  const lazyWildcard = await kame.compile(wildcardSource, observeWildcard);
  assert.equal(await lazyWildcard.evaluate('selected', observeWildcard), '[]');
  await assert.rejects(lazyWildcard.evaluate('unused', observeWildcard), { code: 'CAP_DENIED' });
  assert.equal(wildcardRequests, 0, 'inspection or an unreached or denied wildcard invoked the host');
  await lazyWildcard.dispose();

  const renderer = await kame.compile('value = render ./view.kmt\n', { name: join(directory, 'render.km') });
  const template = Buffer.from('before @{text (read ./payload)} after');
  const cancellation = new AbortController();
  let markReadStarted, completeRead;
  const readStarted = new Promise((resolve) => { markReadStarted = resolve; });
  const cancelledRender = renderer.evaluate('value', { ...options, signal: cancellation.signal, hostRequest: async (request) => {
    assert.equal(request.kind, 1);
    if (request.payload === './view.kmt') return { type: 'bytes', value: template };
    assert.equal(request.payload, './payload');
    return new Promise((resolve) => { completeRead = resolve; markReadStarted(); });
  } });
  await Promise.race([readStarted, cancelledRender.then(() => assert.fail('render completed without the second read'))]);
  cancellation.abort();
  await assert.rejects(cancelledRender, { code: 'EXEC_CANCELLED' });
  const cancelledModule = renderer.module.exports;
  const cancelledHeap = cancelledModule.kame_wasm_host_heap_used();
  completeRead({ type: 'bytes', value: Buffer.alloc(1024 * 1024, 65) });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(cancelledModule.kame_wasm_host_heap_used(), cancelledHeap, 'late completion allocated a host buffer');
  const freshRender = await renderer.evaluate('value', { ...options, hostRequest: async (request) => ({
    type: 'bytes', value: request.payload === './view.kmt' ? template : Buffer.from('fresh'),
  }) });
  assert.equal(JSON.parse(freshRender), 'before fresh after');
  await renderer.dispose();

  await writeFile(join(directory, 'helper.kash'), 'def included = 42\n');
  const composedSource = 'include ./helper.kash\nanswer = included\n';
  const composedOptions = { ...options, name: join(directory, 'composed.km') };
  await assert.rejects(kame.compile(composedSource, { name: composedOptions.name }), { code: 'CAP_DENIED' });
  const composed = await kame.compile(composedSource, composedOptions);
  assert.equal(await composed.evaluate('answer + 1', options), '43');
  assert.equal(await composed.evaluate('answer', options), '42');
  await composed.dispose();

  const selecting = 'if count (files ./large*) > 0\n\tinclude ./helper.kash\nanswer = included\n';
  const selected = await kame.compile(selecting, composedOptions);
  assert.equal(await selected.evaluate('answer'), '42');
  await selected.dispose();

  const virtualDirectory = 'if count (dirs ./virtual/*) > 0\n\tinclude ./helper.kash\nanswer = included\n';
  let directoryRequests = 0;
  const discoveryOptions = { ...composedOptions, hostRequest: async (request) => {
    directoryRequests++;
    assert.equal(request.kind, 45);
    assert.equal(request.capability, 'read');
    assert.equal(request.kindMask, 2);
    assert.equal(request.payload, join(directory, 'virtual', '*'));
    return { type: 'json', value: [join(directory, 'virtual', 'candidate')] };
  } };
  await assert.rejects(kame.compile(virtualDirectory, { ...discoveryOptions, grants: [] }), { code: 'CAP_DENIED' });
  assert.equal(directoryRequests, 0);
  const virtual = await kame.compile(virtualDirectory, discoveryOptions);
  assert.equal(directoryRequests, 1);
  assert.equal(await virtual.evaluate('answer'), '42');
  await virtual.dispose();

  const library = join(directory, 'library.kash');
  await writeFile(library, 'def number = 42\ndef where = module/file\nexport number, where\n');
  const moduleSource = 'import ./library.kash as lib\nfrom ./library.kash import number\nanswer = lib/number + number\n';
  const moduleOptions = { name: join(directory, 'module.km'), grants: [{ capability: 'read', names: [library] }] };
  const imported = await kame.compile(moduleSource, moduleOptions);
  assert.equal(await imported.evaluate('answer'), '84');
  assert.equal(await imported.evaluate('lib/where', { cwd: tmpdir() }), library);
  await assert.rejects(imported.evaluate('read lib/where'), { code: 'CAP_DENIED' });
  await imported.dispose();

  const mixed = await kame.compile(`include ./helper.kash\n${moduleSource}`, composedOptions);
  assert.equal(await mixed.evaluate('answer + included'), '126');
  await mixed.dispose();

  const deniedImport = await kame.compile(moduleSource, {
    ...moduleOptions, grants: [{ capability: 'read', names: [join(directory, 'helper.kash')] }],
  });
  let deniedCallbacks = 0;
  await assert.rejects(deniedImport.evaluate('answer', {
    grants: moduleOptions.grants,
    hostRequest: async () => { deniedCallbacks++; return { type: 'nil' }; },
  }), { code: 'CAP_DENIED' });
  assert.equal(deniedCallbacks, 0);
  await deniedImport.dispose();

  const inline = await kame.compile(`import ${library} as lib\n`, { grants: moduleOptions.grants });
  assert.equal(await inline.evaluate('lib/number'), '42');
  await inline.dispose();

  const exported = await kame.compile('answer = module/dir\nexport answer\n', { name: moduleOptions.name });
  assert.equal(await exported.evaluate('answer'), directory);
  await exported.dispose();

  // Tool tracking has internal read authority, not a user read grant. A large
  // real executable must be hashed without transporting it into WASM.
  const tool = join(directory, 'large-node');
  await cp(process.execPath, tool);
  const executable = await open(tool, 'r+');
  try { await executable.truncate(Math.max(20 * 1024 * 1024, (await executable.stat()).size)); }
  finally { await executable.close(); }
  const probe = await kame.compile(`task probe\n\t${JSON.stringify(tool)} --version\n`);
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
    exports.kame_wasm_collect();
    const heap = exports.kame_wasm_system_heap_used();
    const linear = exports.memory.buffer.byteLength;
    for (let cycle = 0; cycle < 600; cycle++) {
      expectedLength = [64, 128, 256][cycle % 3] * 1024;
      await writeFile(input, Buffer.alloc(expectedLength, 66));
      watch.invalidate([{ kind: 'file', name: input }]);
      await settle();
      assert.equal(exports.kame_wasm_host_heap_used(), baseline, 'watch retained per-read host buffers');
      if (cycle % 50 === 0) { exports.kame_wasm_collect(); assert.ok(exports.kame_wasm_system_heap_used() < heap + 4 * 1024 * 1024, 'watch retained completed read values'); }
      assert.ok(exports.memory.buffer.byteLength < 256 * 1024 * 1024, 'watch memory grew without bound');
    }
  } finally { await watch.close(); }
  await reads.dispose();
} finally {
  await kame.dispose();
  await rm(directory, { recursive: true, force: true });
}
console.log('PASS Go memory reclamation: exact buffers, 4096 live allocations, large fingerprints and 600 watch reads');
