# Completion of deferred features

This specification records the scope authorized after the initial implementation
audit. The features previously deferred in specs 001–018 and the remaining work
listed in the security, performance and improvements reviews are implementation
work. A design document or a successful unrelated test does not complete an item.

## Delivery and ownership

Implement each feature in a separate, reviewable `jj` commit. A specification may
precede its implementation. Keep unrelated working-copy edits intact. Shared
portable semantics belong in the portable runtime; host adapters implement I/O.
New owning objects require explicit disposal, cancellation and failure cleanup.
Authorization, dry-run, diagnostic privacy and authored source locations remain
part of every extension. Existing commands and canonical value behavior remain
compatible unless a new specification explicitly defines a migration.

The initial roadmap's restriction against speculative abstractions does not
prohibit these requested implementations. Architectural interfaces need concrete
adapters and integration evidence rather than placeholder methods.

## Required work

| ID | Feature | Completion evidence |
| --- | --- | --- |
| D01 | WASM watch | Complete: retained graph roots, file/glob invalidation during active work, source reload and repair, shared roots, grants, stream publication, cancellation and disposal; native/WASM public conformance. See 020. |
| D02 | Managed services and provisioning | Complete: typed lifecycle metadata; prerequisite lifetime; readiness/health/restart/stop ownership; bounded logs; process-tree cleanup; correlated lifecycle event parity on native, WASM CLI, and the public Runtime embedding API. See 024, T010-31, and `TestRuntimeEmbeddingServiceLifecycleAndCleanupEvents`. |
| D03 | Generated declarations | Complete: bounded typed batches; full validation before registration; generator provenance and dependencies; deterministic watch replacement; authored diagnostics; native/WASM parity; cycle, limit, and failure-cleanup coverage. See 025, T010-27, T009-14 and T010-23. |
| D04 | Cache lifecycle and backends | Complete: cross-process miss locking with failure, interruption and stale-owner recovery; bounded eviction and explicit inspection/cleanup; local and portable in-memory backends with identity and atomic-publication conformance. See T008-04 and T010-28 through T010-30. |
| D05 | Release integrity | Complete: pinned and verified compiler provisioning (021); operator-key Ed25519 manifests; provenance creation and verification; tamper/wrong-key rejection; actual staged release tests. Keys are provided by the release operator, never committed. |
| D06 | Target arguments | Complete: required and optional named arguments, literal defaults, distinct capture maps, argument-sensitive identity, inspection/planning and execution parity, dry-run, multiple targets, and watch invalidation. T009-15 and T010-24 pass. See 022. |
| D07 | Wildcard expressions and sources | Complete: quoted literal paths, sorted recursive expansion, empty matches, singleton shared dependencies, membership invalidation, and native/WASM watch updates. T004-11, T009-14 and T010-23 pass. See 023. |
| D08 | Parser and runtime performance | Complete: borrowed parsing allocation savings; sorted allocator-owned graph/scope indexes with zero-allocation lookup checks; hostless pure-evaluation request signaling; glob traversal pruning with deep-sibling visit evidence; and five-sample native/WASM workload timings. See 026 and `docs/review-performance.md`. |
| D09 | Jobs and CLI presentation | Complete: requirements enumerated in 027. Human/JSON cancellation, bounded direct/pipeline process display, redirected progress color policy, native/WASM event parity, and runnable documentation lessons are covered. See 027, T003-02, T009-01, T009-04, T010-14, and T013-04. |
| D10 | Embedding APIs | Complete: importable JavaScript/WASM and asynchronous Python/CLI APIs cover compile/evaluate/build/watch, copied results, grants, asynchronous host completion, cancellation, deterministic disposal and repeated lifecycle tests. See 028 and T010-32/T010-33. |
| D11 | Pattern extensions | Complete: anonymous and positional captures, regex groups in expression patterns and file-rule outputs, bounded portable matching, `regex-match`, `capture`, and `regex-replace`; allocator-checked package tests and native/WASM acceptance in T004-15 and T014-04. See 029. |
| D12 | Template extensions | Complete: matched labels, match blocks, trim markers, inline blocks, auto style, PowerShell/batch comments, canonical formatting and loop index/key bindings; native/WASM conformance in T016-02. See 030. |
| D13 | Resource protocols | Complete: canonical `file:` and `mem:` identity, scoped grants, dependency invalidation, URI-aware filesystem traversal, native/POSIX and WASM host mappings, and portable memory storage; native/WASM CLI acceptance in T031-01 plus host, evaluator and program suites. See 031. |
| D14 | Remote execution | Complete: explicit per-file-rule executor selection; versioned host capability checks; workspace-relative input/output artifacts with SHA-256 input digests; retry-stable idempotency; scoped environment; validated output publication; failure, timeout, cancellation and truncation handling. Unsupported forwarded transport fails closed. See 032 and seven remote cases in `program` tests. |
| D15 | Plugins | Complete: explicit versioned operation registrations, canonical bounded-value JSON, Node native-process and JavaScript-callback adapters, capability policy, and cancellation/ownership rules. See 033, T033-01, codec tests, registry identity tests, and the WASM runtime request test. |
| D16 | Platform execution | In progress: the Node/WASM host now has Windows stream pipelines and process-tree termination, with a Windows-only descendant test; Windows-native launch, native-host lifecycle, and APE conformance on advertised non-Linux hosts remain. |
| D17 | Distribution tooling | In progress: signed `Makefile.bootstrap` and `VERSION` release assets, non-overwriting `kame init`, the opt-in `KAME_VERSION=latest` channel, and signed launcher handoff for newer selected releases are specified and covered. Native platform artifacts and package-manager integration remain. |
| D18 | Remaining library/ergonomic extensions | Complete: equality-value `filter`/`filter-out`, `sh`/`shellrun` aliases, the `<-` alternative rule separator, ANSI terminal style functions, safely quoted `shell-template` interpolation, and comma-separated prerequisite sequencing have specified semantics and native/WASM acceptance coverage. |

