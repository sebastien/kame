#!/usr/bin/env bash
# Spec: docs/spec/048-kash-practical-scripting.md — borrowed stdin record readers
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T048-09 Kash stdin record loops"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
CLI_BIN="$CLI_BIN" CLI_ROOT="$CLI_ROOT" STDIN_WORK="$TMPDIR" node <<'JS'
const { spawnSync, spawn } = require('node:child_process');
const { writeFileSync, openSync, closeSync, readSync, writeSync, readFileSync } = require('node:fs');
const { join } = require('node:path');
const assert = require('node:assert/strict');
const work = process.env.STDIN_WORK;
let checks = 0, next = 0;
const runners = [[process.env.CLI_BIN], [process.execPath, join(process.env.CLI_ROOT, 'dist/kame.js')]];
for (const runner of runners) {
  for (const mode of ['file', 'pipe']) {
    const run = (source, input, output, status = 0, code, extra = []) => {
      const name = join(work, `input-${next++}`); writeFileSync(name, input);
      const args = [...runner.slice(1), 'do', 'run', '-C', work, '--allow-run', ...extra, '-l', 'kash', '-c', source];
      let result;
      if (mode === 'file') {
        const fd = openSync(name, 'r');
        try { result = spawnSync(runner[0], args, { stdio: [fd, 'pipe', 'pipe'], encoding: 'utf8', timeout: 12000 }); }
        finally { closeSync(fd); }
      } else {
        result = spawnSync('sh', ['-c', 'source=$1; shift; cat "$source" | "$@"', 'fixture', name, runner[0], ...args], { encoding: 'utf8', timeout: 12000 });
      }
      assert.equal(result.error, undefined, `${mode}: ${source}`);
      assert.equal(result.status, status, `${mode}: ${source}: ${result.stderr}`);
      assert.equal(result.stdout, output, `${mode}: ${source}`);
      if (code) assert.match(result.stderr, new RegExp(code));
      checks++;
    };
    run('for line from :stdin :lines\n\tprintf "[%s]" $line', 'a\r\n\nlast', '[a][][last]');
    run('for line from :stdin :nul\n\tprintf "[%s]" $line', Buffer.from('a\0\0b\n\r\0last'), '[a][][b\n\r][last]');
    run('for line from :stdin :lines\n\tprintf forbidden\nprintf X', '', 'X');
    run('for line from :stdin :lines\n\tprintf "[%s]" $line', '\n', '[]');
    run('for line from :stdin :lines\n\tprintf "[%s]" $line', 'a\n', '[a]');
    run('for line from :stdin :lines\n\tprintf "[%s]" $line', 'a\r', '[a\r]');
    run('for line from :stdin :lines\n\tcat\n\tlet local = line\n\tprintf "[%s]" $local\n\tcontinue', 'a\nb\n', '[a][b]');
    run('for line from :stdin :lines\n\tprintf %s $line\n\tbreak\ncat', 'one\ntwo\n', 'onetwo\n');
    run('for line from :stdin :lines\n\tbreak\nfor line from :stdin :lines\n\tprintf "[%s]" $line', 'one\ntwo\n', '[two]');
    run('for line from :stdin :lines\n\tprintf %s $line\n\tbreak', Buffer.from([97, 10, 255]), 'a');
    run('for line from :stdin :lines\n\twith\n\t\tdefer printf D\n\t\tprintf %s $line\n\t\tcontinue\nprintf X', 'a\nb\n', 'aDbDX');
    run('for line from :stdin :lines\n\tdefer printf %s $line\nprintf X', 'a\nb\n', 'Xba');
    run('for line from :stdin :lines\n\tfor inner from :stdin :lines\n\t\tprintf forbidden', 'a\nb\n', '', 1, 'KASH_DOUBLE_CONSUME');
    run('def helper\n\tfor inner from :stdin :lines\n\t\tprintf forbidden\nfor line from :stdin :lines\n\t(helper)', 'a\nb\n', '', 1, 'KASH_DOUBLE_CONSUME');
    run('def consume\n\tfor line from :stdin :lines\n\t\tprintf forbidden\n\treturn 0\nprintf %s $(printf %s (let [unused (consume)] "C"))\nfor line from :stdin :lines\n\tprintf %s $line', 'a\nb\n', 'Cab');
    run('def consume\n\tfor line from :stdin :lines\n\t\tprintf forbidden\n\treturn 0\ndefer (consume)\nfor line from :stdin :lines\n\tprintf %s $line\n\tbreak', 'a\nb\n', 'a');
    run('def consume\n\tfor line from :stdin :lines\n\t\tprintf forbidden\n\treturn 0\nlet job = (run! :async :true (let [unused (consume)] "cat"))\n(await job)\nfor line from :stdin :lines\n\tprintf %s $line', 'a\nb\n', 'ab');
    run('for line from :stdin :lines\n\tlet event = (parse-json line)\n\tif (eq? event.kind "release")\n\t\tprintf %s $event.name', '{"kind":"skip"}\n{"kind":"release","name":"sdk"}\n', 'sdk');
    run('for line from :stdin :lines\n\tprintf %s (count line)', 'x'.repeat(65536), '65536');
    run('for line from :stdin :lines\n\tprintf %s (count line)', 'x'.repeat(65535) + '\n', '65535');
    run('for line from :stdin :lines\n\tprintf forbidden', 'x'.repeat(65537), '', 1, 'KASH_RECORD_LIMIT');
    run('for line from :stdin :lines\n\tprintf forbidden', 'x'.repeat(65536) + '\n', '', 1, 'KASH_RECORD_LIMIT');
    run('for line from :stdin :lines\n\tprintf forbidden', Buffer.from([255, 10]), '', 1, 'KASH_FRAME_ENCODING');
    run('for line from :stdin :lines\n\tprintf %s $line', Buffer.from('a\0b\n'), '', 1, 'EXPR_INVALID');
    run('for line from :stdin :lines\n\tprintf forbidden', 'unused\n', '', 0, undefined, ['-n']);
  }
  // A borrowed regular file retains its shared position after an early break.
  const name = join(work, `borrowed-${next++}`); writeFileSync(name, 'one\ntwo\n');
  const fd = openSync(name, 'r');
  try {
    const result = spawnSync(runner[0], [...runner.slice(1), 'do', 'run', '--allow-run', '-l', 'kash', '-c', 'for line from :stdin :lines\n\tbreak'], { stdio: [fd, 'pipe', 'pipe'], encoding: 'utf8', timeout: 6000 });
    assert.equal(result.status, 0, result.stderr);
    const rest = Buffer.alloc(16), size = readSync(fd, rest, 0, rest.length, null);
    assert.equal(rest.subarray(0, size).toString(), 'two\n'); checks++;
  } finally { closeSync(fd); }
  const sourceInput = spawnSync(runner[0], [...runner.slice(1), 'do', 'run', '-l', 'kash', '-'], { input: 'for line from :stdin :lines\n\tprintf forbidden\nprintf X', encoding: 'utf8', timeout: 6000 });
  assert.equal(sourceInput.status, 0, sourceInput.stderr); assert.equal(sourceInput.stdout, 'X'); checks++;
}

