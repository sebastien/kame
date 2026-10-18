#!/usr/bin/env bash
# Spec: docs/spec/042-kash-streaming.md — explicit bounded per-record workers
# shellcheck disable=SC1091,SC2016
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T042-11 Kash explicit per-record worker parity"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/each"
mkdir -p "$work"
run_case() {
	local text="$1" expected="${2:-0}" code="${3:-}" status=0
	timeout 20 "${command[@]}" do run --lang kash --allow-run --allow-read --allow-write -c "$text" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" = "$expected" ]; then test-ok "$backend status"; else test-fail "$backend expected $expected got $status: $(cat "$work/$backend.err")"; fi
	if [ -n "$code" ]; then if grep -q "$code" "$work/$backend.err"; then test-ok "$backend $code"; else test-fail "$backend missing $code: $(cat "$work/$backend.err")"; fi; fi
}
expect() { local name="$1"; shift; if "$@"; then test-ok "$backend $name"; else test-fail "$backend $name"; fi; }
pid_token='\$\$'
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	output="$work/$backend-result"
	marker="$work/$backend-marker"
	test-step "$backend: exactly one prepared command per record, stdin EOF and ordered byte output"
	run_case 'printf "a\nb\nlast" |(frame :lines) |(each) sh -c "printf start:; cat; printf :end"'
	printf 'start:a\n:endstart:b\n:endstart:last:end' >"$work/expected"
	expect "each record gets its own EOF" cmp -s "$work/$backend.out" "$work/expected"
	run_case "printf \"\" |(frame :lines) |(each) sh -c \"touch $marker\""
	expect "empty input launches no worker" test ! -e "$marker"
	run_case "printf \"a\\nb\\n\" |(frame :lines) |(each) sh -c \"echo $pid_token; cat > /dev/null\""
	expect "two records use distinct executable processes" test "$(sort -u "$work/$backend.out" | wc -l)" -eq 2
	printf '$(touch BAD); *\n' >"$work/expected"
	run_case "cat < $work/expected |(frame :lines) |(each) cat"
	expect "record data is never reparsed as argv or shell text" cmp -s "$work/$backend.out" "$work/expected"
	run_case "printf \"a\\nb\" |(frame :lines) |(each) cat > $output"
	expect "redirected worker stdout is opened once, not truncated per record" test "$(cat "$output")" = $'a\nb'
	run_case "printf c |(frame :lines) |(each) cat >> $output"
	expect "worker append preserves earlier output" test "$(cat "$output")" = $'a\nbc'
	run_case "printf \"a\\nb\" |(frame :lines) |(each) :cwd $work :EACH_VALUE fixed sh -c \"cat; printenv EACH_VALUE\""
	expect "cwd and environment setup are reused by every worker" test "$(cat "$work/$backend.out")" = $'a\nfixed\nbfixed'
	test-step "$backend: framing, named graphs, capture, and async composition"
	run_case 'printf "a\nb\n" |(frame :lines) |(each) cat |(frame :lines) |(each) tr a-z A-Z'
	expect "explicit reframing composes workers" test "$(cat "$work/$backend.out")" = $'A\nB'
	run_case '(printf "a\nb\n" |(frame :lines) |(each) cat |(frame :lines), printf "x\ny\n" |(frame :lines)) |(merge :order round-robin) cat'
	expect "worker output rejoins only after reframing" test "$(cat "$work/$backend.out")" = $'a\nx\nb\ny'
	run_case "printf \"a\\nb\\n\" |(frame :lines) |(each) cat > $output &"
	expect "async completion joins every worker" test "$(cat "$output")" = $'a\nb'
	run_case 'cat <(printf "a\nb\n" |(frame :lines) |(each) cat)'
	expect "anonymous producer owns each worker" test "$(cat "$work/$backend.out")" = $'a\nb'
	run_case "printf \"a\\nb\\nc\\nd\\n\" |(frame :lines) |(balance) (|(each) cat > $work/$backend-left, |(each) cat > $work/$backend-right)"
	expect "explicit balance branches bound parallel workers" test "$(cat "$work/$backend-left")" = $'a\nc'
	expect "balance assigns complete records to the other worker" test "$(cat "$work/$backend-right")" = $'b\nd'
	test-step "$backend: bounded bidirectional I/O exceeds pipe capacity without deadlock"
	printf '%65535s\n' '' | tr ' ' x >"$work/large"
	run_case "cat < $work/large |(frame :lines) |(each) sh -c \"head -c 262144 /dev/zero; cat\" | wc -c"
	expect "stdout drains before the child reads a full 64KiB record" test "$(tr -d '[:space:]' <"$work/$backend.out")" = 327680
	test-step "$backend: failures never become reserved operator diagnostics"
	for status in 7 118 120 121 124 125 126 127; do
		run_case "printf a |(frame :lines) |(each) sh -c \"cat > /dev/null; exit $status\"" 1 RECIPE_FAIL
	done
	run_case "printf \"a\\nb\\n\" |(frame :lines) |(each) sh -c \"cat; echo worker >> $marker; exit 7\"" 1 RECIPE_FAIL
	expect "fail-fast launches no later record" test "$(wc -l <"$marker")" -eq 1
	run_case '(printf "a\nb\n" |(frame :lines) |(each) sh -c "cat; exit 7", printf z) |(merge :failure collect) cat' 1 RECIPE_FAIL
	expect "collection processes all remaining records" test "$(tr -d z <"$work/$backend.out")" = $'a\nb'
	run_case 'printf a |(frame :lines) |(each) /no-such-worker' 1
	run_case "printf a |(frame :lines) |(each) sh -c \"cat > /dev/null; kill -TERM $pid_token\"" 1 RECIPE_FAIL
	test-step "$backend: malformed/unsupported topology fails before effects"
	printf preserve >"$output"
	for text in "printf a |(each) cat > $output" "printf a |(frame :lines) |(each) cat <& > $output" "printf a |(frame :lines) |(each) cat < $work/large > $output"; do
		run_case "$text" 1
		expect "invalid each graph preserves destinations" test "$(cat "$output")" = preserve
	done
	run_case 'printf a |(frame :lines) |(each :jobs 2) cat' 1 PARSE_ERR
	test-step "$backend: timeout kills an active worker and prevents the next record"
	pid="$work/$backend-pid"
	status=0
	timeout 10 "${command[@]}" do run --lang kash --allow-run --timeout=300 -c "printf \"a\\nb\\n\" |(frame :lines) |(each) sh -c \"echo $pid_token > $pid; exec sleep 30\"" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" -eq 1 ] && grep -q RECIPE_TIMEOUT "$work/$backend.err" && { [ ! -e "$pid" ] || ! kill -0 "$(cat "$pid")" 2>/dev/null; }; then test-ok "$backend timeout reaps worker"; else test-fail "$backend timeout lost worker ownership: $(cat "$work/$backend.err")"; fi
	run_case 'printf a |(frame :lines) |(each) :timeout 0.05 sleep 30' 1 RECIPE_TIMEOUT
	test-step "$backend: accepted Results retain bounded worker identity and payload counters"
	directory="$work/$backend-record"
	mkdir -p "$directory"
	cat >"$directory/Makefile.kmk" <<'KMK'
