# Cached Tasks and Fingerprints

## Purpose

Cached tasks skip successful repeated work when their implementation and every
observed input remain equivalent. Caching is a runtime feature, not a generic
virtual filesystem.

File rules continue to use the freshness contract in `006-runtime.md`.

## Cache Key

One cached task instance is identified by:

- Canonical task target.
- Selected template captures.
- Selected rule declaration fingerprint.

The local backend encodes this identity into a safe path below:

```text
.kame/cache/tasks/
```

The portable in-memory host implements the same file operations and atomic
replacement contract with ephemeral memory. It uses the same portable key and
record formats, so cache identity and hit validation do not depend on storage.
Its records last only as long as the memory host instance.

`kame do cache list` prints a JSON array of managed regular files across the
task, host, and file-context cache directories. Each entry has `backend`,
`key`, and `bytes` fields. `kame do cache clean` removes regular record files
from those directories. Both commands accept `-C DIR` or `--directory DIR` to
select the project root; symlinks and subdirectories are left untouched.

User target text is not used directly as an unchecked filesystem path.

## Fingerprint

A task fingerprint is a canonical binary encoding of:

- Cache format version.
- Canonical task identity and captures.
- Canonically formatted rule header and body AST.
- Resolved declared content-input resource keys and their current fingerprints.
- Dynamic file, glob, resource, and environment dependencies from the
  accepted render generation.
- Resolved executable files referenced with `@(x/NAME)`.
- Stable versions of invoked operations.
- Shell path, working directory, and explicit execution options affecting the
  result.

Tasks and file rules use the engine's accepted `SignatureRecord`, encoded with
the `KSR2` header, implementation signature, a pre-render validity guard,
input/output observation sets, and
a trailing SHA-256 checksum. The surrounding task record retains replayable
stdout/stderr and timing. Each observation names a canonical resource, its
observed aspect, and a typed signature. Duplicate, unavailable, corrupt, or
truncated observations cannot prove reuse.

The guard covers authored source and invocation context. A matching guard permits
validation of recorded resource leaves without reevaluating derived definitions
or rendering recipes. A missing or changed guard falls back to ordinary evaluation;
it is not part of semantic result equality. Unverifiable host computations cannot
use the pre-render shortcut. Older record formats are treated as misses.

Canonical value encoding uses a one-byte tag followed by payload:

| Tag | Value | Payload |
| --- | --- | --- |
| 0 | nil | none |
| 1, 2 | false, true | none |
| 3 | integer | signed 64-bit little-endian |
| 4 | float | IEEE-754 binary64 little-endian |
| 5 | string | uint64 byte length, then UTF-8 bytes |
| 6 | bytes | uint64 length, then bytes |
| 7 | list | uint64 count, then encoded values |
| 8 | record | uint64 count, then sorted string/value pairs |
| 9 | resource reference | kind string followed by canonical-name string |

NaN values use one canonical quiet-NaN bit pattern and negative zero is encoded
as positive zero. Value record fields are sorted by raw UTF-8 key bytes.
No fingerprint depends on pointers, map iteration order, build timestamps, or
formatter whitespace.

The digest is SHA-256 with published standard test vectors. This hash is for
change detection rather than authentication.

Order-only prerequisites still materialize before lookup, but their content,
values and task outcomes do not enter the input or dynamic content sections.
An order-only bare task does not by itself disable caching. Normal occurrences
or explicit expression reads upgrade an edge to a content dependency. The
selected authored rule declaration remains part of the fingerprint, including
its prerequisite syntax.

## Dependency Fingerprints

- File dependency: canonical path and SHA-256 content digest. Kind and size may
  accompany the digest. Modification time is not part of the identity.
- Missing path dependency: canonical path and explicit missing marker.

Content observations follow the host's ordinary read semantics, including
symlinks resolving to readable regular files. Resolved bytes determine identity.
Metadata alone never proves content equality. Non-regular or unreadable content
has an unavailable signature, not the missing marker. An existence-only read
does not consume content; intentional metadata reads have their own aspect.
- Glob dependency: pattern plus sorted matched path membership. Matched file
  contents become dependencies only when explicitly required or read.
- Definition dependency: canonical encoded current value and its dependency
  fingerprints.
- Cached task dependency: successful record fingerprint.
- Environment dependency: a name that rendering or execution actually read, plus
  a present/missing marker and the value read. Unread names are not dependencies.
- Recipe execution: interpreter identity, rendered script, working directory,
  recorded environment dependencies, timeout, retry settings, and invoked
  operation versions. The process environment is not fingerprinted wholesale.

An ordinary bare task is never a cacheable dependency. A cached task depending
on one is therefore always stale.

Engine hashing is incremental; current host content transport buffers a whole
file. Accepted records are limited to 16 MiB of encoded observations. Exceeding
that limit makes the task uncacheable for that run and emits `CACHE_UNUSABLE` as
a warning; execution itself may continue.

