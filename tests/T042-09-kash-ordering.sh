#!/usr/bin/env bash
# Spec: docs/spec/042-kash-streaming.md — strict framed round-robin merge
# shellcheck disable=SC1091,SC2016
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T042-09 Kash explicit merge ordering parity"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/ordering"
mkdir -p "$work"
run_case() {
	local text="$1" expected="${2:-0}" code="${3:-}" language="${4:-kash}" status=0
	timeout 20 "${command[@]}" do run --lang "$language" --allow-run --allow-read --allow-write --capture-limit=128 -c "$text" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" = "$expected" ]; then test-ok "$backend status"; else test-fail "$backend expected $expected got $status: $(cat "$work/$backend.err")"; fi
	if [ -n "$code" ]; then if grep -q "$code" "$work/$backend.err"; then test-ok "$backend $code"; else test-fail "$backend missing $code"; fi; fi
}
expect() {
	local name="$1"; shift
	if "$@"; then test-ok "$backend $name"; else test-fail "$backend $name"; fi
}
seq 1 10000 | awk '{print "a"$0}' >"$work/a"
seq 1 10000 | awk '{print "b"$0}' >"$work/b"
seq 1 10000 | awk '{print "a"$0; print "b"$0}' >"$work/interleaved"
awk 'BEGIN {for(i=0;i<65535;i++)printf "x";printf "\n"}' >"$work/large-a"
awk 'BEGIN {for(i=0;i<65535;i++)printf "y";printf "\n"}' >"$work/large-b"
cat "$work/large-a" "$work/large-b" >"$work/large-expected"
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	output="$work/$backend-result"
	test-step "$backend: complete-record turns, slow inputs, EOF and unequal producers"
	run_case "(sh -c \"sleep 0.1; printf 'a1\\na2\\n'\" |(frame :lines), printf \"b1\\nb2\\n\" |(frame :lines)) |(merge :order round-robin) cat > $output"
	printf 'a1\nb1\na2\nb2\n' >"$work/expected"
	expect "slow first input keeps its turn" cmp -s "$output" "$work/expected"
	run_case "(printf \"a1\\na2\" |(frame :lines), printf \"b1\\nb2\\nb3\\nb4\" |(frame :lines), printf \"\" |(frame :lines)) |(merge :order round-robin :buffer 4) cat > $output"
	printf 'a1\nb1\na2b2\nb3\nb4' >"$work/expected"
	expect "closed inputs are skipped and partial EOF payload stays exact" cmp -s "$output" "$work/expected"
	run_case "(printf \"\" |(frame :lines), printf \"\" |(frame :lines)) |(merge :order round-robin) cat > $output"
	expect "all empty inputs close output" test ! -s "$output"
	run_case "(printf \"a1\\na2\\n\" |(frame :lines), printf \"b1\\nb2\\nb3\\n\" |(frame :lines), printf \"c1\\n\" |(frame :lines)) |(merge :order round-robin) cat > $output"
	printf 'a1\nb1\nc1\na2\nb2\nb3\n' >"$work/expected"
	expect "three-way turns wrap and skip only closed producers" cmp -s "$output" "$work/expected"
	run_case "(cat < $work/a |(frame :lines), cat < $work/b |(frame :lines)) |(merge :order round-robin :buffer 16) cat > $output"
	expect "streaming far beyond capture limits remains deterministic" cmp -s "$output" "$work/interleaved"
	run_case "(cat < $work/large-a |(frame :lines), cat < $work/large-b |(frame :lines)) |(merge :order round-robin :buffer 65536) cat > $output"
	expect "fragmented frame envelopes never advance a turn early" cmp -s "$output" "$work/large-expected"
	test-step "$backend: composed graphs, anonymous operands, async ownership and ready default"
	run_case '$(cat <(printf "1\n2\n3\n4\n" |(frame :lines) |(balance) (cat |(frame :lines), cat |(frame :lines)) |(merge :order round-robin)))' 0 '' expr
	expect "one-to-one workers can rejoin in assignment order" grep -Fq '"1\n2\n3\n4\n"' "$work/$backend.out"
	run_case "(printf \"a\\n\" |(frame :lines), printf \"b\\n\" |(frame :lines)) |(merge :order round-robin) cat > $output &"
	printf 'a\nb\n' >"$work/expected"
	expect "async graph joins every ordered input" cmp -s "$output" "$work/expected"
	run_case "stream first
