# Freestanding WebAssembly Host

## Purpose

The WebAssembly target embeds the portable engine and languages in browser or
server JavaScript. JavaScript supplies host capabilities; the module does not
pretend that browsers provide POSIX files or processes.

Implementation begins only after the native vertical slice passes.

## Build Boundary

The WebAssembly module targets `wasm32-freestanding`. It includes:

- Core values and engine.
- Template, expression, and rule parsers and formatters.
- Program compilation and evaluation.
- Pure collection, text, and path operations.
- Host-request operation stubs.

It excludes POSIX, CLI, `os`, `conc`, `sync`, native process code, and direct
filesystem access.

Translation and link tests must fail if a portable package acquires a hosted
transitive import.

## Execution Model

JavaScript drives the engine synchronously through explicit calls:

1. Create an instance with a fixed or host-provided heap.
2. Compile source.
3. Request one or more targets or expressions.
4. Call `step` until the engine emits an event or needs host work.
5. Perform host work asynchronously in JavaScript.
6. Feed completion data back with request ID and generation.
7. Continue stepping until roots complete.

The module creates no hidden thread and does not block awaiting JavaScript.

## ABI

The exported C ABI provides conceptual operations for:

- Instance create and free.
- Source compile.
- Target or expression request and cancellation.
- Engine step.
- Next event metadata and payload copy.
- Host completion success or failure.
- Diagnostic retrieval.
- Tool declaration, host-supplied tool path, and target-scoped tool checks.
- Inspection capability policy for read-only dependency resolution.

The tool and inspection operations are the WASM counterpart of the native
`do tools`/`do tools check` surface described in `009-cli.md`. `kame_wasm_tools`
copies the declared tool names; `kame_wasm_set_tool_path` supplies a
host-resolved executable (an empty path marks an unavailable tool);
`kame_wasm_tools_check` walks a selected target's dependency plan and returns
schema-1 diagnostic JSON Lines without executing a recipe; and
`kame_wasm_inspection_grant` configures the capability policy used while
resolving read-only computed inputs (an empty capability clears the defaults).
`kame_wasm_tools_check` may return `KAME_WASM_HOST_NEEDED` when inspection must
forward a filesystem or environment request to the embedding host; the host
steps, services the request, and retries the query, which resumes the same
resolver rather than restarting it.

Exact symbol names are implementation details, but the ABI must use fixed-width
integers, pointer-plus-length byte strings, and caller-owned output buffers.
No C struct layout containing pointers is exposed directly to JavaScript.

Every exported symbol is listed explicitly in the build's link line
(`-Wl,--export=...`) in both `Makefile` and `Makefile.kmk`; the C source does not
carry per-symbol `export_name` attributes, so the manifest stays the single
source of truth for the module's public surface.

Every returned handle belongs to one global module handle table and encodes a
table index plus a generation. Its table entry records the owning instance.
Zero is invalid. Freeing increments the slot generation; stale or foreign
handles return `HANDLE_INVALID`. Generation overflow retires the slot for the
remaining module lifetime.

File-backed builds and inspection commands load include fragments through the
host. The optional `kame_wasm_set_build_sources(handle, data, length)` export
accepts a copied JSON descriptor with `sources` entries containing `name`, `text`,
and authored byte `offset`, after initializing an empty source instance and
before target execution or preparation. The portable compiler combines these
fragments and maps diagnostic spans back to authored sources. No filesystem work
occurs during compilation. Registration failures expose their complete diagnostic
list through the target-event query, including in non-JSON presentation modes.

## Host Requests

Initial request kinds are:

- Read file.
- Write file.
- Stat path.
- Expand glob.
- Read environment variable.
- Run process-like operation.
- Read wall or monotonic time when needed.

A browser host may map these to an in-memory filesystem, server API, sandbox,
or reject them with a capability diagnostic. A process request is semantic, not
POSIX-specific: the host streams stdout/stderr chunks and terminal status back
using the same request ID.

Each host request includes the originating node, generation, and attempt.

Late completion for a cancelled or superseded generation is accepted by the
ABI and ignored by the engine.

## Events

Events use a stable versioned binary envelope containing:

- Schema version.
- Event kind.
- Root, node, and request handles as applicable.
- Generation and revision.
- Payload length.

Payloads initially use canonical JSON for structured values and raw bytes for
process/file chunks. The host queries required payload length, then copies into
its buffer. Querying pins that event until copy or explicit discard, so another
step cannot replace it. Completion input bytes are copied synchronously before
the completion call returns; JavaScript may then release its buffer. Payload
pointers into the WebAssembly heap are not retained across calls that may
allocate.

## Memory

The initial module uses a fixed-size arena allocated on first source compilation
and reused by that instance slot. The freestanding ABI reserves 16 MiB per
compiled slot on demand, rather than reserving memory for every possible slot.
This accommodates the default 1 MiB Kash capture plus evaluator allocation and
copy overhead. The module memory maximum still bounds simultaneous compiled
instances and larger captures; an unavailable arena reports `NO_MEMORY`.
Instance destruction releases all logical allocations by resetting that arena.

