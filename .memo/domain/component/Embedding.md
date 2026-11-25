---
identity: 6825ab52-0de8-46a2-b23e-9380766f276d
created: 2026-11-25T18:43:43.319483+00:00
updated: 2026-11-25T18:43:43.786966+00:00
---
# Embedding and distribution

Embedding and distribution cover how Kame runs outside the native CLI and
how releases reach users. One Go core (engine, parsers, evaluator, runtime,
operations, presenter) builds both natively and for `wasip1/wasm`. Hosts
supply only filesystem, process and terminal work.

## Parts

- **WASI reactor (`cmd/kame-wasm`, `host/wasm`).** A Go WASI preview-1
  reactor built with `-buildmode=c-shared`, exporting about 100
  `//go:wasmexport` primitives (ABI v1, `src/c/wasm/kame_wasm.h`). It imports
  no filesystem, socket or process functions, creates no hidden threads and
  never blocks on JavaScript.
- **JavaScript (`src/js/kame.js`).** A Node 18+ ESM CLI and embedding API
  (`Kame.create` → `compile` → `evaluate`/`build`/`watch`). It services every
  host request, running processes through `node:child_process`. Each operation
  gets an isolated ABI instance "so parser/runtime state cannot leak across
  operations". The JavaScript API denies all capabilities unless granted.
- **Python (`src/py/kame.py`).** An asyncio client that runs the installed
  `kame` CLI as subprocesses. It is not a WASM binding, has no host callbacks
  and follows the CLI's grant defaults.
- **Windows (`cmd/kame-launcher`).** Starts the bundled JavaScript CLI with
  Node.js. A native Windows binary is specified (spec 035) but does not ship.
- **Launcher and bootstrap (`bin/kame`, `Makefile`).** Select a native or
  WASM backend, then provision, verify (SHA-256 plus an Ed25519-signed
  manifest) and `exec` a pinned release. The Makefile only forwards goals to
  Kame. `Kamefile` is the real build.

## Flow (WASM)

JavaScript call → wasm export → `step` until an event or host request →
JavaScript services the request asynchronously → completion with request ID
and generation → engine validates it → events (payload pinned until copied
or discarded).

## Invariants

- Native and WASM agree on portable behavior. A missing host capability is
  `FEATURE_UNSUP`, never an approximation.
- Wrappers serialize calls per instance, complete each request exactly once
  and drive bounded finalization before freeing.
- WASM handles are a slot plus a generation, so stale or foreign handles are
  rejected (`HANDLE_INVALID`).
- `do clean` and `do pick` exist only on native POSIX.

Related: [domain/component:Host] [domain/decision:portable_host_boundary]
[domain/decision:implementation_language+002]

Sources: `docs/spec/010-wasm.md`, `docs/spec/015-distribution.md`,
`docs/spec/028-embedding-apis.md`, `docs/ref-embedding.md`,
`docs/ref-installation.md`.
