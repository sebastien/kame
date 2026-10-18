# Freestanding WebAssembly Host

## Purpose

The WebAssembly target embeds the portable engine and languages in browser or
server JavaScript. JavaScript supplies host capabilities; the module does not
pretend that browsers provide POSIX files or processes.

## Build Boundary

The WebAssembly module targets `wasm32-freestanding`. It includes:

- Core values and engine.
- Template, expression, and rule parsers and formatters.
- Program compilation and evaluation.
- Pure collection, text, and path operations.
- Host-request operation stubs.
- Shared portable CLI invocation parsing and command policy (`kame/cli`).

It excludes POSIX, the native CLI executable/host implementation (`cmd/kame`),
`os`, `conc`, `sync`, native process code, and direct filesystem access. The
shared CLI package is included and audited alongside `program` for portability.

Translation and link tests must fail if a portable package acquires a hosted
transitive import.

Both build files compile the module with `-Oz` for size and `-fwrapv` to preserve
the native Solod build's signed-overflow semantics. The freestanding link uses
only needed arithmetic helpers from the SDK's compiler-rt builtins archive,
discovered with its configured Clang's `-print-libgcc-file-name`. Query the SDK's
default target: the `wasm32-unknown-unknown` runtime path need not exist. Linking
these helpers must not add WASI libc or any host imports.

## Execution Model

JavaScript drives the engine synchronously through explicit calls:

1. Create an instance with a fixed or host-provided heap.
2. Compile source.
3. Request one or more targets or expressions, or retain watch roots.
4. Call `step` until the engine emits an event or needs host work.
5. Perform host work asynchronously in JavaScript.
6. Feed completion data back with request ID and generation.
7. Continue stepping until roots complete.

The module creates no hidden thread and does not block awaiting JavaScript.
Watch roots remain retained after settling. The JavaScript host polls external
resources, sends invalidations, and reloads the program when a build source
changes; the freestanding module performs no filesystem watching.

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
- Retained watch roots, resource snapshots, batch invalidation and cancellation.

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

The additive `kame_wasm_tools_check_report` query has the same inspection and
resumption policy, and includes a `tools-check-result` record with required tools
and their availability after the diagnostics. The CLI uses this report query;
the original diagnostic-only `kame_wasm_tools_check` contract remains unchanged.

Exact symbol names are implementation details, but the ABI must use fixed-width
integers, pointer-plus-length byte strings, and caller-owned output buffers.
No C struct layout containing pointers is exposed directly to JavaScript.

Core exports and the declaration predicate query are listed explicitly in the
build link lines (`-Wl,--export=...`) in both `Makefile` and `Makefile.kmk`.
Additional session, process-streaming, and heap-control extensions carry
`export_name` attributes in the C ABI source.

Source-composition hosts call `kame_wasm_composition_condition(handle,
data, length, dst, capacity, out_length)`. Its JSON descriptor accepts optional
`name` and `prefix` strings, a required `predicate` string, and optional
`defines`/`environment` arrays of strings. The prefix contains only already
selected definition declarations, extracted using each source's own portable
parser. Selection follows ordinary `if` truth: only `:nil` and `:false` select
the false branch. The result is UTF-8 `true` or `false`; ordinary query buffer statuses,
instance diagnostics, and allocation-exhaustion recovery apply. The query never
requests host work or acquires runtime grants. Hosts retain authored predicate
spans for diagnostic rendering, select branches and includes in source order,
and supply only selected fragments to compilation. Direct registration of
uncomposed composition forms returns `FEATURE_UNSUP`. The older
`kame_wasm_declaration_predicate` query retains its boolean-only contract for
embedding compatibility; it does not restore the removed `when` source syntax.
Condition failures preserve structured diagnostic fields, including tips, in
the instance diagnostic JSON envelope. Reports are owned by the instance until
its next query replacing the diagnostic or destruction and are bounded at the
65,536-byte source capacity. An oversized composition report returns `NO_MEMORY`
explicitly instead of silently discarding structured fields.
Configuration frame kinds and labels are preserved, but frame locations are
omitted: the definitions-only prefix and isolated predicate do not carry an
authored origin map. Hosts locate the primary failure at the authored condition.

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
occurs during compilation. Build and runner session descriptors carry `force`
as 0 or 1, and `timeoutMS` and `retryCount` as nonnegative integers, into portable runtime options; `--force` bypasses file freshness and
cached-task lookup on either host. Registration failures expose their complete diagnostic
list through the target-event query, including in non-JSON presentation modes.

