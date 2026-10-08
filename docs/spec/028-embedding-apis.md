# Embedding APIs

## Scope

Importable JavaScript and Python interfaces expose Kame without changing
the CLI grammar. The APIs cover source compilation, expression evaluation,
builds and watches; copy all values and events that cross an API boundary; and
provide explicit grants, cancellation and deterministic disposal.

The JavaScript API uses the existing freestanding WASM module and host services.
It is an ESM library entry point that can be imported without starting the CLI.
The Python API is an asynchronous client for the installed `kame` executable;
it reuses the public CLI protocol instead of binding generated C internals. The
Python client exposes copied parse/evaluation results, builds and watch events,
and owns and reaps every subprocess it starts.

## JavaScript contract

The package exports `Kame` and `KameProgram` from `src/js/kame.js`:

```js
import { Kame } from './dist/kame.js';

const kame = await Kame.create({ wasmPath: './dist/kame.wasm' });
try {
  const program = await kame.compile(source, { name: 'project.km' });
  try {
    const value = await program.evaluate('(count [1 2 3])');
    const watch = await program.watch(['./public/index.txt']);
    await watch.close();
  } finally {
    await program.dispose();
  }
} finally {
  await kame.dispose();
}
```

`compile(source, options)` copies the source and its name into a reusable
program descriptor and validates it before returning. Program operations may
be repeated; each operation owns an isolated ABI instance so parser/runtime
state and monotonic WASM scratch allocations cannot leak across operations.
`evaluate(expression, options)` returns a copied UTF-8 string in canonical
Kame display form. `build(targets, options)` returns copied target values in
canonical display form and a copied ordered event list.
`watch(targets, options)` returns an async iterator over copied snapshots and
events; `invalidate(resources)` schedules a refresh, and `close()` cancels and
awaits host requests before freeing its instance.

Every operation accepts an `AbortSignal` and explicit capability grants. An
omitted grant set uses no grants. The optional asynchronous `hostRequest`
callback receives an immutable request with copied payload bytes, a stable
request identifier, capability, and signal. It returns one typed completion
(`bytes`, `text`, `json`, `nil`, or `failure`). The wrapper submits exactly one
completion. Cancellation, disposal, or a rejected callback completes the
request as a failure; late callback results are ignored. Default Node host
services remain available when no callback is supplied.

`dispose()` is idempotent, aborts active work, waits for operations and child
processes to settle, releases active watches and cache leases, and makes later
operations fail with `DISPOSED`. A host callback may finish after cancellation;
its late result is ignored and its promise does not hold disposal open. A
`KameProgram` is invalid after its own
`dispose()` or the owning `Kame.dispose()`.

## Python contract

The `src/py/kame.py` module exports an async context-managed `Kame` client. It
accepts an executable path, a working directory and explicit capability
grants. `compile`, `evaluate`, and `build` return copied Python strings,
records, and event objects. `watch` is an async iterator. Each request starts a
managed CLI process; cancellation terminates and reaps its process group, and
`close()` awaits all active requests before returning. No third-party Python
dependency is required.

The Python client uses Kame's configured CLI host services. It forwards explicit
grants and follows the CLI's documented default grant policy. Its watch API
currently accepts `.kmk` rule programs and yields the CLI's copied JSON events.
User-provided asynchronous host request callbacks are a JavaScript API
feature; the Python client does not claim to execute arbitrary callbacks inside
the WASM runtime.

## Lifecycle and ownership

- API calls are serialized per Kame WASM module instance; independent Kame
  objects may run concurrently.
- Source, result, event, request, and completion buffers are copied before the
  next allocating ABI call or callback yield.
- JavaScript denies every capability unless explicitly granted. Python forwards
  explicit grants and follows the CLI's documented defaults. Filesystem grants
  are canonicalized and checked against request paths before JS host callbacks
  run.
- Each completion is correlated to one request and submitted once. Completion
  after cancellation or disposal has no effect.
- Disposal is safe to call repeatedly and waits for all owned asynchronous work.
- Repeated compile/evaluate/build/dispose cycles do not retain ABI instances,
  callback buffers, watches, child processes, or temporary source files.

## Acceptance

- Node can import the library without running CLI dispatch.
- Native CLI and JavaScript API agree on parse diagnostics and pure evaluation
  values; returned buffers remain unchanged after later API calls.
- An async host callback may yield while another independent Kame object runs;
  request identity and completion are preserved.
- A denied grant invokes no corresponding host callback.
- Build results and events are copied and ordered; watch invalidation yields a
  new snapshot and close cancels pending callbacks before disposal.
- Abort and disposal during pending evaluation/build/watch reap children and
  ignore late host completions; a never-settling user callback does not block
  disposal after its request has been cancelled.
- Python tests cover parse/evaluate/build/watch, grant forwarding, cancellation,
  repeated calls, copied results, and context-manager cleanup.
- At least 100 repeated JS and Python lifecycle cycles leave no active instance,
  child process, temporary file or watch.

Both public APIs must satisfy these lifecycle rules from a clean checkout.
