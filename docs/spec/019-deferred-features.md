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
| D11 | Pattern extensions | In progress: anonymous and positional captures, regex groups in expression patterns and file-rule outputs, bounded portable matching, `regex-match`, `capture`, `regex-replace`, and native/WASM parity are implemented. Remaining review covers all edge-case ownership, diagnostics and regression gates. See 029, T004-15 and T014-04. |
| D12 | Template extensions | In progress: the full syntax and acceptance contract is in 030; implementation and conformance remain. |
| D13 | Resource protocols | Explicit URI syntax, canonical resource identity, grants and dependency invalidation; at least filesystem and a second concrete protocol adapter. |
| D14 | Remote execution | Explicit remote executor selection, declared input/output transport, streaming/status/cancellation, secrets boundary and local/remote conformance. |
| D15 | Plugins | Versioned declarations and bounded operation invocation; explicit loading/policy; concrete native and JS-host adapters; type, ownership, failure and cancellation checks. |
| D16 | Platform execution | Windows process lifecycle and launcher; APE verification on the advertised non-Linux hosts; platform tests prove spawn, pipes, timeout and descendant cleanup. |
| D17 | Distribution tooling | Explicit update/latest selection alongside pinned defaults; native platform artifacts; package-manager integration; `kame init` without overwriting existing project files; provisioning/integrity tests. |
| D18 | Remaining library/ergonomic extensions | Specified tagged shell helpers and aliases, equality-value filtering, terminal color functions, dependency sequencing and alternative build syntax; formatting and execution tests. |

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
bounded eviction and inspection/cleanup on native and WASM. D08–D18 remain open. The preceding
initial-contract audit remains historical evidence, not completion of this work.