Build source entries and session fragments may carry an outer-to-inner `includes`
list. Each entry contains nonempty `source` and `label` strings (the including
source and included source), plus authored byte `start` and `end` offsets with
`0 <= start <= end <= 2147483647`. The host supplies this provenance; the compiler
never opens includes to reconstruct it. Invalid fields fail `PARSE_ERR` before
registration. Compilation errors map primary and related locations separately
and retain both inclusion paths, including distinct sites loading the same file.

## Host Requests

Host request kinds include:

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

Cached-task hosts also receive ABI kinds 24 and 25 to acquire and release an
opaque cache-key lock. The host completes kind 24 only after exclusive
ownership is established. Kind 25 is idempotent so cancellation can release a
pending or acquired lease. Cache get occurs after lock acquisition; cache put
completes before the runtime releases the lock.

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

Payloads use canonical JSON for structured values and raw bytes for
process/file chunks. The host queries required payload length, then copies into
its buffer. Querying pins that event until copy or explicit discard, so another
step cannot replace it. Completion input bytes are copied synchronously before
the completion call returns; JavaScript may then release its buffer. Payload
pointers into the WebAssembly heap are not retained across calls that may
allocate.

## Memory

The module uses a fixed-size backing buffer allocated on first source compilation
and reused by that instance slot. The freestanding ABI reserves 16 MiB per
compiled slot on demand, rather than reserving memory for every possible slot.
This accommodates the default 1 MiB Kash capture plus evaluator allocation and
copy overhead. The module memory maximum still bounds simultaneous compiled
instances and larger captures; an unavailable buffer reports `NO_MEMORY`.
The default linear-memory stack is 512 KiB so optimized evaluator frames can
reach the 32-level evaluation guard and return `EVAL_DEPTH` instead of trapping.
Within each slot, a coalescing heap reclaims arbitrary freed blocks while the
compiled program remains alive; instance destruction resets the supplied buffer.
The JS host reuses separate geometrically grown scratch buffers for process
chunks and event JSON. `kame_wasm_process_retain_limit(instance, request)` reports
the portable process retention budget (zero request selects the pinned request;
`UINT32_MAX` means invalid). Recipe hosts retain that prefix plus at most one
sentinel byte, preserving terminal truncation metadata while forwarding every
live chunk. Binary event JSON scratch uses the instance allocator as well.

The POSIX JavaScript CLI host uses the system `mkfifo` utility for intermediate
Kash pipeline descriptors. Children inherit blocking FIFO endpoints directly,
preserving kernel backpressure, EOF, and SIGPIPE semantics; intermediate stdout
never passes through JavaScript capture buffers. The private FIFO paths are
unlinked before graph execution. An unavailable utility produces `HOST_FAIL`.
Node's socket-based subprocess `pipe` streams are not interchangeable here:
early-reader closure can produce ECONNRESET instead of native SIGPIPE.