The POSIX JavaScript CLI host uses the system `mkfifo` utility for intermediate
Kash pipeline descriptors. Children inherit blocking FIFO endpoints directly,
preserving kernel backpressure, EOF, and SIGPIPE semantics; intermediate stdout
never passes through JavaScript capture buffers. The private FIFO paths are
unlinked before graph execution. An unavailable utility produces `HOST_FAIL`.
Node's socket-based subprocess `pipe` streams are not interchangeable here:
early-reader closure can produce ECONNRESET instead of native SIGPIPE.

File-rule shell requests use ABI kind 16: a JSON object with `script` and
`outputs`. The embedding host creates output parent directories before launch.
After successful process completion, or a recipe containing no shell/yield,
the engine emits ABI kind 17 for each declared output path. The host completes
that internal metadata request with an existence boolean. The engine reports
`OUTPUT_MISSING` and withholds dependent execution when an output is absent.
These checks do not evaluate a user library read or broaden expression grants.

Structured process requests distinguish direct argv (ABI kind 13, a JSON argv
array) from pipelines (kind 14, a JSON array of stage argv arrays) and redirected
configured graphs (kind 15, a JSON object with `stages`, `input`, `output`,
`append`, and a parallel `setup` array). Each setup record carries `cwd`,
`timeoutMS`, and `environment` (`KEY=value` overrides). Stages inherit the
invocation environment and limits; their own timeout terminates the whole graph
but ceases when that stage exits. PATH lookup uses the effective stage environment
and cwd. Setup does not broaden caller grants.
Redirection paths are authorized before opening; input opens before output so a
missing source cannot truncate the destination. Redirected stdout is neither
captured nor UTF-8 decoded. A pipeline is
one owned host request: all stages are validated before launch, intermediate
streams are connected directly, every stage's stderr is live, and only final
stdout is captured. Aggregate failure selects the rightmost failing stage;
signal exits contribute `128 + signal`. Exit completions carry aggregate status,
signal, and per-stage status/signal/outcome records. A successful capture may
complete directly as UTF-8 text to avoid escaping large strings through JSON.
Launch failures, timeout, cancellation, and capture overflow terminate and reap
all remaining stages before completing the request.
Individual `free` operations still release owned engine objects where supported
so native and WebAssembly behavior remain comparable.

Each instance reserves a static emergency diagnostic slot at creation.
Out-of-memory returns allocation-free `NO_MEMORY` through that slot. The module
must not grow memory implicitly unless the embedding contract explicitly
enables it.

## JavaScript CLI Wrapper

### Role

`dist/kame.js` is the canonical JavaScript entry point to the freestanding
module and the WebAssembly counterpart of the native CLI in `009-cli.md`. Its
source is `src/js/kame.js`; packaging copies it unchanged. It is one ESM file
that requires only Node 18 or later and an adjacent `kame.wasm`, with no
third-party dependencies.

```text
node dist/kame.js [OPTIONS] [TARGET...]
node dist/kame.js do COMMAND [OPTIONS] [ARG...]
```

### CLI Contract

The wrapper reproduces the invocation model of `009-cli.md`: the same options
and commands, the same stream separation (normal output and JSON on stdout,
progress and human diagnostics on stderr), and the same exit statuses (0 for
success, 1 for parse, evaluation, build, or cache failure, 2 for usage errors,
and 128 plus the signal number for forced termination). `--json` emits JSON
Lines with `schema: 1`. `-V` and `--version` print `kame VERSION`; the launcher
may answer that request without loading the module (`015-distribution.md`).

For ABI conformance testing, the wrapper also accepts `--wasm-abi-info` and
`--wasm-self-test`, which print ABI exports and self-test results as JSON.
These are diagnostic flags, not part of the `009-cli.md` surface.

### Host Mapping

The wrapper services host requests with JavaScript host capabilities:

- read, stat, glob, and write map to the Node filesystem, rooted by grants.
- env maps to `process.env` through the authorized `env` operation.
- run maps to a child process with streamed stdout and stderr, exit status,
  signal forwarding, and cancellation.
- time maps to the JavaScript wall and monotonic clocks.
- cache get, put, and delete map to opaque records under a per-project cache
  root; the runtime owns key construction and record validation.

Capability grants follow `009-cli.md`: sessions beginning with value/expression
fragments deny host capabilities by default; sessions beginning with rule/Kash
fragments use the documented cwd-scoped
read/write and run defaults. Explicit grants use `--allow-read`, `--allow-write`,
`--allow-run`, and `--allow-env`. Direct file execution and `do run` must normalize
to the same invocation and authority policy as on native hosts. Later fragments
inherit that policy without broadening it through language selection. A browser or
embedded wrapper instead supplies its own host, as described in Host Requests.

