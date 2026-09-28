#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — freestanding host ABI
# Spec: docs/spec/013-tests.md — T010-05-wasm-rule-target
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-05 wasm rule target execution"
cd "$CLI_ROOT"

make wasm >/dev/null

test-step "a file rule materializes with its recipe forwarded to the host"
if node --input-type=module - <<'NODE'
import { readFileSync } from 'node:fs';

const bytes = await readFileSync('./build/wasm/kame.wasm');
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
const decode = (pointer, length) => new TextDecoder().decode(new Uint8Array(exports.memory.buffer, pointer, length));
const headerField = (handle, offset, type) => {
  const header = alloc(48, 8);
  if (exports.kame_wasm_next_event_header(handle, header, 48) !== 0) throw new Error('request header unavailable');
  const view = new DataView(exports.memory.buffer, header, 48);
  if (type === 'request') return view.getBigUint64(20, true);
  return view.getUint32(44, true);
};
const payloadText = (handle) => {
  const length = headerField(handle, 0, 'length');
  const pointer = alloc(length || 1);
  if (exports.kame_wasm_event_payload_copy(handle, pointer, length) !== 0) throw new Error('payload copy failed');
  return decode(pointer, length);
};
const resultText = (handle) => {
  const lengthPointer = alloc(4, 4);
  const query = exports.kame_wasm_result_copy(handle, 0, 0, lengthPointer);
  if (query !== 0 && query !== 3) throw new Error(`result query failed: ${query}`);
  const length = new DataView(exports.memory.buffer, lengthPointer, 4).getUint32(0, true);
  const output = alloc(length || 1);
  if (exports.kame_wasm_result_copy(handle, output, length, lengthPointer) !== 0) throw new Error('result copy failed');
  return decode(output, length);
};

const handle = exports.kame_wasm_instance_create();
const [source, sourceLength] = write('./out.txt :\n\tprintf written\n');
if (exports.kame_wasm_source_compile(handle, source, sourceLength) !== 0) throw new Error('source did not compile');
if (exports.kame_wasm_set_forwarding(handle, 1) !== 0) throw new Error('forwarding was rejected');
const [target, targetLength] = write('./out.txt');
if (exports.kame_wasm_target_begin(handle, target, targetLength) !== 0) throw new Error('target did not begin');
let state = 0;
let recipes = 0;
for (let i = 0; i < 256 && state !== 2; i++) {
  state = exports.kame_wasm_step(handle);
  if (state === 1) {
    if (exports.kame_wasm_next_request_kind(handle) !== 3) throw new Error('recipe kind was not 3');
    const request = headerField(handle, 0, 'request');
    const script = payloadText(handle);
    if (script !== 'printf written') throw new Error(`unexpected recipe script: ${script}`);
    const [pointer, length] = write('');
    if (exports.kame_wasm_complete_text(handle, request, pointer, length) !== 0) throw new Error('recipe completion failed');
    recipes++;
  }
}
if (state !== 2) throw new Error(`rule target did not complete: ${state}`);
if (recipes !== 1) throw new Error(`recipe request count was ${recipes}`);
if (resultText(handle) !== './out.txt') throw new Error('rule target path was wrong');
if (exports.kame_wasm_instance_free(handle) !== 0) throw new Error('instance did not free');
NODE
then
	test-ok "recipe was forwarded and the target completed"
else
	test-fail "wasm rule target assertions failed"
fi

test-end
