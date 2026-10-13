// Raw composition diagnostic ownership/bounds; optional argv[3] enables CLI parity.
import assert from 'node:assert/strict';
import { readFile, mkdtemp, writeFile, rm } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';

const modulePath = resolve(process.argv[2] ?? 'build/wasm/kame.wasm');
const { instance } = await WebAssembly.instantiate(await readFile(modulePath), {});
const e = instance.exports;
const encoder = new TextEncoder();
const decoder = new TextDecoder();
const input = e.kame_wasm_alloc(262144, 8);
const output = e.kame_wasm_alloc(65536, 8);
const length = e.kame_wasm_alloc(4, 4);
let handle = e.kame_wasm_instance_create();
const query = (descriptor, target = handle, capacity = 65536) => {
  const data = encoder.encode(JSON.stringify(descriptor));
  assert.ok(data.length <= 262144);
  new Uint8Array(e.memory.buffer, input, data.length).set(data);
  return e.kame_wasm_composition_condition(target, input, data.length, output, capacity, length);
};
const diagnostic = (target = handle) => {
  const size = e.kame_wasm_instance_diagnostic_length(target);
  assert.ok(size <= 65536);
  assert.equal(e.kame_wasm_instance_diagnostic_copy(target, output, size), 0);
  return decoder.decode(new Uint8Array(e.memory.buffer, output, size));
};
const prefix = 'NamedRead = (text (read ./missing))\n';
const descriptor = { name: 'configuration.km', prefix, predicate: 'NamedRead' };
assert.equal(query(descriptor, handle, 0), 5);
const saved = diagnostic();
assert.ok(encoder.encode(saved).length > 256);
const detail = JSON.parse(saved).diagnostic;
assert.equal(detail.code, 'CAP_DENIED');
assert.deepEqual(detail.frames.map((frame) => frame.label), ['read', 'text', 'NamedRead']);
assert.ok(detail.frames.every((frame) => !('source' in frame) && !('span' in frame)));
assert.equal(e.kame_wasm_instance_diagnostic_copy(handle, output, 256), 3);
// Caller inputs and unrelated temporary queries cannot invalidate the owned report.
new Uint8Array(e.memory.buffer, input, 262144).fill(88);
const other = e.kame_wasm_instance_create();
assert.equal(query({ predicate: ':true' }, other), 0);
assert.equal(diagnostic(), saved);
assert.equal(e.kame_wasm_instance_free(other), 0);
const baseline = e.kame_wasm_system_heap_used();
for (let i = 0; i < 100; i++) {
  assert.equal(query(descriptor), 5);
  assert.equal(diagnostic(), saved);
  assert.equal(e.kame_wasm_system_heap_used(), baseline);
}
assert.equal(query({ name: 'configuration.km', predicate: '@<' }), 5);
assert.deepEqual(JSON.parse(diagnostic()).diagnostic.tips,
  ['use @< in a rule recipe, or pass the input value explicitly']);
assert.equal(query({ predicate: ':false' }), 0);
assert.equal(diagnostic(), '');
assert.equal(query({ name: 'x', predicate: 'UNKNOWN' }), 5);
const overhead = encoder.encode(diagnostic()).length - 1;
assert.equal(query({ name: 'x'.repeat(65536 - overhead), predicate: 'UNKNOWN' }), 5);
assert.equal(e.kame_wasm_instance_diagnostic_length(handle), 65536);
assert.equal(JSON.parse(diagnostic()).diagnostic.code, 'REF_MISSING');
assert.equal(query({ name: 'x'.repeat(65537 - overhead), predicate: 'UNKNOWN' }), 2);
assert.match(diagnostic(), /^NO_MEMORY: composition diagnostic exceeds/);
assert.equal(query({ name: 'x'.repeat(65536), predicate: 'UNKNOWN' }), 2);
assert.equal(diagnostic(), 'NO_MEMORY: composition diagnostic exceeds 65536-byte source capacity');
assert.equal(query(descriptor), 5, 'bounded report overflow must remain recoverable');
assert.equal(diagnostic(), saved);
// Ordinary pure queries replace rich reports, including on failure.
new Uint8Array(e.memory.buffer, input, 1)[0] = 123;
assert.equal(e.kame_wasm_declaration_predicate(handle, input, 1, output, 65536, length), 5);
assert.match(diagnostic(), /^EXPR_INVALID:/);
assert.equal(query(descriptor), 5);
assert.equal(e.kame_wasm_instance_set_heap_limit(handle, 1), 0);
assert.equal(e.kame_wasm_source_compile(handle, 0, 0), 2);
assert.equal(diagnostic(), 'NO_MEMORY: instance heap exhausted');
assert.equal(e.kame_wasm_instance_free(handle), 0);
assert.equal(e.kame_wasm_instance_diagnostic_copy(handle, output, 65536), 1);
handle = e.kame_wasm_instance_create();
assert.equal(diagnostic(), '', 'reused instance slot must not expose the previous report');
assert.equal(e.kame_wasm_instance_free(handle), 0);
console.log('PASS composition diagnostics: owned frames/tips, replacement, bounded overflow, slot reuse');

if (process.argv[3]) {
  const directory = await mkdtemp('/tmp/opencode/kame-composition-diagnostics-');
  try {
    for (const [name, text, expected] of [
      ['read', `${prefix}(if NamedRead (include ./missing.km) :nil)\n`, { frames: detail.frames }],
      ['tip', '(if @< (include ./missing.km) :nil)\n', { tips: ['use @< in a rule recipe, or pass the input value explicitly'] }],
      ['inline', null, { tips: ['save this fragment as a .km or .kmk file and run that file'] }],
    ]) {
      const file = `${directory}/${name}.km`;
      if (text) await writeFile(file, text);
      const args = ['--json', 'do', 'run', ...(text ? [file] : ['-c', '(include ./missing.km)'])];
      const diagnostics = [];
      for (const command of [[resolve(process.argv[3])], ['node', resolve('src/js/kame.js')]]) {
        const result = spawnSync(command[0], [...command.slice(1), ...args], {
          encoding: 'utf8', env: { ...process.env, KAME_WASM: modulePath }, timeout: 15000,
        });
        assert.equal(result.status, 1, result.stdout + result.stderr);
        const report = result.stdout.trim().split('\n').map((line) => JSON.parse(line))
          .find((event) => event.type === 'diagnostic').diagnostic;
        if (expected.frames) assert.deepEqual(report.frames.map((frame) => frame.label), ['read', 'text', 'NamedRead']);
        if (expected.tips) assert.deepEqual(report.tips, expected.tips);
        diagnostics.push(report);
      }
      assert.deepEqual(diagnostics[1], diagnostics[0], `${name}: native/WASM rich diagnostic parity`);
    }
    console.log('PASS named read and remediation tips: native/WASM JSON parity');
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
}
