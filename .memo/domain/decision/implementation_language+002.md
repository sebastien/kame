---
identity: e6faa580-9526-4998-b038-181cb6cbd423
created: 2026-11-25T18:44:08.527262+00:00
updated: 2026-11-25T18:44:48.320712+00:00
status: accepted
date: "2026-11-12"
supersedes: "domain/decision:implementation_language+001"
---
# Replace Solod with pure Go

Status: accepted; supersedes [domain/decision:implementation_language+001].
Date: 2026-11-12 (commit `82fdafbc`); the transition was declared complete on
2026-11-23 (commit `e6fd6ded`).

## Context

The Solod build transpiled to C. Native builds carried C host implementations,
manual allocation with per-instance arena quotas, and APE packaging that
depended on a pinned compiler archive.

## Alternatives

- **Keep Solod, C and APE distribution** (the status quo).
- No other alternative is recorded.

## Decision

Build the CLI and the embedded engine with the standard Go toolchain:

- **Native:** cgo-free (`CGO_ENABLED=0`) binaries for Linux and macOS, x86_64
  and arm64.
- **WebAssembly:** a Go WASI reactor (`GOOS=wasip1 GOARCH=wasm`,
  `-buildmode=c-shared`) that keeps the existing primitive ABI and Node host.
- **Windows:** a launcher bundled with the Node/WASM assets.

## Rationale

The commit records the outcome rather than a motive: removing the Solod
dependency and C hosts while preserving cancellation, cleanup and native/WASM
output parity, and replacing arena quotas with Go GC plus module-wide heap
accounting.

## Consequences

- Go manages memory, but logical ownership still decides who may retain,
  mutate or release state ([domain/architecture:kame_system]).
- Release integrity relies on signed manifests and provenance
  (`docs/spec/015-distribution.md`); no compiler archive is downloaded.
- Native tests moved to Go; the shell conformance suite stays.
- Code and build files no longer depend on Solod or Cosmopolitan, and no
  compiler archive is downloaded or executed (`021`).

Sources: Git history (`82fdafbc`, `e6fd6ded`),
`docs/spec/001-architecture.md`, `docs/spec/021-cosmocc-provisioning.md`.
