#!/usr/bin/env bash
# Spec: docs/spec/033-plugins.md — callback and native-process adapters
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T033-01 plugin adapters"
cd "$CLI_ROOT"

test-step "build the public WASM embedding"
make dist-wasm >/dev/null
test-ok "WASM plugin registration and callback exports are current"

test-step "round-trip all value kinds; verify identity, grants, bounds, timeout, cancellation and cleanup"
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
const responseFor = (request) => ({
  protocol: 1, request: request.request, plugin: request.plugin, pluginVersion: request.pluginVersion,
  operation: request.operation, operationVersion: request.operationVersion, generation: request.generation,
  attempt: request.attempt, value: { kind: 'int', data: 37 },
});

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

const allKinds = {
  kind: 'list',
  items: [
    { kind: 'nil' }, { kind: 'bool', data: true }, { kind: 'int', data: -42 },
    { kind: 'float', data: 1.5 }, { kind: 'string', data: 'text' },
    { kind: 'pattern', data: '{name:*}' }, { kind: 'bytes', data: 'QUJD' },
    { kind: 'resource', resourceKind: 'file', name: 'file:///work/input' },
    { kind: 'list', items: [{ kind: 'string', data: 'nested' }, { kind: 'bool', data: false }] },
    { kind: 'record', fields: [['field', { kind: 'string', data: 'record' }]] },
    { kind: 'resource', resourceKind: 'definition', name: 'answer' },
    { kind: 'resource', resourceKind: 'target', name: 'build' },
    { kind: 'resource', resourceKind: 'task', name: 'test' },
    { kind: 'resource', resourceKind: 'service', name: 'server' },
    { kind: 'resource', resourceKind: 'glob', name: '*.km' },
    { kind: 'resource', resourceKind: 'environment', name: 'PATH' },
    { kind: 'resource', resourceKind: 'tool', name: 'compiler' },
  ],
};
const allKindsDisplay = '[:nil :true -42 1.5 "text" "{name:*}" ABC file:///work/input ["nested" :false] [field: "record"] answer build test server *.km PATH compiler]';
const callbackKinds = await call('(example-run "all-kinds")', declarations(), async (request) => ({
  protocol: 1, request: request.request, plugin: request.plugin, pluginVersion: request.pluginVersion,
  operation: request.operation, operationVersion: request.operationVersion, generation: request.generation,
  attempt: request.attempt, value: allKinds,
}));
assert.equal(callbackKinds, allKindsDisplay, 'JavaScript callback changed a canonical Kame value kind');
for (let i = 0; i < 5; i++) {
  assert.equal(await call('(example-run "repeat")', declarations(), async (request) => ({
    ...responseFor(request), value: { kind: 'int', data: i },
  })), String(i), 'repeated plugin invocation did not return its value');
}

const embeddingKame = await Kame.create();
const embeddingProgram = await embeddingKame.compile('answer = (example-run "embedded")\n', { name: 'plugin-embedding.km' });
let buildCalls = 0;
await embeddingProgram.build('answer', {
  plugins: declarations(),
  pluginCallbacks: { example: async (request) => { buildCalls++; return responseFor(request); } },
});
assert.equal(buildCalls, 1, 'build invocation did not dispatch its plugin callback');
let watchCalls = 0;
const duplicateDeclarations = [...declarations(), ...declarations()];
await assert.rejects(
  embeddingProgram.watch('answer', { plugins: duplicateDeclarations, pluginCallbacks: { example: async (request) => { watchCalls++; return responseFor(request); } } }),
  (error) => error.code === 'PLUGIN_CONFIG',
);
const pluginWatch = await embeddingProgram.watch('answer', {
  plugins: declarations(),
  pluginCallbacks: { example: async (request) => { watchCalls++; return responseFor(request); } },
});
const initialPluginSnapshot = await Promise.race([
  pluginWatch.next(),
  new Promise((_, reject) => setTimeout(() => reject(new Error('plugin watch did not produce its initial snapshot')), 3000)),
]);
assert.equal(initialPluginSnapshot.done, false);
assert.equal(watchCalls, 0, 'watch setup unexpectedly executed a value-only target');
await pluginWatch.close();
await embeddingProgram.dispose();
await embeddingKame.dispose();

