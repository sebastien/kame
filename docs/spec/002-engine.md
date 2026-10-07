# Reactive Engine

## Purpose

The engine evaluates lazy computation nodes mapped to resources. A node may
publish one value immediately or incrementally, publish many values, discover
dependencies while running, fail, or complete without a value.

The engine is a portable state machine. It owns graph state but does not own
threads or perform operating-system work.

## Values

The runtime value model is a closed tagged union with these kinds:

- Nil.
- Boolean.
- Integer (`int64`).
- Float (`float64`).
- String.
- Bytes.
- List of values.
- Record of string keys and values.
- Callable operation or lexical function.
- Resource reference.
- Pattern (match or expansion pattern text; `014-patterns.md`).

Stored and serialized values exclude callables. Values retained outside their
creating arena must be cloned into the destination allocator. Containers own
their elements. Values are immutable after publication. Patterns serialize as
their canonical text.

Sources, batches, and stream controls are not values. `012-streams.md` defines
their explicit resumable protocol and materializes them into these values before
publication.

## Resources

A resource key contains a kind and canonical name. Resource kinds include:

- `definition` for lazy named values.
- `target` for phony named rules.
- `file` for filesystem paths.
- `task` for cached named rules.
- `service` for long-running actions.

The same canonical key identifies a node within one engine. The engine
does not support arbitrary URI schemes. Source syntax has one namespace for bare
names, but rule resolution selects `target`, `task`, or `service` before falling
back to `definition`; these resource keys never collide internally.

## Nodes

A node has:

- Stable numeric identity.
- Resource key.
- State.
- Monotonic revision beginning at zero before any publication.
- Optional latest value.
- Static and dynamic dependency edges.
- Reverse dependent edges.
- Producer callback and opaque producer context.
- Generation number used to reject stale completions.
- Monotonic attempt number within the generation.
- Optional terminal diagnostic.

Node states are conceptually idle, waiting, ready, running, complete, failed,
and cancelled. Implementations may combine states when no behavior is lost.

## Updates

The engine publishes ordered updates:

- Value: a new immutable value and revision.
- Dependency: a dynamic dependency was accepted.
- Invalidated: the previous result is no longer current.
- Completed: no more values will be published for this generation.
- Failed: evaluation stopped with a diagnostic.
- Cancelled: evaluation stopped because its root request was cancelled.

The first value has revision 1. Every later value increments the revision once.
Dependency and terminal updates do not increment the value revision.

## Subscription Semantics

Subscriptions use latest-value plus future-update semantics:

- A new subscriber receives the current value, if one exists.
- It then receives later updates until terminal state or unsubscription.
- Values published before the retained current value are not replayed.
- The engine does not keep an unbounded update history.
- Independent subscribers cannot consume or drain one another's updates.

A subscription queue holds at most eight pending value updates plus one
out-of-band terminal update. When it fills, the engine frees all pending value
payloads except the oldest unconsumed one and appends a clone of the newest
value. Terminal state never occupies value capacity. A popped event transfers
its payload to the receiver; unsubscribe and engine teardown free every unpopped
payload. Exact intermediate values require an explicit sink operation and are
not a property of cells.

Non-value updates use a separate queue with the same capacity and coalescing
rule. Their retained order is preserved, but intermediate dependency and
invalidation updates may be omitted when that queue fills.

## Producers

A producer is a named function pointer and context. It receives an engine
context and node identity, never a mutable node, and returns one of:

- Completed synchronously.
- Waiting for declared dependencies.
- Submitted host work.
- Published a value and remains active.
- Failed with a diagnostic.

Producers publish, request dependencies, submit host work, and attach a source
through engine-context methods; they do not mutate nodes directly. A producer
may attach one source to its active generation. The engine owns that source's
materializer until completion, invalidation, cancellation, or teardown. The
callback context may record evaluator state but must follow the ownership rules
in `001-architecture.md`.

A producer may read the current value of a dependency it has already requested
through the engine context. The value is borrowed for the duration of the
producer call and must be cloned before it is retained; reading does not add an
edge. A dependency without a current value reports none instead of blocking.

