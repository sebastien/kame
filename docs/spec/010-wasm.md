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

Exact symbol names are implementation details, but the ABI must use fixed-width
integers, pointer-plus-length byte strings, and caller-owned output buffers.
No C struct layout containing pointers is exposed directly to JavaScript.

Every returned handle belongs to one global module handle table and encodes a
table index plus a generation. Its table entry records the owning instance.
Zero is invalid. Freeing increments the slot generation; stale or foreign
handles return `HANDLE_INVALID`. Generation overflow retires the slot for the
remaining module lifetime.

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

The initial module uses a fixed-size arena supplied at instance creation.
Instance destruction releases all logical allocations by resetting that arena.
Individual `free` operations still release owned engine objects where supported
so native and WebAssembly behavior remain comparable.

Each instance reserves a static emergency diagnostic slot at creation.
Out-of-memory returns allocation-free `NO_MEMORY` through that slot. The module
must not grow memory implicitly unless the embedding contract explicitly
enables it.

## JavaScript Wrapper

A small wrapper may provide promises and async iterables as JavaScript
conveniences, but these are not engine semantics. The wrapper must:

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