await assert.rejects(call('(example-run "x")', declarations(), async (request) => ({
  protocol: 1, request: 'stale', plugin: request.plugin, pluginVersion: request.pluginVersion,
  operation: request.operation, operationVersion: request.operationVersion, generation: request.generation, attempt: request.attempt, value: { kind: 'nil' },
})), (error) => error.code === 'PLUGIN_PROTOCOL');
await assert.rejects(call('(example-run "x")', declarations(), async (request) => ({
  ...responseFor(request), protocol: 2,
})), (error) => error.code === 'PLUGIN_PROTOCOL');
await assert.rejects(call('(example-run "x")', declarations(), async (request) => ({
  ...responseFor(request), operationVersion: 'stale',
})), (error) => error.code === 'PLUGIN_PROTOCOL');
await assert.rejects(call('(example-run "x")', declarations(), async (request) => ({
  ...responseFor(request), unexpected: true,
})), (error) => error.code === 'PLUGIN_PROTOCOL');
await assert.rejects(call('(example-run "x")', declarations(), async (request) => {
  const { value, ...identity } = responseFor(request);
  return { ...identity, error: { code: 'PLUGIN_FAIL', message: 'failed', private: 'extra' } };
}), (error) => error.code === 'PLUGIN_PROTOCOL');
await assert.rejects(call('(example-run "x")', declarations(), async (request) => ({
  ...responseFor(request), value: { kind: 'bytes', data: '%%%=' },
})), (error) => error.code === 'PLUGIN_PROTOCOL');
await assert.rejects(call('(example-run "x")', declarations(), async (request) => ({
  ...responseFor(request), value: { kind: 'int', data: 'not-an-integer' },
})), (error) => error.code === 'PLUGIN_PROTOCOL');

let arityCalls = 0;
await assert.rejects(call('(example-run "x")', declarations({ minArity: 2, maxArity: 2 }), async () => { arityCalls++; return {}; }), (error) => error.code === 'EXPR_INVALID');
assert.equal(arityCalls, 0, 'arity denial reached the plugin adapter');
const duplicateOperations = [{ name: 'example', version: '1', operations: [
  { name: 'same-operation', version: '1', minArity: 0, maxArity: 0 },
  { name: 'same-operation', version: '2', minArity: 0, maxArity: 0 },
]}];
await assert.rejects(call('(same-operation)', duplicateOperations, async () => ({})), (error) => error.code === 'PLUGIN_CONFIG');
await assert.rejects(call('(example-run "x")', declarations(), undefined), (error) => error.code === 'FEATURE_UNSUP');

let deniedCalls = 0;
await assert.rejects(call('(example-run "x")', declarations({ capabilities: ['run'] }), async () => { deniedCalls++; return {}; }), (error) => error.code === 'CAP_DENIED');
assert.equal(deniedCalls, 0);

let limitedCalls = 0;
await assert.rejects(call('(example-run "x")', declarations({ maxRequestBytes: 1 }), async () => { limitedCalls++; return {}; }), (error) => error.code === 'PLUGIN_LIMIT');
assert.equal(limitedCalls, 0);

await assert.rejects(call('(example-run "x")', declarations({ timeoutMS: 10 }), async () => new Promise(() => {})), (error) => error.code === 'PLUGIN_TIMEOUT');
await assert.rejects(call('(example-run "x")', declarations(), async () => { throw new Error('secret plugin details'); }), (error) => error.code === 'PLUGIN_FAIL' && !error.message.includes('secret'));

