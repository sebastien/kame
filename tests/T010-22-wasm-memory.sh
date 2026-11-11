#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — allocation-free exhaustion diagnostics
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T010-22 WASM memory exhaustion"
cd "$CLI_ROOT"
make wasm >/dev/null
test-step "Go reactor validates buffers and preserves language recursion guards"
if node --input-type=module <<'NODE'; then
import fs from 'node:fs';
import assert from 'node:assert/strict';
import { instantiateReactor } from './tests/wasm/instantiate-reactor.mjs';
const { instance: { exports: e } } = await instantiateReactor(fs.readFileSync('build/wasm/kame.wasm'));
const alloc = (size, align = 1) => { const p = e.kame_wasm_alloc(size, align); assert.ok(p); return p; };
const write = text => { const b = Buffer.from(text), p = alloc(b.length || 1); new Uint8Array(e.memory.buffer,p,b.length).set(b); return [p,b.length]; };
const diagnostic = alloc(256), length = alloc(4,4), output = alloc(128);
const instanceDiagnostic = handle => { assert.equal(e.kame_wasm_instance_diagnostic_copy(handle,diagnostic,256),0); return Buffer.from(e.memory.buffer,diagnostic,e.kame_wasm_instance_diagnostic_length(handle)).toString(); };
assert.equal(e.kame_wasm_alloc(0xffffffff,16),0);
assert.equal(e.kame_wasm_copy(0xffffffff,1,output,1),4);
assert.equal(e.kame_wasm_free(0,0),0);
const recursiveSource = write('(walk TEXT) = (if (= (count TEXT) 0) 0 (character TEXT))\n(character TEXT) = (walk (slice 1 TEXT))\n');
for (const [expression, code] of [
  ['(walk "x,y\\n")', ''],
  ['(walk (reduce ([TEXT STEP] (str TEXT TEXT)) "x,y\\n" [1 2 3 4 5 6 7 8 9 10]))', 'EVAL_DEPTH'],
  ['(walk "x,y\\n")', ''],
]) {
  const bounded = e.kame_wasm_instance_create();
  assert.notEqual(bounded, 0n);
  assert.equal(e.kame_wasm_source_compile(bounded, ...recursiveSource), 0);
  assert.equal(e.kame_wasm_expression_begin(bounded, ...write(expression)), 0);
  let state = 0;
  for (let i = 0; i < 100 && state !== 2; i++) state = e.kame_wasm_step(bounded);
  assert.equal(state, 2);
  // Result retrieval materializes ordinary evaluation diagnostics at the ABI.
  const copied = e.kame_wasm_result_copy(bounded, output, 128, length);
  if (code) {
    assert.equal(copied, 5);
    assert.match(instanceDiagnostic(bounded), /^EVAL_DEPTH:/);
  } else {
    assert.equal(copied, 0);
    assert.equal(new DataView(e.memory.buffer).getUint32(length, true), 1);
    assert.equal(Buffer.from(e.memory.buffer, output, 1).toString(), '0');
    assert.equal(e.kame_wasm_instance_diagnostic_length(bounded), 0);
  }
  assert.equal(e.kame_wasm_instance_free(bounded), 0);
}
NODE
	test-ok "invalid buffer ranges are rejected and evaluator depth remains bounded"
else
	test-fail "Go memory and recursion assertions failed"
fi
test-step "publisher failures kill and reap shell and argv children"
if python3 "$CLI_ROOT/tests/wasm-memory.py" "$CLI_ROOT"; then
	test-ok "stdout/stderr failures preserve NO_MEMORY after process cleanup"
else
	test-fail "process publisher exhaustion assertions failed"
fi
test-step "explicit host buffers are freed and Go reclaims retired evaluator state"
if node tests/wasm/test-memory-reuse.mjs; then
	test-ok "large fingerprints and repeated reads stay within memory budgets"
else
	test-fail "WASM memory reuse regression"
fi
test-end
