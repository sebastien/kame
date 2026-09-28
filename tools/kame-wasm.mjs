#!/usr/bin/env node
/**
 * Load the freestanding Kame module in Node.js.
 *
 * This is intentionally a host wrapper, not a WASI runner: Kame's wasm target
 * imports no POSIX filesystem or process API. Future instance, compile, step,
 * event-copy, and completion exports will be driven from this file.
 */
import { access, readFile, stat, writeFile } from 'node:fs/promises';
import { constants } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import process, { argv, stderr, stdout } from 'node:process';
import { spawn } from 'node:child_process';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const wasmPath = resolve(root, 'build/wasm/kame.wasm');
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
  stdout.write(`       node tools/kame-wasm.mjs do expr -c TEXT | -f FILE\n`);
  stdout.write(`       node tools/kame-wasm.mjs do expr --async -c TEXT | -f FILE\n\n`);
  stdout.write(`Loads build/wasm/kame.wasm with Node's WebAssembly API.\n`);
  stdout.write(`Async expressions provide read, write, exists, stat, environment, and process host capabilities.\n`);
  stdout.write(`Glob and clock requests report FEATURE_UNSUP until the discovery stage lands.\n`);
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

function instanceDiagnostic(exports, instance) {
  const length = exports.kame_wasm_instance_diagnostic_length(instance);
  if (length === 0) return 'WASM operation failed without a diagnostic';
  const pointer = allocate(exports, length, 1);
  if (exports.kame_wasm_instance_diagnostic_copy(instance, pointer, length) !== 0) {
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

function evaluateSourcePure(exports, program, text) {
  const encode = (value) => {
    const bytes = new TextEncoder().encode(value);
    const pointer = allocate(exports, bytes.length || 1, 1);
    new Uint8Array(exports.memory.buffer, pointer, bytes.length).set(bytes);
    return { pointer, length: bytes.length };
  };
  const compiled = encode(program);
  const source = encode(text);
  const lengthPointer = allocate(exports, 4, 4);
  const query = exports.kame_wasm_eval_source_pure(compiled.pointer, compiled.length, source.pointer, source.length, 0, 0, lengthPointer);
  if (query !== 3) throw new Error(diagnostic(exports));
  const length = new DataView(exports.memory.buffer, lengthPointer, 4).getUint32(0, true);
  const output = allocate(exports, length || 1, 1);
  const status = exports.kame_wasm_eval_source_pure(compiled.pointer, compiled.length, source.pointer, source.length, output, length, lengthPointer);
  if (status !== 0) throw new Error(diagnostic(exports));
  return new TextDecoder().decode(new Uint8Array(exports.memory.buffer, output, length));
}

function encode(exports, value) {
  const bytes = typeof value === 'string' ? new TextEncoder().encode(value) : new Uint8Array(value);
  const pointer = allocate(exports, bytes.length || 1, 1);
  new Uint8Array(exports.memory.buffer, pointer, bytes.length).set(bytes);
  return { pointer, length: bytes.length };
}

// Canonical JSON: object keys sorted, no insignificant whitespace. The engine
// parses this back into an owned value for structured host completions.
function encodeJSON(value) {
  if (Array.isArray(value)) return `[${value.map(encodeJSON).join(',')}]`;
  if (value !== null && typeof value === 'object') {
    const keys = Object.keys(value).sort();
    return `{${keys.map((key) => `${JSON.stringify(key)}:${encodeJSON(value[key])}`).join(',')}}`;
  }
  return JSON.stringify(value);
}

function completeJSON(exports, instance, request, value) {
  const bytes = new TextEncoder().encode(encodeJSON(value));
  const pointer = allocate(exports, bytes.length || 1);
  new Uint8Array(exports.memory.buffer, pointer, bytes.length).set(bytes);
  if (exports.kame_wasm_complete_json(instance, request, pointer, bytes.length) !== 0) {
    throw new Error(instanceDiagnostic(exports, instance));
  }
}

function completeFailure(exports, instance, request, code, message) {
  const codeBytes = encode(exports, code);
  const messageBytes = encode(exports, message);
  if (exports.kame_wasm_complete_failure(instance, request, codeBytes.pointer, codeBytes.length, messageBytes.pointer, messageBytes.length) !== 0) {
    throw new Error(instanceDiagnostic(exports, instance));
  }
}

function evaluateInstance(exports, program, text) {
  const instance = exports.kame_wasm_instance_create();
  if (instance === 0n) throw new Error(diagnostic(exports));
  try {
    const compiled = encode(exports, program);
    if (exports.kame_wasm_source_compile(instance, compiled.pointer, compiled.length) !== 0) {
      throw new Error(instanceDiagnostic(exports, instance));
    }
    const expression = encode(exports, text);
    const lengthPointer = allocate(exports, 4, 4);
    const query = exports.kame_wasm_expression_request(instance, expression.pointer, expression.length, 0, 0, lengthPointer);
    if (query !== 3) throw new Error(instanceDiagnostic(exports, instance));
    const length = new DataView(exports.memory.buffer, lengthPointer, 4).getUint32(0, true);
    const output = allocate(exports, length || 1, 1);
    const status = exports.kame_wasm_expression_request(instance, expression.pointer, expression.length, output, length, lengthPointer);
    if (status !== 0) throw new Error(instanceDiagnostic(exports, instance));
    return new TextDecoder().decode(new Uint8Array(exports.memory.buffer, output, length));
  } finally {
    const status = exports.kame_wasm_instance_free(instance);
    if (status !== 0) throw new Error(`instance free failed with status ${status}`);
  }
}

async function runProcess(script) {
  return new Promise((resolveRun) => {
    let child;
    try {
      child = spawn('/bin/sh', ['-c', script], { stdio: ['ignore', 'pipe', 'pipe'] });
    } catch (error) {
      resolveRun({ ok: false, code: 'HOST_FAIL', message: error.message });
      return;
    }
    const stdoutChunks = [];
    const stderrChunks = [];
    let spawnError = null;
    child.stdout.on('data', (chunk) => stdoutChunks.push(chunk));
    child.stderr.on('data', (chunk) => stderrChunks.push(chunk));
    child.once('error', (error) => { spawnError = error; });
    child.once('close', (status) => {
      if (spawnError !== null) {
        resolveRun({ ok: false, code: 'HOST_FAIL', message: spawnError.message });
      } else if (status !== 0) {
        resolveRun({ ok: false, code: 'RECIPE_FAIL', message: `process exited with status ${status}: ${Buffer.concat(stderrChunks)}` });
      } else {
        resolveRun({ ok: true, text: Buffer.concat(stdoutChunks).toString('utf8') });
      }
    });
  });
}

async function evaluateAsync(exports, program, text) {
  const instance = exports.kame_wasm_instance_create();
  if (instance === 0n) throw new Error(diagnostic(exports));
  try {
    const compiled = encode(exports, program);
    if (exports.kame_wasm_source_compile(instance, compiled.pointer, compiled.length) !== 0) throw new Error(instanceDiagnostic(exports, instance));
    const expression = encode(exports, text);
    if (exports.kame_wasm_expression_begin(instance, expression.pointer, expression.length) !== 0) throw new Error(instanceDiagnostic(exports, instance));
    for (;;) {
      const state = exports.kame_wasm_step(instance);
      if (state === 2) break;
      if (state !== 1) continue;
      const header = allocate(exports, 48, 8);
      if (exports.kame_wasm_next_event_header(instance, header, 48) !== 0) throw new Error(diagnostic(exports));
      const view = new DataView(exports.memory.buffer, header, 48);
      const request = view.getBigUint64(20, true);
      const length = view.getUint32(44, true);
      const kind = exports.kame_wasm_next_request_kind(instance);
      let writeData = null;
      if (kind === 2) {
        const dataLength = exports.kame_wasm_next_request_data_length(instance);
        const dataPointer = allocate(exports, dataLength || 1);
        if (exports.kame_wasm_request_data_copy(instance, dataPointer, dataLength) !== 0) throw new Error(diagnostic(exports));
        writeData = new Uint8Array(exports.memory.buffer, dataPointer, dataLength).slice();
      }
      const payload = allocate(exports, length || 1);
      if (exports.kame_wasm_event_payload_copy(instance, payload, length) !== 0) throw new Error(diagnostic(exports));
      const requestPayload = new Uint8Array(exports.memory.buffer, payload, length).slice();
      if (kind === 1) {
        try {
          const data = await readFile(new TextDecoder().decode(requestPayload));
          const result = encode(exports, data);
          if (exports.kame_wasm_complete_bytes(instance, request, result.pointer, result.length) !== 0) throw new Error(diagnostic(exports));
        } catch (error) {
          const code = encode(exports, 'FS_ERR');
          const message = encode(exports, `cannot read file: ${error.message}`);
          if (exports.kame_wasm_complete_failure(instance, request, code.pointer, code.length, message.pointer, message.length) !== 0) throw new Error(diagnostic(exports));
        }
      } else if (kind === 2) {
        const path = new TextDecoder().decode(requestPayload);
        try {
          await writeFile(path, writeData);
          if (exports.kame_wasm_complete_nil(instance, request) !== 0) throw new Error(diagnostic(exports));
        } catch (error) {
          const code = encode(exports, 'FS_ERR');
          const message = encode(exports, `cannot write file: ${error.message}`);
          if (exports.kame_wasm_complete_failure(instance, request, code.pointer, code.length, message.pointer, message.length) !== 0) throw new Error(diagnostic(exports));
        }
      } else if (kind === 4) {
        const name = new TextDecoder().decode(requestPayload);
        const value = process.env[name];
        if (value === undefined) {
          if (exports.kame_wasm_complete_nil(instance, request) !== 0) throw new Error(diagnostic(exports));
        } else {
          const result = encode(exports, value);
          if (exports.kame_wasm_complete_text(instance, request, result.pointer, result.length) !== 0) throw new Error(diagnostic(exports));
        }
      } else if (kind === 3) {
        const script = new TextDecoder().decode(requestPayload);
        const completion = await runProcess(script);
        if (completion.ok) {
          const result = encode(exports, completion.text);
          if (exports.kame_wasm_complete_text(instance, request, result.pointer, result.length) !== 0) throw new Error(diagnostic(exports));
        } else {
          const code = encode(exports, completion.code);
          const message = encode(exports, completion.message);
          if (exports.kame_wasm_complete_failure(instance, request, code.pointer, code.length, message.pointer, message.length) !== 0) throw new Error(diagnostic(exports));
        }
      } else if (kind === 5) {
        const path = new TextDecoder().decode(requestPayload);
        try {
          const info = await stat(path);
          completeJSON(exports, instance, request, { name: path, size: info.size, mode: info.mode, dir: info.isDirectory() });
        } catch (error) {
          completeFailure(exports, instance, request, 'FS_ERR', `cannot stat file: ${error.message}`);
        }
      } else if (kind === 7) {
        const path = new TextDecoder().decode(requestPayload);
        let exists = true;
        try {
          await stat(path);
        } catch {
          exists = false;
        }
        completeJSON(exports, instance, request, exists);
      } else if (kind === 6 || kind === 8 || kind === 9) {
        completeFailure(exports, instance, request, 'FEATURE_UNSUP', 'host capability is not implemented in this stage');
      } else {
        throw new Error(`unsupported host request capability: ${kind}`);
      }
    }
    const outLength = allocate(exports, 4, 4);
    const queried = exports.kame_wasm_result_copy(instance, 0, 0, outLength);
    if (queried !== 0 && queried !== 3) throw new Error(instanceDiagnostic(exports, instance));
    const length = new DataView(exports.memory.buffer, outLength, 4).getUint32(0, true);
    const output = allocate(exports, length || 1);
    if (exports.kame_wasm_result_copy(instance, output, length, outLength) !== 0) throw new Error(instanceDiagnostic(exports, instance));
    return new TextDecoder().decode(new Uint8Array(exports.memory.buffer, output, length));
  } finally {
    exports.kame_wasm_instance_free(instance);
  }
}

async function load() {
  try {
    await access(wasmPath, constants.R_OK);
  } catch {
    throw new Error('WASM module is missing; build it first with `make wasm`');
  }
  const bytes = await readFile(wasmPath);
  return WebAssembly.instantiate(bytes, {});
}

async function main() {
  const args = argv.slice(2);
  const inlineExpression = args[0] === 'do' && args[1] === 'expr' && args[2] === '-c' && args.length === 4 ? args[3] : null;
  const asyncExpression = args[0] === 'do' && args[1] === 'expr' && args[2] === '--async' && args[3] === '-c' && args.length === 5 ? args[4] : null;
  const asyncFileExpression = args[0] === 'do' && args[1] === 'expr' && args[2] === '--async' && args[3] === '-f' && args.length === 5 ? args[4] : null;
  const fileExpression = args[0] === 'do' && args[1] === 'expr' && args[2] === '-f' && args.length === 4 ? args[3] : null;
  if (inlineExpression === null && asyncExpression === null && asyncFileExpression === null && fileExpression === null && (args.length !== 1 || !['--abi-info', '--self-test', '--help', '-h'].includes(args[0]))) {
    usage();
    return 2;
  }
  if (args[0] === '--help' || args[0] === '-h') {
    usage();
    return 0;
  }
  const { instance } = await load();
  const exports = abi(instance);
  if (inlineExpression !== null) {
    stdout.write(`${evaluatePure(exports, inlineExpression)}\n`);
    return 0;
  }
  if (asyncExpression !== null) {
    stdout.write(`${await evaluateAsync(exports, '', asyncExpression)}\n`);
    return 0;
  }
  if (asyncFileExpression !== null) {
    const program = await readFile(resolve(process.cwd(), asyncFileExpression), 'utf8');
    stdout.write(`${await evaluateAsync(exports, program, 'result')}\n`);
    return 0;
  }
  if (fileExpression !== null) {
    const program = await readFile(resolve(process.cwd(), fileExpression), 'utf8');
    stdout.write(`${evaluateInstance(exports, program, 'result')}\n`);
    return 0;
  }
  const result = {
    schema: 1,
    wasm: wasmPath,
    exports: Object.keys(exports).sort(),
    eventHeader: encodeHeader(exports),
  };
  if (args[0] === '--self-test') {
    result.selfTest = selfTest(exports);
    result.pureExpression = evaluatePure(exports, '(join ["a" "b"] ":")');
  }
  stdout.write(`${JSON.stringify(result)}\n`);
  return 0;
}

main().then(
  (status) => { process.exitCode = status; },
  (error) => { stderr.write(`kame-wasm: ${error.message}\n`); process.exitCode = 1; },
);