let callbackSignal;
let releaseLatePlugin;
const abortController = new AbortController();
const cancelled = call('(example-run "x")', declarations(), async (request, { signal }) => {
  callbackSignal = signal;
  return new Promise((resolve) => { releaseLatePlugin = () => resolve(responseFor(request)); });
}, { signal: abortController.signal });
while (!callbackSignal) await new Promise((resolve) => setTimeout(resolve, 1));
abortController.abort();
await assert.rejects(cancelled, (error) => error.code === 'EXEC_CANCELLED');
assert.equal(callbackSignal.aborted, true);
releaseLatePlugin();
await new Promise((resolve) => setTimeout(resolve, 5));

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
  if (process.argv[2] !== 'literal;$(touch should-not-exist)' || request.protocol !== 1 || request.args.length !== 1 || request.args[0].data !== 'native' || !input.endsWith('\\n') || input.slice(0, -1).includes('\\n')) process.exit(7);
  process.stdout.write(JSON.stringify({ protocol: request.protocol, request: request.request, plugin: request.plugin, pluginVersion: request.pluginVersion, operation: request.operation, operationVersion: request.operationVersion, generation: request.generation, attempt: request.attempt, value: JSON.parse(process.argv[1]) }) + '\\n');
});`, JSON.stringify(allKinds), 'literal;$(touch should-not-exist)'];
const nativeRoundTrip = await call('(example-run "native")', processDeclaration, undefined);
assert.equal(nativeRoundTrip, allKindsDisplay, 'native process changed a canonical Kame value kind');

const malformedProcess = declarations();
malformedProcess[0].argv = [process.execPath, '-e', `process.stdin.resume(); process.stdin.on('end', () => process.stdout.write('{}\\n{}\\n'));`];
await assert.rejects(call('(example-run "x")', malformedProcess, undefined), (error) => error.code === 'PLUGIN_PROTOCOL');

const wrongVersionProcess = declarations();
wrongVersionProcess[0].argv = [process.execPath, '-e', `let input=''; process.stdin.setEncoding('utf8'); process.stdin.on('data', chunk => input += chunk); process.stdin.on('end', () => { const request=JSON.parse(input); process.stdout.write(JSON.stringify({ protocol: 2, request: request.request, plugin: request.plugin, pluginVersion: request.pluginVersion, operation: request.operation, operationVersion: request.operationVersion, generation: request.generation, attempt: request.attempt, value: { kind: 'nil' } })+'\\n'); });`];
await assert.rejects(call('(example-run "x")', wrongVersionProcess, undefined), (error) => error.code === 'PLUGIN_PROTOCOL');

const staleRequestProcess = declarations();
staleRequestProcess[0].argv = [process.execPath, '-e', `let input=''; process.stdin.setEncoding('utf8'); process.stdin.on('data', chunk => input += chunk); process.stdin.on('end', () => { const request=JSON.parse(input); process.stdout.write(JSON.stringify({ ...request, request: 'stale', value: { kind: 'nil' } })+'\\n'); });`];
await assert.rejects(call('(example-run "x")', staleRequestProcess, undefined), (error) => error.code === 'PLUGIN_PROTOCOL');

const unknownFieldProcess = declarations();
unknownFieldProcess[0].argv = [process.execPath, '-e', `let input=''; process.stdin.setEncoding('utf8'); process.stdin.on('data', chunk => input += chunk); process.stdin.on('end', () => { const request=JSON.parse(input); process.stdout.write(JSON.stringify({ ...request, unexpected: true, value: { kind: 'nil' } })+'\\n'); });`];
await assert.rejects(call('(example-run "x")', unknownFieldProcess, undefined), (error) => error.code === 'PLUGIN_PROTOCOL');

const mistypedProcess = declarations();
mistypedProcess[0].argv = [process.execPath, '-e', `let input=''; process.stdin.setEncoding('utf8'); process.stdin.on('data', chunk => input += chunk); process.stdin.on('end', () => { const request=JSON.parse(input); process.stdout.write(JSON.stringify({ ...request, value: { kind: 'int', data: 'not-an-int' } })+'\\n'); });`];
await assert.rejects(call('(example-run "x")', mistypedProcess, undefined), (error) => error.code === 'PLUGIN_PROTOCOL');

const oversizedProcess = declarations({ maxResponseBytes: 256 });
oversizedProcess[0].argv = [process.execPath, '-e', `process.stdin.resume(); process.stdin.on('end', () => process.stdout.write('x'.repeat(300)));`];
await assert.rejects(call('(example-run "x")', oversizedProcess, undefined), (error) => error.code === 'PLUGIN_LIMIT');

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

const disposalDir = mkdtempSync(join(tmpdir(), 'kame-plugin-dispose-'));
const disposalReadyPath = join(disposalDir, 'ready');
const disposalStoppedPath = join(disposalDir, 'stopped');
const disposalKame = await Kame.create();
const disposalProgram = await disposalKame.compile('answer = (example-run "dispose")\n', { name: 'plugin-dispose.km' });
const disposalPlugin = declarations();
disposalPlugin[0].argv = [process.execPath, '-e', `
const fs = require('node:fs');
fs.writeFileSync(process.argv[1], 'ready');
process.on('SIGTERM', () => { fs.writeFileSync(process.argv[2], 'stopped'); process.exit(0); });
process.stdin.resume();
setInterval(() => {}, 1000);`, disposalReadyPath, disposalStoppedPath];
const disposedInvocation = disposalProgram.evaluate('answer', { plugins: disposalPlugin });
while (!existsSync(disposalReadyPath)) await new Promise((resolve) => setTimeout(resolve, 1));
await disposalProgram.dispose();
await assert.rejects(disposedInvocation, (error) => error.code === 'EXEC_CANCELLED');
assert.equal(readFileSync(disposalStoppedPath, 'utf8'), 'stopped', 'program disposal did not reap its native plugin process');
await disposalKame.dispose();
rmSync(disposalDir, { recursive: true, force: true });

if (process.platform === 'win32') {
  const treeDir = mkdtempSync(join(tmpdir(), 'kame-plugin-tree-'));
  const treeReady = join(treeDir, 'ready');
  const treePid = join(treeDir, 'child.pid');
  const treeKame = await Kame.create();
  const treeProgram = await treeKame.compile('answer = 0\n', { name: 'plugin-tree.km' });
  const treeDeclaration = declarations();
  treeDeclaration[0].argv = [process.execPath, '-e', `
