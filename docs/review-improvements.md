# Improvements review

Reviewed 2026-10-03 against `TODO.md`, `TODO-GAPS.md`, the current specification
acceptance lists, implementation, and running native/WASM conformance suites.
This document distinguishes implemented behavior, documented boundaries, and
remaining work. It does not close a feature request merely by proposing a design.

## Release priorities

Fix reproducible correctness failures before expanding syntax. The current review
found missing WASM include expansion in discovered builds/inspections (KB-3) and
an aggregate publication output that can remain stale after adding a glob member
immediately after a rebuild. The aggregate defect (KB-4) was caused by Solod dropping timestamp nanoseconds
and is now fixed in the POSIX host: 21 sanitizer tests and all 102 publication
assertions pass. WASM build and inspection include expansion is fixed (KB-3): 24 parity assertions
and 21 WASM host sanitizer tests pass. Complete the requirement-by-requirement spec
comparison and full sanitizer/WASM checks before declaring this project done.

The directory-source bug KB-2 is fixed, as are stale expression command fixtures,
unquoted tool-path definitions in the repository build, and bootstrap test output
pollution from recursive Make directory messages. Native artifact writes now use
exclusive staging files. Native shutdown no longer waits 100 ms with no children. WASM declared file
inputs now query the embedding filesystem (KB-5), expanded spans service host
reads, and declarative write/yield effects publish before dependent recipes
(KB-6). T010-21 uses isolated outputs so native runs cannot mask missing WASM writes.
See the security and performance reviews for evidence and limitations.

## TODO.md disposition

| Request | Assessment and next action |
| --- | --- |
| Operand-aware operation errors and `out` coercion | Already marked complete; retain collection/type/error tests as acceptance evidence. |
| Target-scoped native tool resolution and `tools check` | Already implemented; keep unused missing tools harmless and checks free of recipe execution. |
| WASM tool paths, computed checks, and host-dependent inspection | Existing tests cover the implementation; include-loaded declarations are covered by T010-20. |
| Omit captured process output from causes | Implemented policy; do not reintroduce captured bytes in diagnostic wrapping. |
| Literal wildcard paths | Implemented for unquoted explicit rule-input paths: dependency-tracked wildcard expansion, recursive patterns and membership invalidation. Quoted inputs and plain Kash argv retain literal characters. T004-11 verifies both hosts. Expression-path shorthand remains a separate language decision. |
| Lazy singleton glob sources and future updates | Engine sources and canonical resource identities exist. Prove glob deduplication and membership freshness first; watcher delivery remains separate work. |
| Captures in bare target names | Already supported and verified by T014-02 on both hosts; exact targets take precedence and overlapping templates remain ambiguous. |
| Required/optional target arguments | Remaining design/implementation: define argument binding, default evaluation, escaping, identity, and precedence with invocation args. Do not approximate with file outputs. |
| Conditional forms | Kame lazy `if`/`and`/`or`/`match` and Kash control blocks exist. Rule declaration conditionals and gated includes remain a distinct gap (A4). |
| Improved templates | Spec 016 defines directives, verbatim strings, rendering, and `do render`; The previously missing CLI is implemented and covered by T016-01; portable template units cover directives and verbatim strings. Audit all acceptance clauses rather than introducing unrelated delimiter syntax. |
| More intuitive error names | Keep registered stable codes and improve concrete messages/tips first. Renaming public codes needs coordinated specs, diagnostics, fixtures, and compatibility policy. |
| Streaming standard library | Validate atom/batch/nested-source semantics, callbacks, bounds, fairness, cancellation, and ownership against specs 007/012. Process byte streaming alone does not prove library lifting. |
| Security model and process groups | Reviewed in `review-security.md`; targeted POSIX sanitizer tests pass. Grants are lexical host-operation controls, with documented symlink and child-process boundaries. |
| CLI experience | Unified runner is implemented. Prioritize accurate help, migration errors, examples, and discoverable grants; maintain native/WASM parity. |
| Live incremental updates | Native watch code exists, but the roadmap defers initial watch requirements and WASM reports unsupported. Audit native behavior separately; no claim of universal live-source delivery. |
| Managed services/provisioning | Research item. Define persistent process ownership, health, restart, teardown, and state before exposing commands. |
| General scripting/shell replacement | Kash and shared multi-fragment execution now address this. The standalone `examples/shell/01-values.kash` runs with exit 0 and identical native/WASM output, including repeated lazy-definition reads. Recovery, async joining, and interruption retain their conformance coverage. |
| File templating | Implemented through spec 016. Verify dependency capture, include cycles, source diagnostics, and native/WASM render parity. |
| Jobs/program/argv/runtime display | Existing progress reports activity and totals. Per-job argv/timing presentation needs bounded storage, clear stream behavior, and no leakage into JSON diagnostics. |
| Terminal color functions | Optional library feature; CLI presentation already has a color policy. Avoid mixing terminal escapes into canonical value display. |
| JavaScript API | The module ABI and JS loader exist. A documented embedding API should expose disposal, copied buffers, grants, host completion, and cancellation; CLI internals are not an API contract. |
| Python API | Research item; choose binding/transport and ownership model after the embedding contract is stable. |
| Alternative `<-` build syntax | Design proposal, not a correctness gap. Evaluate readability and formatter compatibility before changing existing rules. |
| Dependency sequencing with `,` | Define whether ordering is an edge or effect sequence; preserve parallel independence and failure propagation. Kash statement sequencing already has different semantics. |
| Learnability | Finish idioms/gotchas and independent value/rule/process lessons with executable smoke coverage. |
| Metaprogramming | A4 remains substantive work. Defaults and literal CLI/environment overrides are implemented; computed record lookup uses `get`. Optional generated declarations have a separate design proposal. Headers compose expressions and interpolated paths. |
| Error taxonomy | Existing codes are registered and machine-readable. Add actionable messages rather than speculative taxonomy churn. |

