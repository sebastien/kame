#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — freestanding host ABI
# Spec: docs/spec/013-tests.md — T010-04-wasm-forwarding
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-04 wasm host-request forwarding"
cd "$CLI_ROOT"

make wasm >/dev/null

work="$(mktemp -d)"
read_file="$work/read.txt"
write_file="$work/write.txt"
printf 'forwarded-read' >"$read_file"
trap 'rm -rf "$work"' EXIT

test-step "target requests are forwarded and completed asynchronously"
if KAME_READ_PATH="$read_file" KAME_WRITE_PATH="$write_file" node --input-type=module - <<'NODE'
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';

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
const requestHandle = () => {
  const pointer = alloc(48, 8);
  if (exports.kame_wasm_next_event_header(current, pointer, 48) !== 0) throw new Error('request header unavailable');
  return new DataView(exports.memory.buffer, pointer, 48).getBigUint64(20, true);
};
const requestKind = () => exports.kame_wasm_next_request_kind(current);
const payloadText = () => {
  const pointer = alloc(48, 8);
  if (exports.kame_wasm_next_event_header(current, pointer, 48) !== 0) throw new Error('request header unavailable');
  const length = new DataView(exports.memory.buffer, pointer, 48).getUint32(44, true);
  const payload = alloc(length || 1);
  if (exports.kame_wasm_event_payload_copy(current, payload, length) !== 0) throw new Error('request payload copy failed');
  return decode(payload, length);
};
const requestData = (handle) => {
  const length = exports.kame_wasm_next_request_data_length(handle);
  const pointer = alloc(length || 1);
  if (exports.kame_wasm_request_data_copy(handle, pointer, length) !== 0) throw new Error('request data copy failed');
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
let current = 0n;

const run = (source, service) => {
  const handle = exports.kame_wasm_instance_create();
  const [sourcePointer, sourceLength] = write(source);
  if (exports.kame_wasm_source_compile(handle, sourcePointer, sourceLength) !== 0) throw new Error('source did not compile');
  if (exports.kame_wasm_set_forwarding(handle, 1) !== 0) throw new Error('forwarding was rejected');
  const [target, targetLength] = write('result');
  if (exports.kame_wasm_target_begin(handle, target, targetLength) !== 0) throw new Error('target did not begin');
  current = handle;
  let state = 0;
  let requests = 0;
  for (let i = 0; i < 256 && state !== 2; i++) {
    state = exports.kame_wasm_step(handle);
    if (state === 1) {
      requests++;
      const request = requestHandle();
      if (requestKind() === 27) {
        const name = payloadText();
        if (!existsSync(name)) {
          if (exports.kame_wasm_complete_nil(handle, request) !== 0) throw new Error('missing file content completion failed');
        } else {
          const data = readFileSync(name);
          const pointer = alloc(data.length || 1);
          new Uint8Array(exports.memory.buffer, pointer, data.length).set(data);
          if (exports.kame_wasm_complete_bytes(handle, request, pointer, data.length) !== 0) throw new Error('file content completion failed');
        }
      } else if (requestKind() === 7) {
        const [pointer, length] = write(JSON.stringify(existsSync(payloadText())));
        if (exports.kame_wasm_complete_json(handle, request, pointer, length) !== 0) throw new Error('file existence completion failed');
      } else service(handle, request);
    }
  }
  if (state !== 2) throw new Error(`target did not complete: ${state}`);
  if (requests === 0) throw new Error('no host request was forwarded');
  return { handle, text: resultText(handle) };
};

const readRun = run(`result = (read "${process.env.KAME_READ_PATH}")`, (handle, request) => {
  if (requestKind() !== 1) throw new Error('read kind was not 1');
  const data = new TextEncoder().encode(readFileSync(process.env.KAME_READ_PATH, 'utf8'));
  const pointer = alloc(data.length || 1);
  new Uint8Array(exports.memory.buffer, pointer, data.length).set(data);
  if (exports.kame_wasm_complete_bytes(handle, request, pointer, data.length) !== 0) throw new Error('read completion failed');
});
if (readRun.text !== 'forwarded-read') throw new Error('forwarded read value was wrong');
exports.kame_wasm_instance_free(readRun.handle);

const shellRun = run('result = (shell "printf forwarded-shell")', (handle, request) => {
  if (requestKind() !== 3) throw new Error('process kind was not 3');
  const child = spawnSync('/bin/sh', ['-c', payloadText()], { encoding: 'utf8' });
  if (child.status !== 0) throw new Error('shell failed');
  const [pointer, length] = write(child.stdout);
  if (exports.kame_wasm_complete_text(handle, request, pointer, length) !== 0) throw new Error('process completion failed');
});
if (shellRun.text !== '"forwarded-shell"') throw new Error('forwarded shell value was wrong');
exports.kame_wasm_instance_free(shellRun.handle);

const writeRun = run(`result = (write "${process.env.KAME_WRITE_PATH}" "forwarded-write")`, (handle, request) => {
  if (requestKind() !== 2) throw new Error('write kind was not 2');
  const data = requestData(handle);
  const target = payloadText();
  writeFileSync(target, data);
  if (exports.kame_wasm_complete_nil(handle, request) !== 0) throw new Error('write completion failed');
});
if (writeRun.text !== ':nil') throw new Error('write target value was not nil');
exports.kame_wasm_instance_free(writeRun.handle);
if (readFileSync(process.env.KAME_WRITE_PATH, 'utf8') !== 'forwarded-write') throw new Error('write target did not write the file');
NODE
then
	test-ok "read, process, and write requests were forwarded and completed"
else
	test-fail "wasm host-request forwarding assertions failed"
fi

test-end
