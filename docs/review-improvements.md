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
| Conditional forms | Kame lazy `if`/`and`/`or`/`match` and Kash control blocks exist. Top-level `when`/`otherwise`/`end` now select declarations and includes before registration (A4; T004-13). |
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
| Metaprogramming | A4 declaration selection, defaults, and literal CLI/environment overrides are implemented; computed record lookup uses `get`. Optional generated declarations have a separate design proposal. Headers compose expressions and interpolated paths. |
| Error taxonomy | Existing codes are registered and machine-readable. Add actionable messages rather than speculative taxonomy churn. |

## Build-port gaps

Every original gap remains explicitly accounted for below. “Remaining” means
implementation/acceptance is not yet proven, not that documentation is sufficient.

| Gap | Current disposition and acceptance direction |
| --- | --- |
| A1 tool discovery without arbitrary parse-time effects | Implemented: dependency-tracked `(tool NAME)`, repeatable `--tool NAME=PATH`, one resolver lookup per name, executable file dependencies, and plan metadata. T007-11 covers native/WASM parity; the counted resolver sanitizer test verifies reuse. |
| A2 configuration defaults/overrides | Implemented: lazy `?=`, literal repeatable `--define`, and case-sensitive `KAME_<NAME>` environment values. Last CLI entry wins over environment and authored defaults; plan JSON reports effective provided values. T009-13 covers both hosts. |
| A3 target-scoped environment/build mode | Recipe inheritance, overrides, shared conflicts and cached-task fingerprints are implemented. Per-artifact debug/release metadata and unchanged-build incrementality now pass T013-05. Persistent scoped-file freshness now covers changed and removed settings; expression/definition configuration remains. |
| A4 declaration conditionals/gated includes | `when`/`otherwise`/`end` selects declarations before registration; inactive includes are never read. T004-13 passes 35 cases per host. Optional `include? PATH` retains T004-12 coverage; repeated nonrecursive includes remain legal. |
| A5 computed lookup / generators | Minimum lookup implemented through `get`; T007-10 pins typed configuration and planning parity. Optional generated declarations have a separate design proposal. |
| A6 continuations/multiline definitions | Implemented backslash LF/CRLF declaration continuations, preserving authored offsets, physical comments and recipe escapes; T004-10 verifies native/WASM execution and formatter idempotence. |
| B1 discovery unions/alternation/file filtering | Pattern unions are implemented: sorted, duplicate-free results and independent glob dependencies; T007-09 covers native/WASM evaluation and membership changes. Directory filtering uses metadata predicates. |
| B2 concatenated wildcard deadlock | Fixed (KB-1); keep both native and WASM multi-read regressions. |
| B3 multiple expression inputs | Already supported by the parser and now verified by T004-09 across multiple host-backed expressions, literal and interpolated paths. |
| B4 header path interpolation | Implemented through shared template rendering for path and quoted input tokens; T004-09 pins planning, expanded inspection and execution parity. |
| B5 scalar expansion/leading captures/bare captures | Implemented scalar expansion text and leading expression patterns. Bare target captures already existed; T014-02 verifies exact precedence and template ambiguity on both hosts. |
| C1 forced file rebuild | Implemented: `always ./output : ./input` preserves file artifacts and output checks while rerunning once per requested-root epoch. T006-03 covers both hosts; parser/program sanitizer suites pass 9/97 tests, including repeated roots and diamond sharing. `--force` remains an invocation override. |
| C2 order-only prerequisites | Implemented with `|`: scheduling/failure edges, freshness/cache exclusions and explicit-read/normal-edge upgrades. T006-04 covers both hosts. |
| C3 automatic variables | Implemented: `@<?` selects newer normal file inputs; explicit pattern captures supply stems and `dirname` supplies input/output directories. T006-07 verifies both hosts, including scoped freshness, oldest/missing outputs, equality, force and phase rejection. |
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