stream second
(printf \"a\\n\" |(frame :lines) >&first, printf \"b\\n\" |(frame :lines) >&second, (&first, &second) |(merge :order round-robin) cat > $output)"
	expect "named channels retain authored port turns" cmp -s "$output" "$work/expected"
	run_case "(printf \"a\\n\" |(frame :lines), printf \"b\\n\" |(frame :lines)) |(merge :order round-robin) |(broadcast :overflow drop-new :items 2) (cat > $output, cat > $work/$backend-copy)"
	expect "ordered and lossy descriptor policies compose" cmp -s "$output" "$work/expected"
	expect "combined policy retains both small-record copies" cmp -s "$output" "$work/$backend-copy"
	gate="$work/$backend-gate"
	run_case "(sh -c \"while [ ! -e $gate ]; do sleep 0.01; done; printf 'a\\n'\" |(frame :lines), printf \"b\\n\" |(frame :lines)) |(merge :order ready) sh -c \"dd bs=1 count=2 2>/dev/null; touch $gate; cat\" > $output"
	printf 'b\na\n' >"$work/expected"
	expect "ready order does not wait for an idle producer" cmp -s "$output" "$work/expected"
	test-step "$backend: invalid policies fail before launch or truncation"
	marker="$work/$backend-marker"
	printf preserve >"$output"
	run_case "(sh -c \"touch $marker\", printf a) |(merge :order round-robin) cat > $output" 1 KASH_FRAME_REQUIRED
	run_case "cat <((sh -c \"touch $marker\", printf a) |(merge :order round-robin)) > $output" 1 KASH_FRAME_REQUIRED
	expect "unframed ordering launches nothing" test ! -e "$marker"
	expect "unframed ordering does not truncate output" test "$(cat "$output")" = preserve
	for policy in ':order random' ':order ready :order round-robin' ':order (read! ./policy)' ':order'; do
		run_case "(sh -c \"touch $marker\", printf a) |(merge $policy) cat > $output" 1 PARSE_ERR
	done
	run_case "printf a |(broadcast :order ready) (cat > $output, cat)" 1 PARSE_ERR
	expect "invalid policy values never run" test ! -e "$marker"
	expect "invalid policy values preserve output" test "$(cat "$output")" = preserve
	run_case "(printf \"abcd\\n\" |(frame :lines), printf x |(frame :lines)) |(merge :order round-robin :buffer 4) cat > $output" 1 KASH_BUFFER_LIMIT
	run_case "(printf \"abcd\\n\" |(frame :lines), printf x |(frame :lines)) |(merge :order round-robin :buffer 4) cat > $output ? printf recovered"
	expect "ordered failure still uses graph recovery" grep -qx recovered "$work/$backend.out"
	test-step "$backend: timeout owns an ordered merger waiting on its current input"
	pid="$work/$backend-pid"
	pid_token='\$\$'
	status=0
	timeout 10 "${command[@]}" do run --lang kash --allow-run --allow-write --timeout=200 -c "(sh -c \"echo $pid_token > $pid; exec sleep 30\" |(frame :lines), printf b |(frame :lines)) |(merge :order round-robin) cat" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" -eq 1 ] && grep -q RECIPE_TIMEOUT "$work/$backend.err" && { [ ! -e "$pid" ] || ! kill -0 "$(cat "$pid")" 2>/dev/null; }; then test-ok "$backend ordered wait timeout reaps producer"; else test-fail "$backend ordered wait timeout cleanup"; fi
done

test-step "forwarded ordering descriptors cannot silently downgrade before effects"
if timeout 30 node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync, unlinkSync, rmdirSync } from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
const { Kame } = await import('./dist/kame.js');
const directory = mkdtempSync(join(tmpdir(), 'kame-ordering-')), output = join(directory, 'output');
writeFileSync(output, 'preserve');
const kame = await Kame.create();
const program = await kame.compile(`SHELL = kash\nprobe :\n\t(printf "a\\n" |(frame :lines), printf "b\\n" |(frame :lines)) |(merge :order round-robin) cat > ${JSON.stringify(output)}\n`);
const prototype = Object.getPrototypeOf(program.module), dispatch = prototype.dispatch, spawn = childProcess.spawn;
let mutate, forwarded = 0, launches = 0;
prototype.dispatch = function (...args) {
  if (args[2] === 36) {
    forwarded++;
    const graph = JSON.parse(args[3]); mutate(graph, graph.kinds.indexOf('merge')); args[3] = JSON.stringify(graph);
  }
  return dispatch.apply(this, args);
};
childProcess.spawn = (...args) => { launches++; return spawn(...args); }; syncBuiltinESMExports();
try {
  const mutations = [
    (graph, node) => { graph.orders[node] = 2; },
    (graph, node) => { graph.orders[node] = '1'; },
    (graph) => { graph.orders[0] = 1; },
    (graph) => { graph.orders.pop(); },
    (graph) => { delete graph.orders; },
    (graph) => { graph.orders = null; },
    (graph) => { delete graph.buffers; },
    (graph) => { delete graph.overflows; },
    (graph) => { delete graph.bufferItems; },
    (graph) => { delete graph.kinds; },
  ];
  for (mutate of mutations) {
    await assert.rejects(program.build('probe', { grants: [{ capability: 'run', names: [] }, { capability: 'write', names: [directory] }] }), (error) => error.code === 'KASH_MUX_ARITY');
    assert.equal(readFileSync(output, 'utf8'), 'preserve');
  }
  assert.equal(forwarded, mutations.length, 'ordered policies must use request kind 36');
  assert.equal(launches, 0);
} finally {
  prototype.dispatch = dispatch; childProcess.spawn = spawn; syncBuiltinESMExports();
  await kame.dispose(); unlinkSync(output); rmdirSync(directory);
}
NODE
then test-ok "Node validates every forwarded ordering field before effects"; else test-fail "forwarded ordering validation"; fi
test-end
