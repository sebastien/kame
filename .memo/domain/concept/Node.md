---
identity: 156224c9-2de7-479b-930b-0530d9dd7b7a
created: 2026-11-25T18:43:56.920782+00:00
updated: 2026-11-25T18:43:57.359159+00:00
---
# Node

A node is the engine's lazy, shareable unit of computation for one
**resource key**: a kind (`definition`, `target`, `file`, `task`, `service`,
plus glob and environment observations) and a canonical name. One canonical
key maps to one node per engine, so concurrent requests share work. Keys of
different kinds never collide, even with the same name.

A node owns its edges, state, latest value, terminal diagnostic and
**producer**, the callback that advances it through an engine context.
Producers publish, request dependencies, attach a stream source or submit
host work. They never mutate the node directly or touch the OS.

## Identity counters

- **Revision:** incremented on each published value; the first value is 1.
- **Generation:** one run of the node. It increments on invalidation or
  cancellation, and generation-owned dynamic edges are replaced.
- **Attempt:** producer invocations within a generation.

A [domain/concept:HostRequest] completion is accepted only if its node,
generation, attempt and request match the active invocation. Late results are
discarded.

## Current and retained

A value is **current** when it satisfies dependency readiness for the active
generation. After invalidation, a stale value may stay **retained** for
observers that already received it. It never satisfies a new dependency and is
never delivered to a new subscriber.

## Demand

- **Roots** carry a caller's interest through the reachable graph. Work is
  cancelled when no root or subscriber needs it, and shared dependencies stay
  alive for other roots.
- **Subscriptions** see the latest value plus future updates. They do not
  replay history, and a full queue coalesces intermediate values.
- **Suspension** pauses work without abandoning it. It is distinct from
  cancellation.

Related: [domain/component:Engine] [domain/model:build_graph]

Sources: `docs/spec/002-engine.md`; `docs/glossary.md` (Reactive engine).
