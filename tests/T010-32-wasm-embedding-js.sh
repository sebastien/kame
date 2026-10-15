#!/usr/bin/env bash
# Spec: docs/spec/028-embedding-apis.md — JavaScript embedding lifecycle
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-32 JavaScript embedding API lifecycle"
test-step "build the importable WASM distribution"
cd "$CLI_ROOT"
if [ "${KAME_TEST_SKIP_BUILD:-0}" != 1 ]; then
	make dist-wasm >/dev/null
fi
test-ok "WASM module and ESM entry point are current"

test-step "imports, copied values, asynchronous host completion, grants, builds, watches and disposal"
if node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import childProcess from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
const { Kame, KameProgram } = await import(process.env.KAME_TEST_JS ?? './dist/kame.js');

assert.equal(typeof Kame.create, 'function');
assert.equal(typeof KameProgram, 'function');
const kame = await Kame.create();
assert.equal(typeof kame.module.exports.kame_wasm_next_request_id, 'function');
// Spec038: direct Kash keeps commands and embedded expressions distinct.
const kashSource = 'printf "%s" (str "two " "words")\n(nop [value: 1])\n';
for (const source of [kashSource, await kame.format('kash', kashSource)]) {
  const items = (await kame.parse('kash', source)).ast.items;
  assert.deepEqual(items.map((item) => item.kind), ['command', 'expression']);
  const argument = items[0].expression.items[0].items[2];
  assert.equal(argument.kind, 'command-word');
  assert.equal(argument.parts[0].form, '(');
  assert.equal(argument.parts[0].expression.kind, 'application');
  assert.equal(argument.parts[0].expression.items[0].text, 'str');
  assert.equal(items[1].expression.items[1].kind, 'record');
}
// Exercise checked narrowing on the actual WASM32 target, not only native64.
const boundsProgram = await kame.compile('');
for (const collection of ['[1 2]', '[value: [1 2]]']) {
  await assert.rejects(boundsProgram.evaluate(`(str ${collection})`), (error) => error.code === 'EXPR_INVALID');
  assert.equal(await boundsProgram.evaluate(`(= (parse-json (json ${collection})) ${collection})`), ':true');
}
const writes = { out: [], err: [] };
const stdoutWrite = process.stdout.write;
const stderrWrite = process.stderr.write;
process.stdout.write = (data) => { writes.out.push(Buffer.from(data).toString()); return true; };
process.stderr.write = (data) => { writes.err.push(Buffer.from(data).toString()); return true; };
try {
  assert.equal(await boundsProgram.evaluate('[value: 1]'), '[value: 1]');
  assert.deepEqual(JSON.parse(JSON.parse(await boundsProgram.evaluate('(json [value: 1])'))), { value: 1 });
  assert.deepEqual(writes, { out: [], err: [] }, 'value display/JSON encoding must not publish effects');
  assert.equal(await boundsProgram.evaluate('(let [emit out!] (emit "out"))'), '"out"');
  assert.equal(await boundsProgram.evaluate('(err! "err")'), '"err"');
  assert.deepEqual(writes, { out: ['out'], err: ['err'] });
  await assert.rejects(boundsProgram.evaluate('(fail! "stop")'), (error) => error.code === 'USER_ERROR');
  await assert.rejects(boundsProgram.evaluate('((kash """value = (fail! "stop") ?? (nop "fallback"); (out! value)"""))'), (error) => error.code === 'USER_ERROR');
  assert.deepEqual(writes, { out: ['out'], err: ['err'] }, 'authored failure must not publish fallback/display');
} finally {
  process.stdout.write = stdoutWrite;
  process.stderr.write = stderrWrite;
}
for (const expression of ['(nth 4294967296 [7])', '(nth -4294967296 "a")']) {
  assert.equal(await boundsProgram.evaluate(expression), ':nil', expression);
}
for (const expression of ['(slice 4294967296 [7])', '(slice 0 4294967296 "a")']) {
  await assert.rejects(boundsProgram.evaluate(expression), (error) => error.code === 'EXPR_INVALID', expression);
}
for (const expression of ['(let [xs [7]] xs.-2147483648)', '(let [xs [7]] xs.0..-2147483648)']) {
  await assert.rejects(boundsProgram.evaluate(expression), (error) => error.code === 'SEL_INDEX_INVALID', expression);
}
assert.equal(await boundsProgram.evaluate('(let [xs [7 8]] xs.1..)'), '[8]');
await boundsProgram.dispose();
const formatted = await kame.format('script', 'value = 42\n');
const hostPointer = kame.module.exports.kame_wasm_alloc(1, 1);
for (let i = 0; i < 100; i++) {
  const parsed = await kame.parse('script', `value = ${i}\n`);
  assert.equal(parsed.ast.items.length, 1);
  assert.equal(parsed.ast.items[0].kind, 'definition');
  parsed.ast.items.length = 0;
  assert.equal(await kame.format('script', 'value = 42\n'), formatted);
}
assert.equal(kame.module.exports.kame_wasm_alloc(1, 1), hostPointer + 1, 'parse/format grew the retained owner host heap');
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
    assert.equal(JSON.parse(await independentProgram.evaluate('other')), 9);
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
const shared = await kame.compile('a : shared\nb : shared\nshared :\n\t@(out! "once")\n', { name: 'shared.kmk' });
const sharedBuild = await shared.build(['a', 'b']);
assert.equal(sharedBuild.events.filter((event) => event.type === 'target-started' && event.target === 'shared').length, 1);
assert.deepEqual(sharedBuild.results.map((result) => result.target), ['a', 'b']);
const abortedBuild = new AbortController();
abortedBuild.abort();
await assert.rejects(shared.build(['a', 'b'], { signal: abortedBuild.signal }), (error) => error.code === 'EXEC_CANCELLED');
const values = await kame.compile('a = 1\nb = 2\n');
assert.deepEqual((await values.build(['a', 'b'])).results.map((result) => result.value), ['1', '2']);
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
// Keep next() active while close releases its state, including an idle wait.
const waitingWatchNext = (async () => {
  while (!watch.closed) {
    const update = await watch.next();
    if (update.done) return update;
  }
  return { done: true, value: undefined };
})();
await new Promise((resolve) => setTimeout(resolve, 20));
const disposeWatch = watch.module.disposeWatch.bind(watch.module);
let releaseWatchClose, markWatchClosing;
const watchClosing = new Promise((resolve) => { markWatchClosing = resolve; });
const watchCloseGate = new Promise((resolve) => { releaseWatchClose = resolve; });
watch.module.disposeWatch = async (state) => { markWatchClosing(); await watchCloseGate; return disposeWatch(state); };
const firstWatchClose = watch.close();
await watchClosing;
let secondWatchClosed = false;
const secondWatchClose = watch.close().then(() => { secondWatchClosed = true; });
await new Promise((resolve) => setTimeout(resolve, 5));
assert.equal(secondWatchClosed, false, 'second watch close skipped cleanup');
assert.ok(watch.module && watch.watch, 'watch released state before cleanup finished');
releaseWatchClose();
await Promise.all([firstWatchClose, secondWatchClose]);
assert.deepEqual(await waitingWatchNext, { done: true, value: undefined });
assert.equal(watch.module, null, 'closed watch retained its WASM module');
assert.equal(watch.watch, null, 'closed watch retained its operational state');
await watch.close();
await assert.rejects(watch.next(), (error) => error.code === 'DISPOSED');
assert.throws(() => watch.snapshot(), (error) => error.code === 'DISPOSED');
const retainedClosedWatches = [watch];
for (let i = 0; i < 3; i++) {
  const closed = await answer.watch('answer');
  await closed.close();
  retainedClosedWatches.push(closed);
}
assert.ok(retainedClosedWatches.every((closed) => closed.module === null && closed.watch === null));

