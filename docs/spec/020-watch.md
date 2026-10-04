# Retained native and WASM watch sessions

## Contract

`--watch` on a build keeps its selected roots in one portable program. Roots
share dependencies and processes as they do in a batch build. A settled root
keeps interest in the latest graph; watching must not run every task repeatedly
or create a new program on every timer tick.

The host scans observed external file metadata and glob membership every 200 ms
and debounces changes for 100 ms. Produced outputs are excluded. Configuration
sources are compared by bytes, including when timestamps and lengths are
unchanged. Discovered source candidates and attempted includes are observed so
missing sources and optional includes can appear later.

The scanner runs while recipes execute. Input changes queue until the current
build settles, then invalidate the affected resource keys in the retained engine.
A recipe publishing output newer than its changed input still reruns: timestamp
freshness cannot erase the observed invalidation. Order-only dependencies retain
their freshness exclusion. Obsolete resource subscriptions are dropped after
the next successful graph evaluation.

Source changes dispose the old program after active work settles and compile the
current source graph before launching new effects. Malformed or missing sources
produce authored diagnostics and keep watching for repair. Failed attempts keep
their attempted source paths; a successful compilation replaces that source set.
Roots, environments, grants, tools, timeouts and retries use the invocation's
original configuration. Watching never broadens language grants.

Live stdout/stderr and JSON events retain ordinary publication and backpressure
contracts. An idle watcher publishes no repeated task events. SIGINT/SIGTERM
cancel active processes, reap descendants, dispose the runtime and exit with
the usual signal-derived status. Module instance disposal also releases retained
roots and ignores stale host completions.

## Freestanding boundary

The module owns roots and dependency invalidation; it performs no filesystem
watching or clock reads. Additive ABI calls accept a JSON array of target names,
copy a nonmutating snapshot of busy state, root results and observed resources,
and invalidate a validated JSON array of file/glob keys. Invalid batches have no
partial effects. Queries preserve buffer-size/copy semantics and the static
allocation-failure boundary. A settled file resource reports whether its cached
observation was missing, allowing hosts to catch a file created before its first
poll. The JS host owns polling, source loading and signals.

## Acceptance

- Native and WASM reload an included source with preserved metadata, diagnose
  malformed input, recover after repair and replace changed dependency graphs.
- An input changed after a recipe's read handshake produces a second build with
  the latest bytes even when the first output has a newer timestamp.
- Multiple roots share a prerequisite; idle watch does not rerun settled tasks.
- Glob additions/removals, absent optional includes and missing source creation
  trigger the appropriate work. Produced outputs do not trigger rebuild loops.
- Root failure can recover after dependency repair; subscriptions from obsolete
  branches stop triggering work.
- Grants and scoped environments survive invalidation and source reload; denied
  operations perform no effects.
- Live output arrives before completion; blocked public sinks remain bounded.
- Interrupting idle and active watchers releases instances and process trees.
- Portable allocator tests cover retained handles, repeated snapshots,
  invalidation, invalid batch rejection, failed starts and disposal.
- Public ABI tests cover query/copy, malformed resource kinds, repeated use,
  instance isolation and allocation failures.

## Implementation status

The portable WASM runtime now retains multiple roots, reports their state and
observed resources, validates invalidation batches before applying them, and
releases retained handles on cancellation or instance disposal. The JavaScript
CLI implements polling, debounce, source reload and repair, queued active-input
changes, live event publication and signal-driven disposal. T010-23 exercises
native and WASM source reload, malformed-source recovery, graph replacement,
failed-root recovery after an input appears or an optional include is created,
input changes during a running recipe, shared roots, glob membership, idle
settled-root stability, obsolete-input subscription removal, scoped-environment
preservation across reload/invalidation and SIGINT cleanup.
Portable allocator coverage includes shared roots, repeated snapshots, invalid
batch atomicity, malformed JSON rejection and failed-start cleanup. T009-14 and
T010-23 also verify grant preservation through invalidation and source reload,
and that denied reads do not run recipes. D01 remains open for the other
acceptance cases. T010-25 adds raw ABI query/copy, malformed target/resource
rejection, retained-root invalidation, allocation-failure diagnostics, instance
isolation and disposal coverage.