File-rule shell requests use ABI kind 16: a JSON object with `script` and
`outputs`, with optional `environment` string assignments and optional `shell`
argv. A supplied shell argv must be nonempty, contain only strings without NUL,
and have a nonempty executable. Remaining arguments, including empty strings,
are preserved before appending the rendered recipe text. The embedding host
creates output parent directories before launch. Existing recipe descriptors
without a shell field retain the invocation shell policy. A present `environment`
is an exact snapshot, including an empty array; an omitted field retains the host
invocation environment. Collected shell calls bound to a target use the same kind
16 descriptor with empty `outputs` and `capture: true`. The marker keeps the
request as a captured shell-operation result, distinct from a recipe even though
both carry an exact target environment.
Kash file recipes request output-parent preparation separately through ABI kind
21: the same recipe descriptor with an empty `script`. The host validates
`outputs`, creates their parent directories, and completes with nil before any
structured process runs. An existing parent must be a directory.

File-rule freshness and successful-output verification use ABI kind 20
(`file-times`): a JSON array of canonical paths. The host completes it with a
JSON array in the same order, containing decimal nanosecond modification times
as strings, or null for absent paths. Strings preserve 64-bit timestamps across
JavaScript transport; portable hosts may supply integer values directly.
Freshness also requests the opaque file-context cache record through the existing
cache-get protocol. A missing record is nil. Scoped successful builds publish
updated records through cache-put.

After successful process completion, or a recipe containing no shell/yield,
the engine verifies every declared output through a separate kind-20 request.
It reports `OUTPUT_MISSING` and withholds dependent execution when an output is
absent; a host verification failure remains a failure. Kind 17 is reserved and
is not the output-verification request. Embedding hosts must service
kind 20 and cache requests when forwarding file rules. These metadata checks do
not evaluate a user library read or broaden expression grants.

External file observations use ABI kind 27 (`file-content`), with a canonical
path payload. Complete with bytes for a readable regular file, nil for a missing
path, false for a present non-regular path, or true for a present regular file
whose bytes could not be read. A stat failure other than absence is a diagnostic,
except kind 27 may return false when no-follow inspection proves a symbolic link
whose target cannot be followed. Its link views remain inspectable; this does not
supply a reusable content signature.
The engine hashes the transported bytes; neither boolean state supplies a
reusable signature. Kind 29 (`output-content`) uses the same protocol for declared
build outputs, under internal build authority rather than expression read grants.
Kind 28 (`tool-content`) uses the same protocol for resolved
executable dependencies, without requiring an additional user read grant.
Embedding hosts must service these requests as well as user file reads.

No-follow user filesystem requests are ABI kinds 30 (`lstat`) and 31 (`readlink`),
using a canonical path payload and read capability checks. Kind 30 completes with
the metadata record defined in `007-library.md`, including `link` and `regular`; kind 31 with
the exact validated UTF-8 target string. Failures complete with `FS_ERR`, never
nil, truncated targets, or replacement-decoded text. Memory resources have no
symlinks. Parent components still follow filesystem resolution.

The portable `FileSystem` and flattened `ProgramHost` interfaces include
`Readlink(allocator, name) LinkResult`: successful target strings are owned by
the caller's allocator and `Failed` reports unavailable/invalid targets. Native
hosts retain a `Symlink` flag in no-follow `FileInfo`. Embedding hosts must add
handlers for the two new wire kinds; the operation names are not serialized as
generic file reads.

Accepted file observations distinguish followed metadata, no-follow metadata,
and target text. A watch resource's `metadata` flag covers all three views and
can become true after a suspended host request completes. Existing content-only
watches must then be promoted rather than retaining their initial flag. A
metadata watch must detect retargeting even when both referents are absent.

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

Explicit process topology uses ABI request kind **32**, not the linear pipeline
kinds above. Its JSON descriptor adds parallel `kinds` (empty for executable
nodes, `merge`, `broadcast`, `frame`, `encode`, `filter`, or `copy` for native nodes) and `edges` (zero-based `[from,
to]` pairs). Native nodes have empty argv arrays. Each `setup` may also carry
`input`, `output`, and `append` file redirections. Hosts validate cardinalities,
acyclicity, grants, and all stage configurations before launch or truncation.
They join every node, retain the causal failure when cancelling peers, and keep
terminal presentation outputs distinct from channel edges. Unsupported hosts
must reject kind 32 rather than silently execute the nodes as a linear pipeline.

