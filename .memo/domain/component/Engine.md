---
identity: 4622d144-b64e-4f1a-8e47-8f889b33290d
created: 2026-11-25T18:43:44.180162+00:00
updated: 2026-11-25T18:43:44.614434+00:00
---
# Engine (`core`)

The engine is Kame's portable reactive core. It holds values, resource keys,
nodes, producers, dependency edges, scheduling, invalidation, subscriptions,
stream materialization and content signatures. It owns graph state, but no
threads and no OS work.

Package: `src/go/kame/core` ("portable reactive values and streams"). It
imports only `diagnostic`, `internal/jsonstream` and `internal/mem`.

## Parts

- `engine.go`: roots, subscribe, publish, invalidate, cancel, retire,
  teardown.
- `scheduler.go`: incremental readiness with a heap-ordered check queue and
  cached prerequisite heights, emitting ready work in deterministic
  resource-key order. It replaced graph-wide readiness scans on 2026-11-24.
- `producer.go`: the `EngineContext` that producers use to request normal and
  order-only dependencies and to submit host work.
- `dependency.go` (edges and interest), `update.go` (subscription
  coalescing), `materializer.go` and `stream_protocol.go` (turning stream
  atoms into values, spec 012).
- `observations.go`, `revalidation.go`, `signature*.go`: dependency
  observations, digests and signature records. These are the facts used by
  [domain/concept:Freshness] and [domain/concept:Cache].

## Interactions

- **Producers come from above.** [domain/component:Program] supplies rule
  instances, and [domain/component:Evaluator] supplies definitions and
  reactive expressions.
- **Host work goes out as requests.** The engine emits
  [domain/concept:HostRequest]s and validates completions. It never calls a
  host directly.

## Invariants

- One [domain/concept:Node] per resource key. Completions are checked against
  generation and attempt.
- Determinism: the same program, resource state and requests give the same
  selection, dependency order, per-node revision sequence and fingerprints.
- Subscriptions see the latest value plus future updates, never history.

Sources: `docs/spec/002-engine.md`, `docs/spec/012-streams.md`,
`docs/spec/026-runtime-performance.md`, `docs/spec/001-architecture.md`
§Determinism.
