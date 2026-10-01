#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — freestanding host ABI
# Spec: docs/spec/013-tests.md — T010-03-wasm-target
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-03 wasm target materialization"
cd "$CLI_ROOT"

make wasm >/dev/null

test-step "definition targets materialize against an in-memory host"
if node --input-type=module - <<'NODE'
const { readFile } = await import('node:fs/promises');
const bytes = await readFile('./build/wasm/kame.wasm');
const { instance } = await WebAssembly.instantiate(bytes, {});
const { exports } = instance;
const alloc = (size, alignment = 1) => {
  const pointer = exports.kame_wasm_alloc(size, alignment);
  if (pointer === 0) throw new Error(`allocation failed: ${size}`);
  return pointer;
};
const write = (text) => {
  const data = new TextEncoder().encode(text);
  const pointer = alloc(data.length || 1);
  new Uint8Array(exports.memory.buffer, pointer, data.length).set(data);
  return [pointer, data.length];
};
const runTarget = (handle) => {
  let state = 0;
  for (let i = 0; i < 128 && state !== 2; i++) state = exports.kame_wasm_step(handle);
  if (state !== 2) throw new Error(`target did not complete: ${state}`);
};
const resultText = (handle) => {
  const lengthPointer = alloc(4, 4);
  const query = exports.kame_wasm_result_copy(handle, 0, 0, lengthPointer);
  if (query !== 0 && query !== 3) throw new Error(`target result query failed: ${query}`);
  const length = new DataView(exports.memory.buffer, lengthPointer, 4).getUint32(0, true);
  const output = alloc(length || 1);
  if (exports.kame_wasm_result_copy(handle, output, length, lengthPointer) !== 0) throw new Error('target result copy failed');
  return new TextDecoder().decode(new Uint8Array(exports.memory.buffer, output, length));
};

const readInstance = exports.kame_wasm_instance_create();
const [source, sourceLength] = write('result = (read "in.txt")\n');
if (exports.kame_wasm_source_compile(readInstance, source, sourceLength) !== 0) throw new Error('read source did not compile');
const [path, pathLength] = write('in.txt');
const [data, dataLength] = write('in-memory');
if (exports.kame_wasm_host_set_file(readInstance, path, pathLength, data, dataLength) !== 0) throw new Error('host file was rejected');
const [target, targetLength] = write('result');
if (exports.kame_wasm_target_begin(readInstance, target, targetLength) !== 0) throw new Error('read target did not begin');
runTarget(readInstance);
if (resultText(readInstance) !== 'in-memory') throw new Error('read target value was wrong');
exports.kame_wasm_instance_free(readInstance);

const envInstance = exports.kame_wasm_instance_create();
const [envSource, envSourceLength] = write('result = (env "KAME_TARGET_ENV")\n');
if (exports.kame_wasm_source_compile(envInstance, envSource, envSourceLength) !== 0) throw new Error('env source did not compile');
const [name, nameLength] = write('KAME_TARGET_ENV');
const [value, valueLength] = write('target-value');
if (exports.kame_wasm_host_set_env(envInstance, name, nameLength, value, valueLength) !== 0) throw new Error('host env was rejected');
if (exports.kame_wasm_target_begin(envInstance, target, targetLength) !== 0) throw new Error('env target did not begin');
runTarget(envInstance);
if (resultText(envInstance) !== '"target-value"') throw new Error('env target value was wrong');
exports.kame_wasm_instance_free(envInstance);

const missingInstance = exports.kame_wasm_instance_create();
if (exports.kame_wasm_source_compile(missingInstance, 0, 0) !== 0) throw new Error('empty source did not compile');
const [missing, missingLength] = write('no-such-target');
if (exports.kame_wasm_target_begin(missingInstance, missing, missingLength) !== 5) throw new Error('missing target did not diagnose');
const diagnosticLength = exports.kame_wasm_instance_diagnostic_length(missingInstance);
const diagnosticPointer = alloc(diagnosticLength || 1);
if (exports.kame_wasm_instance_diagnostic_copy(missingInstance, diagnosticPointer, diagnosticLength) !== 0) throw new Error('missing target diagnostic copy failed');
if (!new TextDecoder().decode(new Uint8Array(exports.memory.buffer, diagnosticPointer, diagnosticLength)).includes('TGT_NO_RULE')) throw new Error('unexpected missing target diagnostic');
exports.kame_wasm_instance_free(missingInstance);
NODE
then
	test-ok "read, environment, and missing-target paths behave"
else
	test-fail "wasm target materialization assertions failed"
fi

test-end