SHELL = kash
probe :
	result = ((kash """(printf "a\nb\n" |(frame :lines) |(each) sh -c "cat; exit 126", printf z) |(merge :failure collect) cat > ./output ?"""))
	(out! (json result))
KMK
	if timeout 20 "${command[@]}" -C "$directory" -f ./Makefile.kmk probe >"$directory/result.json" 2>"$directory/error" && node --input-type=module - "$directory/result.json" <<'NODE'
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
const result = JSON.parse(readFileSync(process.argv[2], 'utf8'));
assert.equal(result.status, 126);
const worker = result.graph.nodes.find((node) => node.kind === 'each');
assert.equal(worker.status, 126); assert.equal(worker.state, 'failed');
assert.equal(result.graph.edges.find((edge) => edge.from === worker.id).bytes, 4);
assert.equal(result.graph.edges.find((edge) => edge.from === worker.id).items, null);
NODE
	then test-ok "$backend Result worker identity and byte accounting"; else test-fail "$backend worker Result: $(cat "$directory/error")"; fi
done

test-step "Node validates worker descriptors and executable grants before effects"
if timeout 30 node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync, unlinkSync, rmdirSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { syncBuiltinESMExports } from 'node:module';
import { Kame } from './dist/kame.js';
const directory = mkdtempSync(join(tmpdir(), 'kame-each-')), output = join(directory, 'output');
writeFileSync(output, 'preserve');
const kame = await Kame.create();
const program = await kame.compile(`SHELL = kash\nprobe :\n\tprintf a |(frame :lines) |(each) cat > ${JSON.stringify(output)}\n`);
const prototype = Object.getPrototypeOf(program.module), dispatch = prototype.dispatch, spawn = childProcess.spawn;
let mutate, calls = 0, launches = 0;
prototype.dispatch = function (...args) {
  if (args[2] === 38) { calls++; const graph = JSON.parse(args[3]); mutate(graph); args[3] = JSON.stringify(graph); }
  return dispatch.apply(this, args);
};
childProcess.spawn = (...args) => { launches++; return spawn(...args); }; syncBuiltinESMExports();
try {
  const mutations = [
    (graph) => { delete graph.kinds; }, (graph) => { delete graph.failurePolicy; },
    (graph) => { graph.failurePolicy = 2; }, (graph) => { graph.failurePolicy = null; },
    (graph) => { delete graph.orders; }, (graph) => { delete graph.buffers; },
    (graph) => { delete graph.overflows; }, (graph) => { delete graph.bufferItems; },
    (graph) => { const i = graph.kinds.indexOf('each'); graph.kinds[i] = ''; },
    (graph) => { const i = graph.kinds.indexOf('each'); graph.stages[i] = []; },
    (graph) => { const i = graph.kinds.indexOf('each'); graph.framed[i] = true; },
    (graph) => { const i = graph.kinds.indexOf('each'); graph.setup[i].input = output; },
    (graph) => { const i = graph.kinds.indexOf('each'); graph.buffers[i] = 1; },
    (graph) => { const i = graph.kinds.indexOf('each'); graph.edges.find((edge) => edge[1] === i).push(1); },
  ];
  for (mutate of mutations) {
    await assert.rejects(program.build('probe', { grants: [{ capability: 'run', names: [] }, { capability: 'write', names: [directory] }] }));
    assert.equal(readFileSync(output, 'utf8'), 'preserve');
  }
  assert.equal(calls, mutations.length, 'workers require wire kind 38');
  assert.equal(launches, 0, 'invalid graphs must not launch producers either');
  prototype.dispatch = dispatch;
  await assert.rejects(program.build('probe', { grants: [{ capability: 'run', names: ['printf'] }, { capability: 'write', names: [directory] }] }), (error) => error.code === 'CAP_DENIED');
  assert.equal(readFileSync(output, 'utf8'), 'preserve');
  assert.equal(launches, 0, 'each must authorize its executable before launching any stage');
} finally {
  prototype.dispatch = dispatch; childProcess.spawn = spawn; syncBuiltinESMExports();
  await kame.dispose(); unlinkSync(output); rmdirSync(directory);
}
NODE
then test-ok "worker trust boundary and grants"; else test-fail "worker trust boundary or grants"; fi