// Retaining the descriptor must not retain a growing operational host heap.
for (let i = 0; i < 100; i++) {
  const previous = answer.module.exports.memory;
  assert.equal(await answer.evaluate('answer'), '42');
  assert.notEqual(answer.module.exports.memory, previous);
}
const evaluatedMemory = answer.module.exports.memory;
await answer.build('answer');
assert.notEqual(answer.module.exports.memory, evaluatedMemory);
const builtMemory = answer.module.exports.memory;
await (await answer.watch('answer')).close();
assert.notEqual(answer.module.exports.memory, builtMemory);
assert.equal(answer.ast.ast.items[0].kind, 'definition');

const preAborted = new AbortController();
preAborted.abort();
await assert.rejects(kame.parse('script', 'value = 42\n', { signal: preAborted.signal }), (error) => error.code === 'EXEC_CANCELLED');
await assert.rejects(kame.format('script', 'value = 42\n', { signal: preAborted.signal }), (error) => error.code === 'EXEC_CANCELLED');
await assert.rejects(answer.evaluate('42', { signal: preAborted.signal }), (error) => error.code === 'EXEC_CANCELLED');

const directory = mkdtempSync(join(tmpdir(), 'kame-js-process-'));
try {
  const typedArgv = await kame.compile('SHELL = kash\n./argv.txt :\n\tprintf "<%s>" (concat [(str """@(missing)""" " two words")] ["*.c" """$(touch injected)""" ""]) > ./argv.txt\n\t(nop [value: 1])\n', { name: 'typed-argv.kmk' });
  try {
    await typedArgv.build('./argv.txt', { cwd: directory, grants: [{ capability: 'run', names: [] }, { capability: 'write', names: [directory] }] });
    assert.equal(readFileSync(join(directory, 'argv.txt'), 'utf8'), '<@(missing) two words><*.c><$(touch injected)><>');
    assert.equal(existsSync(join(directory, 'injected')), false);
  } finally { await typedArgv.dispose(); }
  assert.equal(JSON.parse(await answer.evaluate('(sh! "pwd")', { cwd: directory, grants: [{ capability: 'run', names: [] }] })).trim(), directory);
  for (const cleanup of ['abort', 'program', 'owner']) {
    const owner = await Kame.create();
    const retained = await owner.compile('answer = 42\n');
    const controller = new AbortController();
    const filename = join(directory, `${cleanup}.pid`);
    const running = retained.evaluate(`(sh! "sleep 30 & echo $$ $! > ${filename}; wait")`, {
      cwd: directory, grants: [{ capability: 'run', names: [] }], signal: controller.signal,
    });
    const rejected = assert.rejects(running, (error) => error.code === 'EXEC_CANCELLED');
    try {
      const deadline = Date.now() + 3000;
      while (!existsSync(filename)) {
        assert.ok(Date.now() < deadline, 'shell did not start');
        await new Promise((resolve) => setTimeout(resolve, 10));
      }
      if (cleanup === 'abort') controller.abort();
      else {
        const disposing = cleanup === 'owner' ? owner : retained;
        await Promise.all([disposing.dispose(), disposing.dispose()]);
      }
      await rejected;
      const pid = Number(readFileSync(filename, 'utf8').trim().split(' ')[0]);
      assert.throws(() => process.kill(pid, 0), (error) => error.code === 'ESRCH', 'shell was not reaped');
    } finally {
      controller.abort();
      await owner.dispose();
    }
  }
  // Capture real spawned children: publication failure must prevent later
  // stages from launching, not merely kill the children seen so far.
  const processPrototype = Object.getPrototypeOf(answer.module);
  const processStarted = processPrototype.processStarted;
  const spawnProcess = childProcess.spawn;
  const platform = Object.getOwnPropertyDescriptor(process, 'platform');
  const expression = '(pipe! (run! "/bin/sh" "-c" "exec sleep 30 # producer") (run! "/bin/sh" "-c" "exec sleep 30 # consumer"))';
  const faultProgram = await kame.compile(`probe :\n\t@${expression}\n`, { name: 'fault.kmk' });
  for (const failure of ['publication', 'EIO']) {
    const children = [];
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 3000);
    childProcess.spawn = function (executable, args, options) {
      const child = spawnProcess(executable, args, options);
      if (executable === '/bin/sh' && args.some((arg) => arg.includes('# producer') || arg.includes('# consumer'))) {
        children.push(child);
        if (failure !== 'publication' && args.some((arg) => arg.includes('# consumer'))) {
          queueMicrotask(() => child.stdin.emit('error', Object.assign(new Error('injected live-consumer input failure'), { code: failure })));
        }
      }
      return child;
    };
    syncBuiltinESMExports();
    if (failure === 'publication') processPrototype.processStarted = () => { throw Object.assign(new Error('injected process publication failure'), { code: 'NO_MEMORY' }); };
    // Select the Windows stream-piping branch on POSIX for an injected error
    // probe; this is not a substitute for native Windows process-tree testing.
    else Object.defineProperty(process, 'platform', { ...platform, value: 'win32' });
    try {
      await assert.rejects(faultProgram.build('probe', { cwd: directory, grants: [{ capability: 'run', names: [] }], signal: controller.signal }), (error) => error.code === (failure === 'publication' ? 'NO_MEMORY' : 'HOST_FAIL'));
      assert.equal(children.length, failure === 'publication' ? 1 : 2, 'publication failure launched a later stage');
      for (const child of children) {
        assert.ok(child.exitCode !== null || child.signalCode !== null, 'owned child has not exited');
        assert.throws(() => process.kill(child.pid, 0), (error) => error.code === 'ESRCH', 'owned child was not reaped');
      }
    } finally {
      controller.abort(); clearTimeout(timeout);
      processPrototype.processStarted = processStarted;
      Object.defineProperty(process, 'platform', platform);
      childProcess.spawn = spawnProcess; syncBuiltinESMExports();
    }
  }
  await faultProgram.dispose();
  // Select Windows stream piping on POSIX: these real early consumers exercise
  // pipe closure, but do not establish native Windows process-tree conformance.
  for (const closure of ['exit', 'read-one', 'close-stdin-live', 'EPIPE', 'ECONNRESET']) {
    const marker = join(directory, `closed-input-${closure}`);
    const producer = `trap 'exit 17' PIPE; exec 2>/dev/null; while :; do printf '%s\\n' producer || exit 17; done # producer`;
    const consumer = closure === 'exit' ? 'exit 0 # consumer'
      : closure === 'read-one' ? 'read line; printf "%s\\n" "$line" # consumer'
      : closure === 'close-stdin-live' ? `exec 0<&-; sleep 0.1; printf done > ${marker} # consumer`
      : `cat >/dev/null; printf done > ${marker} # consumer`;
    const source = `probe :\n\t@(pipe! (run! "/bin/sh" "-c" ${JSON.stringify(producer)}) (run! "/bin/sh" "-c" ${JSON.stringify(consumer)}))\n`;
    const early = await kame.compile(source, { name: 'early.kmk' });
    const children = [];
    let injected = false;
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 3000);
    childProcess.spawn = function (executable, args, options) {
      const child = spawnProcess(executable, args, options);
      if (executable === '/bin/sh' && args.some((arg) => arg.includes('# producer') || arg.includes('# consumer'))) {
        children.push(child);
        if (['EPIPE', 'ECONNRESET'].includes(closure) && args.some((arg) => arg.includes('# consumer'))) {
          queueMicrotask(() => {
            assert.equal(child.exitCode, null, 'inject pipe closure before consumer exit');
            assert.equal(child.signalCode, null);
            injected = true;
            child.stdin.emit('error', Object.assign(new Error('injected pipe closure'), { code: closure }));
          });
        }
      }
      return child;
    };
    syncBuiltinESMExports();
    Object.defineProperty(process, 'platform', { ...platform, value: 'win32' });
    try {
      await assert.rejects(early.build('probe', { cwd: directory, grants: [{ capability: 'run', names: [] }], signal: controller.signal }), (error) => error.code === 'RECIPE_FAIL');
      assert.equal(controller.signal.aborted, false, 'closed pipe left the producer hanging');
      assert.equal(children.length, 2);
      assert.equal(children[0].stdout.destroyed, true, 'closed input retained the upstream read end');
      assert.equal(children[0].exitCode, 17, 'producer did not observe broken output');
      assert.equal(children[1].exitCode, 0, 'ordinary pipe closure killed the consumer');
      if (closure === 'close-stdin-live' || injected) assert.equal(readFileSync(marker, 'utf8'), 'done');
      if (['EPIPE', 'ECONNRESET'].includes(closure)) assert.equal(injected, true);
      for (const child of children) assert.throws(() => process.kill(child.pid, 0), (error) => error.code === 'ESRCH', 'pipeline child was not reaped');
    } finally {
      controller.abort(); clearTimeout(timeout);
      Object.defineProperty(process, 'platform', platform);
      childProcess.spawn = spawnProcess; syncBuiltinESMExports();
      await early.dispose();
    }
  }
  // Actual kind-23 service cancellation must find every independently
  // detached pipeline stage by raw event identity, not the opaque ABI handle.
  const stage = (name) => `(run! "/bin/sh" "-c" "trap '' TERM; echo $$ > ${join(directory, name)}; while :; do sleep 1; done")`;
  const pipeline = await kame.compile(`service daemon : ; [ready: [argv: ["test" "-f" "stage-two.pid"] interval-ms: 10 timeout-ms: 3000] stop: [grace-ms: 0]]\n\t@(pipe! ${stage('stage-one.pid')} ${stage('stage-two.pid')})\nconsumer : daemon\n\twhile [ ! -f release-pipeline ]; do sleep 0.01; done\n`, { name: 'pipeline.kmk' });
  const peer = await kame.compile(`service daemon : ; [ready: [argv: ["test" "-f" "peer-two.pid"] interval-ms: 10 timeout-ms: 3000] stop: [grace-ms: 0]]\n\t@(pipe! ${stage('peer-one.pid')} ${stage('peer-two.pid')})\nconsumer : daemon\n\twhile [ ! -f release-peer ]; do sleep 0.01; done\nsecond : daemon\n\ttrue\n`, { name: 'peer.kmk' });
  const controller = new AbortController();
  const options = { cwd: directory, signal: controller.signal, grants: [{ capability: 'run', names: [] }, { capability: 'read', names: [directory] }, { capability: 'write', names: [directory] }] };
  const deadline = setTimeout(() => controller.abort(), 5000);
  const processModule = Object.getPrototypeOf(pipeline.module);
  const dispatchProcess = processModule.dispatch;
  const beginProcessWatch = processModule.beginWatch;
  const materializeProcess = processModule.materialize;
  let pipelineModule, pipelineContext;
  processModule.materialize = async function (...args) {
    if (args[3] === 'pipeline.kmk') { pipelineModule = this; pipelineContext = args[2]; }
    return materializeProcess.apply(this, args);
  };
  const watchStates = new Map();
  processModule.beginWatch = async function (...args) {
    const state = await beginProcessWatch.apply(this, args);
    watchStates.set(args[2], { module: this, state });
    return state;
  };
  const identities = new Map();
  const cancelledGroups = [];
  processModule.dispatch = async function (...args) {
    if ([14, 15].includes(args[2])) {
      const payload = JSON.parse(args[3]);
      const stages = args[2] === 14 ? payload : payload.stages;
      if (stages.length === 2) {
        assert.equal(typeof args[8], 'bigint', 'raw request ID must be read before detach');
        assert.notEqual(args[8], 0n);
        identities.set(this, { instance: args[0], raw: args[8] });
      }
    }
    if (args[2] === 23) {
      const raw = BigInt(JSON.parse(args[3]).id);
      cancelledGroups.push({ module: this, instance: args[0], raw, size: this.processGroups.get(`${args[0]}:${raw}`)?.size });
    }
    return dispatchProcess.apply(this, args);
  };
  const peerBuild = peer.build(['consumer', 'second'], options);
  const peerSettled = peerBuild.catch(() => {});
  let pipelineSettled;
  try {
    while (!['peer-one.pid', 'peer-two.pid'].every((name) => existsSync(join(directory, name)))) {
      assert.equal(controller.signal.aborted, false, 'peer pipeline did not start');
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    const pipelineBuild = pipeline.build('consumer', options);
    pipelineSettled = pipelineBuild.catch(() => {});
    while (!['stage-one.pid', 'stage-two.pid'].every((name) => existsSync(join(directory, name)))) {
      assert.equal(controller.signal.aborted, false, 'owned pipeline did not start');
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    const peerWatch = watchStates.get('peer.kmk');
    assert.ok(pipelineModule && peerWatch, 'capture both actual operational modules');
    const peerModule = peerWatch.module;
    assert.notEqual(pipelineModule, peerModule);
    const rawRequest = pipelineContext.events.find((event) => event.type === 'process-started' && event.target === 'daemon').request;
    assert.equal(identities.get(peerModule).instance, peerWatch.state.instance);
    assert.equal(identities.get(pipelineModule).raw.toString(), String(rawRequest), 'ABI raw ID differs from exported process event');
    assert.equal(identities.get(peerModule).raw.toString(), String(rawRequest));
    assert.equal(peerModule.processGroups.get(`${peerWatch.state.instance}:${rawRequest}`)?.size, 2, 'colliding peer identity must retain both pipeline stages');
    // Drive the host cancellation boundary explicitly while the real ABI
    // process request is detached and live. No ABI completion target is created
    // for this injected service request; its completion is intentionally invalid.
    const cancellationStatus = await pipelineModule.dispatch(identities.get(pipelineModule).instance, 0n, 23, JSON.stringify({ id: String(rawRequest), graceMS: 0 }), null, pipelineContext);
    assert.notEqual(cancellationStatus, 0, 'injected cancellation has no ABI completion target');
    assert.ok(cancelledGroups.some((group) => group.module === pipelineModule && group.instance === identities.get(pipelineModule).instance && group.raw.toString() === String(rawRequest) && group.size === 2), `kind-23 must find both owned stages by the raw ABI ID: ${JSON.stringify(cancelledGroups.map((group) => ({ owned: group.module === pipelineModule, instance: String(group.instance), raw: String(group.raw), size: group.size })))}`);
    for (const name of ['stage-one.pid', 'stage-two.pid']) {
      const pid = Number(readFileSync(join(directory, name), 'utf8'));
      assert.throws(() => process.kill(pid, 0), (error) => error.code === 'ESRCH', `${name} was not reaped`);
    }
    for (const name of ['peer-one.pid', 'peer-two.pid']) process.kill(Number(readFileSync(join(directory, name), 'utf8')), 0);
    writeFileSync(join(directory, 'release-pipeline'), 'release');
    await pipelineSettled;
    assert.equal(pipelineModule.processGroups.size, 0);
    // A retained input change, unlike build disposal, naturally queues a
    // cancellation while the detached service pipeline is still live.
    writeFileSync(join(directory, 'automatic-input'), 'before');
    const automatic = await kame.compile(`service daemon : ./automatic-input ; [ready: [argv: ["test" "-f" "automatic-two.pid"] interval-ms: 10 timeout-ms: 3000] stop: [grace-ms: 0]]\n\t@(pipe! ${stage('automatic-one.pid')} ${stage('automatic-two.pid')})\nconsumer : daemon\n\ttrue\n`, { name: 'automatic.kmk' });
    let automaticWatch;
    try {
      automaticWatch = await automatic.watch('consumer', options);
      const automaticEvents = [];
      const nextAutomatic = async () => {
        const update = await automaticWatch.next();
        automaticEvents.push(...(update.value?.events ?? []));
      };
      while (automaticWatch.snapshot().busy || !['automatic-one.pid', 'automatic-two.pid'].every((name) => existsSync(join(directory, name)))) {
        assert.equal(controller.signal.aborted, false, 'automatic pipeline did not start');
        await nextAutomatic();
      }
      const actualModule = automaticWatch.module;
      const identity = identities.get(actualModule);
      assert.ok(identity, 'capture actual watch module and raw pipeline identity');
      assert.notEqual(actualModule, peerModule);
      assert.equal(identity.instance, automaticWatch.watch.instance);
      assert.equal(String(automaticEvents.find((event) => event.type === 'process-started' && event.target === 'daemon').request), String(identity.raw), 'automatic cancellation identity must match the actual process event');
      assert.equal(actualModule.processGroups.get(`${identity.instance}:${identity.raw}`)?.size, 2);
      const oldPids = ['automatic-one.pid', 'automatic-two.pid'].map((name) => Number(readFileSync(join(directory, name), 'utf8')));
      const input = automaticWatch.snapshot().resources.find((resource) => resource.name.endsWith('/automatic-input'));
      assert.ok(input, 'watch must retain the changed service input');
      writeFileSync(join(directory, 'automatic-input'), 'after');
      automaticWatch.invalidate([input]);
      while (!cancelledGroups.some((group) => group.module === actualModule && group.instance === identity.instance && group.raw === identity.raw && group.size === 2)) {
        assert.equal(controller.signal.aborted, false, 'input invalidation did not dispatch automatic kind-23');
        await nextAutomatic();
      }
      while (oldPids.some((pid) => { try { process.kill(pid, 0); return true; } catch (error) { assert.equal(error.code, 'ESRCH'); return false; } })) {
        assert.equal(controller.signal.aborted, false, 'automatic cancellation did not reap both stages');
        await nextAutomatic();
      }
      for (const name of ['peer-one.pid', 'peer-two.pid']) process.kill(Number(readFileSync(join(directory, name), 'utf8')), 0);
      while (automaticWatch.snapshot().busy || identities.get(actualModule).raw === identity.raw || ['automatic-one.pid', 'automatic-two.pid'].some((name, index) => Number(readFileSync(join(directory, name), 'utf8')) === oldPids[index])) {
        assert.equal(controller.signal.aborted, false, 'changed input did not rebuild the service');
        await nextAutomatic();
      }
      assert.equal(actualModule.processGroups.get(`${identity.instance}:${identities.get(actualModule).raw}`)?.size, 2, 'rebuilt pipeline must publish both stages');
      const rebuiltPids = ['automatic-one.pid', 'automatic-two.pid'].map((name) => Number(readFileSync(join(directory, name), 'utf8')));
      await automaticWatch.close();
      for (const pid of rebuiltPids) assert.throws(() => process.kill(pid, 0), (error) => error.code === 'ESRCH', 'rebuilt stage was not reaped');
      assert.equal(actualModule.processGroups.size, 0);
      for (const name of ['peer-one.pid', 'peer-two.pid']) process.kill(Number(readFileSync(join(directory, name), 'utf8')), 0);
    } finally { await automaticWatch?.close(); await automatic.dispose(); }
    writeFileSync(join(directory, 'release-peer'), 'release');
    const peerBuilt = await peerBuild;
    assert.deepEqual(peerBuilt.results.map((result) => result.target), ['consumer', 'second']);
    for (const name of ['peer-one.pid', 'peer-two.pid']) {
      assert.throws(() => process.kill(Number(readFileSync(join(directory, name), 'utf8')), 0), (error) => error.code === 'ESRCH', `${name} was not reaped`);
    }
    assert.equal(peerModule.processGroups.size, 0);
  } finally { clearTimeout(deadline); controller.abort(); await Promise.all([peerSettled, pipelineSettled]); processModule.dispatch = dispatchProcess; processModule.beginWatch = beginProcessWatch; processModule.materialize = materializeProcess; await peer.dispose(); await pipeline.dispose(); }
} finally { rmSync(directory, { recursive: true, force: true }); }

// Reserve the program while watch creation is pending, not just once published.
const modulePrototype = Object.getPrototypeOf(answer.module);
const beginWatch = modulePrototype.beginWatch;
let releaseCreation;
const creationGate = new Promise((resolve) => { releaseCreation = resolve; });
modulePrototype.beginWatch = async function (...args) { await creationGate; return beginWatch.apply(this, args); };
const pendingWatch = answer.watch('answer');
await assert.rejects(answer.watch('answer'), (error) => error.code === 'BUSY');
await assert.rejects(answer.build('answer'), (error) => error.code === 'BUSY');
releaseCreation();
await (await pendingWatch).close();
modulePrototype.beginWatch = beginWatch;
await answer.dispose();

for (const disposeOwner of [false, true]) {
  const owner = await Kame.create();
  const pendingProgram = await owner.compile('answer = 42\n');
  const module = Object.getPrototypeOf(pendingProgram.module);
  const begin = module.beginWatch;
  let markCreated, release;
  const created = new Promise((resolve) => { markCreated = resolve; });
  const gate = new Promise((resolve) => { release = resolve; });
  let closed = 0;
  const close = module.disposeWatch;
  module.disposeWatch = async function (watch) { closed++; return close.call(this, watch); };
  module.beginWatch = async function (...args) {
    const watch = await begin.apply(this, args);
    markCreated();
    await gate;
    return watch;
  };
  const pending = pendingProgram.watch('answer');
  const rejected = assert.rejects(pending, (error) => error.code === 'EXEC_CANCELLED');
  await created;
  const disposing = disposeOwner ? owner.dispose() : pendingProgram.dispose();
  let secondFinished = false;
  const second = (disposeOwner ? owner.dispose() : pendingProgram.dispose()).then(() => { secondFinished = true; });
  await new Promise((resolve) => setTimeout(resolve, 5));
  assert.equal(secondFinished, false, 'second disposal skipped the shared completion barrier');
  release();
  await Promise.all([disposing, second, rejected]);
  module.beginWatch = begin;
  module.disposeWatch = close;
  assert.equal(closed, 1, 'pending watch leaked during disposal');
  assert.equal(owner.watches.size, 0);
  assert.equal(owner.operations.size, 0);
  assert.equal(pendingProgram.module, null);
  await owner.dispose();
}

for (let i = 0; i < 100; i++) {
  const cycle = await kame.compile(`value = ${i}\n`, { name: `cycle-${i}.km` });
  assert.equal(JSON.parse(await cycle.evaluate('value')), i);
  await cycle.dispose();
}

await program.dispose();
await kame.dispose();
await kame.dispose();
await assert.rejects(kame.compile('value = 1\n'), (error) => error.code === 'DISPOSED');
await assert.rejects(kame.parse('script', 'value = 1\n'), (error) => error.code === 'DISPOSED');
await assert.rejects(kame.format('script', 'value = 1\n'), (error) => error.code === 'DISPOSED');
NODE
then
	test-ok "JS embedding copies results, observes grants, completes async requests, and reuses 100 lifecycle cycles"
else
	test-fail "JS embedding API conformance failed"
fi

test-end
