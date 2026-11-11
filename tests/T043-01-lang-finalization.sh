#!/usr/bin/env bash
# Spec: docs/spec/043-kame-finalization.md
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T043-01 Kame scoped finalization"
cli_require_tools
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
test-step "native/WASM results, effects, deadlines, cancellation and reaping"
if node --input-type=module - "$CLI_ROOT" "$CLI_BIN" "$TMPDIR" <<'JS'
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
const [root, native, work] = process.argv.slice(2);
// Write-capable invocations must not acquire the parent suite's project lock.
process.chdir(work);
const wrapper = process.env.KAME_TEST_WASM_WRAPPER ?? join(root, 'dist/kame.js');
const wasmPath = process.env.KAME_TEST_WASM_PATH ?? join(root, 'dist/kame.wasm');
process.env.KAME_WASM = wasmPath;
const shellQuote = text => `'${text.replaceAll("'", "'\\''")}'`;
for (const backend of ['native', 'wasm']) {
  const binary = backend === 'native' ? native : process.execPath;
  const prefix = backend === 'native' ? [] : [wrapper];
  const run = (expression, options = []) => spawnSync(binary, [...prefix, 'do', 'run', ...options, '--lang', 'expr', '-c', expression], { encoding: 'utf8', timeout: 8000 });
  const check = (expression, output, options = []) => {
    const r = run(expression, options);
    assert.equal(r.status, 0, `${backend}: ${expression}\n${r.stderr}`);
    assert.equal(r.stdout, output, backend);
  };
  check('(finally! ([] 42) ([] (out! "cleanup\\n")))', 'cleanup\n42');
  check('(finally! ([] :false) ([] :nil))', ':false');
  check('(finally! ([] :nil) ([] :false))', ':nil');
  check('(apply finally! [([] 42) ([] (out! "cleanup"))])', 'cleanup42');
  check('(finally! ([] (out! "body") 42) ([] (out! "cleanup")))', 'bodycleanup42');
  check('(finally! ([] (finally! ([] 42) ([] (out! "inner")))) ([] (out! "outer")))', 'innerouter42');
  check('(if :true 42 (finally! ([] (fail! "unused")) ([] (out! "unused"))))', '42');
  check('(finally! ([] (run! :async :true "sh" "-c" "sleep 0.02; printf job") 42) ([] (out! "cleanup")))', 'jobcleanup42', ['--allow-run']);
  check('(finally! ([] 42) ([] (run! :async :true "sh" "-c" "sleep 0.02; printf job")))', 'job42', ['--allow-run']);
  check('(str "unknown" (finally! ([] (out! "body")) ([] (out! "cleanup"))))', '', ['-n']);
  const rejected = run('(finally! [timeout-ms: 0] ([] (out! "body")) ([] (out! "cleanup")))');
  assert.equal(rejected.status, 1, backend);
  assert.equal(rejected.stdout, '', backend);
  assert.match(rejected.stderr, /EXPR_INVALID/, backend);
  const rendering = run('(render "plain" """@(finally! ([] 42) ([] (out! "forbidden")))""")');
  assert.equal(rendering.status, 1, backend);
  assert.equal(rendering.stdout, '', backend);
  assert.match(rendering.stderr, /PHASE_INVALID/, backend);
  const lazyRendering = spawnSync(binary, [...prefix, 'do', 'run', '-c', 'value = (finally! ([] 42) ([] (out! "forbidden")))\n(render "plain" """@{value}""")'], { encoding: 'utf8', timeout: 8000 });
  assert.equal(lazyRendering.status, 1, backend);
  assert.equal(lazyRendering.stdout, '', backend);
  assert.match(lazyRendering.stderr, /PHASE_INVALID/, backend);
  const denied = run('(? (finally! ([] missing) ([] (read "forbidden"))) 42)');
  assert.equal(denied.status, 1, backend);
  assert.equal(denied.stdout, '', backend);
  assert.match(denied.stderr, /REF_MISSING/, backend);
  assert.match(denied.stderr, /CAP_DENIED/, backend);
  const failure = run('(finally! ([] (out! "unaccepted") (fail! "primary")) ([] (out! "cleanup")))');
  assert.equal(failure.status, 1, backend);
  assert.equal(failure.stdout, 'cleanup', backend);
  assert.match(failure.stderr, /primary/, backend);
  const timeout = run('(finally! [timeout-ms: 20] ([] 42) ([] (sh! "sleep 30")))', ['--allow-run']);
  assert.equal(timeout.status, 1, `${backend}: cleanup timeout hung`);
  assert.match(timeout.stderr, /FINALIZE_TIMEOUT/, backend);
  const invocationTimeout = run('(finally! ([] (sh! "sleep 30")) ([] (fail! "cleanup-failure")))', ['--allow-run', '--timeout', '30']);
  assert.equal(invocationTimeout.status, 1, backend);
  assert.match(invocationTimeout.stderr, /RECIPE_TIMEOUT/, backend);
  assert.match(invocationTimeout.stderr, /cleanup-failure/, backend);
  const first = join(work, `${backend}-first`), second = join(work, `${backend}-second`);
  check(`(finally! ([] (def held 42) (write! ${JSON.stringify(first)} "one") (write! ${JSON.stringify(second)} "two") held) ([] (out! "cleanup")))`, 'cleanup42', ['--allow-write']);
  assert.equal(readFileSync(first, 'utf8'), 'one');
  assert.equal(readFileSync(second, 'utf8'), 'two');
  for (const phase of ['body', 'cleanup']) {
    const marker = join(work, `${backend}-${phase}-started`);
    const script = `printf ready > ${shellQuote(marker)}; sleep ${phase === 'body' ? '30' : '0.1'}`;
    const expression = phase === 'body'
      ? `(finally! ([] (sh! ${JSON.stringify(script)})) ([] (out! "cleaned")))`
      : `(finally! ([] 42) ([] (sh! ${JSON.stringify(script)}) (out! "cleaned")))`;
    const child = spawn(binary, [...prefix, 'do', 'run', '--allow-run', '--lang', 'expr', '-c', expression]);
    let output = '', errors = '';
    child.stdout.on('data', data => { output += data; });
    child.stderr.on('data', data => { errors += data; });
    const exited = new Promise(resolve => child.once('exit', (code, signal) => resolve({ code, signal })));
    const killTimer = setTimeout(() => child.kill('SIGKILL'), 8000);
    const deadline = Date.now() + 4000;
    while (!existsSync(marker) && Date.now() < deadline && child.exitCode === null) await new Promise(resolve => setTimeout(resolve, 5));
    if (!existsSync(marker)) { child.kill('SIGKILL'); await exited; clearTimeout(killTimer); assert.fail(`${backend} ${phase} did not start: ${errors}`); }
    child.kill('SIGINT');
    const result = await exited;
    clearTimeout(killTimer);
    assert.deepEqual(result, { code: 130, signal: null }, `${backend} ${phase}: ${errors}`);
    assert.equal(output, 'cleaned', `${backend}: cancellation during ${phase} lost cleanup: ${errors}`);
  }
  console.log(`${backend}: finalization checks passed`);
}
// Leave the selected public pipe unread until exit: resuming it first would
// hide the native blocked-write bug. Require partial delivery to prove that the
// deadline was serviced inside write/poll, not just during expression building.
for (const stream of ['stdout', 'stderr', 'json', 'process', 'signal']) {
  const payload = JSON.stringify('x'.repeat(32768));
  const operation = stream === 'stderr' ? 'err!' : 'out!';
  const cleanup = stream === 'process'
    ? '(finally! ([] (run! :async :true "sh" "-c" "head -c 1048576 /dev/zero") :nil) ([] :nil))'
    : `(let [chunk ${payload}] (${operation} (str ${Array(32).fill('chunk').join(' ')})))`;
  const body = stream === 'process' ? '(fail! "body-primary")' : '42';
  const expression = `(finally! [timeout-ms: 2000] ([] ${body}) ([] ${cleanup}))`;
  const options = stream === 'json' ? ['--json'] : stream === 'process' ? ['--allow-run'] : [];
  const child = spawn(native, ['do', 'run', ...options, '--lang', 'expr', '-c', expression]);
  const blocked = stream === 'stderr' ? child.stderr : child.stdout;
  const readable = stream === 'stderr' ? child.stdout : child.stderr;
  blocked.pause();
  if (stream === 'signal') blocked.once('readable', () => child.kill('SIGINT'));
  let diagnostics = '', delivered = 0;
  readable.on('data', data => { diagnostics += data; });
  const closed = new Promise(resolve => child.once('close', resolve));
  const kill = setTimeout(() => child.kill('SIGKILL'), 6500);
  const result = await new Promise(resolve => child.once('exit', (code, signal) => resolve({code, signal})));
  clearTimeout(kill);
  blocked.on('data', data => { delivered += data.length; });
  blocked.resume();
  await closed;
  assert.deepEqual(result, {code: stream === 'signal' ? 130 : 1, signal: null}, `native ${stream}: deadline or signal outcome was lost`);
  assert.ok(delivered > 0 && delivered < 1048576, `native ${stream}: no partial deadline-bound delivery`);
  if (stream !== 'stderr' && stream !== 'json') assert.match(diagnostics, /FINALIZE_TIMEOUT/, stream);
  else assert.equal(diagnostics, '', `${stream}: leaked a body result or crossed diagnostic streams`);
  if (stream === 'process') assert.match(diagnostics, /body-primary/, 'output timeout masked the body failure');
}
console.log('native: stdout, stderr and process-output backpressure deadlines passed');
const { Kame } = await import(pathToFileURL(wrapper).href);
for (const mode of ['abort', 'program-dispose', 'owner-dispose', 'abort-cleanup-failure', 'dispose-cleanup-failure']) {
  const kame = await Kame.create({ wasmPath });
  const program = await kame.compile('', { language: 'km' });
  const cancellation = new AbortController();
  let entered;
  const started = new Promise(resolve => { entered = resolve; });
  let bodyStopped = false, cleanups = 0;
  const cleanupFails = mode.endsWith('cleanup-failure');
  const cleanup = cleanupFails ? '(write! "cleanup" "done") (fail! "cleanup-secondary")' : '(write! "cleanup" "done")';
  const running = program.evaluate(`(finally! ([] (read "body")) ([] ${cleanup}))`, {
    signal: cancellation.signal,
    grants: [{ capability: 'read' }, { capability: 'write' }],
    async hostRequest(request) {
      if (request.kind === 1) {
        entered();
        await new Promise(resolve => request.signal.addEventListener('abort', () => setTimeout(resolve, 5), { once: true }));
        bodyStopped = true;
        return { type: 'failure', code: 'EXEC_CANCELLED', message: 'stopped' };
      }
      assert.equal(request.kind, 2);
      assert.equal(bodyStopped, true, 'cleanup preceded callback quiescence');
      assert.equal(request.signal.aborted, false, 'cleanup inherited body cancellation');
      cleanups++;
      return { type: 'nil' };
    },
  }).then(() => assert.fail('cancelled API invocation succeeded'), error => {
    assert.equal(error.code, 'EXEC_CANCELLED');
    assert.equal(error.diagnostics?.[0]?.code, 'EXEC_CANCELLED', mode);
    if (cleanupFails) assert.match(JSON.stringify(error.diagnostics), /USER_ERROR.*cleanup-secondary/, mode);
    // This is an owned JS copy, not a view into memory freed during disposal.
    return error;
  });
  await started;
  if (mode.startsWith('abort')) cancellation.abort();
  else if (mode === 'program-dispose') await program.dispose();
  else await kame.dispose();
  const failure = await running;
  assert.equal(cleanups, 1, mode);
  await kame.dispose();
  if (cleanupFails) assert.match(JSON.stringify(failure.diagnostics), /cleanup-secondary/, mode);
}
const kame = await Kame.create({ wasmPath });
const program = await kame.compile('', { language: 'km' });
await assert.rejects(program.evaluate('(finally! ([] (fail! "body-primary")) ([] (fail! "cleanup-secondary")))'), error => {
  assert.equal(error.code, 'USER_ERROR');
  assert.equal(error.message, 'body-primary');
  assert.equal(error.diagnostics?.[0]?.message, 'body-primary');
  assert.match(JSON.stringify(error.diagnostics), /cleanup-secondary/);
  return true;
});
await kame.dispose();
console.log('WASM API: cancellation, quiescence, disposal and secondary diagnostic ownership passed');
JS
then test-ok "native and WASM finalization conformance"; else test-fail "finalization parity checks failed"; fi
test-end
