---
identity: 20a5bebd-be81-45fe-a8c4-740b93627e83
created: 2026-11-25T18:44:10.025727+00:00
updated: 2026-11-25T18:44:50.048493+00:00
status: accepted
date: "2026-09-13"
---
# Portable engine behind a host request boundary

Status: accepted.
Date: 2026-09-13 (initial specs 001 and 002); kept through the Go migration
([domain/decision:implementation_language+002]).

## Context

Kame runs as a native CLI and embedded in JavaScript through WebAssembly. In
the embedded case, filesystem, process, clock and cache work belong to the
embedder and may complete asynchronously.

## Alternatives

- **Let the engine and evaluator call the OS directly.** Simple natively, but
  it would tie graph logic to one platform.
- No other alternative is recorded.

## Decision

- Portable packages build for `GOOS=wasip1 GOARCH=wasm` without cgo, and do
  no native filesystem, network or process work.
- Host work is a [domain/concept:HostRequest]. The host returns a completion,
  and the engine validates it against its node, generation, attempt and
  request identity.
- "The host never owns graph policy. The engine never invokes POSIX
  directly." (`001` §Dependency Direction)

## Consequences

- The native host may complete a request immediately. The WASM reactor
  returns requests to JavaScript and receives completions later.
- [domain/concept:Capability] checks run before dispatch, so a denial has no
  side effect.
- Late or stale completions are discarded ([domain/concept:Node]).
- Native and WASM backends must produce the same output, so tests cover both.
- Watch, cache backends, remote execution, plugins, Windows processes and
  resource URIs all use portable interfaces (`001` §Host Boundaries).

Sources: `docs/spec/001-architecture.md`, `docs/spec/002-engine.md`,
`docs/spec/010-wasm.md`.
