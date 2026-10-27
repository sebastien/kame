#!/usr/bin/env bash
# Spec: docs/spec/042-kash-streaming.md — Slice E2 explicit framed lossy fan-out
# shellcheck disable=SC1091,SC2016
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T042-06 Kash bounded lossy broadcast parity"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/lossy"
mkdir -p "$work"
awk 'BEGIN { for (i=0;i<1016;i++) pad=pad"x"; for (i=1;i<=2048;i++) printf "%06d:%s\n",i,pad }' >"$work/source"
awk 'BEGIN { for (i=0;i<65528;i++) pad=pad"x"; for (i=1;i<=16;i++) printf "%06d:%s\n",i,pad }' >"$work/large"

run_case() {
	local text="$1" expected="$2" code="${3:-}" status=0
	timeout 20 "${command[@]}" 'do' run --lang kash --allow-run --allow-read --allow-write --capture-limit=128 -c "$text" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" = "$expected" ]; then test-ok "$backend status"; else test-fail "$backend expected $expected got $status: $(cat "$work/$backend.err")"; fi
	if [ -n "$code" ]; then
		if grep -q "$code" "$work/$backend.err"; then test-ok "$backend: $code"; else test-fail "$backend missing $code: $(cat "$work/$backend.err")"; fi
	fi
}
expect() {
	local message="$1"
	shift
	if "$@"; then test-ok "$backend: $message"; else test-fail "$backend: $message"; fi
}
records_valid() {
	awk -v width="$2" -v total="$3" 'length($0)!=width-1 {exit 1} {n=substr($0,1,6)+0; if(n<=last||n>total) exit 1; last=n} END {if(NR==0||NR>total) exit 1}' "$1"
}

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	left="$work/$backend-left"
	right="$work/$backend-right"
	test-step "$backend: pending-record policies consume while both readers are blocked"
	for policy in drop-new drop-old latest; do
		done="$work/$backend-$policy-done"
		items=2
		if [ "$policy" = latest ]; then items=1; elif [ "$policy" = drop-old ]; then items=1024; fi
		# A third reader releases its peers only after receiving EOF. Thus all
		# input is processed before either blocked destination can consume.
		# A lossless broadcaster deadlocks here; lossy queues must stay bounded
		# and continue reading without a permanent dummy pipe reader/writer.
		run_case "cat < $work/source |(frame :lines) |(broadcast :buffer 4096 :items $items :overflow $policy) (sh -c \"while [ ! -e '$done' ]; do sleep 0.01; done; cat\" > $left, sh -c \"while [ ! -e '$done' ]; do sleep 0.01; done; cat\" > $right, sh -c \"cat > /dev/null; touch '$done'\")" 0
		expect "$policy drains its producer before blocked readers are released" test -e "$done"
		expect "$policy first output contains whole ordered records" records_valid "$left" 1024 2048
		expect "$policy second output contains whole ordered records" records_valid "$right" 1024 2048
		expect "$policy explicitly drops under pressure" test "$(wc -l <"$left")" -lt 2048
		expect "$policy second queue drops independently" test "$(wc -l <"$right")" -lt 2048
		if [ "$policy" != drop-new ]; then
			expect "$policy retains the final pending record" test "$(tail -n 1 "$left" | cut -c 1-6)" = 002048
			expect "$policy final pending record reaches both outputs" test "$(tail -n 1 "$right" | cut -c 1-6)" = 002048
		else
			expect "drop-new preserves older pending records rather than replacing with the final input" test "$(tail -n 1 "$left" | cut -c 1-6)" != 002048
		fi
	done

	test-step "$backend: partially written large records cannot be evicted"
	done="$work/$backend-large-done"
	run_case "cat < $work/large |(frame :lines) |(broadcast :buffer 65536 :overflow latest) (sh -c \"while [ ! -e '$done' ]; do sleep 0.01; done; cat\" > $left, sh -c \"while [ ! -e '$done' ]; do sleep 0.01; done; cat\" > $right, sh -c \"cat > /dev/null; touch '$done'\")" 0
	expect "first output preserves full 64 KiB envelopes during replacement" records_valid "$left" 65536 16
	expect "second output preserves full 64 KiB envelopes during replacement" records_valid "$right" 65536 16
	expect "latest flushes the final large record after EOF" test "$(tail -n 1 "$left" | cut -c 1-6)" = 000016

	test-step "$backend: EOF, framing limits, composed graphs, and failure ownership"
	run_case "printf \"\" |(frame :lines) |(broadcast :overflow latest) (cat > $left, cat > $right)" 0
	expect "empty input closes the first queue" test ! -s "$left"
	expect "empty input closes every queue" test ! -s "$right"
	run_case "printf \"a\\nb\\n\" |(frame :lines) |(broadcast :overflow latest) (cat > $left, cat > $right) &" 0
	expect "async graph joins its retained first queue" test "$(tail -n 1 "$left")" = b
	expect "async graph joins every retained queue" test "$(tail -n 1 "$right")" = b
	run_case 'printf "a\nb\n" |(frame :lines) |(broadcast :overflow latest) (cat |(frame :lines), cat |(frame :lines)) |(merge) |(encode :lines)' 0
	expect "lossy fan-out composes with frame-safe explicit merging" awk '!/^[ab]$/ {exit 1} END {if(NR<2||NR>4) exit 1}' "$work/$backend.out"
	run_case "printf \"abc\\n\" |(frame :lines) |(broadcast :buffer 2 :overflow drop-new) (cat > $left, cat > $right)" 1 KASH_BUFFER_LIMIT
	run_case "yes |(frame :lines) |(broadcast :overflow latest) (head -n 1 > $left, cat > /dev/null)" 141 RECIPE_FAIL
	status=0
	timeout 10 "${command[@]}" 'do' run --lang kash --allow-run --timeout=200 -c 'yes |(frame :lines) |(broadcast :overflow drop-old :buffer 64 :items 2) (sleep 30, sleep 30)' >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" -eq 1 ] && grep -q RECIPE_TIMEOUT "$work/$backend.err"; then test-ok "$backend timeout cancels pressured lossy queues"; else test-fail "$backend pressured queue timeout: $status $(cat "$work/$backend.err")"; fi

	test-step "$backend: invalid lossy policies cannot launch or truncate outputs"
	marker="$work/$backend-invalid"
	printf preserve >"$left"
	run_case "sh -c \"touch '$marker'\" |(broadcast :overflow latest) (cat > $left, cat > $right)" 1 KASH_FRAME_REQUIRED
	run_case "sh -c \"touch '$marker'\" |(frame :lines) |(balance :overflow drop-old) (cat > $left, cat > $right)" 1 PARSE_ERR
	run_case "sh -c \"touch '$marker'\" |(frame :lines) |(broadcast :overflow latest :items 2) (cat > $left, cat > $right)" 1 PARSE_ERR
	run_case "sh -c \"touch '$marker'\" |(frame :lines) |(broadcast :overflow drop-old :items 1025) (cat > $left, cat > $right)" 1 PARSE_ERR
	expect "invalid lossy policy starts no source" test ! -e "$marker"
	expect "invalid lossy policy preserves output bytes" test "$(cat "$left")" = preserve
