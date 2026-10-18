#!/usr/bin/env bash
# Spec: docs/spec/042-kash-streaming.md — explicit graph-wide exit collection
# shellcheck disable=SC1091,SC2016
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T042-10 Kash graph failure policy parity"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/failure-policy"
mkdir -p "$work"
printf '\377' >"$work/invalid-utf8"
run_case() {
	local text="$1" expected="${2:-0}" code="${3:-}" status=0
	timeout 20 "${command[@]}" do run --lang kash --allow-run --allow-read --allow-write -c "$text" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" = "$expected" ]; then test-ok "$backend status"; else test-fail "$backend expected $expected got $status: $(cat "$work/$backend.err")"; fi
	if [ -n "$code" ]; then if grep -q "$code" "$work/$backend.err"; then test-ok "$backend $code"; else test-fail "$backend missing $code: $(cat "$work/$backend.err")"; fi; fi
}
expect() { local name="$1"; shift; if "$@"; then test-ok "$backend $name"; else test-fail "$backend $name"; fi; }
for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	output="$work/$backend-result"
	marker="$work/$backend-joined"
	test-step "$backend: ordinary exits are collected without accepting success"
	run_case "(sh -c \"exit 7\", sh -c \"sleep 0.1; printf retained; touch $marker\") |(merge :failure collect) cat > $output" 1 RECIPE_FAIL
	expect "sibling reaches EOF before failure is reported" test -e "$marker"
	expect "ordinary nonzero exit does not discard sibling output" test "$(cat "$output")" = retained
	run_case "(sh -c \"exit 127\", sh -c \"sleep 0.1; printf kept\") |(merge :failure collect) cat > $output" 1 RECIPE_FAIL
	expect "an authored executable status 127 remains an ordinary exit" test "$(cat "$output")" = kept
	run_case "(sh -c \"exit 7\", sh -c \"sleep 0.1; printf joined\") |(merge :failure collect) cat > $output &" 1 RECIPE_FAIL
	expect "async collection owns its peers until joined" test "$(cat "$output")" = joined
	run_case "(sh -c \"exit 7\", sh -c \"sleep 0.1; printf before-recovery\") |(merge :failure collect) cat > $output ? printf recovered"
	expect "whole-graph recovery happens after sibling completion" test "$(cat "$output")" = before-recovery
	expect "recovery remains explicit" grep -qx recovered "$work/$backend.out"
	run_case "(sh -c \"printf 'a\\n'; exit 7\" |(frame :lines), sh -c \"printf 'b\\n'; exit 9\" |(frame :lines)) |(merge :failure collect :order round-robin) cat > $output" 1 RECIPE_FAIL
	printf 'a\nb\n' >"$work/expected"
	expect "strict ordering composes with collected producer failures" cmp -s "$output" "$work/expected"
	run_case "cat <((sh -c \"exit 7\", sh -c \"sleep 0.1; printf operand\") |(merge :failure collect)) > $output" 1 RECIPE_FAIL
	expect "anonymous producers share their owning graph's collection policy" test "$(cat "$output")" = operand
	test-step "$backend: fail-fast remains default and operator/signal failures remain fatal"
	for policy in '' ':failure fail-fast'; do
		late="$work/$backend-fast-${policy:+explicit}"
		run_case "(sh -c \"exit 7\", sh -c \"sleep 1; touch $late\") |(merge $policy) cat > $output" 1 RECIPE_FAIL
		expect "fail-fast cancels unfinished sibling" test ! -e "$late"
	done
	late="$work/$backend-frame-late"
	run_case "(cat < $work/invalid-utf8 |(frame :lines), sh -c \"sleep 1; touch $late; printf x\" |(frame :lines)) |(merge :failure collect) cat > $output" 1 KASH_FRAME_ENCODING
	expect "framing failure still cancels peers" test ! -e "$late"
	late="$work/$backend-predicate-late"
	run_case "(printf a |(frame :lines) |(filter (nop item) | cat > /dev/null), sh -c \"sleep 1; touch $late; printf x\" |(frame :lines)) |(merge :failure collect) cat > $output" 1 KASH_PREDICATE_BOOLEAN
	expect "predicate failure still cancels peers" test ! -e "$late"
	late="$work/$backend-buffer-late"
	run_case "(printf \"abcd\\n\" |(frame :lines), sh -c \"sleep 1; touch $late; printf x\" |(frame :lines)) |(merge :failure collect :buffer 4) cat > $output" 1 KASH_BUFFER_LIMIT
	expect "operator budget failure still cancels peers" test ! -e "$late"
	late="$work/$backend-signal-late"
	pid_token='\$\$'
	run_case "(sh -c \"kill -TERM $pid_token\", sh -c \"sleep 1; touch $late\") |(merge :failure collect) cat > $output" 1 RECIPE_FAIL
	expect "signal exits are not ordinary collected statuses" test ! -e "$late"
	run_case "yes |(broadcast :failure collect) (head -c 1 > $output, cat > /dev/null)" 1 RECIPE_FAIL
	test-step "$backend: invalid and conflicting policies are effect-free"
	marker="$work/$backend-invalid"
	printf preserve >"$output"
	run_case "(sh -c \"touch $marker\", printf a) |(merge :failure collect) |(broadcast :failure fail-fast) (cat > $output, cat)" 1 KASH_FAILURE_POLICY
	expect "conflicting annotations launch nothing" test ! -e "$marker"
	expect "conflicting annotations preserve destinations" test "$(cat "$output")" = preserve
	for policy in ':failure ignore' ':failure collect :failure collect' ':failure (read! ./policy)' ':failure'; do
		run_case "(sh -c \"touch $marker\", printf a) |(merge $policy) cat > $output" 1 PARSE_ERR
	done
	expect "invalid syntax never runs" test ! -e "$marker"
	expect "invalid syntax never truncates" test "$(cat "$output")" = preserve
	test-step "$backend: collector timeout overrides earlier ordinary failures"
	pid="$work/$backend-pid"
	status=0
	timeout 10 "${command[@]}" do run --lang kash --allow-run --timeout=200 -c "(sh -c \"exit 7\", sh -c \"echo $pid_token > $pid; exec sleep 30\") |(merge :failure collect) cat" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" -eq 1 ] && grep -q RECIPE_TIMEOUT "$work/$backend.err" && { [ ! -e "$pid" ] || ! kill -0 "$(cat "$pid")" 2>/dev/null; }; then test-ok "$backend timeout reaps collector peers"; else test-fail "$backend timeout was masked by an earlier status"; fi
	test-step "$backend: accepted Result retains every failure and deterministic aggregate"
	directory="$work/$backend-result-record"
	mkdir -p "$directory"
	cat >"$directory/Makefile.kmk" <<'KMK'
