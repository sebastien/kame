#!/usr/bin/env node
/**
 * Enforce the freestanding build boundary of docs/spec/010-wasm.md.
 *
 * A portable package that acquires a hosted transitive import (os, conc, sync,
 * a native host) shows up as an import on the linked module, because the module
 * is built with -nostdlib and no host imports are supplied. This check fails
 * closed on any import so the boundary is a link-time property, not a promise.
 */
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
if (imports.length !== 0) {
  const rendered = imports.map((entry) => `${entry.module}.${entry.name} (${entry.kind})`).join(', ');
  stderr.write(`wasm-imports: freestanding module has hosted imports: ${rendered}\n`);
  process.exit(1);
}

const exports = WebAssembly.Module.exports(module);
stdout.write(`wasm-imports: ${wasmPath} imports nothing and exports ${exports.length} symbols\n`);
