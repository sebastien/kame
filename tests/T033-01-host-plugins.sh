#!/usr/bin/env bash
# Spec: docs/spec/033-plugins.md — callback and native-process adapters
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T033-01 plugin adapters"
cd "$CLI_ROOT"

test-step "build the public WASM embedding"
make dist-wasm >/dev/null
test-ok "WASM plugin registration and callback exports are current"

test-step "round-trip, identity checks, capability gates, limits, timeout and failure"
if node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Kame } from './dist/kame.js';

const declarations = (operation = {}) => [{
  name: 'example',
  version: '1',
  operations: [{ name: 'example-run', version: '2', minArity: 1, maxArity: 1, ...operation }],
}];
async function call(expression, plugin, callback, options = {}) {
  const kame = await Kame.create();
  const program = await kame.compile('answer = 0\n', { name: 'plugin.km' });
  try {
    return await program.evaluate(expression, { plugins: plugin, pluginCallbacks: { example: callback }, ...options });
  } finally {
    await program.dispose();
    await kame.dispose();
  }
}

let calls = 0;
const roundTrip = await call('(example-run "hello")', declarations(), async (request, { signal }) => {
  calls++;
  assert.equal(signal.aborted, false);
  assert.equal(request.protocol, 1);
  assert.equal(request.plugin, 'example');
  assert.equal(request.operation, 'example-run');
  assert.deepEqual(request.args, [{ kind: 'string', data: 'hello' }]);
  return { protocol: 1, request: request.request, plugin: request.plugin, pluginVersion: request.pluginVersion, operation: request.operation, operationVersion: request.operationVersion, generation: request.generation, attempt: request.attempt, value: { kind: 'string', data: 'plugin value' } };
});
assert.equal(JSON.parse(roundTrip), 'plugin value');
assert.equal(calls, 1);

await assert.rejects(call('(example-run "x")', declarations(), async (request) => ({
  protocol: 1, request: 'stale', plugin: request.plugin, pluginVersion: request.pluginVersion,
  operation: request.operation, operationVersion: request.operationVersion, generation: request.generation, attempt: request.attempt, value: { kind: 'nil' },
})), (error) => error.code === 'PLUGIN_PROTOCOL');

let deniedCalls = 0;
await assert.rejects(call('(example-run "x")', declarations({ capabilities: ['run'] }), async () => { deniedCalls++; return {}; }), (error) => error.code === 'CAP_DENIED');
assert.equal(deniedCalls, 0);

let limitedCalls = 0;
await assert.rejects(call('(example-run "x")', declarations({ maxRequestBytes: 1 }), async () => { limitedCalls++; return {}; }), (error) => error.code === 'PLUGIN_LIMIT');
assert.equal(limitedCalls, 0);

await assert.rejects(call('(example-run "x")', declarations({ timeoutMS: 10 }), async () => new Promise(() => {})), (error) => error.code === 'PLUGIN_TIMEOUT');
await assert.rejects(call('(example-run "x")', declarations(), async () => { throw new Error('secret plugin details'); }), (error) => error.code === 'PLUGIN_FAIL' && !error.message.includes('secret'));

let callbackSignal;
const abortController = new AbortController();
const cancelled = call('(example-run "x")', declarations(), async (_request, { signal }) => {
  callbackSignal = signal;
  return new Promise(() => {});
}, { signal: abortController.signal });
while (!callbackSignal) await new Promise((resolve) => setTimeout(resolve, 1));
abortController.abort();
await assert.rejects(cancelled, (error) => error.code === 'EXEC_CANCELLED');
assert.equal(callbackSignal.aborted, true);

await assert.rejects(call('(example-run "x")', declarations({ maxResponseBytes: 256 }), async (request) => ({
  protocol: 1, request: request.request, plugin: request.plugin, pluginVersion: request.pluginVersion,
  operation: request.operation, operationVersion: request.operationVersion, generation: request.generation, attempt: request.attempt,
  value: { kind: 'string', data: 'x'.repeat(512) },
})), (error) => error.code === 'PLUGIN_LIMIT');

const processDeclaration = declarations();
processDeclaration[0].argv = [process.execPath, '-e', `
let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => input += chunk);
process.stdin.on('end', () => {
  const request = JSON.parse(input);
  if (process.argv[1] !== 'literal;$(touch should-not-exist)') process.exit(7);
  process.stdout.write(JSON.stringify({ protocol: request.protocol, request: request.request, plugin: request.plugin, pluginVersion: request.pluginVersion, operation: request.operation, operationVersion: request.operationVersion, generation: request.generation, attempt: request.attempt, value: { kind: 'string', data: 'native value' } }) + '\\n');
});`, 'literal;$(touch should-not-exist)'];
const nativeRoundTrip = await call('(example-run "native")', processDeclaration, undefined);
assert.equal(JSON.parse(nativeRoundTrip), 'native value');

const malformedProcess = declarations();
malformedProcess[0].argv = [process.execPath, '-e', `process.stdin.resume(); process.stdin.on('end', () => process.stdout.write('{}\\n{}\\n'));`];
await assert.rejects(call('(example-run "x")', malformedProcess, undefined), (error) => error.code === 'PLUGIN_PROTOCOL');

const failedProcess = declarations();
failedProcess[0].argv = [process.execPath, '-e', `process.stdin.resume(); process.stdin.on('end', () => { process.stderr.write('secret'); process.exit(9); });`];
await assert.rejects(call('(example-run "x")', failedProcess, undefined), (error) => error.code === 'PLUGIN_FAIL' && !error.message.includes('secret'));

const timedProcess = declarations({ timeoutMS: 20 });
timedProcess[0].argv = [process.execPath, '-e', `process.stdin.resume(); setInterval(() => {}, 1000);`];
await assert.rejects(call('(example-run "x")', timedProcess, undefined), (error) => error.code === 'PLUGIN_TIMEOUT');

const processDir = mkdtempSync(join(tmpdir(), 'kame-plugin-'));
const readyPath = join(processDir, 'ready');
const stoppedPath = join(processDir, 'stopped');
const cancelProcess = declarations();
cancelProcess[0].argv = [process.execPath, '-e', `
const fs = require('node:fs');
fs.writeFileSync(process.argv[1], 'ready');
process.on('SIGTERM', () => { fs.writeFileSync(process.argv[2], 'stopped'); process.exit(0); });
process.stdin.resume();
setInterval(() => {}, 1000);`, readyPath, stoppedPath];
const processAbortController = new AbortController();
const cancelledProcess = call('(example-run "x")', cancelProcess, undefined, { signal: processAbortController.signal });
while (!existsSync(readyPath)) await new Promise((resolve) => setTimeout(resolve, 1));
processAbortController.abort();
await assert.rejects(cancelledProcess, (error) => error.code === 'EXEC_CANCELLED');
assert.equal(readFileSync(stoppedPath, 'utf8'), 'stopped');
rmSync(processDir, { recursive: true, force: true });
NODE
then
	test-ok "callback and process adapters enforce identity, capability, bounds, timeout and failure contracts"
else
	test-fail "JavaScript plugin callback conformance failed"
fi

test-end
