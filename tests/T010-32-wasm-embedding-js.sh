#!/usr/bin/env bash
# Spec: docs/spec/028-embedding-apis.md — JavaScript embedding lifecycle
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-32 JavaScript embedding API lifecycle"
test-step "build the importable WASM distribution"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
test-ok "WASM module and ESM entry point are current"

test-step "imports, copied values, asynchronous host completion, grants, builds, watches and disposal"
if node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import { Kame, KameProgram } from './dist/kame.js';

assert.equal(typeof Kame.create, 'function');
assert.equal(typeof KameProgram, 'function');
const kame = await Kame.create();
const program = await kame.compile('value = (read "data.txt")\n', { name: 'embedded.km' });
const ast = program.ast;
assert.equal(ast.ast.items[0].kind, 'definition');
ast.ast.items.length = 0;
assert.equal(program.ast.ast.items.length, 1, 'AST getter leaked mutable internal storage');

let deniedCalls = 0;
await assert.rejects(
  program.evaluate('value', { hostRequest: async () => { deniedCalls++; return { type: 'text', value: 'denied' }; } }),
  (error) => error.code === 'CAP_DENIED',
);
assert.equal(deniedCalls, 0, 'denied host request reached the callback');

let callbackCalls = 0;
const bytes = new TextEncoder().encode('copied host value');
const value = await program.evaluate('value', {
  grants: [{ capability: 'read', names: ['data.txt'] }],
  hostRequest: async (request) => {
    callbackCalls++;
    assert.equal(request.kind, 1);
    const independent = await Kame.create();
    const independentProgram = await independent.compile('other = 9\n', { name: 'other.km' });
    assert.equal(JSON.parse(await independentProgram.evaluate('other')), '9');
    await independent.dispose();
    return { type: 'bytes', value: bytes };
  },
});
bytes.fill(0);
assert.equal(value, 'copied host value');
assert.equal(callbackCalls, 1);

let markHostStarted;
const hostStarted = new Promise((resolve) => { markHostStarted = resolve; });
let releaseLateHost;
const abortController = new AbortController();
const cancelled = program.evaluate('value', {
  grants: [{ capability: 'read', names: ['data.txt'] }],
  signal: abortController.signal,
  hostRequest: async () => {
    markHostStarted();
    return new Promise((resolve) => { releaseLateHost = resolve; });
  },
});
await hostStarted;
abortController.abort();
await assert.rejects(cancelled, (error) => error.code === 'EXEC_CANCELLED');
releaseLateHost({ type: 'text', value: 'late completion' });
await new Promise((resolve) => setTimeout(resolve, 5));

const answer = await kame.compile('answer = 42\n', { name: 'answer.km' });
const built = await answer.build('answer');
assert.equal(built.results.length, 1);
assert.equal(built.results[0].target, 'answer');
assert.ok(Array.isArray(built.events));
built.events.push({ type: 'caller-mutation' });
assert.equal((await answer.build('answer')).events.some((event) => event.type === 'caller-mutation'), false);
await assert.rejects(kame.compile('x = 1\nx = 2\n'), (error) => ['DEF_INVALID', 'PARSE_ERR'].includes(error.code));
const shared = await kame.compile('a : shared\nb : shared\nshared :\n\t@(out "once")\n', { name: 'shared.kmk' });
const sharedBuild = await shared.build(['a', 'b']);
assert.equal(sharedBuild.events.filter((event) => event.type === 'target-started' && event.target === 'shared').length, 1);
assert.deepEqual(sharedBuild.results.map((result) => result.target), ['a', 'b']);
const abortedBuild = new AbortController();
abortedBuild.abort();
await assert.rejects(shared.build(['a', 'b'], { signal: abortedBuild.signal }), (error) => error.code === 'EXEC_CANCELLED');
const values = await kame.compile('a = 1\nb = 2\n');
assert.deepEqual((await values.build(['a', 'b'])).results.map((result) => result.value), ['"1"', '"2"']);
await shared.dispose();
await values.dispose();

const watch = await answer.watch('answer');
const first = await Promise.race([
  watch.next(),
  new Promise((_, reject) => setTimeout(() => reject(new Error('watch did not yield its initial snapshot')), 3000)),
]);
assert.equal(first.done, false);
assert.ok(first.value.snapshot);
first.value.snapshot.resources?.push({ name: 'caller-mutation' });
assert.equal(watch.snapshot().resources?.some((resource) => resource.name === 'caller-mutation'), false);
await watch.close();

// Reserve the program while watch creation is pending, not just once published.
const beginWatch = answer.module.beginWatch.bind(answer.module);
let releaseCreation;
const creationGate = new Promise((resolve) => { releaseCreation = resolve; });
answer.module.beginWatch = async (...args) => { await creationGate; return beginWatch(...args); };
const pendingWatch = answer.watch('answer');
await assert.rejects(answer.watch('answer'), (error) => error.code === 'BUSY');
await assert.rejects(answer.build('answer'), (error) => error.code === 'BUSY');
releaseCreation();
await (await pendingWatch).close();
await answer.dispose();

for (const disposeOwner of [false, true]) {
  const owner = await Kame.create();
  const pendingProgram = await owner.compile('answer = 42\n');
  const module = pendingProgram.module;
  const begin = module.beginWatch.bind(module);
  let markCreated, release;
  const created = new Promise((resolve) => { markCreated = resolve; });
  const gate = new Promise((resolve) => { release = resolve; });
  let closed = 0;
  const close = module.disposeWatch.bind(module);
  module.disposeWatch = async (watch) => { closed++; return close(watch); };
  module.beginWatch = async (...args) => {
    const watch = await begin(...args);
    markCreated();
    await gate;
    return watch;
  };
  const pending = pendingProgram.watch('answer');
  const rejected = assert.rejects(pending, (error) => error.code === 'EXEC_CANCELLED');
  await created;
  const disposing = disposeOwner ? owner.dispose() : pendingProgram.dispose();
  release();
  await Promise.all([disposing, rejected]);
  assert.equal(closed, 1, 'pending watch leaked during disposal');
  assert.equal(owner.watches.size, 0);
  assert.equal(owner.operations.size, 0);
  assert.equal(pendingProgram.module, null);
  await owner.dispose();
}

for (let i = 0; i < 100; i++) {
  const cycle = await kame.compile(`value = ${i}\n`, { name: `cycle-${i}.km` });
  assert.equal(JSON.parse(await cycle.evaluate('value')), String(i));
  await cycle.dispose();
}

await program.dispose();
await kame.dispose();
await kame.dispose();
await assert.rejects(kame.compile('value = 1\n'), (error) => error.code === 'DISPOSED');
NODE
then
	test-ok "JS embedding copies results, observes grants, completes async requests, and reuses 100 lifecycle cycles"
else
	test-fail "JS embedding API conformance failed"
fi

test-end
