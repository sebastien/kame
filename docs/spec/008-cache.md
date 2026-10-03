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

User target text is not used directly as an unchecked filesystem path.

## Fingerprint

A task fingerprint is a canonical binary encoding of:

- Cache format version.
- Canonical task identity and captures.
- Canonically formatted rule header and body AST.
- Resolved declared input resource keys and their current fingerprints.
- Dynamic file, glob, resource, and environment dependencies from the
  accepted render generation.
- Resolved executable files referenced with `@(x/NAME)`.
- Stable versions of invoked operations.
- Shell path, working directory, and explicit execution options affecting the
  result.

The canonical encoding begins with ASCII `LMKF` and byte version `1`. Each value
uses a one-byte tag followed by payload:

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
as positive zero. Fingerprint sections are records with fixed keys `format`,
`task`, `captures`, `rule`, `inputs`, `dynamic`, `operations`, and `execution`;
missing sections encode as nil. Record fields are sorted by raw UTF-8 key bytes.
No fingerprint depends on pointers, map iteration order, build timestamps, or
formatter whitespace.

The digest is SHA-256 with published standard test vectors. This hash is for
change detection rather than authentication.

## Dependency Fingerprints

- File dependency: canonical path, kind, size, modification time, and SHA-256
  content digest.
- Missing path dependency: canonical path and explicit missing marker.

Only a regular file is a cacheable present file. Its fingerprint uses `Lstat`,
so a symlink is not treated as the file it points to, and symlinks are not
followed. The present-file payload is the existing tagged 32-byte digest. The
digest's kind byte is `1` for a regular file, followed by size, modification
time, and an incremental content hash.

A missing path is `Lstat` returning not-found. After the canonical path, the
manifest stores one `0` byte and no content digest. A later appearance of that
path is a different fingerprint and invalidates the task. A dangling symlink is
not missing.

Directories, symlinks, fifos, sockets, devices, and any other non-regular type
are uncacheable. So is any stat, open, or read error other than not-found. The
task still executes, no successful record is written, and verbose mode emits
one `CACHE_UNUSABLE` warning. Those inputs never share the regular-file marker.
- Glob dependency: pattern plus sorted matched path and file fingerprints.
- Definition dependency: canonical encoded current value and its dependency
  fingerprints.
- Cached task dependency: successful record fingerprint.
- Environment dependency: variable name plus present/missing marker and value.
- Recipe execution: shell identity, script, working directory, complete process
  environment, timeout, retry settings, and invoked operation versions.

An ordinary bare task is never a cacheable dependency. A cached task depending
on one is therefore always stale.

Hashing is incremental and never requires loading a complete file into memory.
A dependency manifest is limited to 16 MiB of encoded entries. That cap applies
while constructing the identity, rule text, nested definition values, and the
final encoding; construction does not allocate past the cap and releases the
partial buffer on overflow. Exceeding that limit makes the task uncacheable for
that run and emits `CACHE_UNUSABLE` as a warning; execution itself may continue.

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

Concurrent production of one task is already deduplicated by the engine.
Cross-process cache locking is deferred. Concurrent Kame processes may
both execute a miss, but each atomic successful record remains readable.

## Invalidations

These changes must cause a miss:

- Rule header or body AST.
- Capture values.
- Declared dependency content or identity.
- Dynamic dependency set or content.
- Invoked operation version.
- Explicit shell, cwd, environment dependency, timeout, or retry settings.
- Any value in the complete process environment used by the recipe. This includes
  inherited and rule-local `; env` assignments, after overrides and canonical
  ordering.

The process host has no implicit ambient environment. Kame display options
do not participate.

## Limits

Default retained stdout and stderr are each limited to 64 KiB by bytes. The
runtime may configure a different positive limit. The cache has no eviction or
garbage collection in the initial implementation.

## Acceptance Tests

- Running one unchanged cached task twice executes once and reports one hit.
- Two template capture values create distinct cache identities.
- Rule body, declared file content, glob membership, environment dependency,
  operation version, and shell changes each invalidate a record.
- File mtime changes without content changes retain the same content digest but
  still recompute safely.
- A bare task dependency prevents a cache hit.
- A failed task reruns and does not replace an earlier successful record for a
  different fingerprint.
- Cancellation leaves the previous record intact.
- Cached stdout and stderr replay separately with truncation flags.
- A truncated or malformed record is a miss and never crashes the runtime.
- Concurrent atomic writers leave a complete readable record.
- Fingerprint encoding is identical across two native runs and matches checked-in
  hexadecimal vectors for every value tag and one complete task record.
- SHA-256 tests use standard empty, short-string, and multi-block vectors.
