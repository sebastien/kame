---
identity: 4de61920-803e-4ac5-b450-def4a35befbc
created: 2026-11-25T18:44:07.717162+00:00
updated: 2026-11-25T18:44:47.871299+00:00
status: superseded
date: "2026-09-13"
superseded-by: "domain/decision:implementation_language+002"
---
# Rewrite LittleMake in Solod

Status: superseded by [domain/decision:implementation_language+002] on
2026-11-12.
Date: 2026-09-13 (commit `b96f2dc6`, "Solod rewrite: full specification and
project scaffolding for LittleMake").

## Context

- Kame began as LittleMake, a TypeScript implementation. Its author described
  it as a reactive streaming engine whose nodes map to rules and artifacts,
  with a library for files and processes and embeddable languages, ideally
  usable from JavaScript through WebAssembly ([world/task:plan+001]).
- The rewrite started spec-first: the whole `docs/spec` set was written before
  code, with milestones from platform proof to engine, native slice,
  language/library, production execution and WebAssembly.

## Alternatives

- **Port the TypeScript architecture.** Rejected: the legacy implementation
  "serves as behavioral reference only; the architecture is not being ported".
- Why Solod was preferred over other languages is not recorded.

## Decision

Rewrite in Solod, a strict Go subset that transpiles to C, with a POSIX host in
C and a freestanding WebAssembly target. By October 2026, native releases were
Cosmopolitan APE binaries built with a pinned `cosmocc`.

## Consequences

- Manual memory management shaped the design that remains today: four logical
  lifetimes and explicit clone/transfer at boundaries
  (`docs/spec/001-architecture.md` §Ownership), plus sanitizer leak gates.
- The product was renamed LittleMake → Kame on 2026-09-27 (commit `56b95bf5`)
  and the module moved to `src/go/kame`.
- The Solod/C toolchain and Cosmopolitan provisioning are retired
  (`docs/spec/021-cosmocc-provisioning.md`).

Sources: Git history (`b96f2dc6`, `56b95bf5`, `e6edccda`), [world/task:plan+001].