The explicit boundaries in this table are a finite work inventory. General
phrases such as "library breadth" do not authorize an unspecified infinite API.
Named deferred clauses in the original specs are included even when a broader
row groups them. Feature specifications must enumerate those clauses before
their implementation is declared complete.

## Verification and completion

Each implementation updates its original spec, the test catalog, native/WASM
build gates and this document's evidence table. Test scripts used directly by
Make or Kame must be executable. Host-specific conformance requires evidence
from that host; a cross-compiled artifact alone does not prove execution.

Before final completion, all rows require direct implementation evidence and
acceptance results. Run the full native, sanitizer/leak and WASM gates at a stable
revision, verify public embedding and actual release artifacts, and update the
review documents with the resulting behavior and remaining material boundaries.

## Evidence

All D01–D18 items were open at introduction of this specification. D01 is now
complete: retained portable roots, additive WASM watch ABI calls, JavaScript
polling and invalidation, and fourteen passing native/WASM live scenarios cover
shared roots, scoped environments across reload, optional-include creation,
settled-root idleness, obsolete-input subscription cleanup, active-child
disposal and bounded output under pipe pressure. T010-25 covers public ABI
query/copy, malformed input, retained-root invalidation, allocation-failure
diagnostics, instance isolation and disposal. T009-14 and T010-23 verify grant
preservation through invalidation and source reload, with denied reads blocked
before recipe effects. D05 is complete: the versioned, digest-verified
Cosmocc provisioner has fixture coverage, and actual staged release tests
verify operator-key Ed25519 signatures, provenance and fail-closed tamper and
wrong-key behavior. D06 now has required and optional named task arguments, default
binding, dependency and recipe scope, plan JSON, argument-sensitive identity,
and native/WASM execution, dry-run and watch coverage in T009-15 and T010-24.
D07 is covered by first-class wildcard inputs and shared lazy glob dependencies;
T004-11, T009-14 and T010-23 pass expansion, quoted-literal,
membership-addition/removal and watch-update checks on native and WASM. D02's
typed lifecycle metadata is parsed and bounded before dependency recipes run;
native and forwarded readiness probes gate dependents, readiness timeouts tear
down the service, and configured SIGTERM-to-SIGKILL grace periods are honored
by native and JavaScript CLI hosts. Native and forwarded health probes now
restart unhealthy services within configured bounds, and startup failures
retry before dependents resume. Per-service log retention is bounded on native
and forwarded hosts; Go tests and T010-26 cover these paths. T010-31 now verifies
correlated provisioning, start, readiness, health, stop, and terminal events,
including matching human and JSON output across native and forwarded hosts.
`TestRuntimeEmbeddingServiceLifecycleAndCleanupEvents` exercises the public
embedding request/completion contract through process start, target release,
cancellation, reap, and terminal event delivery. D04 now has
native and WASM `do cache list|clean` commands plus bounded 1024-record
eviction on successful publication. Native POSIX CLI processes also serialize
cold task misses with bounded advisory-lock stripes and crash release. Forwarded
JavaScript hosts now hold per-key lock directories through lookup and
publication, reclaim stale owners, serialize two concurrent Node hosts, and
release locks on failure, owner interruption and waiter cancellation;
T010-30 covers these paths. The
portable in-memory filesystem stores complete task records and reuses them
through the shared identity and validation path in T008-04. T010-28 covers
bounded eviction and inspection/cleanup on native and WASM. D15 now has a
versioned plugin registry with all-or-nothing declaration validation,
capability and arity enforcement, canonical value serialization, request
generation/attempt correlation, and Go/WASM host bridges. The Node.js embedding
supports both callbacks and direct-argv native processes; T033-01 covers every
canonical value kind, identity and protocol rejection, limits, process
termination, cancellation, disposal, and evaluate/build/watch configuration.
The core codec and plugin registry tests also verify allocator ownership and
version-sensitive operation identity. D16–D17 remain open. The
Node's Windows host path now connects pipeline stages with child streams, uses
`taskkill /T /F` for process-tree cancellation, and waits for tree-stop and
direct-child completion. T033-01 retains a Windows-only descendant cleanup
case; this workspace has no Windows runner, so that case is not yet execution
evidence. D16's native launcher, platform lifecycle matrix, and non-Linux APE
verification remain open. The initial-contract audit remains historical
evidence, not completion of this work.
D18's equality-value shorthand is implemented: `(filter LIST VALUE)` retains
strict scalar-equal items, and `filter-out` removes them with the same numeric,
cross-kind, and invalid-composite rules as `eq`. T007-02 now covers both
operations, numeric int/float equivalence, kind mismatch, and invalid list
values. `sh` and `shellrun` now alias `shell` with the same run grant, result,
and phase behavior; T007-06 covers allowed and denied execution. The same test
also verifies native captured-script completion. The alternative `<-` build
separator has the same dependency semantics as `:` and canonicalizes to `:`;
T004-09 verifies planning and execution parity on native and WASM. ANSI terminal
style functions wrap text in foreground or bold/dim sequences; T007-03 verifies
all styles, selective nesting resets, nonstring rejection, canonical value
display and native/WASM output. `shell-template` combines trusted fragments
with POSIX-quoted dynamic strings and shares the `shell` result and phase
contract; T007-06 verifies injection resistance and result parity on native and
WASM. Comma-separated prerequisite groups request later dependencies only when
all earlier group members are current, while whitespace members remain parallel;
T006-08 verifies ordering, parallel independence, failure blocking, and
formatting on native and WASM. Sequence boundaries are part of cached-task
identity and are exposed in plan JSON.
D17 packages `Makefile.bootstrap` and `VERSION` inside the signed release
manifest. `kame init` verifies the bootstrap asset, creates a pinned sidecar
atomically, preserves an existing `Makefile`, and refuses to replace an
existing sidecar. The opt-in `KAME_VERSION=latest` channel fetches the selector
on each invocation, then uses the ordinary signed manifest for the selected
immutable release; pinned versions remain the default. When that version is
newer than the running launcher's stamp, it atomically caches and verifies the
release's launcher and hands off to its pinned version before backend dispatch.
T015-01 covers selector resolution, no-provisioning version output,
disabled-download rejection, verified launcher handoff and cached-launcher
tampering. T015-04 exercises launcher handoff against the actual signed
release. Native platform artifacts and package-manager integration remain
open.