Records are published after execution, so they include execution-time reads.
Reuse restores discovered resource edges and validates through the actual host,
not a forwarding runtime's in-memory filesystem. The cache miss lock remains
held through validation and successful publication. Logs are replayed only after
the accepted signature record matches.
Restoration is speculative: a failed old dependency forces a cache miss rather
than failing the new invocation. On a miss, restored edges and observations are
discarded before execution; the next record contains only the new branch's reads.
Persisted definition observations use authored names, not environment-snapshot
graph identities. Validation rebinds each name to the current invocation's
environment and explicit overrides, then consumes its published value signature.
Unread ambient changes therefore do not invalidate otherwise equal definitions.

## Records

A successful record contains:

- Cache and schema versions.
- Task identity.
- Complete fingerprint.
- Start and completion wall times.
- Duration.
- Exit status.
- Bounded stdout and stderr bytes with truncation flags.
- Dependency manifest sufficient to recompute the fingerprint.
- Optional structured provenance for diagnostics.

A host that cannot expose a synchronous wall clock records zero start,
completion, and duration times. Zero timing is still a valid record: ordering
and a zero exit status are required, but the absolute times are informational.

A failed attempt may store diagnostic metadata for inspection, but it is never
a cache hit. Cancellation does not create or replace a successful record.

## Lookup

Before running a cached task, the runtime:

1. Reads and validates its record.
2. Recomputes each recorded dependency fingerprint.
3. Computes the candidate task fingerprint.
4. Hits only when schema, identity, and complete fingerprint match.

Malformed, unsupported, or partially written records are misses. They produce a
warning only in verbose mode and may be replaced after a successful run.

A cache hit emits cached status and replays retained stdout/stderr through
runtime events. Replayed logs are identified as cached and retain per-stream
order.

## Commit

Only successful completed execution commits a record. Commit writes a complete
temporary file in the cache directory, syncs it as supported, and atomically
renames it over the previous record. A crash must leave either the old valid
record or no valid record, never a valid-looking partial record.

Concurrent production of one task is deduplicated by the engine. Native
processes acquire one of 256 advisory lock stripes derived from the cache key
before reading a task record and retain it until the target completes. This
second read under the lock prevents duplicate cold execution across processes.
POSIX process exit releases held advisory locks even after a crash. Stripe
collisions only serialize unrelated keys; lock files are fixed in count.

Forwarded JavaScript hosts acquire a per-key lock directory under
`.kame/cache/locks/` before the cache read and keep it through execution and
atomic publication. The owner PID allows a later host to reclaim a lock left
by an exited process. Hits, failures, and host interruption release the lock;
lock waiters can be cancelled with the invocation.

## Invalidations

These changes must cause a miss:

- Rule header or body AST.
- Capture values.
- Declared dependency content or identity.
- Dynamic dependency set or content.
- Invoked operation version.
- Explicit shell, cwd, timeout, or retry settings.
- A recorded environment dependency's name or value. An unread inherited value
  does not invalidate. Selecting a shell does not snapshot the process
  environment.

The process host has no implicit ambient environment. Kame display options
do not participate. File and environment identity follow `006-runtime.md`.

## Limits

Default retained stdout and stderr are each limited to 64 KiB by bytes. The
runtime may configure a different positive limit. Each local cache backend
retains at most 1024 regular records. A successful publication prunes the
oldest records by modification time; equal timestamps are ordered by key bytes.
Symlinks, directories, and atomic-write staging files are not eviction
candidates. On the JavaScript host, forwarded task and file-context records
share the host backend's limit.

## Acceptance Tests

- Running one unchanged cached task twice executes once and reports one hit.
- Two template capture values create distinct cache identities.
- Rule body, declared file content, glob membership, environment dependency,
  operation version, and shell changes each invalidate a record.
- A modification-time change that leaves the bytes alone does not invalidate.
  A content change invalidates even when the modification time is unchanged.
- A bare task dependency prevents a cache hit.
- A failed task reruns and does not replace an earlier successful record for a
  different fingerprint.
- `kame do cache list` reports managed records consistently on native and WASM.
- `kame do cache clean` removes regular records and preserves symlinks.
- Publishing above the backend limit evicts oldest regular records on native
  and WASM while preserving the newest record and symlinks.
- Cancellation leaves the previous record intact.
- Cached stdout and stderr replay separately with truncation flags.
- A truncated or malformed record is a miss and never crashes the runtime.
- Concurrent atomic writers leave a complete readable record.
- Separate native processes execute one cold cache miss once and release the
  lock after publication.
- Separate forwarded Node hosts execute one cold WASM cache miss once and
  release the lock after publication.
- Failed and interrupted forwarded recipes release their locks; a cancelled
  waiter leaves the owner undisturbed; an exited owner is reclaimed before
  lookup resumes.
- The portable in-memory host publishes complete records and reuses them with
  the same task identity and hit validation as the local filesystem backend.
- Fingerprint encoding is identical across two native runs and matches checked-in
  hexadecimal vectors for every value tag and one complete task record.
- SHA-256 tests use standard empty, short-string, and multi-block vectors.
