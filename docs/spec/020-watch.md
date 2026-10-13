# Retained native and WASM watch sessions

## Contract

`--watch` on a build keeps its selected roots in one portable program. Roots
share dependencies and processes as they do in a batch build. A settled root
keeps interest in the latest graph; watching must not run every task repeatedly
or create a new program on every timer tick.

The host uses OS notifications to trigger fingerprint scans and debounces changes
for 100 ms. Native Linux uses inotify (including Cosmopolitan when running on
Linux); Node/WASM uses Node's built-in `fs.watch`. Healthy notification mode
reconciles every 10 seconds. If any resource cannot be subscribed, the host falls
back to 200 ms scans while retaining successful subscriptions. Other native
platforms currently use polling fallback. Produced outputs are excluded. Configuration
sources are compared by bytes, including when timestamps and lengths are
unchanged. Discovered source candidates and attempted includes are observed so
missing sources and optional includes can appear later.

On hosts with reliable change timestamps, unchanged external files reuse their
content fingerprints between scans. The polling hint includes device/inode,
mode, size, and nanosecond modification/change timestamps for both following and
no-follow path views. It is not a content identity: the first scan still checks
accepted bytes, metadata-view promotion forces a check, and changes during a
read force another check. Missing/error transitions remain observable. Sources,
memory resources and hosts without reliable change timestamps retain full checks.

Directory notifications are hints, not content identities: duplicate events and
unchanged bytes do not rebuild roots. Subscriptions are installed before scans
and replaced as resources change. Parent directories cover atomic replacement
and recreation; missing directories use their nearest existing ancestor. Existing
symlink targets and direct dangling-link destinations are also observed. Glob
roots subscribe to existing descendant directories and refresh after topology
changes. Directory events conservatively scan all tracked resources; overflow,
lost subscriptions and watcher errors also request a scan and refresh. Slow
reconciliation recovers silently missed events. Memory resources require fallback.

Idle hosts wait for notifications, cancellation or timer deadlines, rather than
waking every 10 ms. Active builds retain short process-service ticks. Shutdown
closes notification handles, including after source reload failures.

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
scan. The JS host owns notifications, fallback polling, source loading and signals.
Automatic OS subscriptions are CLI-only; the JavaScript embedding API retains
explicit `invalidate(resources)` for custom hosts. The Python wrapper inherits
the selected CLI host's notification behavior.

## Acceptance

- Native and WASM reload an included source with preserved metadata, diagnose
  malformed input, recover after repair and replace changed dependency graphs.
- An input changed after a recipe's read handshake produces a second build with
  the latest bytes even when the first output has a newer timestamp.
- Multiple roots share a prerequisite; idle watch does not rerun settled tasks.
- Glob additions/removals, absent optional includes and missing source creation
  trigger the appropriate work. Produced outputs do not trigger rebuild loops.
- Unchanged large tool executables are not reread each polling interval on
  change-timestamp-capable hosts. Same-size edits with restored mtime, inode
  replacement, symlink-target edits and metadata-only changes remain observable.
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
- Recursive glob directories can appear and be replaced; subsequent edits in the
  new subtree remain observable. Missing ancestors can appear and recover roots.
- Linux `/proc` read counters verify that healthy idle watches stop frequent
  source reads and that unwatchable memory resources enable polling fallback.