The optional parallel Boolean `framed` array declares each node's output kind
(omitting it means all-byte topology). Hosts validate input/output framing as
well as cardinality. Framed pipes carry a private uint32 little-endian length
followed by 1–65536 exact payload bytes; empty input emits no records. Framed
merges never interleave envelopes. Executables receive decoded payloads only.

`kame_wasm_stream_item(instance, request, node, data, length)` synchronously
evaluates the live request's authored predicate in the shared evaluator; payload
is data, never source. `node = -1` only validates UTF-8. Return values are 0/1 or
negative reserved operator statuses: 120 encoding, 121 record limit, 122 forbidden
effect, 123 non-Boolean result, 124 predicate failure. Hosts report positive
statuses in that native node's result, not as executable exit reinterpretation.
Calls accept active or parked request handles belonging to the same instance;
stale/foreign handles fail without changing the active request. The call cannot
submit host work or suspend. A missing export means framed execution is unsupported.

Live stream operands use request kind **33** with the same topology descriptor.
Edges may now be `[from, to, argument]`, where a positive `argument` is the actual
zero-based argv index (never index 0). Ordinary `[from, to]` edges still feed
stdin. Hosts enforce one incoming stdin edge, unique argument slots, one reader
per source output, valid slot bounds, and byte encoding at executable boundaries.
The evaluator resolves authored slots after list splicing; descriptor argv values
at injected slots are placeholders, not usable filesystem paths. The host maps
them to live pipe paths only at launch. Node uses private FIFO paths with owned
POSIX `dd` relays and a real FIFO open rendezvous; native POSIX can use inherited
descriptors directly. No capture, trimming, expression
parsing, or temporary-file materialization is permitted. Kind 33 prevents older
hosts from silently treating an argument connection as stdin. Unsupported hosts
must report `KASH_STREAM_OPERAND_UNSUPPORTED` before launching/truncating anything.

Native `ProcessHost` / `ProgramHost` implementations expose
`SupportsStreamOperands() bool`; return false unless live pipe-backed operands
are validated for that backend. `ProcessEdge.Argument` uses the same zero/stdin,
positive/argv convention. Static parse inspection instead reports the one-based
authored AST item as `argumentItem`, because inspection cannot expand argv values.

Configured working buffers use request kind **34**, including when a graph also
has injected arguments. It adds `buffers`, one integer per stage: zero selects
the bounded default, and 1..65536 is the per-input payload budget. Only native
`merge`, `broadcast`, and `balance` nodes accept nonzero budgets. A framed payload
larger than its configured budget fails with native operator status 126,
classified as `KASH_BUFFER_LIMIT`; the four-byte envelope does not count against
the budget. Kind 34 prevents an older host from silently ignoring a bound.

`balance` is a framed 1:N node (N >= 2). It routes each complete item to exactly
one output in edge order, round-robin, and blocks until that assignment is
delivered. It closes every output at EOF, including never-assigned outputs.
It neither starts per-record workers nor drops/skips records for a slow child.

Lossy framed broadcasts use request kind **35**, taking precedence over kinds
33/34 when argument inputs or explicit byte budgets are also present. They add
mandatory stage-aligned integer arrays `overflows` (0 block, 1 drop-new,
2 drop-old, 3 latest) and `bufferItems` (0 default, otherwise 1..1024). Nonzero
overflow is valid only on framed `broadcast`. Block requires zero pending-item
limit; latest accepts zero/default or one. The evaluator supplies effective
defaults (64 pending items, or one for latest). A byte budget bounds input
assembly and queued payload bytes per output; one additional immutable in-flight
record per output is protected until its write completes. Frame headers and
bounded queue metadata are extra. EOF flushes retained records. Destination
closure still fails the graph; dropping a record is never a failure by itself.
Hosts must reject missing/malformed policies before launching/truncating anything,
not silently downgrade them to a lossless or unbounded implementation.

