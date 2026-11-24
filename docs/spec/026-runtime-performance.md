---
revision: 2
---

# Runtime performance and allocation contract

## Scope

This contract covers the parser and runtime costs listed here. Existing
owning APIs keep their ownership guarantees; faster APIs state when the caller
must retain storage. Optimizations preserve authored byte spans, lexical
shadowing, graph identity, glob ordering, diagnostics, and native/WASM results.

## Source ownership and parsing

`source.New` and `script.Parse` own copies of their inputs. Borrowing APIs may
retain the caller's name and text without copying. The caller keeps both strings
alive and immutable until the parsed script is freed. Parsing remains byte
oriented and does not decode or normalize source text; UTF-8 decoding is needed
only when mapping a byte span to display columns.

Acceptance compares the tracker allocation delta of owning and borrowing parse
paths for the same representative source. The borrowing path must allocate at
least the source-text byte length less while producing equivalent AST kinds,
text, diagnostics, and spans. Existing owning APIs must continue to work after
the caller releases its input.

## Lookup and traversal

Engine resource lookup and lexical scope lookup use indexes for repeated key
queries. Indexes confirm complete keys after hash matches, preserve last-binding
shadowing, and remain consistent when nodes or bindings are added and released.

Index resolved literal outputs by canonical resource identity for registration
and selection. Preserve authored output spans and template precedence. Measure
registration separately from parsing and from work reachable by the chosen root.

Glob expansion visits only directories compatible with the literal and
wildcard path segments. `**` retains recursive semantics, results remain
sorted and duplicate-free, and watch invalidation continues to use the complete
pattern membership. Acceptance records directory entries visited for a large
fixture with unrelated sibling trees and requires fewer visits than a full
walk, while returning byte-identical results.

## Pure evaluation

Synchronous host-free expressions complete without entering an engine wait or
poll loop. Expressions that need a host request return the existing
`HostNeeded`/request contract; capability failures and diagnostics remain
unchanged. Tests cover literals, local definitions, pure operations, lazy
host-backed values, and denied effects.

## File content signatures

Native hosts compute file and executable content digests with the platform
SHA-256, byte-identical to the portable `core.Digest`. Content signatures for
bytes already in memory also use the standard SHA-256 implementation. A
process-wide memo reuses a digest while the file's device, inode, size,
modification time and change time are unchanged. Files changed within two
seconds before hashing are not memoized, so a same-tick rewrite cannot keep
identical metadata and a stale digest. Signatures remain content signatures;
the memo only avoids re-reading.

Files of at least 256 KiB (typically executables) also persist their memo
entry in the user cache directory (`kame/digests` below `os.UserCacheDir`),
one fixed-size record per absolute path holding the same stat identity and
digest. A record is used only on an exact identity match and is replaced
atomically; any read, write or format failure falls back to hashing.
`KAME_DIGEST_CACHE=0` disables the store. It is a per-user optimization, not
project cache state, so `kame do cache clean` does not manage it.

## Measurements

The acceptance suite records tracker total allocations for ownership-sensitive
paths and deterministic workload elapsed times for source parsing, deep and
wide scope lookup, graph lookup over many resource keys, and wildcard traversal
with irrelevant directory trees. Memory and traversal-count assertions are
deterministic gates. Wall-clock measurements are reported as repeated medians
and are informational, not brittle pass/fail thresholds. Native and
portable-runtime results are recorded separately.

For retained builds, measure graph size independently from the size of the
affected branch. Report live heap above an empty engine, marginal edge storage,
first evaluation throughput, and edit-to-settled latency. A fixed three-node
change must publish the expected values without rerunning unrelated settled
producers. Measure host notification and debounce separately from engine work.
Steady-state scheduler work for that edit must not scan unrelated settled or
waiting nodes. Measure wide joins separately, where the affected set grows,
and time first dispatch in deep chains registered consumer-first.
File invalidation looks up accepted content observers by canonical path; a
speculative rollback, generation replacement or retirement drops obsolete facts.
Fixed-working-set and fresh-path watch cycles must discard detached external
resources; shrinking the working set after repeated peaks must restore the live
node, edge and watch counts. Sample collected heap outside timed updates; RSS
and WASM linear memory are additional footprint observations, not liveness proofs.

Prepared lexical scopes and resource indexes resolve repeated lookups without
tracked allocation. Allocation checks cover both wide and deep scopes and at
least 512 indexed resource lookups, preserving lexical shadowing and node identity.

Interactive entry tests require independent source storage and bounded completed
statement nodes, operation states, callable cleanup scopes, transient syntax and
queued work. They also verify that reclaiming entries preserves escaped
callables, deferred work and source diagnostics without replaying lazy effects.
`BenchmarkInteractiveEntries`
measures parse-and-execute cost in one retained session; it excludes the terminal
editor and prompt rendering. Retained declarations and genuinely live jobs are
separate from completed command history.

Repeated interactive rebinding of a fixed set of names must not grow the
binding table or parsed declaration history. Tests retain direct and nested
callable aliases, rebind while a call is suspended, and cancel a call whose
deferred cleanup still reads its old local definitions. These owners must keep
old code alive only for as long as it is needed.
Rejecting a transient callable as a statement result must also release its
captures, so a failed publication cannot pin a replaced function body.
`BenchmarkInteractiveRebinding` measures repeated `let` and function replacement
without retaining unused declaration history.

`BenchmarkInteractiveAsync` measures repeated process launches and collection.
Completed jobs must release task contexts, operation progress and launcher
statements rather than making subsequent launches scan session history. Stable
process IDs are not retained task-array indices. Retained handles own only
completion snapshots after collection; genuinely live consumers and unreported
failures remain valid owners. Cancellation tests must assert that nodes survive
until the host acknowledges physical reaping, then disappear after collection.
Native CLI tests cover failure observation, terminal job control and a PTY
response probe before and after 1,000 entries. The probe reports latency without
a machine-dependent timing threshold.
