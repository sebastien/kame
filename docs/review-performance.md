# Performance review

Reviewed 2026-10-03 on Linux x86-64, Node v24.18.0, using the native debug build
and the freestanding WASM module through its JS CLI. All timings include process
startup, loading, compilation, evaluation/inspection, and teardown. Five samples
per case were checked for correct output. Other conformance tests were running,
so these observations are not stable release benchmarks or isolated CPU profiles.

## Verification scope

The measurements below are historical 2026-10-03 observations; they have not been
remeasured after the scoped-definition and streaming fixes. The current complete
leak gate passes 409 portable tests, two external engine tests, Go CLI ASAN tests
and 113 selected CLI suites. These establish runtime conformance and ownership
under the exercised workloads, not throughput or allocation benchmarks.
The normal gate also passes all 114 CLI suites; the dedicated WASM gate passes
all 60 suites and both import audits. [review-specs.md](review-specs.md) records
commands and release-artifact checks.

## Measured results

### Runtime workload measurements, 2026-10-06

Five checked CLI samples were collected for each workload on Linux x86-64 with
Node v24.18.0. Each sample includes process startup, source loading, execution,
and teardown. These workloads exercise parsing 512 definitions, lookup in one
wide scope with 1,000 bindings, lookup through 16 nested scopes, planning one
resource from a 512-task graph, and a selective wildcard over 128 sibling
directories. The harness checks the selected definition, value, plan target, or
match count before recording time.

| Workload | Native median (ms) | Native range (ms) | WASM median (ms) | WASM range (ms) |
| --- | ---: | ---: | ---: | ---: |
| Parse 512 definitions | 3.01 | 2.98–5.95 | 264.57 | 190.60–282.75 |
| Wide scope, 1,000 bindings | 13.58 | 12.83–15.12 | 418.06 | 355.14–548.27 |
| Deep scope, 16 levels | 11.46 | 11.32–11.62 | 216.99 | 203.55–283.08 |
| Graph lookup, 512 resources | 6.38 | 5.56–7.64 | 1,578.34 | 1,413.57–1,783.72 |
| Selective glob, 128 directories | 43.35 | 42.50–45.43 | 225.83 | 202.02–273.00 |

The native binary SHA-256 is
`9b72e08fc45585c42e3d3eaaa799ae828f1a506b62b52a57a3d22a192619cdbc`; the JS
wrapper is `f61b2dd0c1a6327ce6bfe7085aa307f9cf6050bbd25d9f118aa76a3f58d8033e`,
and the WASM module is
`500b360fd9c626f6734e2b77623f9d27c93f406f10c0dfc615ef923f985119f9`.
The WASM values include Node and runtime startup and vary substantially under
load. They are host-specific observations, not portable performance targets.
The suite also confirms that 10,000 parsed definitions and 256 nested scopes
exceed the current WASM instance limits, so it uses the largest smaller cases
that complete on both hosts. Tracker allocation comparison remains focused on
owning versus borrowed parsing; wide-scope and graph lookup allocation totals
are still outstanding.

Reproduce with `python3 tools/benchmark-cli.py --samples 5` after building the
debug CLI and WASM distribution.

| Workload | Native before (median ms) | Native after (median ms) | WASM after (median ms) |
| --- | ---: | ---: | ---: |
| `(count [1 2 3])` through expression runner | 115.38 | 11.32 | 267.24 |
| Plan one task with 100 scalar definitions | 107.97 | 1.67 | 397.95 |
| Plan one task with 1,000 scalar definitions | 143.09 | 21.77 | 494.78 |

The strongest finding is structural: `km_host_free` unconditionally waited
`KM_GRACE_MS` (100 ms), including hosts with no processes. The fix waits only
when unreaped children receive TERM. Active-child TERM/KILL escalation is
preserved. This removes a fixed shutdown penalty from pure evaluation,
inspection, and already-reaped successful builds. The after measurements also
include concurrent checkout changes; attribute the removed fixed wait to this
source change, not every timing difference to this patch.

The changed POSIX host passes 20 sanitizer tests, including cancellation of
background grandchildren and cleanup of active process groups. The CLI signal
suite passes ten consecutive targeted runs after correcting the repeated-signal
test: identical standard signals may coalesce, so it now sends distinct signal
kinds and checks a conventional forced-termination status. The earlier broad
run exposed that test assumption; the correction changes no shutdown timing.

## Remaining costs and priorities

1. Native expression execution still calls `p.Tick(10)` before checking the
   result. Pure completion therefore retains roughly a 10 ms floor in this
   workload. Separate ready engine work from host waits; avoid busy polling or
   changing process cancellation semantics. Prove the change with pure,
   suspended-host, timeout, and cancellation cases before applying it.
2. WASM CLI startup dominates small workloads. These samples range widely under
   load (184–514 ms for the small expression); do not infer a regression from
   native/WASM ratios alone. Benchmark a reused embedding instance separately
   from CLI startup before optimizing ABI evaluation.
3. `core.Engine.find` scans nodes linearly, and scopes/definitions use linear
   lookup. Repeated registration and resolution can scale quadratically. Use
   larger dependency graphs and profile lookup work before selecting an index;
   preserve deterministic ordering and resource-key normalization.
