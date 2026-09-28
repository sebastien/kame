#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

make wasm >/dev/null
info="$(node ./tools/kame-wasm.mjs --self-test)"
for export in memory kame_wasm_abi_version kame_wasm_alloc kame_wasm_copy kame_wasm_event_header kame_wasm_eval_pure; do
	case "$info" in
	*"\"$export\""*) ;;
	*)
		echo "WASM loader did not expose ABI export $export: $info" >&2
		exit 1
		;;
	esac
done
case "$info" in
*'"schema":1'*'"payloadLength":6'*'"memoryBytes":'*'"pureExpression":"a:b"'*) ;;
*)
	echo "WASM loader did not complete ABI self-test: $info" >&2
	exit 1
	;;
esac
actual="$(node ./tools/kame-wasm.mjs do expr -c '(count [1 2 3])')"
if [ "$actual" != "3" ]; then
	echo "WASM expression CLI = $actual, want 3" >&2
	exit 1
fi
program="$(mktemp)"
input="$(mktemp)"
written="$(mktemp)"
trap 'rm -f "$program" "$input" "$written"' EXIT
printf 'result = (join ["wasm" "source"] "-")\n' >"$program"
actual="$(node ./tools/kame-wasm.mjs do expr -f "$program")"
if [ "$actual" != "wasm-source" ]; then
	echo "WASM source expression CLI = $actual, want wasm-source" >&2
	exit 1
fi
printf 'result = (shell "printf async-source")\n' >"$program"
actual="$(node ./tools/kame-wasm.mjs do expr --async -f "$program")"
if [ "$actual" != "async-source" ]; then
	echo "WASM async source expression CLI = $actual, want async-source" >&2
	exit 1
fi
printf 'async-read' >"$input"
actual="$(node ./tools/kame-wasm.mjs do expr --async -c "(read \"$input\")")"
if [ "$actual" != "async-read" ]; then
	echo "WASM async read CLI = $actual, want async-read" >&2
	exit 1
fi
printf 'result = (read "%s")\n' "$input" >"$program"
actual="$(node ./tools/kame-wasm.mjs do expr --async -f "$program")"
if [ "$actual" != "async-read" ]; then
	echo "WASM async source read CLI = $actual, want async-read" >&2
	exit 1
fi
actual="$(node ./tools/kame-wasm.mjs do expr --async -c "(write \"$written\" \"async-write\")")"
if [ "$actual" != "nil" ] || [ "$(cat "$written")" != "async-write" ]; then
	echo "WASM async write CLI failed" >&2
	exit 1
fi
printf 'result = (write "%s" "source-write")\n' "$written" >"$program"
actual="$(node ./tools/kame-wasm.mjs do expr --async -f "$program")"
if [ "$actual" != "nil" ] || [ "$(cat "$written")" != "source-write" ]; then
	echo "WASM async source write CLI failed" >&2
	exit 1
fi
actual="$(KAME_WASM_TEST_VALUE=async-env node ./tools/kame-wasm.mjs do expr --async -c '(env "KAME_WASM_TEST_VALUE")')"
if [ "$actual" != "async-env" ]; then
	echo "WASM async environment CLI = $actual, want async-env" >&2
	exit 1
fi
actual="$(node ./tools/kame-wasm.mjs do expr --async -c '(shell "printf async-wasm")')"
if [ "$actual" != "async-wasm" ]; then
	echo "WASM async expression CLI = $actual, want async-wasm" >&2
	exit 1
fi
if failed="$(node ./tools/kame-wasm.mjs do expr --async -c '(shell "false")' 2>&1)"; then
	echo "WASM async failed process unexpectedly succeeded" >&2
	exit 1
fi
case "$failed" in
*"RECIPE_FAIL: process exited with status 1"*) ;;
*)
	echo "WASM async failed process diagnostic = $failed" >&2
	exit 1
	;;
esac
node --input-type=module - <<'NODE'
import { readFile } from 'node:fs/promises';

const bytes = await readFile('./build/wasm/kame.wasm');
const { instance } = await WebAssembly.instantiate(bytes, {});
const { exports } = instance;
const handle = exports.kame_wasm_instance_create();
if (handle === 0n || exports.kame_wasm_instance_free(handle) !== 0 || exports.kame_wasm_instance_free(handle) !== 1) {
  throw new Error('instance handles did not reject a stale free');
}