function fifo() {
  const name = join(work, `fifo-${next++}`);
  const result = spawnSync('mkfifo', [name], { encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  return openSync(name, require('node:fs').constants.O_RDWR | require('node:fs').constants.O_NONBLOCK);
}
async function waits() {
  for (const runner of runners) {
    // An actual pipe tests exact handoff and unchanged status flags, without a
    // parent-side byte relay or an inferred external producer.
    const fd = fifo();
    try {
      writeSync(fd, 'one\ntwo\n');
      const flags = () => readFileSync(`/proc/self/fdinfo/${fd}`, 'utf8').match(/^flags:\s*(\S+)/m)[1];
      const before = flags();
      // Node's spawnSync clears O_NONBLOCK on inherited stdio before exec.
      // Restore it in the launcher so this assertion measures the reader.
      const result = spawnSync('python3', ['-c', 'import os,sys; os.set_blocking(0,False); os.execvp(sys.argv[1],sys.argv[1:])', ...runner, 'do', 'run', '--allow-run', '-l', 'kash', '-c', 'for line from :stdin :lines\n\tbreak'], { stdio: [fd, 'pipe', 'pipe'], encoding: 'utf8', timeout: 6000 });
      assert.equal(result.status, 0, result.stderr);
      const rest = Buffer.alloc(4); assert.equal(readSync(fd, rest, 0, 4, null), 4);
      assert.equal(rest.toString(), 'two\n'); assert.equal(flags(), before); checks++;
    } finally { closeSync(fd); }
    for (const signal of [false, true]) {
      const fd = fifo();
      try {
        const child = spawn(runner[0], [...runner.slice(1), 'do', 'run', '--timeout', signal ? '3000' : '100', '-l', 'kash', '-c', 'printf R\ndefer printf D\nfor line from :stdin :lines\n\tprintf forbidden'], { stdio: [fd, 'pipe', 'pipe'] });
        let output = '', error = '', sent = false;
        child.stdout.on('data', (chunk) => {
          output += chunk;
          if (signal && output.includes('R') && !sent) { sent = true; setTimeout(() => child.kill('SIGINT'), 30); }
        });
        child.stderr.on('data', (chunk) => { error += chunk; });
        const timer = setTimeout(() => child.kill('SIGKILL'), 6000);
        const status = await new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve); });
        clearTimeout(timer);
        assert.equal(status, signal ? 130 : 1, error); assert.equal(output, 'RD');
        if (!signal) assert.match(error, /RECIPE_TIMEOUT/);
        writeSync(fd, 'future\n');
        await new Promise((resolve) => setTimeout(resolve, 20));
        const future = Buffer.alloc(7); assert.equal(readSync(fd, future, 0, 7, null), 7);
        assert.equal(future.toString(), 'future\n'); checks++;
      } finally { closeSync(fd); }
    }
  }
  // Node cannot safely poll arbitrary sockets with its public FD API. It must
  // reject one before consuming, rather than start a blocking thread-pool read.
  const unsupported = spawnSync(process.execPath, [join(process.env.CLI_ROOT, 'dist/kame.js'), 'do', 'run', '-l', 'kash', '-c', 'for line from :stdin :lines\n\tprintf forbidden'], { input: 'socket input\n', encoding: 'utf8', timeout: 6000 });
  assert.equal(unsupported.status, 1); assert.equal(unsupported.stdout, ''); assert.match(unsupported.stderr, /FEATURE_UNSUP/); checks++;
  console.log(`${checks} native/Node stdin-record checks passed`);
}
waits().catch((error) => { console.error(error); process.exitCode = 1; });
JS
test-ok "stdin records preserve ownership, claims, input policy and cancellation"
test-end