Topology process completions may add `edgeMetrics`, an edge-aligned array of
seven-integer rows: `[measurement, bytes, items, droppedItems, queueHighWaterBytes,
queueHighWaterItems, saturated]`. Measurement 0 means unavailable, 1 means
completed operator writes, 2 means operator reads, and 3 means queued completed
operator writes with measured high-water marks. Items/drops are -1 on byte edges;
public graph Results expose them as nil. All counters are exact, nonnegative,
and at most 2^53-1; saturation sets the final field to 1. Frame headers are not
payload bytes. Partial writes are not counted as completed writes. Rows are
validated before publication, including queue limits and stream kinds.

The field is optional: older hosts and uninstrumented direct OS pipes remain
explicitly unknown. It does not require a new request kind or WASM export.
Statement/Result completions retain it through JSON; successful stdout captures
still complete as text. Native `ProcessEvent.EdgeMetrics` supplies the same
data through `ProcessMetricsPayload`. Graph Results add terminal node topology,
tracked executable paths, and per-node outcomes without rerunning predicates.

Optional `kame_wasm_process_snapshot(handle, request, data, length)` accepts a
canonical JSON object with `states` (one integer per prepared node: 0 unknown,
1 running, 2 waiting-input, 3 waiting-output, 4 completed, 5 failed, 6 cancelled,
7 timed-out) and optional `edgeMetrics` rows in the completion format above.
The export validates the owned active/parked process request, memory range, 1 MiB
wire cap, node/edge counts, state range, and counter shape/ranges. It emits a
coalesced `process-snapshot` event, never completes a request. Events carry
`graph: {terminal:false,nodes:[{id,kind,program,state}],edges:[{id,from,to,kind,argvIndex,measurement,...}]}` with
prepared IDs scoped by request and node/generation/attempt identity. Standalone
expression evaluations have no target event stream and ignore progress.

The Node host uses the optional export only when available, samples every 100 ms
plus launch/final samples, reuses bounded WASM scratch space, and cancels its timer
before completion. Missing support does not downgrade execution semantics.
Live fields are independent observations, not a cross-field transaction. Final
Result counters retain the stronger post-completion contract.

Process request kind **36** requires explicit merge-order support. Its descriptor
adds a node-aligned `orders` integer array (0 ready, 1 strict framed round-robin)
and always includes `buffers`, `overflows`, and `bufferItems`, including defaults.
Only merge nodes may use order 1, and their inputs must be framed. Hosts reject
missing/malformed policy arrays before launch or redirection truncation; older
hosts must reject the unknown request kind rather than silently reorder output.
Kinds 32–35 retain their existing default ready-merge semantics.

Each instance reserves a static emergency diagnostic slot at creation.
Out-of-memory returns allocation-free `NO_MEMORY` through that slot. The module
must not grow memory implicitly unless the embedding contract explicitly
enables it.

`kame_wasm_instance_set_heap_limit(handle, bytes)` sets a logical allocator
budget from 1 byte through 16 MiB before compilation; the backing buffer size
remains unchanged. Allocator exhaustion invalidates the affected instance
runtime: evaluator calls and result copies return `NO_MEMORY`, and `step` returns
terminal state 2. Its emergency diagnostic remains readable and destruction
remains valid. Create a new instance to resume work; other instances survive.
Temporary host-free queries restore their module-heap allocation checkpoint on
exhaustion. Other panics and out-of-bounds accesses remain traps.

