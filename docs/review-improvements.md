# Improvements review

Reviewed 2026-10-03 against `TODO.md`, `TODO-GAPS.md`, the current specification
acceptance lists, implementation, and running native/WASM conformance suites.
This document distinguishes implemented behavior, documented boundaries, and
remaining work. It does not close a feature request merely by proposing a design.

## Release priorities

Fix reproducible correctness failures before expanding syntax. The current review
found missing WASM include expansion in discovered builds/inspections (KB-3) and
an aggregate publication output that can remain stale after adding a glob member
immediately after a rebuild. The latter failed in the full suite and an isolated
rerun; retain it as a correctness issue until its freshness decision is explained
and regression coverage passes. Complete the requirement-by-requirement spec
comparison and full sanitizer/WASM checks before declaring this project done.

The directory-source bug KB-2 is fixed, as are stale expression command fixtures,
unquoted tool-path definitions in the repository build, and bootstrap test output
pollution from recursive Make directory messages. Native artifact writes now use
exclusive staging files. Native shutdown no longer waits 100 ms with no children.
See the security and performance reviews for evidence and limitations.

## TODO.md disposition

| Request | Assessment and next action |
| --- | --- |
| Operand-aware operation errors and `out` coercion | Already marked complete; retain collection/type/error tests as acceptance evidence. |
| Target-scoped native tool resolution and `tools check` | Already implemented; keep unused missing tools harmless and checks free of recipe execution. |
| WASM tool paths, computed checks, and host-dependent inspection | Existing tests cover the implementation; include-loaded declarations still need KB-3 coverage. |
| Omit captured process output from causes | Implemented policy; do not reintroduce captured bytes in diagnostic wrapping. |
| Literal wildcard paths | Remaining language work: recognize a wildcard resource in input position and keep a plain Kash wildcard literal. Define expression-path semantics without silently changing argv. |
| Lazy singleton glob sources and future updates | Engine sources and canonical resource identities exist. Prove glob deduplication and membership freshness first; watcher delivery remains separate work. |
| Captures in bare target names | Remaining parser/runtime work; reuse pattern matching and ambiguity rules, with exact targets taking precedence. |
| Required/optional target arguments | Remaining design/implementation: define argument binding, default evaluation, escaping, identity, and precedence with invocation args. Do not approximate with file outputs. |
| Conditional forms | Kame lazy `if`/`and`/`or`/`match` and Kash control blocks exist. Rule declaration conditionals and gated includes remain a distinct gap (A4). |
| Improved templates | Spec 016 defines directives, verbatim strings, rendering, and `do render`; T016 covers these. Audit all acceptance clauses rather than introducing unrelated delimiter syntax. |
| More intuitive error names | Keep registered stable codes and improve concrete messages/tips first. Renaming public codes needs coordinated specs, diagnostics, fixtures, and compatibility policy. |
| Streaming standard library | Validate atom/batch/nested-source semantics, callbacks, bounds, fairness, cancellation, and ownership against specs 007/012. Process byte streaming alone does not prove library lifting. |
| Security model and process groups | Reviewed in `review-security.md`; targeted POSIX sanitizer tests pass. Grants are lexical host-operation controls, with documented symlink and child-process boundaries. |
| CLI experience | Unified runner is implemented. Prioritize accurate help, migration errors, examples, and discoverable grants; maintain native/WASM parity. |
| Live incremental updates | Native watch code exists, but the roadmap defers initial watch requirements and WASM reports unsupported. Audit native behavior separately; no claim of universal live-source delivery. |
| Managed services/provisioning | Research item. Define persistent process ownership, health, restart, teardown, and state before exposing commands. |
| General scripting/shell replacement | Kash and shared multi-fragment execution now address this. Confirm repeated lazy-definition reuse, recovery, async joining, and interruption before teaching complete standalone lessons. |
| File templating | Implemented through spec 016. Verify dependency capture, include cycles, source diagnostics, and native/WASM render parity. |
| Jobs/program/argv/runtime display | Existing progress reports activity and totals. Per-job argv/timing presentation needs bounded storage, clear stream behavior, and no leakage into JSON diagnostics. |
| Terminal color functions | Optional library feature; CLI presentation already has a color policy. Avoid mixing terminal escapes into canonical value display. |
| JavaScript API | The module ABI and JS loader exist. A documented embedding API should expose disposal, copied buffers, grants, host completion, and cancellation; CLI internals are not an API contract. |
| Python API | Research item; choose binding/transport and ownership model after the embedding contract is stable. |
| Alternative `<-` build syntax | Design proposal, not a correctness gap. Evaluate readability and formatter compatibility before changing existing rules. |
| Dependency sequencing with `,` | Define whether ordering is an edge or effect sequence; preserve parallel independence and failure propagation. Kash statement sequencing already has different semantics. |
| Learnability | Finish idioms/gotchas and independent value/rule/process lessons with executable smoke coverage. |
| Metaprogramming | A2/A4/A5 and B3/B4 remain substantive work, not solved by putting shell syntax in recipes. Keep generated declarations visible to inspection and deterministic registration. |
| Error taxonomy | Existing codes are registered and machine-readable. Add actionable messages rather than speculative taxonomy churn. |