Invocation-owned asynchronous process graphs use independent engine roots, not
detached daemons. A host copies each request and calls `kame_wasm_request_detach`
to release its query pin while servicing other nodes in the same instance.
Completions retain their request handles; request-aware process stream/start
exports correlate events without relying on whichever request is currently
pinned. `kame_wasm_request_attach` selects a detached request for the legacy
terminal-event export. Foreign-instance handles remain invalid. Recompilation
while detached requests are outstanding is rejected.

The JavaScript runner services outstanding requests concurrently, executes a
final portable invocation join, and cancels/reaps every owned process group on
failure or interruption before releasing the instance. A later fragment can
await a shared process handle, but handles cannot escape into another invocation.

### Staged Coverage

The ABI and host services arrive in stages, and the wrapper reports coverage
honestly rather than faking effects:

| Stage | ABI available | CLI coverage |
| --- | --- | --- |
| 0 | pure evaluation, instance create/free, source compile | `--version`, `-h`/`--help`, `do help`, pure inline/stdin `do run --lang expr`, and pure parse and format of stdin |
| 1 | step, event, completion, and read/stat/glob/write host requests | source discovery and inspection: `do plan`, `do inputs`, `do outputs`, `do span`, and `do cat` for values and files |
| 2 | process host requests | primary materialization, recipe execution, retries, timeouts, and cancellation |
| 3 | full ABI, including cache | full `009-cli.md` parity, including `--json` event streams |

An invocation whose selected work requires a capability the current stage does
not provide fails with `FEATURE_UNSUP`. The wrapper does not skip or approximate
the effect.

### Wrapper Rules

The wrapper must:

- Serialize calls into one instance.
- Copy bytes before a subsequent allocating ABI call.
- Dispatch host requests by capability.
- Feed every completion or cancellation back exactly once.
- Free the instance and detach pending callbacks on disposal.

The wrapper's stream exposes current-plus-future updates, not full replay.

## Acceptance Tests

- Portable packages translate and link for `wasm32-freestanding`.
- An expression using only pure operations evaluates without host requests.
- Template and script parse/format results match native fixtures byte-for-byte.
- An in-memory host services read, stat, glob, and write requests.
- A simulated process host streams stdout and stderr before completion.
- Cancelling a request causes a late completion to be ignored safely.
- Fingerprint encoding matches native output.
- A queried event remains stable until copied or discarded.
- Completion buffers may be overwritten immediately after the ABI call returns.
- Repeated create, compile, evaluate, and free cycles exhaust no logical arena
  capacity.
- An undersized heap produces a diagnostic rather than memory corruption.
- The JavaScript wrapper copies payloads before further allocation and rejects
  use after disposal.
- `node dist/kame.js --version` prints `kame VERSION` and exits 0.
- `do run --lang expr` output matches native output byte-for-byte for pure expressions.
- Direct `.km`/`.kmk`/`.kash`/`.ksh` execution and `do run` share native dispatch,
  ordered multi-source compilation, shared scope/engine, first-fragment policy,
  selected-work, argument, grant, and stream semantics. A later parse failure
  prevents earlier effects; fragment boundaries retain authored source spans.
  Missing backend support
  reports `FEATURE_UNSUP`; removed command names are not wrapper-only aliases.
- `--json` output is valid JSON Lines on stdout and leaves stderr unused.
- Exit statuses and stream separation match `009-cli.md`.
- An invocation that needs a capability absent from the current stage reports
  `FEATURE_UNSUP`.
- `do tools check TARGETS...` resolves host paths, forwards read-only filesystem
  and environment requests, and reports missing tools without executing a recipe.
- `--wasm-abi-info` and `--wasm-self-test` report the current stage's ABI.

Build source descriptors are bounded to 512 KiB of encoded JSON and 64 KiB
of combined source text, matching the single-source text capacity. Oversize
input returns `NO_MEMORY`; offsets must fit a nonnegative 32-bit span.

Expanded span queries service read-only host requests through the same pending
query protocol as tool inspection: `HOST_NEEDED` means step, complete the yielded
request, and retry the query. Static queries perform no host effects.

Forwarded build effects publish explicit writes and concatenated yields through
write requests. The target waits for host completion, preserves authored effect
order, and propagates publication failures. A dependent recipe runs after the
file is published. The JavaScript host writes through a unique private sibling
staging directory and atomic rename, cleaning staging on success and failure.

Tool resolution uses ABI request kind 18 with an executable name or selected
path. The host completes it with the resolved path as a JSON string or a
`TOOL_MISSING` diagnostic. Kind 19 checks existence of an internally declared
executable dependency and completes with a JSON boolean. These requests do not
execute a process; user filesystem reads retain their ordinary capability checks.
Build/source descriptors carry `toolOverrides` as `NAME=PATH` strings. The JS
host shares a resolution cache across the invocation and also supplies paths for
literal declarations through `kame_wasm_set_tool_path`.
