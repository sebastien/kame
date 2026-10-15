#!/usr/bin/env node
// Raw ABI probe only. Evaluation and host services belong to src/js/kame.js.
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import process, { argv, stderr, stdout } from 'node:process';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const wasmPath = process.env.KAME_WASM ? resolve(process.env.KAME_WASM) : resolve(root, 'build/wasm/kame.wasm');
const requiredExports = [
  'memory',
  'kame_wasm_abi_version',
  'kame_wasm_alloc',
  'kame_wasm_copy',
  'kame_wasm_diagnostic_copy',
  'kame_wasm_diagnostic_length',
  'kame_wasm_instance_diagnostic_copy',
  'kame_wasm_instance_diagnostic_length',
  'kame_wasm_event_header',
  'kame_wasm_next_request_kind',
  'kame_wasm_next_request_data_length',
  'kame_wasm_request_data_copy',
  'kame_wasm_eval_pure',
  'kame_wasm_eval_source_pure',
  'kame_wasm_expression_request',
  'kame_wasm_set_forwarding',
  'kame_wasm_host_set_file',
  'kame_wasm_host_set_env',
  'kame_wasm_target_begin',
  'kame_wasm_expression_begin',
  'kame_wasm_expression_cancel',
  'kame_wasm_expression_effect_kind',
  'kame_wasm_expression_effect_length',
  'kame_wasm_expression_effect_copy',
  'kame_wasm_step',
  'kame_wasm_next_event_header',
  'kame_wasm_event_payload_copy',
  'kame_wasm_complete_bytes',
  'kame_wasm_complete_text',
  'kame_wasm_complete_nil',
  'kame_wasm_complete_json',
  'kame_wasm_complete_failure',
  'kame_wasm_result_copy',
  'kame_wasm_instance_create',
  'kame_wasm_instance_free',
  'kame_wasm_source_compile',
];

function usage() {
  stdout.write(`Usage: node tools/kame-wasm.mjs --abi-info | --self-test\n`);
  stdout.write(`Loads KAME_WASM or build/wasm/kame.wasm with Node's WebAssembly API.\n`);
}

function abi(instance) {
  const { exports } = instance;
  const missing = requiredExports.filter((name) => !(name in exports));
  if (missing.length !== 0) {
    throw new Error(`WASM ABI is incomplete: missing ${missing.join(', ')}`);
  }
  if (exports.kame_wasm_abi_version() !== 1) {
    throw new Error('unsupported WASM ABI schema');
  }
  return exports;
}

function allocate(exports, size, alignment = 1) {
  const pointer = exports.kame_wasm_alloc(size, alignment);
  if (pointer === 0) throw new Error(`WASM allocation failed for ${size} bytes`);
  return pointer;
}

function encodeHeader(exports) {
  const pointer = allocate(exports, 48, 8);
  const status = exports.kame_wasm_event_header(pointer, 48, 9, 1n, 2n, 3n, 4n, 5n, 6);
  if (status !== 0) throw new Error(`event-header encoding failed with status ${status}`);
  // Reacquire the view after every ABI call that may grow memory.
  const view = new DataView(exports.memory.buffer, pointer, 48);
  const header = {
    schema: view.getUint16(0, true),
    kind: view.getUint16(2, true),
    root: view.getBigUint64(4, true).toString(),
    node: view.getBigUint64(12, true).toString(),
    request: view.getBigUint64(20, true).toString(),
    generation: view.getBigInt64(28, true).toString(),
    revision: view.getBigInt64(36, true).toString(),
    payloadLength: view.getUint32(44, true),
  };
  if (header.schema !== 1 || header.kind !== 9 || header.root !== '1' || header.payloadLength !== 6) {
    throw new Error('event-header bytes did not round-trip through linear memory');
  }
  return header;
}

function selfTest(exports) {
  const source = allocate(exports, 4, 1);
  const destination = allocate(exports, 4, 1);
  new Uint8Array(exports.memory.buffer, source, 4).set([0, 255, 1, 2]);
  if (exports.kame_wasm_copy(destination, 4, source, 4) !== 0) {
    throw new Error('linear-memory copy failed');
  }
  const copied = [...new Uint8Array(exports.memory.buffer, destination, 4)];
  if (copied.join(',') !== '0,255,1,2') throw new Error('linear-memory copy corrupted bytes');
  const before = exports.memory.buffer.byteLength;
  allocate(exports, before + 1, 16);
  if (exports.memory.buffer.byteLength <= before) throw new Error('memory.grow was not enabled');
  return { copied, memoryBytes: exports.memory.buffer.byteLength };
}

function diagnostic(exports) {
  const length = exports.kame_wasm_diagnostic_length();
  if (length === 0) return 'WASM operation failed without a diagnostic';
  const pointer = allocate(exports, length, 1);
  if (exports.kame_wasm_diagnostic_copy(pointer, length) !== 0) {
    return 'WASM operation failed while copying its diagnostic';
  }
  return new TextDecoder().decode(new Uint8Array(exports.memory.buffer, pointer, length));
}

function evaluatePure(exports, text) {
  const source = new TextEncoder().encode(text);
  const sourcePointer = allocate(exports, source.length || 1, 1);
  new Uint8Array(exports.memory.buffer, sourcePointer, source.length).set(source);
  const lengthPointer = allocate(exports, 4, 4);
  // Status 3 is BUFFER_TOO_SMALL, the ABI's payload-length query result.
  const queried = exports.kame_wasm_eval_pure(sourcePointer, source.length, 0, 0, lengthPointer);
  if (queried !== 3) throw new Error(diagnostic(exports));
  const length = new DataView(exports.memory.buffer, lengthPointer, 4).getUint32(0, true);
  const outputPointer = allocate(exports, length || 1, 1);
  const status = exports.kame_wasm_eval_pure(sourcePointer, source.length, outputPointer, length, lengthPointer);
  if (status !== 0) throw new Error(diagnostic(exports));
  // Evaluation may allocate internally, so acquire this view only after it returns.
  return new TextDecoder().decode(new Uint8Array(exports.memory.buffer, outputPointer, length));
}

async function load() {
  const bytes = await readFile(wasmPath);
  return WebAssembly.instantiate(bytes, {});
}

async function main() {
  const args = argv.slice(2);
  if (args.length !== 1 || !['--abi-info', '--self-test', '--help', '-h'].includes(args[0])) {
    usage();
    return 2;
  }
  if (args[0] === '--help' || args[0] === '-h') {
    usage();
    return 0;
  }
  const { instance } = await load();
  const exports = abi(instance);
  const result = {
    schema: 1,
    wasm: wasmPath,
    exports: Object.keys(exports).sort(),
    eventHeader: encodeHeader(exports),
  };
  if (args[0] === '--self-test') {
    result.selfTest = selfTest(exports);
    result.pureExpression = evaluatePure(exports, '(join ":" ["a" "b"])');
  }
  stdout.write(`${JSON.stringify(result)}\n`);
  return 0;
}

main().then(
  (status) => { process.exitCode = status; },
  (error) => { stderr.write(`kame-wasm: ${error.message}\n`); process.exitCode = 1; },
);
