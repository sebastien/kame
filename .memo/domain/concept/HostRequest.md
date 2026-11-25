---
identity: 39f784b4-dbb2-48da-9e8e-229a717859f2
created: 2026-11-25T18:43:53.839481+00:00
updated: 2026-11-25T18:43:54.377809+00:00
---
# Host request

A host request is a portable, owned description of work that only the
embedding environment can do: reading or writing files, expanding globs,
running processes, reading the environment or clock, persisting cache
records, and similar work. The host answers with a **completion**.

- Producers and evaluators submit requests through the engine context. The
  engine never calls POSIX itself ([domain/decision:portable_host_boundary]).
- The native host (`host/posix`) may complete a request immediately. The WASM
  reactor (`host/wasm`) hands it to JavaScript and receives the completion
  later.
- A completion is accepted only when its node ID, generation, attempt and
  request ID match the active invocation ([domain/concept:Node]).
- Authorization happens before submission. A denied request fails with
  `CAP_DENIED` and has no side effect ([domain/concept:Capability]).
- A queued request owns its payload. On teardown, outstanding requests may be
  abandoned by transferring payload ownership to the host. A later completion
  then disposes of its own payload without touching the engine.

## Boundaries

- A host request is not a graph decision. The host never owns graph policy:
  readiness, invalidation, freshness and caching stay in the engine and
  program.
- It is not a Kash process stage. A [domain/concept:ProcessGraph] is
  submitted as host work, but its channels and framing are Kash semantics.

Related: [domain/component:Host] [domain/component:Engine]

Sources: `docs/spec/001-architecture.md` §Portable Boundary, §Ownership;
`docs/spec/002-engine.md` §Scheduling; `docs/spec/010-wasm.md`;
`docs/glossary.md` (Host request).