done

test-step "Node rejects malformed or missing forwarded lossy policies before effects"
if timeout 30 node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { mkdtempSync, readFileSync, rmdirSync, unlinkSync, writeFileSync } from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
const { Kame } = await import('./dist/kame.js');
const directory = mkdtempSync(join(tmpdir(), 'kame-lossy-'));
const left = join(directory, 'left'), right = join(directory, 'right');
writeFileSync(left, 'preserve'); writeFileSync(right, 'preserve');
const kame = await Kame.create();
const source = `SHELL = kash\nprobe :\n\tprintf "x\\n" |(frame :lines) |(broadcast :buffer 2 :overflow drop-new :items 2) (cat > ${JSON.stringify(left)}, cat > ${JSON.stringify(right)})\n`;
const program = await kame.compile(source, { name: 'lossy.kmk' });
const prototype = Object.getPrototypeOf(program.module);
const dispatch = prototype.dispatch, spawn = childProcess.spawn;
let launches = 0, forwarded = 0, mutate;
childProcess.spawn = (...args) => { launches++; return spawn(...args); };
syncBuiltinESMExports();
prototype.dispatch = function (...args) {
  if (args[2] === 35) {
    forwarded++;
    const graph = JSON.parse(args[3]);
    mutate(graph, graph.kinds.indexOf('broadcast'));
    args[3] = JSON.stringify(graph);
  }
  return dispatch.apply(this, args);
};
try {
  const mutations = [
    (graph, node) => { graph.overflows[node] = 4; },
    (graph, node) => { graph.bufferItems[node] = 1025; },
    (graph, node) => { graph.overflows[node] = 3; graph.bufferItems[node] = 2; },
    (graph) => { graph.overflows[0] = 1; graph.bufferItems[0] = 1; },
    (graph) => { delete graph.overflows; },
    (graph) => { delete graph.bufferItems; },
    (graph) => { graph.overflows = null; },
    (graph, node) => { graph.bufferItems[node] = '1'; },
    (graph) => { graph.buffers = null; },
    (graph) => { delete graph.kinds; },
  ];
  for (mutate of mutations) {
    await assert.rejects(program.build('probe', { grants: [{ capability: 'run', names: [] }, { capability: 'write', names: [directory] }] }), (error) => error.code === 'KASH_MUX_ARITY');
    assert.equal(readFileSync(left, 'utf8'), 'preserve');
    assert.equal(readFileSync(right, 'utf8'), 'preserve');
  }
  assert.equal(forwarded, mutations.length, 'lossy policies did not use request kind 35');
  assert.equal(launches, 0, 'invalid lossy policy launched a process');
} finally {
  prototype.dispatch = dispatch;
  childProcess.spawn = spawn; syncBuiltinESMExports();
  await kame.dispose();
  unlinkSync(left); unlinkSync(right); rmdirSync(directory);
}
NODE
then test-ok "lossy wire policies cannot silently downgrade or bypass limits"; else test-fail "Node forwarded lossy policy validation"; fi
test-end