4. JS `wildcardPaths` collects an entire subtree beneath the static glob root
   before matching and sorting. Traversal work and memory depend on the subtree,
   not just result count. Prune with remaining pattern segments and measure
   broad and selective patterns against native parity, including `**` matching
   zero directory levels.
5. Task caches have no eviction or cross-process locking in the initial spec.
   They avoid repeated task execution but can grow without bound. Measure cache
   hit/miss time, bytes hashed, and cache growth before designing lifecycle
   controls. Do not replace declared source dependencies with always-run tasks.

## Reproduction and limits

Run `make build/kame.debug dist-wasm`, then
`python3 tools/benchmark-cli.py --samples 5`. The script prints artifact hashes,
OS/Node versions, medians, extrema, and checks output before counting a sample.
Use `--native dist/kame` for optimized release measurements. Repeat on an idle
host for comparison; no timing assertion is added to correctness tests.

Before native artifact SHA-256:
`510b27cfae47eb24f5f19db72e86f75a34d471f3142c57af3117763c362e83d1`.
After native artifact SHA-256:
`9dee7bee194bd54db061c669481caaedd471de3ae3fa98c32b8c3448b3ff0201`.
WASM artifact SHA-256 for both measurements:
`8c3c712fa754456ace548ac82029bf85fc48709459327823b4c661b8abe6c9f6`.

These workloads do not establish throughput for large process pipelines,
streaming latency, maximum graph size, retained-memory bounds, or release
performance. Those need dedicated measurements; source-level complexity findings
above are opportunities, not measured bottlenecks.

The CLI test harness now excludes unit-package directories and Go-only test files
from native executable freshness checks. Those files do not enter `so build
./cmd/kame`; counting their timestamps caused avoidable native recompilation.
A temporary filesystem check confirms that unit-only changes keep the binary
current and a production source change still requests a rebuild. Production
package directories and module metadata remain covered.

Definition materialization previously waited indefinitely for a terminal node
state even after a lazy definition had published its current value. Handle
polling now copies that value before releasing root interest, allowing the
portable `Materialize` API to return. Program sanitizer coverage includes the
definition path; this is a termination fix, without a timing benchmark claim.

Tool configuration now shares one dependency producer per name. The counted
resolver regression verifies one native lookup across repeated materialization
and planning; the JS host caches resolved paths across its invocation contexts.
This removes repeated resolver calls, without claiming a wall-clock speedup.

A blocked-recipe test exposed unbounded publication latency: native redirected
stdio and WASM events queued during awaited host requests stayed hidden until
completion. Native event drains now flush stdio, and WASM process callbacks drain
queued events. The regression proves markers arrive before releasing a waiting
recipe; it does not claim a latency percentile or throughput improvement.

The original 8 MiB stream-growth trap (KB-9) is fixed without increasing the
16 MiB instance capacity. A coalescing heap reuses arbitrary freed event blocks,
binary JSON scratch uses the instance allocator, and JS reuses geometrically
grown input/output scratch buffers. Recipe capture retains the native budget
plus one sentinel byte, so truncation metadata survives bounded retention.
T012-03 verifies 8 MiB on each stdout/stderr stream for NUL and binary data in
human/JSON mode, including failures, and 64 KiB cache replay. All 28 WASM host
sanitizer tests pass; 1,000 unordered allocation/free cycles fit in 4 KiB.
The original NUL recipe now forwards exactly 8,388,608 bytes and exits 0; its
observed 1.530s is one run, not a comparative throughput benchmark. True heap
exhaustion now unwinds to an allocation-free ABI diagnostic. T010-22 verifies
100 undersized instance cycles, a surviving instance, a failed 1 MiB host
completion in a 64 KiB logical heap, and repeated temporary module-heap recovery.
The runtime is discarded after instance exhaustion; normal heap capacity and
stream retention budgets remain unchanged.

The post-fix compiled CLI gate passes all 102 selected suites with ASAN/UBSAN
and leak detection, with instrumentation verified before and after the run.
Focused sanitizer coverage passes 28 WASM host tests and 96 program tests; Go
CLI sanitizer tests pass too. These are ownership/correctness checks, without
claims about production throughput or all remaining specification requirements.

An unread-pipe reproduction showed the Node host previously drained an 8 MiB
recipe to completion while public stdout was blocked. Capture limits did not
bound the writable publication queue. The host now pauses publishing child
pipes when either public sink needs draining, and waits between runtime steps
so cached replay cannot advance work behind a blocked sink. Resuming the reader
publishes every byte. Cancellation releases paused readers and waits before
reaping the child group; timeout kills the whole group and drains killed pipes.
T012-04 covers stdout, stderr, JSON, direct argv, cached replay, cancellation and
timeout with deliberately unread pipes. This establishes backpressure behavior,
without a production latency or peak-memory benchmark claim.

The allocator checkpoint build passes the existing eight WASM publication
backpressure checks and large-output suite in an isolated checkout. This
verifies lossless normal streaming, bounded retention and cancellation after
adding WebAssembly exception handling; it is not a timing benchmark. T010-22
also verifies that shell/argv callback exhaustion terminates and reaps real
stdout/stderr publishers before surfacing the diagnostic. All 29 WASM host
ASAN/UBSAN tests pass for the committed allocator changes.


