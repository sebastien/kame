#!/usr/bin/env bash
# Spec: docs/spec/042-kash-streaming.md — Slice E scheduling and working buffers
# shellcheck disable=SC1091,SC2016
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T042-05 Kash scheduling and buffer parity"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/scheduling"
mkdir -p "$work"
seq 1 10000 >"$work/source"
awk 'NR % 2 == 1' "$work/source" >"$work/odd"
awk 'NR % 2 == 0' "$work/source" >"$work/even"

run_case() {
	local text="$1" expected="$2" code="${3:-}" language="${4:-kash}" status=0
	timeout 20 "${command[@]}" 'do' run --lang "$language" --allow-run --allow-read --allow-write --capture-limit=128 -c "$text" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
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

for backend in native wasm; do
	if [ "$backend" = native ]; then command=("$CLI_BIN"); else command=(node "$CLI_ROOT/dist/kame.js"); fi
	left="$work/$backend-left"
	right="$work/$backend-right"
	test-step "$backend: whole-record round-robin and slow-destination backpressure"
	run_case "cat < $work/source |(frame :lines) |(balance :buffer 64 :strategy round-robin) (sh -c \"sleep 0.1; cat\" > $left, cat > $right)" 0
	expect "odd records remain ordered and complete" cmp -s "$left" "$work/odd"
	expect "even records remain ordered and complete" cmp -s "$right" "$work/even"
	run_case "printf \"\\nalpha\\r\\nz\" |(frame :lines) |(balance) (cat > $left, cat > $right)" 0
	printf '\nz' >"$work/expected"
	expect "empty and partial final records are each assigned once" cmp -s "$left" "$work/expected"
	printf 'alpha\r\n' >"$work/expected"
	expect "record delimiters survive injection into an external child" cmp -s "$right" "$work/expected"
	run_case "printf empty |(frame :lines) |(filter :false | cat > $work/unused) |(balance) (cat > $left, cat > $right)" 0
	expect "one record is not broadcast" test "$(cat "$left")" = empty
	expect "unassigned child receives EOF" test ! -s "$right"
	run_case "printf \"\" |(frame :lines) |(balance) (cat > $left, cat > $right)" 0
	expect "empty input closes every child" test ! -s "$left"
	expect "empty input closes the second child" test ! -s "$right"
	run_case "printf \"1\\n2\\n3\\n\" |(frame :lines) |(balance :buffer 2) (cat > $left, cat > $right) &" 0
	printf '1\n3\n' >"$work/expected"
	expect "async ownership joins balanced workers" cmp -s "$left" "$work/expected"
	third="$work/$backend-third"
	run_case "printf \"1\\n2\\n3\\n4\\n5\\n6\\n7\" |(frame :lines) |(balance) (cat > $left, cat > $right, cat > $third)" 0
	printf '1\n4\n7' >"$work/expected"
	expect "three-way assignment wraps in group order" cmp -s "$left" "$work/expected"
	printf '2\n5\n' >"$work/expected"
	expect "second of three destinations is independent" cmp -s "$right" "$work/expected"
	printf '3\n6\n' >"$work/expected"
	expect "third destination receives its own whole records" cmp -s "$third" "$work/expected"

	test-step "$backend: named ports, nested fan-out, and anonymous operands"
	run_case "stream left
stream right
(printf \"1\\n2\\n3\\n4\\n\" |(frame :lines) |(balance) (>&left, >&right), &left | cat > $left, &right | cat > $right)" 0
	printf '1\n3\n' >"$work/expected"
	expect "named balance output retains framed transport" cmp -s "$left" "$work/expected"
	printf '2\n4\n' >"$work/expected"
	expect "second named output is an independent channel" cmp -s "$right" "$work/expected"
	run_case '$(cat <(printf "1\n2\n3\n4\n" |(frame :lines) |(balance :buffer 2) (cat, cat) |(merge :buffer 3) cat) | sort)' 0 '' expr
	expect "injected balanced producers own one explicit merge" grep -Fq '"1\n2\n3\n4\n"' "$work/$backend.out"

	test-step "$backend: byte and record budgets are lossless, explicit, and bounded"
	run_case "head -c 32768 /dev/zero |(broadcast :buffer 7) (cat > $left, cat > $right)" 0
	expect "small byte transfer budget preserves every byte" test "$(wc -c <"$left")" -eq 32768
	expect "byte broadcast still delivers both copies" cmp -s "$left" "$right"
	run_case '$(printf "abc\n" |(frame :lines) |(balance :buffer 4) (cat, cat) |(merge) cat)' 0 '' expr
	expect "budget includes the delimiter, but not the private header" grep -Fq '"abc\n"' "$work/$backend.out"
	run_case "head -c 65536 /dev/zero |(frame :lines) |(balance :buffer 65536) (wc -c > $left, wc -c > $right)" 0
	expect "maximum budget accepts one complete 64 KiB payload" test "$(cat "$left")" -eq 65536
	expect "maximum-size record is not split between destinations" test "$(cat "$right")" -eq 0
	run_case "printf \"abcd\\n\" |(frame :lines) |(balance :buffer 4) (cat > $left, cat > $right)" 1 KASH_BUFFER_LIMIT
	run_case "printf \"abcd\\n\" |(frame :lines) |(broadcast :buffer 4) (cat > $left, cat > $right)" 1 KASH_BUFFER_LIMIT
	run_case '(printf "abcd\n" |(frame :lines), printf "x\n" |(frame :lines)) |(merge :buffer 4) |(encode :lines)' 1 KASH_BUFFER_LIMIT
	run_case "printf \"abcd\\n\" |(frame :lines) |(balance :buffer 4) (cat > $left, cat > $right) ? printf recovered" 0
	expect "budget failure participates in whole-graph recovery" grep -qx recovered "$work/$backend.out"
	run_case "yes |(frame :lines) |(balance) (head -n 1 > $left, cat > /dev/null)" 1 RECIPE_FAIL

	test-step "$backend: invalid scheduling fails before effects"
	marker="$work/$backend-marker"
	printf preserve >"$left"
	run_case "sh -c \"touch $marker\" |(balance) (cat > $left, cat > $right)" 1 KASH_FRAME_REQUIRED
	expect "unframed balance starts nothing" test ! -e "$marker"
	expect "unframed balance preserves output" test "$(cat "$left")" = preserve
	run_case "printf a |(frame :lines) |(balance) cat > $left" 1 KASH_MUX_ARITY
	run_case "sh -c \"touch $marker\" |(broadcast :buffer 65537) (cat > $left, cat > $right)" 1 PARSE_ERR
	run_case "sh -c \"touch $marker\" |(frame :lines) |(balance :strategy random) (cat > $left, cat > $right)" 1 PARSE_ERR
	expect "invalid options never execute a source" test ! -e "$marker"
	expect "invalid options preserve outputs" test "$(cat "$left")" = preserve
done

test-step "Node validates forwarded budgets before launch or output truncation"
if timeout 30 node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { mkdtempSync, readFileSync, rmdirSync, unlinkSync, writeFileSync } from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
const { Kame } = await import('./dist/kame.js');
const directory = mkdtempSync(join(tmpdir(), 'kame-scheduling-'));
const left = join(directory, 'left'), right = join(directory, 'right');
writeFileSync(left, 'preserve'); writeFileSync(right, 'preserve');
const kame = await Kame.create();
const source = `SHELL = kash\nprobe :\n\tprintf "x\\n" |(frame :lines) |(balance :buffer 2) (cat > ${JSON.stringify(left)}, cat > ${JSON.stringify(right)})\n`;
const program = await kame.compile(source, { name: 'budgets.kmk' });
const prototype = Object.getPrototypeOf(program.module);
const dispatch = prototype.dispatch, spawn = childProcess.spawn;
let launches = 0, forwarded = 0, mutate;
childProcess.spawn = (...args) => { launches++; return spawn(...args); };
syncBuiltinESMExports();
prototype.dispatch = function (...args) {
  if (args[2] === 34) {
    forwarded++;
    const descriptor = JSON.parse(args[3]);
    mutate(descriptor, descriptor.kinds.indexOf('balance'));
    args[3] = JSON.stringify(descriptor);
  }
  return dispatch.apply(this, args);
};
try {
  for (mutate of [
    (graph, node) => { graph.buffers[node] = 65537; },
    (graph, node) => { graph.buffers[node] = -1; },
    (graph, node) => { graph.buffers[node] = '2'; },
    (graph) => { graph.buffers = []; },
    (graph) => { graph.buffers[0] = 2; },
  ]) {
    await assert.rejects(program.build('probe', { grants: [{ capability: 'run', names: [] }, { capability: 'write', names: [directory] }] }), (error) => error.code === 'KASH_MUX_ARITY');
    assert.equal(readFileSync(left, 'utf8'), 'preserve');
    assert.equal(readFileSync(right, 'utf8'), 'preserve');
  }
  assert.equal(forwarded, 5, 'configured buffers did not use request kind 34');
  assert.equal(launches, 0, 'invalid budgets launched a process');
} finally {
  prototype.dispatch = dispatch;
  childProcess.spawn = spawn; syncBuiltinESMExports();
  await kame.dispose();
  unlinkSync(left); unlinkSync(right); rmdirSync(directory);
}
NODE
then test-ok "invalid forwarded budgets cannot launch or truncate outputs"; else test-fail "Node forwarded budget validation"; fi
test-end