const fs = require('node:fs');
const { spawn } = require('node:child_process');
const descendant = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' });
fs.writeFileSync(process.argv[1], String(descendant.pid));
fs.writeFileSync(process.argv[2], 'ready');
process.stdin.resume();
setInterval(() => {}, 1000);`, treePid, treeReady];
  const treeAbort = new AbortController();
  const treeInvocation = treeProgram.evaluate('(example-run "tree")', { plugins: treeDeclaration, signal: treeAbort.signal });
  while (!existsSync(treeReady)) await new Promise((resolve) => setTimeout(resolve, 1));
  const descendantPid = Number(readFileSync(treePid, 'utf8'));
  treeAbort.abort();
  await assert.rejects(treeInvocation, (error) => error.code === 'EXEC_CANCELLED');
  let descendantAlive = true;
  for (let attempt = 0; attempt < 100 && descendantAlive; attempt++) {
    try { process.kill(descendantPid, 0); }
    catch { descendantAlive = false; break; }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  assert.equal(descendantAlive, false, 'Windows cancellation left a plugin descendant running');
  await treeProgram.dispose();
  await treeKame.dispose();
  rmSync(treeDir, { recursive: true, force: true });
}
NODE
then
	test-ok "callback and process adapters enforce identity, capability, bounds, timeout and failure contracts"
else
	test-fail "JavaScript plugin callback conformance failed"
fi

test-end
