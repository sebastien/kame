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
| D01 | WASM watch | Retained graph roots, file/glob invalidation during active work, source reload and repair, shared roots, grants, stream publication, cancellation and disposal; native/WASM public conformance. See 020. |
| D02 | Managed services and provisioning | Typed service configuration; start/readiness/health/restart/stop ownership; prerequisite lifetime; bounded logs; interruption and process-tree cleanup on native and WASM. |
| D03 | Generated declarations | Bounded typed batches; complete validation before registration; generator dependency tracking; deterministic replacement; authored diagnostics; native/WASM parity and failure cleanup. |
| D04 | Cache lifecycle and backends | Cross-process miss locking, failure/crash recovery, bounded eviction and explicit inspection/cleanup; local and second concrete backend; identity and atomic-publication conformance. |
| D05 | Release integrity | Pinned and verified compiler provisioning; signed manifests and provenance creation/verification; tamper/wrong-key rejection; actual staged release tests. Keys are provided by the release operator, never committed. |
| D06 | Target arguments | Required `{name}` and optional `{name=value}` standalone arguments; typed binding/defaults, capture distinction, identity, planning and execution parity. |
| D07 | Wildcard expressions and sources | Explicit unquoted expression paths expand globs; quoted text stays literal; singleton dependency sources track membership and future updates. |
| D08 | Parser and runtime performance | Borrow/adopt source lifetimes with zero-copy parsing where decoding is unnecessary; remove unconditional pure-expression waits; indexed graph/scope lookup; pruned glob traversal; allocation and workload measurements. |
| D09 | Jobs and CLI presentation | Bounded per-job program/argv/runtime display, cancellation state, stable JSON separation, color policy and executable lessons. |
| D10 | Embedding APIs | Public JS and Python APIs for compile/evaluate/build/watch, copied values, grants, asynchronous host completion, cancellation and deterministic disposal; reusable-instance tests. |
| D11 | Pattern extensions | Anonymous and positional rule captures; runtime pattern construction; regular-expression groups/operations and capture processors; anchored matching, bounds, coercion, source style and parity. |
| D12 | Template extensions | Labeled ends, match clauses, trim markers, inline blocks, content/style inference and additional comment conventions, canonical formatting and loop index/key bindings. |
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

All D01–D18 items were open at introduction of this specification. D01 now has
retained portable roots, additive WASM watch ABI calls, JavaScript polling and
invalidation, and five passing native/WASM live scenarios; its remaining
acceptance cases are listed in 020. D02–D18 remain open. The preceding
initial-contract audit remains historical evidence, not completion of this work.