The earlier full `make test` passed package, external example, Go CLI unit, and
all 100 shell conformance suites before optional includes and the live/large
stream regressions were added. The broad Clang AddressSanitizer snapshot passed
359 portable package tests; the two external engine examples passed too. Go CLI sanitizer compatibility tests pass
with the explicit `GOGC=off` setting documented in spec 013. The compiled CLI
LeakSanitizer conformance now passes all 102 selected CLI suites under a
verified ASAN/UBSAN binary; the debug-binary metadata suite is intentionally
excluded. Current stream changes also pass 28 WASM host and 96 program sanitizer
tests, plus Go CLI sanitizer tests. These checks do not close A3, A4, order-only
prerequisites, or other requirements listed as remaining above.

The live CLI stream boundary now has before-completion proof: T012-02 observes
human/JSON stdout and stderr while a recipe remains blocked on a release file,
on both hosts, and observes native watch output before termination. This closes
KB-8; completion-order-only event assertions would not have detected it.

KB-9 is fixed: bounded instance-block reuse, binary encoding scratch and JS
buffers now pass 8 MiB stdout/stderr parity on both hosts in human/JSON mode.
Failure truncation metadata and bounded cached replay are verified too. True
logical-heap exhaustion now returns static `NO_MEMORY` through WASM ABI
checkpoints, using native exception handling without host imports. T010-22
checks undersized compilation, asynchronous completion, 100 failed slot reuse
cycles, surviving instances, temporary module-heap recovery and unrelated traps.
Validation uses an isolated checkout to exclude concurrent unfinished Kash and
source-formatting changes in the shared working copy.

C1 now has a persistent file-rule escape hatch: `always` bypasses freshness
without turning outputs into named tasks. The new native/WASM regression passes
15 assertions for reruns, diamond sharing, multiple artifacts, captures, cat, inspection,
formatting and pre-effect rejection. Parser and program ASAN/UBSAN suites pass
9 and 97 tests respectively. The previous 102-suite compiled CLI leak gate
predates this new feature; focused sanitizer conformance and existing freshness
regressions verify this change separately. The new 15-assertion native/WASM
suite also passes against the compiled sanitizer CLI with leak detection enabled
and ASAN symbols verified.

The A3 recipe-environment foundation now passes 21 native/WASM assertions for
inheritance, local/last overrides, root isolation, cached-task identity,
equivalent assignment order, shared-context conflicts, dynamic file producers,
retries, inspection and invalid-assignment preflight. The same suite passes with
compiled CLI ASAN/UBSAN and leak detection; instrumentation is verified before
and after execution. Rule parser and program sanitizer suites pass 11 and 99
tests. Existing freshness, dependency, always-rule, cache, parser and catalog
suites pass separately. This does not close A3: scoped file recipes currently
rebuild conservatively and expression/definition configuration remains global.

Per-artifact build mode is now bound by native compiler flags, while the shared
version source contains only target-independent metadata. Real debug/release
binaries report their respective modes, and T013-05 verifies that changing the
mode does not rewrite generated source or relink unchanged Kame file outputs.
Go CLI ASAN tests pass, and the compiled sanitizer CLI passes all five build-mode
assertions with leak detection and instrumentation verified. Four existing
catalog/help/binary/environment conformance suites pass too. This closes A3's
concrete repository build-mode example;
it does not yet close general scoped-file freshness.

Scoped-file freshness now persists digests bound to successful output timestamps.
Native and WASM use the same portable decision with nanosecond host metadata.
T006-06 extends the environment tests with unchanged, changed and removed-scope
checks on isolated host fixtures. Expression/definition configuration remains open.

The scoped-file change passes 31 native/WASM environment assertions both with
normal and compiled sanitizer CLIs, including corrupt-record and replaced-output
recovery. ASAN/UBSAN program tests pass all 99 cases. Existing file freshness,
dependency, always-rule, declarative publication, native/WASM cache and catalog
checks pass. The broad suite has not been rerun for this change.

A follow-up fixes content-equal `yield` overriding `always` freshness on the
native host. T006-03 now checks that an equal payload republishes an intentionally
old output on both hosts; all 18 assertions pass.

