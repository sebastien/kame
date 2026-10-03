# Performance review

Reviewed 2026-10-03 on Linux x86-64, Node v24.18.0, using the native debug build
and the freestanding WASM module through its JS CLI. All timings include process
startup, loading, compilation, evaluation/inspection, and teardown. Five samples
per case were checked for correct output. Other conformance tests were running,
so these observations are not stable release benchmarks or isolated CPU profiles.

## Measured results

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

An 8 MiB NUL-byte recipe now provides a concrete memory failure reproduction:
native exits 0 with 8,388,608 bytes, while WASM exits 1 after 790,528 bytes in this
run, trapping inside event JSON allocation. The partial count varies with chunk
boundaries. The source shows a fixed arena with limited free reuse and the JS
recipe host accumulating every output chunk. KB-9 remains open; fix allocation
reuse and retention before tuning throughput or increasing memory limits.
