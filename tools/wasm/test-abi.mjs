// Raw ABI regressions; run after linking: node tools/wasm/test-abi.mjs [module]
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const bytes = await readFile(process.argv[2] ?? 'build/wasm/kame.wasm');
const encoder = new TextEncoder();
const decoder = new TextDecoder();
let failed = false;
const paths = ['./dir', './file', './link'];
// POSIX modes are realistic host payloads, not the normalized kind discriminator.
const metadata = [
  { name: './dir', size: 0, mode: 0o040755, regular: false, dir: true, link: false },
  { name: './file', size: 4, mode: 0o100644, regular: true, dir: false, link: false },
  { name: './link', size: 6, mode: 0o120777, regular: false, dir: false, link: true },
];
const glob = { kind: 6, path: './*', value: paths };
const stats = metadata.map((value) => ({ kind: 30, path: value.name, value }));
const malformed = 'HOST_FAIL: host completion is not valid canonical JSON';
const invalidKind = 'HOST_FAIL: wildcard kind filtering requires normalized lstat regular, dir and link fields';
const pathCases = [
  { name: 'glob dispatch', expression: '(wildcard "./*")', requests: [glob], result: paths },
  ...stats.map((request) => ({ name: `lstat ${request.path}`, expression: `(lstat ${request.path})`, requests: [request], result: request.value })),
  { name: 'readlink dispatch', expression: '(readlink ./link)', requests: [{ kind: 31, path: './link', value: '../target' }], result: '../target' },
  { name: 'compiled readlink helper', source: 'op = [readlink: readlink]\n', expression: '(op.readlink ./link)', requests: [{ kind: 31, path: './link', value: '../target' }], result: '../target' },
  ...[['+files', ['./file']], ['+dir', ['./dir']], ['+symlinks', ['./link']], ['-symlinks', ['./dir', './file']]].map(([mask, result]) => ({
    name: `glob normalized no-follow ${mask}`, expression: `(wildcard "${mask}" "./*")`, requests: [glob, ...stats], result,
  })),
  { name: 'glob normalized kind is not inferred from mode', expression: '(wildcard "+symlinks" "./*")',
    requests: [{ ...glob, value: ['./link'] }, { kind: 30, path: './link', value: { ...metadata[2], mode: 0o100644 } }], result: ['./link'] },
  ...[6, 30, 31].map((kind) => ({
    name: `kind ${kind} malformed JSON`, expression: kind === 6 ? '(wildcard "./*")' : kind === 30 ? '(lstat ./x)' : '(readlink ./x)',
    requests: [{ kind, path: kind === 6 ? './*' : './x', raw: '{', status: 5 }], diagnostic: malformed,
  })),
  ...[
    { dir: false, link: false },
    { regular: 'true', dir: false, link: false },
    { regular: true, dir: 0, link: false },
    { regular: true, dir: false, link: null },
  ].map((fields, index) => ({
    name: `glob malformed normalized metadata ${index}`, expression: '(wildcard "+files" "./*")',
    requests: [{ ...glob, value: ['./file'] }, { kind: 30, path: './file', value: { name: './file', mode: 0o100644, ...fields } }], diagnostic: invalidKind,
  })),
];
for (const test of ['instance identity', 'failure input lifetime', 'malformed JSON', ...pathCases.map((entry) => entry.name)]) {
  try {
    // Fresh module makes the first instance occupy slot zero: child value zero
    // used to resolve to that instance despite a different table handle.
    const { instance } = await WebAssembly.instantiate(bytes, {});
    const e = instance.exports;
    const alloc = (length, alignment = 1) => {
      const pointer = e.kame_wasm_alloc(length || 1, alignment);
      assert.notEqual(pointer, 0);
      return pointer;
    };
    const write = (text) => {
      const data = encoder.encode(text);
      const pointer = alloc(data.length);
      new Uint8Array(e.memory.buffer, pointer, data.length).set(data);
      return [pointer, data.length];
    };
    const diagnostic = (handle) => {
      const length = e.kame_wasm_instance_diagnostic_length(handle);
      const pointer = alloc(length);
      assert.equal(e.kame_wasm_instance_diagnostic_copy(handle, pointer, length), 0);
      return decoder.decode(new Uint8Array(e.memory.buffer, pointer, length));
    };
    const handle = e.kame_wasm_instance_create();
    assert.notEqual(handle, 0n);
    const pathCase = pathCases.find((entry) => entry.name === test);
    if (pathCase) {
      assert.equal(e.kame_wasm_source_compile(handle, ...write(pathCase.source ?? '')), 0);
    } else {
      assert.equal(e.kame_wasm_source_compile(handle, 0, 0), 0);
    }
    assert.equal(e.kame_wasm_expression_begin(handle, ...write(pathCase ? `(json ${pathCase.expression})` : '(exists? "x")')), 0);
    if (pathCase) {
      let state = 0;
      for (let i = 0; i < 100 && state === 0; i++) state = e.kame_wasm_step(handle);
      assert.equal(state, 1, 'first filesystem request is pinned');
    } else {
      assert.equal(e.kame_wasm_step(handle), 1);
    }
    const header = alloc(48, 8);
    assert.equal(e.kame_wasm_next_event_header(handle, header, 48), 0);
    const view = new DataView(e.memory.buffer, header, 48);
    const root = view.getBigUint64(4, true);
    const node = view.getBigUint64(12, true);
    const request = view.getBigUint64(20, true);

    if (pathCase) {
      for (const [index, expected] of pathCase.requests.entries()) {
        if (index !== 0) {
          let state = 0;
          for (let i = 0; i < 100 && state === 0; i++) state = e.kame_wasm_step(handle);
          assert.equal(state, 1, 'next filesystem request is pinned');
          assert.equal(e.kame_wasm_next_event_header(handle, header, 48), 0);
        }
        assert.equal(e.kame_wasm_next_request_kind(handle), expected.kind, 'exact public dispatch kind');
        const size = new DataView(e.memory.buffer, header, 48).getUint32(44, true);
        const pointer = alloc(size);
        assert.equal(e.kame_wasm_event_payload_copy(handle, pointer, size), 0);
        assert.equal(decoder.decode(new Uint8Array(e.memory.buffer, pointer, size)), expected.path, 'exact path/glob payload');
        const id = new DataView(e.memory.buffer, header, 48).getBigUint64(20, true);
        assert.equal(e.kame_wasm_complete_json(handle, id, ...write(expected.raw ?? JSON.stringify(expected.value))), expected.status ?? 0);
        assert.equal(e.kame_wasm_next_event_header(handle, header, 48), 4, 'completion clears pending event');
        if (expected.status === 5) assert.equal(diagnostic(handle), pathCase.diagnostic);
      }
      let state = 0;
      for (let i = 0; i < 100 && state === 0; i++) state = e.kame_wasm_step(handle);
      assert.equal(state, 2, 'filesystem completion reaches terminal result without extra requests');
      const length = alloc(4, 4);
      assert.equal(e.kame_wasm_result_copy(handle, 0, 0, length), pathCase.diagnostic ? 5 : 3);
      if (pathCase.diagnostic) {
        assert.equal(diagnostic(handle), pathCase.diagnostic);
      } else {
        const size = new DataView(e.memory.buffer, length, 4).getUint32(0, true);
        const pointer = alloc(size);
        assert.equal(e.kame_wasm_result_copy(handle, pointer, size, length), 0);
        const text = decoder.decode(new Uint8Array(e.memory.buffer, pointer, size));
        assert.deepEqual(JSON.parse(JSON.parse(text)), pathCase.result);
      }
    } else if (test === 'instance identity') {
      for (const child of [root, node, request]) {
        assert.notEqual(child, 0n);
        assert.equal(e.kame_wasm_instance_free(child), 1, 'child must not free its owner');
        assert.equal(e.kame_wasm_source_compile(child, 0, 0), 1);
      }
      assert.equal(e.kame_wasm_expression_cancel(handle), 0);
    } else {
      let expected;
      if (test === 'failure input lifetime') {
        const code = write('CUSTOM_FAIL');
        const message = write('original host message');
        assert.equal(e.kame_wasm_complete_failure(handle, request, ...code, ...message), 0);
        // Inputs may be reused immediately, before the engine accepts the queue.
        for (const [pointer, length] of [code, message]) {
          new Uint8Array(e.memory.buffer, pointer, length).fill(88);
        }
        expected = 'CUSTOM_FAIL: original host message';
      } else {
        assert.equal(e.kame_wasm_complete_json(handle, request, ...write('{')), 5);
        expected = 'HOST_FAIL: host completion is not valid canonical JSON';
        assert.equal(diagnostic(handle), expected, 'emergency diagnostic remains inspectable');
      }
      assert.equal(e.kame_wasm_next_event_header(handle, header, 48), 4, 'completion clears pending event');
      // A late duplicate remains accepted without reviving the request.
      assert.equal(e.kame_wasm_complete_nil(handle, request), 0);
      let state = 0;
      for (let i = 0; i < 10 && state !== 2; i++) state = e.kame_wasm_step(handle);
      assert.equal(state, 2, 'failed request reaches a terminal result');
      const length = alloc(4, 4);
      assert.equal(e.kame_wasm_result_copy(handle, 0, 0, length), 5);
      assert.equal(diagnostic(handle), expected);
    }
    assert.equal(e.kame_wasm_instance_free(handle), 0);
    assert.equal(e.kame_wasm_instance_free(handle), 1, 'stale instance rejected');
    console.log(`PASS ${test}`);
  } catch (error) {
    failed = true;
    console.error(`FAIL ${test}: ${error.message}`);
  }
}
if (failed) process.exitCode = 1;
await import('./test-handles.mjs');
await import('./test-composition-diagnostics.mjs');