## Declaration source selection

Each reached `when` compiles the growing prefix of selected definitions before
evaluating its predicate. The native loader reparses selected parts to extract
definitions in their original language; JavaScript extracts them from the
existing portable AST. This introduces repeated parsing and registration work
for sources with many conditions. Inactive nested predicates bypass the query,
and inactive includes perform no reads. The conformance checks prove selection
and ownership behavior, but do not provide a selection-latency benchmark.
Measure source loading separately from final compilation before replacing this
simple implementation with a persistent configuration evaluator; any reuse
must preserve source order, lazy defaults, override precedence, and effect
restrictions.

## Scoped snapshot reads

Direct scoped env reads scan the already owned target environment and clone only
the selected value. They avoid the previous shared ambient resource node and
host request/replay cycle. Complete environment fingerprints already bind cached
tasks and scoped file rules, so changed inherited values still invalidate work.
T006-06 proves unchanged cache reuse and changed-root invalidation on both hosts;
103 program sanitizer tests pass. No timing or large-environment benchmark has
been run, so this is a correctness and allocation-path review, not a measured
throughput improvement. Scoped tool resolution remains unfinished.

## Structured recipe ownership and retention

Structured recipes retain one evaluation context through suspended process
steps, avoiding dangling scope/resolver pointers. Script constructors own parsed
ASTs and retained lexical scopes for the invocation. `freeKashFunctions` releases
these at program teardown. Repeated construction adds entries to `KashFunctions`;
the implementation does not reclaim an unreferenced constructed script earlier.
Long watch sessions that reconstruct scripts after invalidation can therefore
retain increasing AST/scope storage. This follows from the lifetime code and has
not been quantified with a long-running memory benchmark. Any future reclamation
must preserve scripts stored in lazy definitions, cross-node values and captured
scopes. Existing ASAN/UBSAN gates prove teardown ownership, not bounded memory
throughout an arbitrarily long invocation.

The structural formatter also repeatedly computes compact subtree text during
width decisions (`inlineFits`). Deeply nested expanded input can cause repeated
allocation and traversal; `layout.go` records this optimization point. Width
caching should be considered after measuring realistic source depth, preserving
actual starting columns, closing-delimiter budgets and Unicode/tab widths.

## Scoped lazy definition storage

Each reached definition retains one immutable environment snapshot per digest and
phase. The digest includes the canonical complete environment and working directory;
repeated reads reuse the same engine node. This prevents cross-target value/cache
reuse, but every new environment/definition pair copies the complete environment
and remains registered until engine teardown. Definition lookup still scans source
items and engine lookup remains linear. Sharing immutable environment storage and
reclaiming inactive namespaces may improve long watch sessions; neither behavior
has a measured memory or throughput benchmark yet. Separate phases prevent a
value computed during recipe evaluation from bypassing dynamic-input restrictions.

Native source watch compares complete source bytes every 200 ms, so edits with
preserved size and timestamps still reload the graph. This costs source reads and
stamp copies on every scan; retained stamp storage scales with total source size.
External input watching still uses metadata fingerprints. No large-source watch
benchmark has been run. The four T009-14 scenarios establish correctness, including
invalidation after a running recipe publishes an output newer than the changed input.

## Collected shell environment costs and tool policy

Collected target-bound shell requests serialize the complete immutable snapshot
for forwarding hosts and retain owned environment values in the portable payload.
Native dispatch constructs a temporary borrowed string slice, starts the host
request and frees that slice. Empty snapshots remain explicit. This adds work
proportional to environment size per collected request; no large-environment
benchmark has been run.

Declared tool resolution already follows the startup-PATH contract in specs
007/009 and retains its per-name resolver cache. Recipe PATH does not trigger
another tool lookup. The older unfinished scoped-tool statement above is
superseded by this verified policy; T007-11 passes 15 assertions. T013-05 passes
five active assertions at a stable revision, including unchanged native build
outputs. Changing revision during compilation changes generated build metadata
and invalidates that incrementality experiment.

## Reactive restart retention and parser copying

A pending invocation restart temporarily retains its prior dynamic dependencies
while discovering the replacement set. Reusing an edge transfers its existing
interest; accepted publication releases unused holds. Cancellation, termination
and explicit invalidation also release them. This avoids cancelling and restarting
a live input stream merely to replace a consumer callback. Retained edge storage
scales with the previous dependency set and can last through a suspended
replacement. No graph-size throughput or peak-retention benchmark has been run.
Core and standard-library sanitizer regressions establish ownership and branch
release, not a measured performance improvement.

The newly noted zero-copy parser request is not fully implemented. `source.New`
clones name and text, while spans and many raw tokens borrow source storage;
decoded strings and target literals require separate owned buffers. An adopted
source constructor could remove a redundant copy from source-loading paths, but
must distinguish owned input from borrowed embedding buffers and preserve
include/source diagnostic lifetime. Allocation measurements should precede
changing this ownership contract.