A producer that emits incrementally uses the source protocol in
`012-streams.md`. Node subscribers receive only its materialized value updates.

## Dependencies

Static dependencies are registered before a node starts. A running producer may
request a dynamic dependency. The engine then:

1. Canonicalizes and interns the resource key.
2. Rejects an edge that creates a cycle.
3. Adds the forward and reverse edge once.
4. Requests the dependency if it is not current.
5. Suspends or continues the producer according to the request.

Repeated discovery of the same edge is idempotent. Dynamic edges belong to one
generation and are replaced when that node is reevaluated.

An order-only dynamic edge retains ordinary scheduling interest, cycle detection
and failure propagation. A normal request for that edge upgrades its purpose;
an order-only request cannot downgrade a normal edge. Invalidation still restarts
the consumer, but records when every path to it crosses an order-only edge. The
runtime can then reevaluate file freshness without treating ordering work as a
content change. A normal path in a diamond overrides the order-only reason.
Reasons are collected before generation edges are removed.

## Scheduling

One owner calls the engine step function. A step processes queued completions,
propagates invalidations, and emits ready work in deterministic resource-key
order.

A source protocol atom consumed without a materialized value is internal
progress, not waiting. The engine requeues that source behind other ready work;
only a source that explicitly returns waiting blocks for a dependency or host
completion.

The host may execute ready jobs concurrently. A completion includes node ID,
generation, and attempt. The engine discards a completion whose generation or
attempt no longer matches the active invocation.

Only one producer generation per node may run at once. Concurrent requests for
the same resource share that generation.

## Invalidation

Invalidating a node while demand remains starts a replacement producer
generation. Invalidating a node:

- Increments its generation.
- Cancels or logically abandons running work.
- Clears its terminal state and generation-owned dynamic edges.
- Marks its latest value stale and retains it only for observers that already
  received it, until their queued copies are freed.
- Recursively invalidates dependents once.

The engine retains root and subscriber demand across invalidation. It abandons
the old generation, requests host cancellation for submitted work, and schedules
the replacement generation without a second root request. Producers recreate
their generation-local source through the engine context; callers do not replace
sources on nodes.

Stale values do not satisfy dependency readiness and are not delivered to new
subscribers. Publishing a replacement value makes it current. Failure leaves no
current value, though observers may already own an earlier copy.

## Cancellation

A root request owns interest in its reachable graph. Cancelling it removes that
interest. Work is cancelled when no live root request or active subscriber
requires it. Shared dependencies remain active for other roots.

The core emits cancellation requests to the host but does not assume immediate
host termination. Late completions are rejected through generation checks.

## Parallelism

The deterministic engine can run with executor capacity one. A hosted executor
may request multiple ready jobs up to a configured positive capacity. Jobs must
not synchronously wait for jobs submitted to the same bounded worker pool.

Parallelism is graph-based: independent ready nodes may run together; recipe
lines inside one build node remain one ordered shell script.

## Acceptance Tests

- A lazy node does not start before it is requested.
- Two requests for one node execute its producer once.
- Static dependencies complete before their dependent becomes ready.
- Independent nodes are offered as ready together and produce deterministic
  per-node revisions regardless of completion order.
- A producer adds a dynamic dependency, waits, resumes, and publishes a value.
- A producer reads the current value of a declared dependency and publishes a
  derived value.
- Direct and indirect dependency cycles return `DEP_CYCLE`.
- Two subscribers receive the current value and all future values independently.
- A late subscriber receives only the latest retained value, not full history.
- A full subscriber queue coalesces values without losing terminal events.
- Invalidation propagates once through a diamond graph.
- Completion from an invalidated generation is ignored.
- Completion from an older attempt in the current generation is ignored.
- Cancelling one of two roots does not cancel their shared dependency.
- Cancelling the last interested root requests host cancellation.
- Engine teardown under `mem.Tracker` leaks no owned value, node, edge, or event.
