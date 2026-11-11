// Opt-in toolchain check: the current WASM CLI must bootstrap a clean native
// artifact, without raising its memory budget or using a prebuilt kame.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { cp, mkdir, mkdtemp, readFile, rm } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { Kame } from '../../src/js/kame.js';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const wasmPath = resolve(process.env.KAME_WASM ?? join(root, 'build/wasm/kame.wasm'));
const files = execFileSync('git', ['ls-files', '-z', '--cached', '--others', '--exclude-standard'], { cwd: root }).toString().split('\0').filter(Boolean);
const directory = await mkdtemp(join(tmpdir(), 'kame-wasm-bootstrap-'));
const originalDirectory = process.cwd();
const kame = await Kame.create({ wasmPath });
let watch;
try {
  for (const name of new Set(files)) {
    await mkdir(dirname(join(directory, name)), { recursive: true });
    await cp(join(root, name), join(directory, name));
  }
  const version = (await readFile(join(directory, 'VERSION'), 'utf8')).trim();
  const source = await readFile(join(directory, 'Makefile.kmk'), 'utf8');
  // This standalone test owns the process cwd, matching CLI execution.
  process.chdir(directory);
  const program = await kame.compile(source, { language: 'kmk', name: 'Makefile.kmk' });
  watch = await program.watch('./dist/kame', {
    cwd: directory,
    grants: ['read', 'write', 'run', 'env'].map(capability => ({ capability, names: [] })),
    signal: AbortSignal.timeout(10 * 60 * 1000),
  });
  const settle = async () => {
    for (let step = 0; step < 100000; step++) {
      const state = watch.module.drainWatch(watch.watch);
      if (state === 1) watch.module.serviceWatchRequest(watch.watch);
      if (watch.watch.pending.size) await Promise.all([...watch.watch.pending]);
      if (watch.watch.hostError) throw watch.watch.hostError;
      watch.watch.context.events.splice(0);
      // A full graph snapshot after every file request is quadratic in the
      // source count. Inspect only when the driver has no queued host request.
      if (state === 1 || (state !== 2 && step % 256 !== 0)) continue;
      const snapshot = watch.snapshot();
      if (!snapshot.busy) {
        assert.equal(snapshot.roots[0].diagnosticJSON, undefined, JSON.stringify(snapshot.roots));
        return;
      }
    }
    assert.fail('self-build did not settle');
  };
  await settle();
  const output = execFileSync(join(directory, 'dist/kame'), ['--version'], { encoding: 'utf8' });
  assert.ok(output.startsWith(`kame ${version} `), output);
  const e = watch.module.exports;
  e.kame_wasm_collect();
  const heap = e.kame_wasm_system_heap_used();
  assert.ok(heap < 128 * 1024 * 1024, 'self-build retained excessive evaluator state');
  const linear = e.memory.buffer.byteLength;
  // Recheck an unchanged input in the same module: no process/module reset
  // may conceal growing transport buffers or retained instance allocations.
  watch.invalidate([{ kind: 'file', name: join(directory, 'VERSION') }]);
  await settle();
  const warmed = e.memory.buffer.byteLength;
  for (let cycle = 0; cycle < 3; cycle++) {
    watch.invalidate([{ kind: 'file', name: join(directory, 'VERSION') }]);
    await settle();
    e.kame_wasm_collect();
    assert.ok(e.kame_wasm_system_heap_used() < heap + 4 * 1024 * 1024, 'self-build retained invalidated state');
  }
  console.log(JSON.stringify({ test: 'wasm-self-build', version, liveHeap: e.kame_wasm_system_heap_used(), hostPeak: e.kame_wasm_host_heap_peak(), linearBytes: linear, warmLinearBytes: warmed }));
} finally {
  await watch?.close();
  await kame.dispose();
  process.chdir(originalDirectory);
  await rm(directory, { recursive: true, force: true });
}
