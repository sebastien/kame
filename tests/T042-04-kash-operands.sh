#!/usr/bin/env bash
# Spec: docs/spec/042-kash-streaming.md — Slice D live stream operands
# shellcheck disable=SC1091,SC2016
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T042-04 Kash live stream operand parity"
cd "$CLI_ROOT"
make dist-wasm >/dev/null
cli_build
work="$TMPDIR/operands"
mkdir -p "$work"
printf stdin >"$work/input"

run_case() {
	local text="$1" expected="$2" code="${3:-}" language="${4:-expr}" status=0
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
	test-step "$backend: live producer arguments and current-input ownership"
	run_case '$(cat <(printf "abc\n"))' 0
	expect "anonymous output retains its delimiter" grep -Fq '"abc\n"' "$work/$backend.out"
	run_case '$(cat <(printf a) <(printf b))' 0
	expect "two producer arguments remain independent" grep -Fq '"ab"' "$work/$backend.out"
	run_case '$(cat <(printf 1) <(printf 2) <(printf 3) <(printf 4) <(printf 5) <(printf 6) <(printf 7) <(printf 8))' 0
	expect "many independently opened operands do not exhaust an I/O worker pool" grep -Fq '"12345678"' "$work/$backend.out"
	run_case '$(printf "abc\n" | sh -c "if read line; then exit 9; fi; cat \"\$1\"" receiver <&)' 0
	expect "<& claims upstream without also feeding stdin" grep -Fq '"abc\n"' "$work/$backend.out"
	run_case "\$(printf upstream | cat < $work/input - <&)" 0
	expect "independent stdin may precede a current stream operand" grep -Fq '"stdinupstream"' "$work/$backend.out"
	run_case "\$(printf upstream | cat - <& < $work/input)" 0
	expect "independent stdin may follow a current stream operand" grep -Fq '"stdinupstream"' "$work/$backend.out"
	run_case "\$(cat < $work/input | cat - <& < $work/input)" 0
	expect "producer and receiver have independent stdin redirections" grep -Fq '"stdinstdin"' "$work/$backend.out"
	run_case '$(cat (nop ["--"]) <(printf abc))' 0
	expect "argument ports follow actual list splicing" grep -Fq '"abc"' "$work/$backend.out"
	run_case '$(cat <((printf a, printf b) |(merge) cat) | wc -c)' 0
	expect "nested producer graph is one live input" grep -Fq '"2\n"' "$work/$backend.out"
	run_case "\$(cat <((printf log > $work/$backend-log, printf payload)))" 0
	expect "explicit side sinks do not make producer capture ambiguous" grep -Fq '"payload"' "$work/$backend.out"
	expect "anonymous producer owns its explicit side sink" test "$(cat "$work/$backend-log")" = log
	run_case '$(cat <(printf "a\n" |(frame :lines)) | wc -l)' 0
	expect "external operands receive bytes, not private frame headers" grep -Fq '"1\n"' "$work/$backend.out"
	run_case '$(cat <(head -c 4194304 /dev/zero) | wc -c)' 0
	expect "producer data streams beyond capture bounds" grep -Fq '"4194304\n"' "$work/$backend.out"

	test-step "$backend: named arguments and one graph owner"
	run_case 'stream left
stream right
(printf a >&left, printf b >&right, cat <&left <&right)' 0 '' kash
	expect "named positional streams do not conflate stdin" grep -qx ab "$work/$backend.out"
	run_case 'diff -u <(printf same) <(printf same)' 0 '' kash
	run_case '$(node -e "const fs = require(\"node:fs\"); fs.readSync(fs.openSync(process.argv[1], \"r\"), Buffer.alloc(1), 0, 1, 0)" <(printf a))' 1 RECIPE_FAIL
	expect "seekability requirements fail visibly instead of materializing a file" grep -q ESPIPE "$work/$backend.err"
	run_case '$(head -c 1 <(yes))' 1 RECIPE_FAIL
	run_case '$(head -c 1 <(yes) ?)' 0
	run_case '$(true <(printf ignored) ?)' 0
	run_case '$(sh -c "sleep 0.1; cat \"\$1\"" receiver <(printf early))' 0
	expect "receiver may open its operand after the producer exits" grep -Fq '"early"' "$work/$backend.out"
	run_case '$(cat <(yes))' 1 CAPTURE_LIMIT
	run_case 'stream terminal
(&terminal, printf abc >&terminal)' 0 '' kash
	expect "a native node may precede the first executable" grep -qx abc "$work/$backend.out"

	test-step "$backend: static validation has no launch or truncation effects"
	marker="$work/$backend-marker"
	output="$work/$backend-output"
	printf preserve >"$output"
	run_case "sh -c \"touch $marker\" <& > $output" 1 KASH_CHANNEL_UNBOUND kash
	expect "unbound current input starts nothing" test ! -e "$marker"
	expect "unbound current input preserves output" test "$(cat "$output")" = preserve
	run_case '$(printf a | cat <& <&)' 1 KASH_DOUBLE_CONSUME
	run_case '$(cat <((printf a, printf b)))' 1 KASH_AMBIGUOUS_CAPTURE
	run_case "sh -c \"touch $marker\" <(no-such-kash-tool) > $output" 1 TOOL_MISSING kash
	expect "missing producer starts no receiver" test ! -e "$marker"
	expect "missing producer preserves output" test "$(cat "$output")" = preserve
	run_case 'stream reports
(printf a >&reports, cat <&reports <&reports)' 1 KASH_CHANNEL_DUP_READER kash
	run_case '$(cat prefix<(printf a))' 1 PARSE_ERR
	run_case '$(cat <(printf a)suffix)' 1 PARSE_ERR

	test-step "$backend: timeout reaps anonymous producer graphs"
	pid="$work/$backend.pid"
	pid_token='\$\$'
	status=0
	timeout 10 "${command[@]}" 'do' run --lang kash --allow-run --timeout=200 -c "cat <(sh -c \"echo $pid_token > '$pid'; exec sleep 30\")" >"$work/$backend.out" 2>"$work/$backend.err" || status=$?
	if [ "$status" -eq 1 ] && grep -q RECIPE_TIMEOUT "$work/$backend.err" && { [ ! -e "$pid" ] || ! kill -0 "$(cat "$pid")" 2>/dev/null; }; then test-ok "$backend timeout reaped producer graph"; else test-fail "$backend timeout cleanup failed"; fi

	test-step "$backend: generated producer inputs gate launch, reuse, and invalidation"
	build="$work/$backend-build"
	mkdir -p "$build"
	printf 'first\n' >"$build/seed"
	cat >"$build/Makefile.kmk" <<'KMK'
SHELL = kash
./generated : ./seed
	cat < ./seed > @>
./result :
	cat <(sh -c "echo run >> runs; cat" < ./generated) > @>
KMK
	for attempt in first cached changed; do
		if [ "$attempt" = changed ]; then printf 'changed\n' >"$build/seed"; fi
		if timeout 20 "${command[@]}" -C "$build" -f ./Makefile.kmk ./result >"$work/$backend.out" 2>"$work/$backend.err"; then test-ok "$backend generated input: $attempt"; else test-fail "$backend build: $(cat "$work/$backend.err")"; fi
		if [ "$attempt" = changed ]; then expected=2; else expected=1; fi
		expect "generated input reuse: $attempt" test "$(wc -l <"$build/runs")" -eq "$expected"
	done
	expect "producer observes changed generated bytes" test "$(cat "$build/result")" = changed
	if compgen -G "$TMPDIR/kame-operands-*" >/dev/null; then test-fail "$backend leaked FIFO operand paths"; else test-ok "$backend released FIFO operand paths"; fi
done

test-step "Node embedding cancellation owns relays and releases operand paths"
if timeout 30 node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmdirSync, unlinkSync, writeFileSync } from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
const { Kame } = await import('./dist/kame.js');
const kame = await Kame.create();
// Kash captures resolve tracked tools through a build/source session, not evaluate().
const program = await kame.compile('SHELL = kash\nwarmup :\n\t(nop $(cat <(printf warmup)))\nprobe :\n\t(nop $(cat <(sleep 30)))\nrecovered :\n\t(nop $(cat <(printf recovered)))\n', { name: 'operands.kmk' });
const spawn = childProcess.spawn;
const spawnSync = childProcess.spawnSync;
const options = { grants: [{ capability: 'run', names: [] }] };
try {
  const descriptors = () => existsSync('/proc/self/fd') ? readdirSync('/proc/self/fd').length : 0;
  await program.build('warmup', options);
  const baseline = descriptors();
  for (let attempt = 0; attempt < 8; attempt++) {
    const controller = new AbortController();
    const children = [], paths = [];
    childProcess.spawn = (executable, args, configuration) => {
      const child = spawn(executable, args, configuration);
      children.push(child);
      if (args[2] === 'kame-stream') {
        paths.push(args[3]);
        child.once('spawn', () => controller.abort());
      }
      return child;
    };
    syncBuiltinESMExports();
    await assert.rejects(program.build('probe', { ...options, signal: controller.signal }), (error) => error.code === 'EXEC_CANCELLED');
    assert.ok(paths.length > 0, 'operand relay was not started');
    assert.ok(paths.every((path) => !existsSync(path)), 'cancelled operand paths survived');
    assert.ok(children.every((child) => child.exitCode !== null || child.signalCode !== null), 'cancelled graph retained a live child');
  }
  childProcess.spawn = spawn;
  syncBuiltinESMExports();
  await program.build('recovered', options);
  assert.ok(descriptors() <= baseline, 'cancelled operands leaked descriptors');
  const directory = mkdtempSync(join(tmpdir(), 'kame-operand-unsupported-'));
  const output = join(directory, 'output');
  try {
    writeFileSync(output, 'preserve');
    let launches = 0;
    childProcess.spawn = (...args) => { launches++; return spawn(...args); };
    childProcess.spawnSync = () => ({ status: 1 });
    syncBuiltinESMExports();
    const unsupported = await kame.compile(`SHELL = kash\nprobe :\n\tcat <(printf ignored) > ${JSON.stringify(output)}\n`, { name: 'unsupported.kmk' });
    await assert.rejects(unsupported.build('probe', { grants: [...options.grants, { capability: 'write', names: [directory] }] }), (error) => error.code === 'KASH_STREAM_OPERAND_UNSUPPORTED');
    assert.equal(launches, 0, 'unsupported operands launched a graph');
    assert.equal(readFileSync(output, 'utf8'), 'preserve', 'unsupported operands truncated output');
  } finally { unlinkSync(output); rmdirSync(directory); }
} finally {
  childProcess.spawn = spawn;
  childProcess.spawnSync = spawnSync;
  syncBuiltinESMExports();
  await kame.dispose();
}
NODE
then test-ok "Node cancellation reaps producers/relays, removes FIFOs, and retains no descriptors"; else test-fail "Node stream operand cancellation lifecycle"; fi
test-end