const alloc = (size, alignment = 1) => {
  const pointer = exports.kame_wasm_alloc(size, alignment);
  if (pointer === 0) throw new Error(`allocation failed: ${size}`);
  return pointer;
};
const write = (text) => {
  const data = new TextEncoder().encode(text);
  const pointer = alloc(data.length || 1);
  new Uint8Array(exports.memory.buffer, pointer, data.length).set(data);
  return [pointer, data.length];
};
const asyncInstance = exports.kame_wasm_instance_create();
if (exports.kame_wasm_source_compile(asyncInstance, 0, 0) !== 0) throw new Error('empty source did not compile');
const [expression, expressionLength] = write('(shell "echo wasm")');
if (exports.kame_wasm_expression_begin(asyncInstance, expression, expressionLength) !== 0) throw new Error('async expression did not begin');
if (exports.kame_wasm_step(asyncInstance) !== 1) throw new Error('step did not yield a host request');
const header = alloc(48, 8);
if (exports.kame_wasm_next_event_header(asyncInstance, header, 48) !== 0) throw new Error('request header was unavailable');
const view = new DataView(exports.memory.buffer, header, 48);
const request = view.getBigUint64(20, true);
const payloadLength = view.getUint32(44, true);
const payload = alloc(payloadLength || 1);
if (exports.kame_wasm_event_payload_copy(asyncInstance, payload, payloadLength) !== 0) throw new Error('request payload copy failed');
if (new TextDecoder().decode(new Uint8Array(exports.memory.buffer, payload, payloadLength)) !== 'echo wasm') throw new Error('unexpected process payload');
if (exports.kame_wasm_source_compile(asyncInstance, 0, 0) !== 4) throw new Error('compile replaced a pending expression');
const [completion, completionLength] = write('wasm');
if (exports.kame_wasm_complete_bytes(asyncInstance, request + 1n, completion, completionLength) !== 1) throw new Error('foreign request completion was accepted');
if (exports.kame_wasm_complete_bytes(asyncInstance, request, completion, completionLength) !== 0) throw new Error('completion was rejected');
if (exports.kame_wasm_complete_bytes(asyncInstance, request, completion, completionLength) !== 0) throw new Error('late completion was not ignored');
let state = 0;
for (let i = 0; i < 4 && state !== 2; i++) state = exports.kame_wasm_step(asyncInstance);
if (state !== 2) throw new Error(`completed expression did not become terminal: ${state}`);
if (exports.kame_wasm_instance_free(asyncInstance) !== 0) throw new Error('async instance did not free');

const cancelledInstance = exports.kame_wasm_instance_create();
if (exports.kame_wasm_source_compile(cancelledInstance, 0, 0) !== 0) throw new Error('cancelled instance did not compile');
if (exports.kame_wasm_expression_begin(cancelledInstance, expression, expressionLength) !== 0) throw new Error('cancelled expression did not begin');
if (exports.kame_wasm_step(cancelledInstance) !== 1) throw new Error('cancelled expression did not yield');
const cancelledHeader = alloc(48, 8);
if (exports.kame_wasm_next_event_header(cancelledInstance, cancelledHeader, 48) !== 0) throw new Error('cancelled request header was unavailable');
const cancelledRequest = new DataView(exports.memory.buffer, cancelledHeader, 48).getBigUint64(20, true);
if (exports.kame_wasm_expression_cancel(cancelledInstance) !== 0) throw new Error('host request cancellation was rejected');
if (exports.kame_wasm_complete_bytes(cancelledInstance, cancelledRequest, completion, completionLength) !== 0) throw new Error('cancelled request late completion was not ignored');
if (exports.kame_wasm_step(cancelledInstance) !== 2) throw new Error('cancelled expression was not terminal');
const cancelledLength = alloc(4, 4);
if (exports.kame_wasm_result_copy(cancelledInstance, 0, 0, cancelledLength) !== 5) throw new Error('cancelled expression lacked a diagnostic');
const diagnosticLength = exports.kame_wasm_instance_diagnostic_length(cancelledInstance);
if (diagnosticLength === 0) throw new Error('cancelled instance had no diagnostic');
const diagnosticPointer = alloc(diagnosticLength || 1);
if (exports.kame_wasm_instance_diagnostic_copy(cancelledInstance, diagnosticPointer, diagnosticLength) !== 0) throw new Error('cancellation diagnostic copy failed');
if (!new TextDecoder().decode(new Uint8Array(exports.memory.buffer, diagnosticPointer, diagnosticLength)).includes('EXEC_CANCELLED')) throw new Error('unexpected cancellation diagnostic');
if (exports.kame_wasm_instance_free(cancelledInstance) !== 0) throw new Error('cancelled instance did not free');
NODE
