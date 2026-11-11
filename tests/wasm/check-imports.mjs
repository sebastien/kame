#!/usr/bin/env node
// Enforce the Go WASI reactor import boundary.
import { readFile } from 'node:fs/promises';
import process, { argv, stderr, stdout } from 'node:process';

const wasmPath = argv[2] ?? 'build/wasm/kame.wasm';

let bytes;
try {
  bytes = await readFile(wasmPath);
} catch (error) {
  stderr.write(`wasm-imports: cannot read ${wasmPath}: ${error.message}\n`);
  process.exit(1);
}

let module;
try {
  module = await WebAssembly.compile(bytes);
} catch (error) {
  stderr.write(`wasm-imports: ${wasmPath} is not a valid module: ${error.message}\n`);
  process.exit(1);
}

const imports = WebAssembly.Module.imports(module);
const runtimeImports = new Set(['sched_yield', 'proc_exit', 'args_get', 'args_sizes_get', 'clock_time_get', 'environ_get', 'environ_sizes_get', 'fd_write', 'random_get', 'poll_oneoff', 'fd_close', 'fd_read', 'fd_fdstat_get', 'fd_fdstat_set_flags', 'fd_prestat_get', 'fd_prestat_dir_name']);
if (imports.some(entry => entry.module !== 'wasi_snapshot_preview1' || entry.kind !== 'function' || !runtimeImports.has(entry.name))) {
  const rendered = imports.map((entry) => `${entry.module}.${entry.name} (${entry.kind})`).join(', ');
  stderr.write(`wasm-imports: module has unexpected host imports: ${rendered}\n`);
  process.exit(1);
}

const exports = WebAssembly.Module.exports(module);
stdout.write(`wasm-imports: ${wasmPath} has only Go runtime WASI imports and exports ${exports.length} symbols\n`);
