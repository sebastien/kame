#!/usr/bin/env bash
# Spec: docs/spec/042-kash-streaming.md — terminal process graph telemetry
# shellcheck disable=SC1091,SC2016
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T042-07 Kash terminal graph telemetry parity"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/metrics"
mkdir -p "$work"

run_graph() {
	local name="$1" body="$2" directory="$work/$backend-$1" status=0
	mkdir -p "$directory"
	cat >"$directory/Makefile.kmk" <<KMK
SHELL = kash
probe :
	result = ((kash """$body"""))
	(out! (json result.graph))
KMK
	timeout 30 "${command[@]}" -C "$directory" -f ./Makefile.kmk probe >"$directory/graph.json" 2>"$directory/error" || status=$?
	if [ "$status" -eq 0 ]; then test-ok "$backend $name completed"; else test-fail "$backend $name status $status: $(cat "$directory/error")"; fi
	if node --input-type=module - "$directory/graph.json" "$name" <<'NODE'
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
const graph = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const scenario = process.argv[3];
assert.equal(graph.terminal, true);
assert.ok(graph.nodes.length > 0);
graph.nodes.forEach((node, id) => {
  assert.equal(node.id, id);
  if (scenario !== 'failed') {
    assert.equal(node.state, 'completed');
    assert.equal(node.status, 0);
    assert.equal(node.signal, 0);
  }
  if (node.kind === 'process') assert.ok(node.program.length > 0);
});
for (const edge of graph.edges) {
  assert.ok(graph.nodes[edge.from]); assert.ok(graph.nodes[edge.to]);
  assert.equal(edge.saturated, false);
  if (edge.measurement === 'unavailable') {
    for (const field of ['bytes', 'items', 'droppedItems', 'queueHighWaterBytes', 'queueHighWaterItems']) assert.equal(edge[field], null);
  } else assert.ok(Number.isSafeInteger(edge.bytes) && edge.bytes >= 0);
}
if (scenario === 'lossless') {
  const outputs = graph.edges.filter((edge) => graph.nodes[edge.from].kind === 'broadcast');
  assert.equal(outputs.length, 2);
  for (const edge of outputs) {
    assert.equal(edge.measurement, 'completed-write');
    assert.equal(edge.bytes, 9, 'private headers must not count as payload');
    assert.equal(edge.items, 3);
    assert.equal(edge.droppedItems, 0);
    assert.equal(edge.queueHighWaterBytes, null, 'lossless writes have no pending queue');
  }
  const input = graph.edges.find((edge) => graph.nodes[edge.to].kind === 'frame');
  assert.equal(input.measurement, 'read'); assert.equal(input.bytes, 9); assert.equal(input.items, null);
} else if (scenario === 'direct') {
  assert.ok(graph.edges.some((edge) => edge.measurement === 'unavailable'), 'direct external pipes must not invent zero counters');
  const output = graph.edges.find((edge) => graph.nodes[edge.from].kind === 'merge');
  assert.equal(output.bytes, 6); assert.equal(output.items, null);
} else if (scenario === 'lossy') {
  const outputs = graph.edges.filter((edge) => graph.nodes[edge.from].kind === 'broadcast');
  assert.equal(outputs.length, 3);
  for (const edge of outputs) {
    assert.equal(edge.items + edge.droppedItems, 512, 'every destination accounts for every source record');
    assert.equal(edge.bytes, edge.items * 1024);
    assert.ok(edge.queueHighWaterBytes > 0 && edge.queueHighWaterBytes <= 4096);
    assert.ok(edge.queueHighWaterItems > 0 && edge.queueHighWaterItems <= 2);
  }
  assert.ok(outputs.some((edge) => edge.droppedItems > 0), 'pressured destinations must expose drops');
} else assert.ok(graph.nodes.some((node) => node.state === 'failed' && node.status === 7), 'accepted exit still retains the failing node');
NODE
	then test-ok "$backend $name telemetry"; else test-fail "$backend $name telemetry"; fi
}

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	test-step "$backend: terminal node outcomes and payload-only counters"
	run_graph lossless 'printf "a\nbb\nlast" |(frame :lines) |(broadcast) (cat > ./left, cat > ./right)'
	run_graph direct '(printf abc | cat, printf def) |(merge) cat > ./output'
	run_graph failed '(sh -c "exit 7", printf x) |(merge) cat > ./output ?'
	test-step "$backend: bounded queue high-water marks and per-destination conservation"
	mkdir -p "$work/$backend-lossy"
	awk 'BEGIN {for(i=0;i<1016;i++)pad=pad"x";for(i=1;i<=512;i++)printf "%06d:%s\n",i,pad}' >"$work/$backend-lossy/input"
	run_graph lossy 'cat < ./input |(frame :lines) |(broadcast :buffer 4096 :items 2 :overflow drop-old) (sh -c "while [ ! -e done ]; do sleep 0.01; done; cat" > ./left, sh -c "while [ ! -e done ]; do sleep 0.01; done; cat" > ./right, sh -c "cat > /dev/null; touch done")'
done

test-step "optional and malformed forwarded telemetry cannot fabricate measurements"
if timeout 30 node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import { mkdtempSync, rmdirSync, unlinkSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
const { Kame } = await import('./dist/kame.js');
const directory = mkdtempSync(join(tmpdir(), 'kame-metrics-'));
const kame = await Kame.create();
const source = 'SHELL = kash\nprobe :\n\tresult = ((kash """printf "x\\n" |(frame :lines) |(broadcast) (cat > ./left, cat > ./right)"""))\n\t(out! (json result.graph))\n';
const program = await kame.compile(source, { name: 'metrics.kmk' });
const prototype = Object.getPrototypeOf(program.module), complete = prototype.completeJSON;
let mutate, calls = 0;
prototype.completeJSON = function (instance, request, value) {
  if (Array.isArray(value?.edgeMetrics)) { calls++; mutate(value); }
  return complete.call(this, instance, request, value);
};
const options = { cwd: directory, grants: [{ capability: 'run', names: [] }, { capability: 'write', names: [directory] }] };
try {
  mutate = (value) => { delete value.edgeMetrics; };
  const built = await program.build('probe', options);
  const graph = JSON.parse(built.events.filter((event) => event.type === 'stdout').map((event) => event.data).join(''));
  assert.ok(graph.edges.every((edge) => edge.measurement === 'unavailable' && edge.bytes === null && edge.items === null));
  for (mutate of [
    (value) => { value.edgeMetrics[0] = [1]; },
    (value) => { value.edgeMetrics[0][1] = -2; },
    (value) => { value.edgeMetrics[0][0] = 4; },
    (value) => { value.edgeMetrics[0][1] = Number.MAX_SAFE_INTEGER + 1; },
  ]) {
    await assert.rejects(program.build('probe', options), (error) => error.code === 'HOST_FAIL' && /telemetry/.test(error.message));
  }
  assert.equal(calls, 5);
} finally {
  prototype.completeJSON = complete;
  await kame.dispose();
  unlinkSync(join(directory, 'left')); unlinkSync(join(directory, 'right')); rmdirSync(directory);
}
NODE
then test-ok "missing counters remain unknown and malformed rows are rejected"; else test-fail "forwarded telemetry validation"; fi
test-end