Order-only prerequisites pass 20 isolated native/WASM assertions, 64 engine
sanitizer tests, 13 rule-parser sanitizer tests and 100 program sanitizer tests.
The program test verifies sequential root epochs rerun ordering work while
skipping the unchanged artifact. Existing freshness, dependency, always-rule,
environment, native/WASM cache, plan and catalog suites pass separately.
The compiled sanitizer CLI passes all 19 order-only assertions with leak detection;
ASAN instrumentation is verified before and after the run. The normal run includes
one additional binary-build assertion. Broad full-suite coverage remains pending.

The forwarded file metadata protocol now has embedding-host coverage as well as
CLI coverage. All 28 `host/wasm` tests pass under ASAN/UBSAN after updating three
fixtures for timestamp-array and cache-get requests. The output-verification
fixture accepts a decimal timestamp beyond JavaScript's safe integer range and
still checks missing outputs, host failures and completion ordering. Spec 010
documents ABI kind 20 and the cache responses required by forwarding hosts.

C3 now passes all 25 native/WASM assertions against the compiled sanitizer CLI
with leak detection and ASAN symbols verified before and after execution.
Expression/evaluator sanitizer suites pass 27/66 tests; program tests pass 100
cases. WASM embedding sanitizer tests pass 29 cases, including force descriptor
transport. The equality test exposed a missing WASM `--force` option transport;
both primary builds and mixed runner sessions now pass it to portable runtime
options. The scoped fingerprint uses stable authored context so publication's
change to the newer-input subset does not trigger a redundant rebuild.
Existing freshness, dependency, always-rule, order-only, environment, WASM
cache, mixed-session and catalog suites all pass separately against the sanitizer
CLI. The catalog verifies 442 assertions. Full-suite completion remains pending.

KB-10 fixes the mixed WASM runner's failure-summary clock. Its reproduction
changed from epoch-sized elapsed seconds to 0.047 s; T017-07 passes all 78
assertions with the compiled sanitizer CLI and WASM, including the bounded
invocation-duration check. This is a correctness check, not a latency benchmark.

WASM process publishers now preserve allocator diagnostics and contain errors
from start/stream callbacks. Shell and argv hosts kill their process groups and
wait for child close before propagating failure. T010-22 additionally runs real
unbounded stdout/stderr children against a 256 KiB logical heap and checks
clean `NO_MEMORY` reporting plus child reaping on both launch paths.

Declaration-predicate work exposed a caller-context bug in inline lazy
definition evaluation: reading a global definition as one function operand
replaced the function scope, so its following parameter operand failed with
`REF_MISSING`. The evaluator now restores the caller engine, scope and request
queue after inline definition evaluation. A direct regression and all 67
evaluator ASAN/UBSAN tests pass in the isolated conditional-work checkout.


A4 declaration conditions now select source fragments before registration on
native and WASM. All 35 public cases pass per host, with the native executable
instrumented by ASAN/UBSAN. Predicate configuration contains only selected
lazy definitions; it does not reparse value statements as rule syntax or execute
statements. Earlier included and explicit-file configuration participates in
source order. Portable parser/program/host sanitizer suites pass 16/102/30 tests.
Uncomposed evaluator registration is explicitly rejected, and host predicates
have no capabilities. Full-suite validation remains pending.


The declaration-loader regression checks exposed two preflight diagnostic issues:
native JSON inspection selected the error stream, while WASM legacy composition
reparsed a malformed include and attributed a trailing error to the parent.
The loaders now validate each authored source before composition and use the
invocation diagnostic writer. T010-20 passes all 24 native/WASM parity assertions;
optional includes, configuration, and shared sessions pass 19/20/78 assertions.
The evaluator sanitizer suite passes 68 cases, including rejection of raw
conditional markers. The isolated catalog passes 446 checks. These declaration checks used the isolated checkout while the shared recipe-settings
work was still being integrated. The subsequent integration checks below describe
the shared checkout. Full-suite validation remains pending.


The shared checkout now builds native and WASM after correcting recipe-settings
span conversions, allocating returned rule frames, and connecting the previously
unused Kash recipe helpers. In-progress metadata integration passes 22 public
cases per host, including selected shells, computed environment metadata, failure
short-circuiting, file outputs, dry-run, inspection, and once-only stdout. Native
cases use the compiled ASAN/UBSAN CLI. Program/evaluator/core sanitizer suites
pass 102/68/64 cases, and the shell-descriptor host suite passes four cases.
The existing scoped-environment regression passes all 31 assertions. This does
not close A3: definition/environment reads and tool resolution still require
scoped-configuration implementation. Structured recipe cache, retry, cancellation
and callable ownership now have focused coverage and the feature is committed.

