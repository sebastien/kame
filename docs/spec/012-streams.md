# Streams and Batches

## Purpose

This specification defines the portable stream protocol used by the reactive
engine. It replaces the legacy asynchronous iterator implementation with an
explicit resumable state machine suitable for Solod. It also resolves the
legacy ambiguity around `EOB`: a batch is materialized as an immutable list,
not as a nil update.

This specification amends `002-engine.md`. Where they differ, this document
wins for source, batch, and stream behavior.

## Terms

- **Atom:** one already materialized `core.Value` publication.
- **Chunk:** one value belonging to the batch currently being assembled.
- **Batch:** zero or more chunks committed as one immutable list value.
- **Stream:** a finite or open-ended sequence of atoms and batches.
- **Source:** a resumable producer of stream protocol atoms.

An operation may return an atom, a batch, or a stream. These are one protocol,
not three unrelated asynchronous APIs:

```text
atom:   Atom(value), EndStream
batch:  Chunk(value)*, EndBatch, EndStream
stream: (Atom(value) | Chunk(value)*, EndBatch)*, EndStream
```

An atom publishes its value directly. `EndBatch` publishes the accumulated
chunks as one list value. An empty batch publishes an empty list. `EndStream`
publishes one pending, nonempty batch before completing. It does not publish an
empty implicit final batch.

## Source Protocol

A source is a named poll function, an opaque state pointer, and a named free
function. This follows the extension boundary in `001-architecture.md`; Solod
has no closures, goroutines, or native async iterators.

```text
Source {
  poll(engine context, source state) -> emitted | waiting | completed | failed
  free(source state)
  state
}
```

`poll` emits at most one protocol atom per engine step. It may instead register
a dependency or submit host work and return `waiting`. The completion is copied
into an engine-owned message and a later step resumes the same source. A source
that returns `completed` has no implicit value or batch to emit.

The engine invokes `free` exactly once after normal completion, failure,
invalidation, cancellation, or engine teardown. A source transfers each emitted
payload to the engine before returning from `poll`; `free` must not free a
transferred payload. Source state must use an allocator that outlives every
wait. It must not contain pointers to a run arena, stack string, stack slice,
or caller-owned host-completion buffer.

## Protocol Atoms

The source may emit exactly one of these atoms from one poll:

| Atom | Meaning |
| --- | --- |
| `Atom(value)` | Publish one immutable value update. |
| `Chunk(value)` | Append one value to the current batch. |
| `EndBatch` | Commit the current batch as an immutable list. |
| `StartCollection` | Start a nested list within the current batch. |
| `EndCollection` | Commit the current nested list as one chunk of its parent. |
| `Nested(source)` | Transfer an inner source onto the materializer stack. |
| `EndStream` | End the current source. |
| `Failed(diagnostic)` | Fail the current source and its enclosing node. |

`Chunk` and collection controls are source-local. `Atom` is valid only when no
batch or nested collection is open. `EndBatch` is valid only at collection depth
zero. A source protocol violation fails the node with `LM-EXPRV`.

`Nested(source)` transfers ownership of the nested source to the materializer.
An inner `EndStream` pops only that source and resumes the outer source. A
failure in an inner source fails the enclosing node. This preserves local nested
stream termination without treating a stream as a `Value`.

## Materialization

The engine owns a materializer for every active source node generation. It owns
the source stack and every open batch builder. Raw protocol atoms are never
delivered to node subscribers.

- `Atom(value)` publishes `value` with the node's next revision.
- `Chunk(value)` transfers the value into the current collection builder.
- `StartCollection` pushes a collection builder.
- `EndCollection` turns the top builder into one immutable list and transfers
  that list into its parent builder.
- `EndBatch` turns the root builder into one immutable list, publishes it, and
  resets the root builder.
- `EndStream` commits a nonempty root builder, rejects unclosed nested
  collections, then pops the source.
- `Failed(diagnostic)` frees every uncommitted builder value and terminates the
  node without publishing a partial batch.

Builders use heap-backed dynamic slices. They own every appended value until
the list is published or discarded. Building a list moves the builder's element
storage and element ownership into the immutable `Value.List`; it must not
clone values merely to keep a builder alive. The builder is reset before any
subscriber delivery so reentrant engine activity cannot alias its storage.

Published atom and list values follow `002-engine.md`: the node retains one
engine-owned latest value, and each subscription queue owns a separate clone.
No raw source buffer, builder storage, or host completion buffer may be retained
by a node or subscription.

## Scheduling and Backpressure

Sources run only while their node has live root or subscriber interest. The
single-threaded scheduler polls ready sources in canonical resource-key order.
After one emitted atom, it requeues an interested nonterminal source behind
other ready work. This bounds work per step and prevents an infinite source
from starving dependency processing.

Subscription coalescing occurs after materialization. A full queue may discard
intermediate published batch lists according to `002-engine.md`, but it never
mutates an already published list or drops a terminal event. Sources are not
asked to replay discarded values.

## Host Work and Cancellation

A waiting source records its node ID, generation, attempt, and host request ID.
The engine copies completion bytes into its message allocation before the host
call returns. On resume, the source may consume, clone, or free that message
payload; it may not retain the host buffer.

Invalidation and cancellation detach the waiting source from the active node
generation, free its materializer, and request host cancellation where needed.
Late completions are freed without polling the source when their node,
generation, or attempt is stale. This applies equally to atom, batch, and
stream operations.

## Reactive Lifting

The evaluator lifts operations over materialized publications, never protocol
atoms. A batch therefore reaches an ordinary operation as one immutable list;
an atom reaches it as its scalar value. When one or more input nodes publish,
the evaluator coalesces changes and invokes the operation once with the newest
complete argument set, as defined in `005-evaluation.md`.

An operation result is normalized into a source. Pure scalar operations use a
one-atom source. Collection operations may use a finite batch source. Host and
service operations use waiting sources. No operation needs to implement its own
subscriber replay, dependency graph, or asynchronous iterator wrapper.

## Ownership Summary

| Object | Owner | Release point |
| --- | --- | --- |
| Source state | Active materializer | Source pop, failure, invalidation, cancellation, or engine free |
| Emitted value payload | Materializer after emission | Publication transfer or discarded protocol atom |
| Open batch chunks | Collection builder | List publication or failed/cancelled discard |
| Published latest value | Node | Replacement, invalidation, or node free |
| Subscriber event value | Subscription queue | Pop transfer, coalescing, unsubscribe, or engine free |
| Host completion copy | Engine message queue | Source consumption or stale-message discard |

## Acceptance Tests

- A scalar source publishes one scalar value and completes.
- A source with chunks `1`, `2`, `EndBatch` publishes `[1 2]` as one value.
- `EndBatch` with no chunks publishes an empty list.
- A nonempty batch is committed by `EndStream`; no empty implicit final batch is
  emitted.
- A nested source ending with `EndStream` resumes its outer source.
- Nested collection controls produce an immutable nested list, including an
  empty nested list.
- An unbalanced collection or an atom emitted during an open batch fails with
  `LM-EXPRV` and frees all builders.
- A waiting source resumes only from a matching copied completion.
- Invalidation, cancellation, failure, and normal completion each call a
  source free function exactly once.
- Two subscribers receive independently owned materialized batch values; queue
  coalescing cannot mutate either received list.
- An unbounded ready source cannot prevent a ready dependency or completion
  from being processed.
- Repeated atom, batch, nested-source, cancellation, and stale-completion tests
  using `mem.Tracker` leak no source state, builder, value, or message payload.
