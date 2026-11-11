#!/usr/bin/env bash
# Spec: docs/spec/042-kash-streaming.md — bounded live progress snapshots
# shellcheck disable=SC1091
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T042-08 Kash live graph snapshots"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
test-step "native/WASM report waits before completion without capturing stream data"
if timeout 60 node --input-type=module - "$CLI_BIN" "$TMPDIR" <<'NODE'
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
// Keep even the retained lossy records larger than a pipe's spare capacity.
// Small records may all drain into kernel buffers before a snapshot is sampled.
const input = ('x'.repeat(65535) + '\n').repeat(32);
for (const backend of ['native', 'wasm']) for (const overflow of ['', ' :overflow drop-old :items 2 :buffer 65536']) {
  const directory = join(process.argv[3], backend + (overflow ? '-lossy' : '-block'));
  mkdirSync(directory);
  writeFileSync(join(directory, 'input'), input);
  writeFileSync(join(directory, 'Makefile.kmk'), `SHELL = kash
probe :
\tresult = ((kash """sh -c "while [ ! -e source ]; do sleep 0.01; done; cat input" |(frame :lines) |(broadcast${overflow}) (sh -c "while [ ! -e sink ]; do sleep 0.01; done; cat" > ./left, sh -c "while [ ! -e sink ]; do sleep 0.01; done; cat" > ./right)"""))
\t(out! (json result.graph))
`);
  const command = backend === 'native' ? [process.argv[2]] : [process.execPath, join(process.cwd(), 'dist/kame.js')];
  const child = spawn(command[0], [...command.slice(1), '--json', '-C', directory, '-f', './Makefile.kmk', 'probe'], { stdio: ['ignore', 'pipe', 'pipe'] });
  const deadline = setTimeout(() => {
    writeFileSync(join(directory, 'source'), ''); writeFileSync(join(directory, 'sink'), ''); child.kill('SIGTERM');
  }, 10000);
  let errors = '', sawInput = false, sawOutput = false, snapshots = 0, ended = false, terminal;
  const failures = [], events = [];
  child.stderr.on('data', (data) => { errors += data; });
  const lines = createInterface({ input: child.stdout });
  lines.on('line', (line) => {
    try {
      const event = JSON.parse(line); events.push(event);
      if (event.type === 'process-snapshot') {
        snapshots++;
        assert.ok(!ended, 'snapshot arrived after process-exited');
        assert.ok(event.request > 0 && event.node > 0 && event.attempt > 0);
        assert.equal(event.graph.terminal, false);
        assert.ok(!('data' in event), 'progress is not a stdout capture');
        const states = event.graph.nodes.map((node) => node.state);
        assert.ok(event.graph.nodes.some((node) => node.kind === 'broadcast'));
        assert.ok(event.graph.nodes.some((node) => node.kind === 'process' && node.program.length > 0));
        for (const edge of event.graph.edges) {
          assert.ok(event.graph.nodes[edge.from]); assert.ok(event.graph.nodes[edge.to]);
          assert.ok(['bytes', 'lines'].includes(edge.kind));
        }
        if (states.includes('waiting-input') && !sawInput) {
          sawInput = true; writeFileSync(join(directory, 'source'), '');
        }
        if (sawInput && states.includes('waiting-output') && event.graph.edges.some((edge) => edge.bytes > 0)) {
          sawOutput = true; writeFileSync(join(directory, 'sink'), '');
        }
      }
      if (event.type === 'process-exited') ended = true;
      if (event.type === 'stdout') terminal = JSON.parse(event.data);
    } catch (error) { failures.push(error); child.kill('SIGKILL'); }
  });
  const status = await new Promise((resolve, reject) => { child.on('error', reject); child.on('close', resolve); });
  clearTimeout(deadline);
  assert.equal(status, 0, `${backend}: ${errors}`);
  if (failures.length) throw failures[0];
  assert.ok(sawInput && sawOutput, `${backend} did not publish both live waits`);
  assert.ok(snapshots >= 2 && snapshots < 100, 'snapshots must be sampled, not per-record');
  assert.equal(terminal.terminal, true);
  if (!overflow) assert.equal(readFileSync(join(directory, 'left'), 'utf8'), input);
  for (const edge of terminal.edges) {
    if (edge.queueHighWaterItems !== null) assert.ok(edge.queueHighWaterItems <= 2);
  }
  console.log(`PASS ${backend} ${overflow ? 'lossy' : 'block'} live waits and terminal counters`);
}
NODE
then test-ok "live waits and terminal measurements agree"; else test-fail "live snapshot parity"; fi
test-end
