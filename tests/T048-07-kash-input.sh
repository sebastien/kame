#!/usr/bin/env bash
# Spec: docs/spec/048-kash-practical-scripting.md — invocation input
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-07 Kash invocation input"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
CLI_BIN="$CLI_BIN" CLI_ROOT="$CLI_ROOT" INPUT_WORK="$TMPDIR" node <<'JS'
const { spawnSync, spawn } = require('node:child_process');
const { writeFileSync, openSync, readSync, closeSync } = require('node:fs');
const { join } = require('node:path');
const assert = require('node:assert/strict');
const work = process.env.INPUT_WORK;
writeFileSync(join(work, 'file'), 'file');
writeFileSync(join(work, 'source.kash'), 'cat');
let checks = 0;
for (const runner of [[process.env.CLI_BIN], [process.execPath, join(process.env.CLI_ROOT, 'dist/kame.js')]]) {
  const invoke = (source, input = 'payload\n', extra = []) => {
    const result = spawnSync(runner[0], [...runner.slice(1), 'do', 'run', '-C', work, ...extra, '-l', 'kash', '-c', source], { input, encoding: 'utf8', timeout: 6000, maxBuffer: 8 * 1024 * 1024 });
    assert.equal(result.error, undefined, source);
    assert.equal(result.status, 0, `${source}: ${result.stderr}`);
    checks++;
    return result.stdout;
  };
  assert.equal(invoke('cat'), 'payload\n');
  assert.equal(invoke('cat | cat'), 'payload\n');
  assert.equal(invoke('dd bs=1 count=2 status=none\ncat'), 'payload\n');
  assert.equal(invoke('dd bs=1 count=2 status=none\ncat\ncat'), 'payload\n');
  assert.equal(invoke('cat < ./file\ncat'), 'filepayload\n');
  assert.equal(invoke('cat < ./file', 'unconsumed'), 'file');
  assert.equal(invoke('printf C%s $(cat)\ncat'), 'Cpayload\n');
  assert.equal(invoke('defn helper\n\tcat\n\treturn 0\nprintf %s $(printf %s (let [unused (helper)] "C"))\ncat'), 'Cpayload\n');
  assert.equal(invoke('defn helper\n\tcat\n\treturn 0\nlazy = (helper)\nprintf %s $(printf %s (let [unused lazy] "C"))\ncat'), 'Cpayload\n');
  assert.equal(invoke('defn helper\n\tprintf H\n\tcat\n\treturn 0\nlazy = (helper)\nprintf %s $(printf %s (let [unused lazy] "C"))\n(let [unused lazy] :nil)\ncat'), 'HCpayload\n');
  assert.equal(invoke('cat &\ncat'), 'payload\n');
  assert.equal(invoke('let job = (run! :async :true "cat")\n(await job)\ncat'), 'payload\n');
  assert.equal(invoke('defer cat\nprintf X'), 'X');
  assert.equal(invoke('defn helper\n\tcat\n\treturn 0\ndefer (helper)\nprintf X'), 'X');
  assert.equal(invoke('defn helper\n\tcat\n\treturn 0\n(finally! ([] :nil) ([] (helper)))\nprintf X'), 'X');
  assert.equal(invoke('defn helper\n\tcat\nwith\n\t(helper)'), 'payload\n');
  assert.equal(invoke('(run! "cat")'), 'payload\n');
  assert.equal(invoke('(pipe! (run! "cat") (run! "cat"))'), 'payload\n');
  assert.equal(invoke('cat |(broadcast) (cat, cat)'), 'payload\npayload\n');
  assert.equal(invoke('(cat, cat) |(merge) cat\ncat'), 'payload\n');
  assert.equal(invoke('(cat < ./file, cat) |(merge) cat\ncat'), 'filepayload\n');
  assert.equal(invoke('sh -c "cat \\"\\$1\\"; cat" receiver <(cat)\ncat'), 'payload\n');
  assert.equal(invoke('printf edge | sh -c "cat \\"\\$1\\"; cat" receiver <&\ncat'), 'edgepayload\n');
  const binary = Buffer.from([0, 255, 10]);
  const raw = spawnSync(runner[0], [...runner.slice(1), 'do', 'run', '-l', 'kash', '-c', 'cat'], { input: binary, timeout: 6000 });
  assert.equal(raw.status, 0, raw.stderr.toString()); assert.deepEqual(raw.stdout, binary); checks++;
  const large = 'x'.repeat(2 * 1024 * 1024);
  assert.equal(invoke('cat', large), large);
  assert.equal(invoke('cat', 'payload', ['--allow-run']), 'payload');
  const fromFile = spawnSync(runner[0], [...runner.slice(1), 'do', 'run', '-C', work, './source.kash'], { input: 'data', encoding: 'utf8', timeout: 6000 });
  assert.equal(fromFile.status, 0, fromFile.stderr); assert.equal(fromFile.stdout, 'data'); checks++;
  const sourceInput = spawnSync(runner[0], [...runner.slice(1), 'do', 'run', '-l', 'kash', '-'], { input: 'cat\n', encoding: 'utf8', timeout: 6000 });
  assert.equal(sourceInput.status, 0, sourceInput.stderr); assert.equal(sourceInput.stdout, ''); checks++;
  const shared = spawnSync(runner[0], [...runner.slice(1), 'do', 'run', '-l', 'kash', '-c', 'cat', '-c', 'cat'], { input: 'one', encoding: 'utf8', timeout: 6000 });
  assert.equal(shared.status, 0, shared.stderr); assert.equal(shared.stdout, 'one'); checks++;
  const sourceSession = spawnSync(runner[0], [...runner.slice(1), 'do', 'run', '-l', 'kash', '-c', 'cat', '-'], { input: 'printf source\n', encoding: 'utf8', timeout: 6000 });
  assert.equal(sourceSession.status, 0, sourceSession.stderr); assert.equal(sourceSession.stdout, 'source'); checks++;
  // A child that reads exactly two bytes must leave the borrowed stream's rest
  // available to the caller, not consume it through a parent-side relay.
  const name = join(work, 'borrowed'); writeFileSync(name, 'abcdef');
  const fd = openSync(name, 'r');
  try {
    const result = spawnSync(runner[0], [...runner.slice(1), 'do', 'run', '-l', 'kash', '-c', 'defer cat\ncat &\ndd bs=1 count=2 status=none'], { stdio: [fd, 'pipe', 'pipe'], encoding: 'utf8', timeout: 6000 });
    assert.equal(result.status, 0, result.stderr); assert.equal(result.stdout, 'ab');
    const rest = Buffer.alloc(8); const size = readSync(fd, rest, 0, rest.length, null);
    assert.equal(rest.subarray(0, size).toString(), 'cdef'); checks++;
  } finally { closeSync(fd); }
}
async function deadlines() {
  for (const runner of [[process.env.CLI_BIN], [process.execPath, join(process.env.CLI_ROOT, 'dist/kame.js')]]) {
    const child = spawn(runner[0], [...runner.slice(1), 'do', 'run', '--timeout', '100', '-l', 'kash', '-c', 'defer printf D\ncat'], { stdio: ['pipe', 'pipe', 'pipe'] });
    let output = '', error = '';
    child.stdout.on('data', (chunk) => { output += chunk; });
    child.stderr.on('data', (chunk) => { error += chunk; });
    child.stdin.on('error', () => {});
    const timer = setTimeout(() => child.kill('SIGKILL'), 6000);
    const status = await new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve); });
    clearTimeout(timer); child.stdin.destroy();
    assert.equal(status, 1, error); assert.equal(output, 'D'); assert.match(error, /RECIPE_TIMEOUT/); checks++;
  }
  console.log(`${checks} native/Node invocation input checks passed`);
}
deadlines().catch((error) => { console.error(error); process.exitCode = 1; });
JS
test-ok "native/Node input ownership, exclusions and remaining-byte handoffs"
test-end