## Build-port gaps

Every original gap remains explicitly accounted for below. “Remaining” means
implementation/acceptance is not yet proven, not that documentation is sufficient.

| Gap | Current disposition and acceptance direction |
| --- | --- |
| A1 tool discovery without arbitrary parse-time effects | Remaining: dependency-tracked resolver plus overrides, resolved once and visible in plans. Quoted shell-text definitions now make the repository build parse, but do not satisfy resolver acceptance. |
| A2 configuration defaults/overrides | Remaining: define-only-if-absent and typed invocation overrides applied before planning, with effective values in inspection. Resolve conflict/precedence explicitly. |
| A3 target-scoped environment/build mode | Remaining: inherited environment must affect execution and fingerprints; debug/release artifacts need correct metadata without generated-source churn. |
| A4 declaration conditionals/gated includes | Remaining; expression/Kash control flow does not conditionally register build declarations. Repeated nonrecursive includes are now legal, so the old blanket repeated-include prohibition is stale. |
| A5 computed lookup/generated rules | Remaining design and implementation; record lookup is useful but does not by itself generate rule declarations. Preserve no-effects compilation. |
| A6 continuations/multiline definitions | Remaining parser/formatter work; verbatim template literals do not prove general continued rule headers or definitions. |
| B1 discovery unions/alternation/file filtering | Remaining; single-pattern glob plus collection filtering is a workaround. Prove union dependency membership and exclusion behavior. |
| B2 concatenated wildcard deadlock | Fixed (KB-1); keep both native and WASM multi-read regressions. |
| B3 multiple expression inputs | Remaining: independently parse/evaluate/flatten each token with ordered dependencies; do not concatenate text into one accidental expression. |
| B4 header path interpolation | Remaining: render path tokens from their Kame values and retain dynamic edges; reject invalid types precisely. |
| B5 scalar expansion/leading captures/bare captures | Separate parser, coercion, and target-matching changes. Preserve strict string operations and matcher/expansion classification tests. |
| C1 forced file rebuild | `--force` is an existing invocation override. Document it; a persistent per-rule always-rebuild declaration is a separate requested escape hatch. |
| C2 order-only prerequisites | Remaining rule semantics; maintain scheduling edges while excluding them from freshness/cache content identity where specified. |
| C3 automatic variables | Existing selectors cover inputs/outputs. Newer-input and stem equivalents need precise freshness/capture definitions; directory extraction can use pure path operations. |
| C4 one shell per recipe | Intentional behavior; document the porting implications and keep process-host multiline tests. |
| D1 capability-gated wildcard | Intentional; confirm denial names the missing authority and show `do run --lang expr --allow-read` in examples. |
| D2 relative `-f` beneath `-C` | Fixed on native and WASM, with regression tests. Align spec prose with the corrected behavior. |
| D3 idioms/gotchas | Remaining documentation deliverable: selectors, literal dollars, quoted shell text, list references, empty definitions, interpolation, pattern expansion, and grants. Test examples before publishing them as runnable. |

## Completion gates

Use specification acceptance clauses as requirements, not just the existence of
numbered test files. The first full `make test` attempt passed portable package,
example-package, and Go CLI tests but failed five of 89 CLI suites. Isolated
reruns confirmed fixed table fixtures, bootstrap, self-build, and signal coverage;
publication graph growth still fails. Rerun affected suites after fixes, then the
broad suite on the final checkout. Broad sanitizers and the explicit `test-wasm`
gate remain required. Cross-host source locations, includes, dry-run side effects,
and invocation capability inheritance need direct evidence.

Maintain this review as decisions become verified. Do not erase remaining design
items or mark the overall objective complete while named gaps or spec clauses
lack implementation evidence.