test-step "Node keeps one active worker, cancels repeated invocations, and releases descriptors"
if timeout 30 node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { existsSync, readdirSync } from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { Kame } from './dist/kame.js';
const kame = await Kame.create(), spawn = childProcess.spawn;
const program = await kame.compile('SHELL = kash\nmany :\n\tseq 1 128 |(frame :lines) |(each) cat | wc -l\nblocked :\n\tprintf "a\\nb\\n" |(frame :lines) |(each) sleep 30\n');
const options = { grants: [{ capability: 'run', names: [] }] };
const descriptors = () => existsSync('/proc/self/fd') ? readdirSync('/proc/self/fd').length : 0;
try {
  await program.build('many', options);
  const baseline = descriptors();
  let active = 0, maximum = 0, count = 0, controller, children = [];
  childProcess.spawn = (executable, args, configuration) => {
    const child = spawn(executable, args, configuration);
    if (configuration.stdio[0] === 'pipe' && configuration.stdio[1] === 'pipe') {
      active++; count++; maximum = Math.max(maximum, active); children.push(child);
      child.once('close', () => active--);
      if (controller) child.once('spawn', () => controller.abort());
    }
    return child;
  };
  syncBuiltinESMExports();
  const result = await program.build('many', options);
  assert.equal(count, 128); assert.equal(maximum, 1); assert.equal(active, 0);
  assert.ok(result.events.some((event) => event.type === 'stdout' && event.data.includes('128')));
  for (let attempt = 0; attempt < 4; attempt++) {
    controller = new AbortController(); children = []; const before = count;
    await assert.rejects(program.build('blocked', { ...options, signal: controller.signal }), (error) => error.code === 'EXEC_CANCELLED');
    assert.equal(count - before, 1, 'cancellation must never advance to the next record');
    assert.ok(children.every((child) => child.exitCode !== null || child.signalCode !== null));
    assert.equal(active, 0);
  }
  childProcess.spawn = spawn; syncBuiltinESMExports();
  await program.build('many', options);
  assert.ok(descriptors() <= baseline, 'workers leaked descriptors across invocations');
} finally { childProcess.spawn = spawn; syncBuiltinESMExports(); await kame.dispose(); }
NODE
then test-ok "bounded worker lifecycle and cancellation"; else test-fail "worker lifecycle or cancellation"; fi
test-end
