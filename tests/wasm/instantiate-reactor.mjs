import { WASI } from 'node:wasi';

// Raw ABI tests use the same empty WASI environment as the production loader.
export async function instantiateReactor(bytes) {
  const wasi = new WASI({ version: 'preview1', returnOnExit: true, args: [], env: {}, preopens: {} });
  const result = await WebAssembly.instantiate(bytes, { wasi_snapshot_preview1: wasi.wasiImport });
  wasi.initialize(result instanceof WebAssembly.Instance ? result : result.instance);
  return result;
}