WASM primary `--dry-run` now uses the shared session dry-run path rather than
returning `FEATURE_UNSUP`. T017-10 passes 23 behavioral assertions (24 total when rebuilding the CLI) on
the shared sanitizer checkout, including primary explicit/discovered/inline rule inputs,
JSON output, mixed values/processes, retained grants, and absence of effects.
Source-style integration also passes all 38 native/WASM golden cases; complete
spec-018 acceptance review remains pending.

Structured recipe review now covers 29 policy cases per host, including bounded
stdout/stderr cache replay from synchronous and async commands, inherited process
environments, successful retries, aggregate timeout budgets, and cancellation of
background work after a later command fails. Native checks use ASAN/UBSAN. The
WASM reusable-script helper also passes 33 cases. The instance-heap sanitizer
suite passes 31 tests, including a forwarded async structured recipe.

The review exposed a deferred call-path double-free during recursive retries and
a retained context that kept its previous completion-consumed flag. Stable local
cleanup handles and explicit context resumption fix both. The same generated
WASM C that trapped during nested async argv cloning succeeds with a 256 KiB
linker stack; both build files now reserve that stack explicitly and GNU Make
rebuilds the module when its build settings change. Primary WASM builds now
service independent requests concurrently and forward timeout/retry policy into
portable build and session options.

The structured recipe feature is committed with isolated host and sanitizer
verification. Reusable script file freshness now passes on both hosts: declared inputs permit
skipping unchanged outputs, while input-free file recipes rerun as spec 006
requires. A3 scoped definitions and tool resolution remain unfinished.

Nested operand scope restoration is committed separately with a focused
operation-context regression. The shared evaluator passes 71 ASAN/UBSAN tests;
the temporary-operand Kash constructor reproducer now passes on native and WASM.
Existing timeout/retry, signal cancellation, cached-task, and async-graph suites
pass 87 behavioral assertions. The expanded reusable-script helper has also
added file-freshness cases; that integration review remains open.

Direct recipe-template environment reads now bind to the effective target
snapshot and retain capability checks. Operation and program ASAN/UBSAN suites
pass 9 and 103 tests, including empty/missing snapshots, denial, and cached
prerequisite rebinding across debug/release roots. General scoped definition
configuration, tool lookup and collected shell requests remain open under A3.

Dynamic prerequisite expressions now use the same bound snapshot as rendering.
T006-06 passes all 40 native/WASM assertions, including grants, cache identity,
changed roots and dependency selection. Native checks use a compiled sanitizer
binary whose ASAN symbols remain present after harness refresh. Primary WASM
target evaluation now receives the CLI policy; missing env grants fail inside
the module before effects. Embedding hosts retain the previous all-capability
default unless they supply an explicit policy.

The committed Kash feature is verified independently from pending formatter and
watch edits. T017-19 passes 29 policy and 39 script cases per host, with native
ASAN symbols verified. Isolated core/evaluator/program/WASM sanitizer suites pass
64/71/103/32 tests. Recipes expand templates before Kash parsing; selected shell
and environment metadata are pure, with cache, freshness, timeout, retry,
cancellation, file publication and invocation-owned constructor coverage.

Spec 018 canonical layouts are committed. T018-01 passes 41 goldens per host,
including nested special forms, binding pairs, records, long operators/headers,
80/81-column Unicode and prefix boundaries, literal preservation and declaration
predicate width. Every golden also checks AST equivalence and idempotence;
formatted multiline value programs execute on both hosts. Formatting an effectful
declaration predicate produces no effects. Existing canonical, AST stability and
WASM formatter suites pass 193/93/6 assertions, and 27 expression sanitizer tests
pass. The registered catalog passes all 454 assertions. Advisory naming and
organization conventions remain author guidance; the formatter preserves names,
declaration order and surrounding language syntax.