## Build-port gaps

Every original gap remains explicitly accounted for below. “Remaining” means
implementation/acceptance is not yet proven, not that documentation is sufficient.

| Gap | Current disposition and acceptance direction |
| --- | --- |
| A1 tool discovery without arbitrary parse-time effects | Implemented: dependency-tracked `(tool NAME)`, repeatable `--tool NAME=PATH`, one resolver lookup per name, executable file dependencies, and plan metadata. T007-11 covers native/WASM parity; the counted resolver sanitizer test verifies reuse. |
| A2 configuration defaults/overrides | Implemented: lazy `?=`, literal repeatable `--define`, and case-sensitive `KAME_<NAME>` environment values. Last CLI entry wins over environment and authored defaults; plan JSON reports effective provided values. T009-13 covers both hosts. |
| A3 target-scoped environment/build mode | Remaining: inherited environment must affect execution and fingerprints; debug/release artifacts need correct metadata without generated-source churn. |
| A4 declaration conditionals/gated includes | Optional `include? PATH` is implemented and covered by T004-12 on both hosts. Declaration conditionals and gated includes remain: expression/Kash control flow does not conditionally register build declarations. Repeated nonrecursive includes are legal. |
| A5 computed lookup / generators | Minimum lookup implemented through `get`; T007-10 pins typed configuration and planning parity. Optional generated declarations have a separate design proposal. |
| A6 continuations/multiline definitions | Implemented backslash LF/CRLF declaration continuations, preserving authored offsets, physical comments and recipe escapes; T004-10 verifies native/WASM execution and formatter idempotence. |
| B1 discovery unions/alternation/file filtering | Pattern unions are implemented: sorted, duplicate-free results and independent glob dependencies; T007-09 covers native/WASM evaluation and membership changes. Directory filtering uses metadata predicates. |
| B2 concatenated wildcard deadlock | Fixed (KB-1); keep both native and WASM multi-read regressions. |
| B3 multiple expression inputs | Already supported by the parser and now verified by T004-09 across multiple host-backed expressions, literal and interpolated paths. |
| B4 header path interpolation | Implemented through shared template rendering for path and quoted input tokens; T004-09 pins planning, expanded inspection and execution parity. |
| B5 scalar expansion/leading captures/bare captures | Implemented scalar expansion text and leading expression patterns. Bare target captures already existed; T014-02 verifies exact precedence and template ambiguity on both hosts. |
| C1 forced file rebuild | `--force` is an existing invocation override. Document it; a persistent per-rule always-rebuild declaration is a separate requested escape hatch. |
| C2 order-only prerequisites | Remaining rule semantics; maintain scheduling edges while excluding them from freshness/cache content identity where specified. |
| C3 automatic variables | Existing selectors cover inputs/outputs. Newer-input and stem equivalents need precise freshness/capture definitions; directory extraction can use pure path operations. |
| C4 one shell per recipe | Intentional behavior; document the porting implications and keep process-host multiline tests. |
| D1 capability-gated wildcard | Fixed diagnostic: missing or insufficient read authority names `--allow-read=ROOT` on native and WASM. T007-06/T010-08 cover denials and successful grants; examples use `do run --lang expr --allow-read=.`. |
| D2 relative `-f` beneath `-C` | Fixed on native and WASM, with regression tests. Align spec prose with the corrected behavior. |
| D3 idioms/gotchas | Added `docs/idioms-and-gotchas.md` covering selectors, dollars, shell text, references, empty definitions, interpolation, patterns, and grants. Expression examples were executed against the native CLI. |

## Completion gates

Use specification acceptance clauses as requirements, not just the existence of
numbered test files. The first full `make test` attempt passed portable package,
example-package, and Go CLI tests but failed five of 89 CLI suites. Isolated
reruns confirmed fixed table fixtures, bootstrap, self-build, and signal coverage;
publication graph growth now passes after the timestamp-precision fix. Rerun affected suites after fixes, then the
broad suite on the final checkout. Broad sanitizers and the explicit `test-wasm`
gate remain required. Cross-host source locations, includes, dry-run side effects,
and invocation capability inheritance need direct evidence.

Maintain this review as decisions become verified. Do not erase remaining design
items or mark the overall objective complete while named gaps or spec clauses
lack implementation evidence.

## Latest verification

A fresh `make test` passes the package, external example, Go CLI unit, and all
100 shell conformance suites in the current checkout. Broad Clang
AddressSanitizer passes all 359 portable package tests; the two external engine
examples also pass with sanitizers. Go CLI sanitizer compatibility tests pass
with the explicit `GOGC=off` setting documented in spec 013. The compiled CLI
LeakSanitizer conformance run remains a separate pending gate. These passing
checks do not close A3, A4, order-only prerequisites, or other requirements still
listed as remaining above.

The live CLI stream boundary now has before-completion proof: T012-02 observes
human/JSON stdout and stderr while a recipe remains blocked on a release file,
on both hosts, and observes native watch output before termination. This closes
KB-8; completion-order-only event assertions would not have detected it.

KB-9 remains a verified release blocker: an 8 MiB recipe output streams successfully natively but traps during WASM event JSON allocation. Live publication fixes latency, but does not yet prove bounded retained/transient memory.
