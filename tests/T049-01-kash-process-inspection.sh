#!/usr/bin/env bash
# Spec: docs/spec/049-process-inspection.md — typed capture and optional lookup
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}" )")/lib-bootstrap.sh"
test-start "T049-01 typed process inspection"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
CLI_BIN="$CLI_BIN" CLI_ROOT="$CLI_ROOT" INSPECT_WORK="$TMPDIR" node <<'JS'
const {spawnSync, spawn} = require('node:child_process');
const {writeFileSync, readFileSync, existsSync, chmodSync, unlinkSync} = require('node:fs');
const {join} = require('node:path');
const assert = require('node:assert/strict');
const work = process.env.INSPECT_WORK, q = JSON.stringify;
let checks = 0;
const runners = [[process.env.CLI_BIN], [process.execPath, join(process.env.CLI_ROOT, 'dist/kame.js')]];
async function main() {
for (const runner of runners) {
  const args = (source, extra = []) => [...runner.slice(1), 'do', 'run', '-C', work, '--allow-run', '--allow-read', '--allow-write', ...extra, '-l', 'kash', '-c', source];
  const run = (source, out = '', status = 0, code, extra = [], err = '') => {
    const r = spawnSync(runner[0], args(source, extra), {encoding:'utf8', timeout:10000});
    assert.equal(r.error, undefined, source); assert.equal(r.status, status, `${source}\n${r.stderr}`); assert.equal(r.stdout, out, source);
    if (code) assert.match(r.stderr, new RegExp(code)); else if (!extra.includes('-n')) assert.equal(r.stderr, err, source);
    checks++; return r;
  };
  run('let r = (capture! [stdout: :true stderr: :true] "sh" "-c" "printf out; printf err >&2")\nprintf "%s:%s" (text r.stdout) (text r.stderr)', 'out:err');
  run('let r = (capture! [stdout: :true] "sh" "-c" "printf out; printf live >&2")\nprintf %s (text r.stdout)', 'out', 0, undefined, [], 'live');
  run('let r = (capture! [stderr: :true] "sh" "-c" "printf live; printf err >&2")\nprintf %s (text r.stderr)', 'liveerr');
  run('let r = (capture! [stdout: :true stderr: :true] "true")\nprintf "%s:%s:%s:%s" (bytes? r.stdout) (count r.stdout) (bytes? r.stderr) (count r.stderr)', 'true:0:true:0');
  run('let r = (capture! [stdout: :true] "true")\nprintf "%s:%s:%s" $r.stdoutCaptured $r.stderrCaptured (nil? r.stderr)', 'true:false:true');
  const binary = Buffer.from([0, 255, 13, 10, 10]);
  const name = join(work, 'capture-binary'); writeFileSync(name, binary);
  run(`let r = (capture! [stdout: :true] "cat" ${q(name)})\nprintf "%s:%s" (bytes? r.stdout) (eq? r.stdout (read ${q(name)}))`, 'true:true');
  run(`let r = (capture! [stdout: :true] "cat" ${q(name)})\nprintf %s (text r.stdout)`, '', 1, 'EXPR_INVALID');
  run('let r = (capture! [stdout: :true] "printf" "a\\n\\n")\nprintf %s (text r.stdout)', 'a\n\n');
  run('let r = (capture! [stdout: :true] :accept-exit [1] "sh" "-c" "printf out; exit 1")\nprintf "%s:%s:%s" $r.status $r.effectiveStatus (text r.stdout)', '1:0:out');
  run('let r = (capture! [stdout: :true] "sh" "-c" "printf secret; exit 7")', '', 7, 'RECIPE_FAIL');
  run('let r = (capture! [stdout: :true] :accept-exit [143] "sh" "-c" "kill -TERM $$")', '', 143, 'RECIPE_FAIL');
  run('let r = (capture! [stdout: :true] "missing-capture-executable")', '', 1, 'TOOL_MISSING');
  run('let r = (capture! [stdout: :true stdoutLimit: 2] "printf" "ab")\nprintf %s (text r.stdout)', 'ab');
  run('let r = (capture! [stdout: :true stdoutLimit: 2] "printf" "abc")', '', 1, 'CAPTURE_LIMIT');
  run('let r = (capture! [stderr: :true stderrLimit: 2] "sh" "-c" "printf abc >&2")', '', 1, 'CAPTURE_LIMIT');
  run('let r = (capture! [stdout: :true stderr: :true stdoutLimit: 4 stderrLimit: 4] "sh" "-c" "printf abcd; printf efgh >&2")\nprintf "%s:%s" (count r.stdout) (count r.stderr)', '4:4');
  run('let r = (capture! [stdout: :true stderr: :true stdoutLimit: 4 stderrLimit: 2] "sh" "-c" "printf abcd; printf efg >&2")', '', 1, 'CAPTURE_LIMIT');
  run('let r = (capture! [stdout: :true] "printf" "abc")', '', 1, 'CAPTURE_LIMIT', ['--capture-limit', '2']);
  run('defer printf D\nlet r = (capture! [stdout: :true stdoutLimit: 2] "sh" "-c" "while true; do printf overflow; done")', 'D', 1, 'CAPTURE_LIMIT');
  run('defer printf D\nlet r = (capture! [stderr: :true] :timeout 0.03 "sleep" "3")', 'D', 1, 'RECIPE_TIMEOUT');
  const both = `process.stdout.write(Buffer.alloc(70000,97));process.stderr.write(Buffer.alloc(70000,98))`;
  run(`let r = (capture! [stdout: :true stderr: :true stdoutLimit: 70000 stderrLimit: 70000] ${q(process.execPath)} "-e" ${q(both)})\nprintf "%s:%s" (count r.stdout) (count r.stderr)`, '70000:70000');
  for (const policy of [':nil', '[]', '[stdout: "true"]', '[stdout: :false]', '[stdout: :true stdoutLimit: 0]', '[stdout: :true stderrLimit: -1]', '[stdout: :true stdoutLimit: 1.5]', '[stdout: :true secret: :true]']) {
    const marker = join(work, 'invalid-capture-marker');
    run(`let r = (capture! ${policy} "touch" ${q(marker)})`, '', 1, 'EXPR_INVALID'); assert.equal(existsSync(marker), false);
  }
  run('(capture! [stdout: :true] :async :true "true")', '', 1, 'EXPR_INVALID');
  run('(pipe! (capture! [stdout: :true] "true") (run! "cat"))', '', 1, 'EXPR_INVALID');
  run('let r = (capture! (read! ./missing-policy) "missing-unused-tool")', '', 0, undefined, ['-n']);
  const input = join(work, 'capture-stdin'); writeFileSync(input, 'remaining\n'); const fd = require('node:fs').openSync(input, 'r');
  try {
    const r = spawnSync(runner[0], args('let r = (capture! [stdout: :true] "cat")\nprintf %s (count r.stdout)\ncat'), {stdio:[fd,'pipe','pipe'],encoding:'utf8',timeout:5000});
    assert.equal(r.status, 0, r.stderr); assert.equal(r.stdout, '0remaining\n'); checks++;
  } finally { require('node:fs').closeSync(fd); }
  const marker = join(work, `capture-once-${checks}`);
  run(`defn observe\n\tsh -c ${q(`printf x >> ${marker}`)}\n\treturn 0\nlet r = (capture! [stdout: :true] "printf" (let [unused (observe)] "once"))\nprintf %s (text r.stdout)`, 'once');
  assert.equal(readFileSync(marker,'utf8'), 'x');
  run('printf "%s:%s" (nil? (tool "missing-optional-executable" | ?)) (string? (tool "sh" | ?))', 'true:true');
  run('let executable = (tool "printf" | ?)\n$executable found', 'found');
  run('printf "%s:%s" (nil? (? (tool "missing-optional-executable"))) (? (tool "missing-optional-executable") "fallback")', 'true:fallback');
  run('printf %s (tool "missing-optional-executable" | ? _ "fallback")', 'fallback');
  run('printf %s (nil? (missing-reference | ?))', 'true');
  run('printf "%s:%s" (nil? (? :nil (fail! "unreached"))) (eq? (? :false (fail! "unreached")) :false)', 'true:true');
  run('printf %s (str "ab" "cd" | uppercase)', 'ABCD');
  run('(? (fail! "unexpected") :nil)', '', 1, 'USER_ERROR');
  run('(tool "missing-strict-executable")', '', 1, 'TOOL_MISSING');
  const denied = spawnSync(runner[0], [...runner.slice(1), 'do', 'run', '-l', 'expr', '-c', '(capture! [stdout: :true] "true")'], {encoding:'utf8',timeout:5000});
  assert.equal(denied.status,1); assert.match(denied.stderr,/CAP_DENIED/); checks++;
  run('printf %s (nil? (tool "alias" | ?))', 'false', 0, undefined, ['--tool', 'alias=/bin/sh']);
  run('printf %s (nil? (tool "alias" | ?))', 'true', 0, undefined, ['--tool', 'alias=/missing/optional']);
  for (const value of ['""', '1', ':nil']) {
    run(`(tool ${value})`, '', 1, 'EXPR_INVALID');
    run(`(tool ${value} | ?)`, '', 1, 'EXPR_INVALID');
  }
  writeFileSync(join(work, 'optional-nul'), 'a\0b');
  run('(tool (text (read ./optional-nul)) | ?)', '', 1, 'EXPR_INVALID');
  // Readiness is a published byte, not a timing assumption. Capture stderr while
  // stdout signals that the child is running; cancellation must drain cleanup.
  const child = spawn(runner[0], args('defer printf D\nlet r = (capture! [stderr: :true] "sh" "-c" "printf READY; exec sleep 30")'), {stdio:['ignore','pipe','pipe']});
  let out = '', err = '', signalled = false;
  child.stdout.on('data', chunk => { out += chunk; if (!signalled && out.includes('READY')) { signalled = true; child.kill('SIGINT'); } });
  child.stderr.on('data', chunk => { err += chunk; });
  const timer = setTimeout(() => child.kill('SIGKILL'), 8000);
  const status = await new Promise((resolve,reject) => { child.once('error',reject); child.once('close',resolve); }); clearTimeout(timer);
  assert.equal(status,130,err); assert.equal(out,'READYD'); checks++;
}
const {Kame} = await import(join(process.env.CLI_ROOT, 'dist/kame.js'));
const kame = await Kame.create();
let watch;
try {
  const executable = join(work, 'optional-watch-tool');
  const program = await kame.compile(`available = (tool ${q(executable)} | ?)\n`, {language:'kmk'});
  watch = await program.watch('available', {signal:AbortSignal.timeout(10000)});
  const settle = async () => {
    for (let step = 0; step < 1000; step++) {
      const state = watch.module.drainWatch(watch.watch);
      if (state === 1) watch.module.serviceWatchRequest(watch.watch);
      if (watch.watch.pending.size) await Promise.all([...watch.watch.pending]);
      if (watch.watch.hostError) throw watch.watch.hostError;
      const snapshot = watch.snapshot();
      if (!snapshot.busy) { assert.equal(snapshot.roots[0].diagnosticJSON, undefined); return snapshot.roots[0]; }
    }
    assert.fail('optional tool watch did not settle');
  };
  const absent = await settle(); assert.equal(absent.value, ':nil');
  watch.invalidate([{kind:'tool',name:executable}]);
  const unchanged = await settle(); assert.equal(unchanged.value, ':nil'); assert.equal(unchanged.revision, absent.revision); checks++;
  writeFileSync(executable, '#!/bin/sh\nexit 0\n'); chmodSync(executable, 0o755);
  watch.invalidate([{kind:'tool',name:executable}]);
  assert.equal((await settle()).value, q(executable)); checks++;
  unlinkSync(executable); watch.invalidate([{kind:'tool',name:executable}]);
  assert.equal((await settle()).value, ':nil'); checks++;
  const prototype = Object.getPrototypeOf(program.module), dispatch = prototype.dispatch;
  try {
    // A computed name bypasses literal-tool preloading and exercises host lookup.
    const probe = await kame.compile('probe = (tool (str "missing-" "operational-probe") | ?)\n');
    for (const code of ['CAP_DENIED', 'HOST_FAIL']) {
      prototype.dispatch = function(instance,request,kind,...rest) {
        if (kind === 18) return this.completeFailure(instance,request,code,'lookup operation failed');
        return dispatch.call(this,instance,request,kind,...rest);
      };
      await assert.rejects(probe.build('probe'), error => error.code === code); checks++;
    }
    const marker = join(work,'unsupported-capture-marker');
    prototype.dispatch = async function(instance,request,kind,payload,...rest) {
      if (![13,14,15,32,33,34,35,36,37,38].includes(kind) || !JSON.parse(payload).capture) return dispatch.call(this,instance,request,kind,payload,...rest);
      const exports = this.exports;
      this.exports = {...exports, kame_wasm_complete_capture:undefined};
      try { return await dispatch.call(this,instance,request,kind,payload,...rest); } finally { this.exports = exports; }
    };
    const captured = await kame.compile(`captured = (capture! [stdout: :true] "touch" ${q(marker)})\n`);
    await assert.rejects(captured.build('captured', {grants:[{capability:'run',names:[]}]}), error => error.code === 'FEATURE_UNSUP');
    assert.equal(existsSync(marker),false); checks++;
  } finally { prototype.dispatch = dispatch; }
} finally { await watch?.close(); await kame.dispose(); }
console.log(`${checks} native/Node process-inspection checks passed`);
}
main().catch(error => { console.error(error); process.exitCode = 1; });
JS
test-ok "typed byte capture and optional tools preserve bounds, ownership and failure policy"
test-end