SHELL = kash
probe :
	result = ((kash """(sh -c "sleep 0.1; exit 7", sh -c "exit 9") |(merge :failure collect) cat > ./output ?"""))
	(out! (json result))
KMK
	if timeout 20 "${command[@]}" -C "$directory" -f ./Makefile.kmk probe >"$directory/result.json" 2>"$directory/error" && node --input-type=module - "$directory/result.json" <<'NODE'
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
const result = JSON.parse(readFileSync(process.argv[2], 'utf8'));
assert.equal(result.status, 9, 'last failing prepared stage, not last observed exit');
assert.equal(result.signal, 0);
assert.equal(result.graph.failure, 'collect');
assert.ok(result.graph.nodes.some((node) => node.state === 'failed' && node.status === 7));
assert.ok(result.graph.nodes.some((node) => node.state === 'failed' && node.status === 9));
assert.ok(result.graph.nodes.some((node) => node.kind === 'process' && node.state === 'completed'));
NODE
	then test-ok "$backend collected Result retains stage identity"; else test-fail "$backend collected Result: $(cat "$directory/error")"; fi
done

test-step "Node requires complete failure-collection descriptors before effects"
if timeout 30 node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync, unlinkSync, rmdirSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { syncBuiltinESMExports } from 'node:module';
const { Kame } = await import('./dist/kame.js');
const directory = mkdtempSync(join(tmpdir(), 'kame-collect-')), output = join(directory, 'output');
writeFileSync(output, 'preserve');
const kame = await Kame.create();
const program = await kame.compile(`SHELL = kash\nprobe :\n\t(printf a, printf b) |(merge :failure collect) cat > ${JSON.stringify(output)}\n`);
const prototype = Object.getPrototypeOf(program.module), dispatch = prototype.dispatch, spawn = childProcess.spawn;
let mutate, calls = 0, launches = 0;
prototype.dispatch = function (...args) {
  if (args[2] === 37) { calls++; const graph = JSON.parse(args[3]); mutate(graph); args[3] = JSON.stringify(graph); }
  return dispatch.apply(this, args);
};
childProcess.spawn = (...args) => { launches++; return spawn(...args); }; syncBuiltinESMExports();
try {
  const mutations = [
    (graph) => { delete graph.failurePolicy; }, (graph) => { graph.failurePolicy = 0; },
    (graph) => { graph.failurePolicy = 2; }, (graph) => { graph.failurePolicy = '1'; },
    (graph) => { graph.failurePolicy = null; }, (graph) => { delete graph.kinds; },
    (graph) => { delete graph.orders; }, (graph) => { delete graph.buffers; },
    (graph) => { delete graph.overflows; }, (graph) => { delete graph.bufferItems; },
  ];
  for (mutate of mutations) {
    await assert.rejects(program.build('probe', { grants: [{ capability: 'run', names: [] }, { capability: 'write', names: [directory] }] }), (error) => error.code === 'KASH_MUX_ARITY');
    assert.equal(readFileSync(output, 'utf8'), 'preserve');
  }
  assert.equal(calls, mutations.length, 'failure collection must use wire kind 37');
  assert.equal(launches, 0);
} finally {
  prototype.dispatch = dispatch; childProcess.spawn = spawn; syncBuiltinESMExports();
  await kame.dispose(); unlinkSync(output); rmdirSync(directory);
}
NODE
then test-ok "failure collection cannot silently downgrade on forwarded hosts"; else test-fail "forwarded failure policy validation"; fi
test-end