The freestanding module uses native WebAssembly exception handling and LLVM
WASM SjLj lowering to unwind allocator panics to an ABI checkpoint. It provides
its own small [compiler helper ABI](https://github.com/emscripten-core/emscripten/blob/main/system/lib/compiler-rt/emscripten_setjmp.c)
implementation and exception tag, without host imports. Embedders therefore
need WebAssembly exception-handling support.

The JavaScript host preserves the diagnostic when an ABI process publisher
fails. On POSIX it uses detached process groups and FIFO-backed pipeline
connections. On Windows it connects pipeline stages with Node streams and uses
`taskkill /T /F` to stop a child and its descendants; completion waits for the
tree-stop request and every direct child to close. Streaming callbacks
must not throw uncaught exceptions or retry an exhausted instance.

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

Host-service conformance tests use this production wrapper, not a second test
host implementation. `tools/kame-wasm.mjs` is limited to raw ABI information and
self-tests (`--abi-info` and `--self-test`). Independent raw ABI tests retain
coverage of synchronous queries, event headers, memory growth, handles and
completion validation even when the production wrapper uses asynchronous calls.

### CLI Contract

The wrapper reproduces the invocation model of `009-cli.md`: the same options
and commands, the same stream separation (normal output and JSON on stdout,
progress and human diagnostics on stderr), and the same exit statuses (0 for
success, 1 for parse, evaluation, build, or cache failure, 2 for usage errors,
and 128 plus the signal number for forced termination). `--json` emits execution
JSON Lines with `schema: 1`; static inspection uses its command-specific framing.
`plan`, `inputs`, and `outputs` follow the recursive, read-only schema-2 contract
in `037-build-inspection.md`, including stages and explicit deferred discovery.
Host-backed resolution resumes through the inspection host protocol, never an
empty memory-filesystem substitute or execution of an artifact producer. Repeated
size/copy queries preserve identities and release inspection interest on failure
or cancellation. `-V` and `--version` print `kame VERSION (runtime=WASM)`; the launcher
may answer that request without loading the module (`015-distribution.md`).
Runtime spelling and its distinction from `buildMode` are defined by the
[canonical runtime table](009-cli.md#runtime-names). This short human format is
an intentional exception to the native four-field format; `--json --version`
includes `runtime: "WASM"` and `buildMode: "wasm"` without loading the module.
Its version and build ID identify the selected wrapper/module pair only when
local provenance matches both assets, following
[WASM identity reporting](015-distribution.md#wasm-identity-reporting). Hashing
module bytes for that check is not module instantiation. `KAME_WASM` changes the
selected module and therefore the bytes that must match; metadata for the
default adjacent module cannot identify a different override. Missing or
unusable metadata leaves build identity unknown without fetching, provisioning
or executing anything. This reporting does not change the WASM ABI.

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
  root; cache lock/unlock serialize misses through publication; the runtime
  owns key construction and record validation.

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

### Host Capability Requirements

The wrapper follows the full CLI contract in `009-cli.md`, including JSON event
streams, inspection, materialization, process control and caching. When a selected
host lacks a capability required by the invocation, it fails with `FEATURE_UNSUP`
rather than skipping, approximating or pretending to perform the effect.

### Wrapper Rules

The wrapper must:

- Serialize calls into one instance.
- Copy bytes before a subsequent allocating ABI call.
- Dispatch host requests by capability.
- Feed every completion or cancellation back exactly once.
- Finish bounded scoped finalization before freeing the instance and detaching
  pending callbacks on graceful disposal.

### Scoped finalization driver

[`finally!`](043-kame-finalization.md) requires an explicit graceful driver.
The additive `kame_wasm_finalization_tick(instance, now_ns, stopping)` entry
supplies monotonic nanoseconds and opts the driver into this contract. Tick
before stepping and while requests or public sinks are blocked. Stale clock
samples cannot move the clock backward or extend an established deadline.
Without that opt-in, reject `finally!` with `FEATURE_UNSUP` before either callback.

Set `stopping` to 1 for graceful disposal, then continue stepping, servicing
requests and draining effects/events while tick returns 1 (armed scopes remain).
Every withdrawn request must still be acknowledged after its owned host work is
quiescent/reaped, even though its evaluation result is stale. The finalization
clock and event driver must outlive those acknowledgements. `instance_free`
returns `STATE_INVALID` while scopes remain armed; fatal allocator exhaustion is
an execution-state-loss boundary, not a promise to run user cleanup.
Use `stopping` 2 when the established shutdown cause is an invocation timeout;
its primary diagnostic remains `RECIPE_TIMEOUT`, not `EXEC_CANCELLED`.

For a pinned request, `kame_wasm_finalization_budget(instance, 0)` returns 0 outside
cleanup, positive remaining milliseconds for runtime-designated cleanup, or -1
after its deadline. Copy this metadata before detaching. Only designated cleanup
requests may use a separate bounded cancellation signal after body cancellation;
keep their original grants, cwd, environment and host policy. A process started
by cleanup is protected from ordinary invocation signal forwarding, but not its
cleanup deadline. Body work and later fragments remain cancelled. No callback,
timer, blocked sink or repeated signal may restart the absolute cleanup deadline.
Passing 1 instead of 0 queries the earliest active cleanup deadline across all
scopes, allowing a backpressured public sink to yield back to the deadline driver.

After teardown, `kame_wasm_result_diagnostic(instance, dst, capacity, out_len)`
copies the terminal schema-1 diagnostic document, including `notes`, `related`,
frames and sanitized cause metadata. It is a non-consuming query: no result,
effect or event is retired, and no root is released. Empty bytes mean there is
no terminal diagnostic. Copy it before freeing the instance. JavaScript API
errors expose this diagnostic through their existing `diagnostics` array, also
when cancellation/disposal caused the initial rejection. Secondary cleanup
failures must not replace that rejection's cancellation control code.

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
- Repeated create, compile, evaluate, and free cycles exhaust no logical heap
  capacity.
- An undersized heap produces a diagnostic rather than memory corruption.
- The JavaScript wrapper copies payloads before further allocation and rejects
  use after disposal.
- `node dist/kame.js --version` prints `kame VERSION (runtime=WASM)` and exits 0.
- JSON version queries match provenance against the actual wrapper and selected
  module without instantiating it. Missing or invalid metadata and differing
  `KAME_WASM` bytes cannot inherit an unrelated release's identity.
- `do run --lang expr` output matches native output byte-for-byte for pure expressions.
- Direct `.km`/`.kmk`/`.kash`/`.ksh` execution and `do run` share native dispatch,
  ordered multi-source compilation, shared scope/engine, first-fragment policy,
  selected-work, argument, grant, and stream semantics. A later parse failure
  prevents earlier effects; fragment boundaries retain authored source spans.
  Missing backend support
  reports `FEATURE_UNSUP`; removed command names are not wrapper-only aliases.
- Execution `--json` output is valid JSON Lines on stdout and leaves stderr unused;
  static inspection follows its specified document framing, including schema-2
  build inspection parity in `037-build-inspection.md`.
- Exit statuses and stream separation match `009-cli.md`.
- An invocation that needs a capability absent from the current stage reports
  `FEATURE_UNSUP`.
- `do tools check TARGETS...` resolves host paths, forwards read-only filesystem
  and environment requests, and reports missing tools without executing a recipe.
- `--wasm-abi-info` and `--wasm-self-test` report the current stage's ABI.

Build source descriptors are bounded to 512 KiB of encoded JSON and 64 KiB
of combined source text, matching the single-source text capacity. Oversize
input returns `NO_MEMORY`; offsets must fit a nonnegative 32-bit span.

Build-inspection and expanded span queries service read-only host requests through
the same pending query protocol as tool inspection: `HOST_NEEDED` means step,
complete the yielded request, and retry the query. Read-only inspection never
starts artifact producers or commits effects. Parsing and unexpanded authored
span queries perform no host effects.

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
