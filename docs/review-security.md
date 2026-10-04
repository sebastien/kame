# Security review

Reviewed 2026-10-03; cache publication rechecked 2026-10-04. This review covers the native POSIX host, evaluation grants,
JS/WASM forwarding, artifact writes, process cancellation, and diagnostic output.
It is a source review with selected runtime reproductions, not a claim of complete
memory-safety verification. Full repository verification remains in progress.

## Trust boundary

Kame builds and explicitly authorized processes run with the caller's OS
privileges. Grants control language host operations; they do not contain an
executable's subsequent filesystem, network, or environment access. Source files
and includes are loaded before evaluation, as build configuration. The WASM
module has no hosted imports, but its JS wrapper intentionally supplies host I/O.

Filesystem grant checks are lexical, as specified in
[005-evaluation.md](spec/005-evaluation.md#capabilities). Both native and WASM
permit reading a symlink inside a granted root whose destination is outside it.
A reproduction using a temporary directory and the public text
`public-test-marker` returned that text on both hosts. This is a documented limit,
not evidence of filesystem sandboxing. Embedders must supply OS containment for
untrusted builds. Avoid describing `--allow-read=DIR` as a filesystem sandbox.

## Findings

| ID | Severity | Finding | Disposition |
| --- | --- | --- | --- |
| SEC-1 | High when output directories contain attacker-controlled entries | Non-durable native writes opened `OUTPUT.kame-write.tmp` directly, following a pre-created symlink and allowing concurrent writers to share staging data. | Fixed: exclusively create a unique sibling temporary, apply requested permissions, and rename only after successful write and close. |
| SEC-2 | Trust-boundary limitation | Lexical roots permit symlink escape and executable grants do not constrain child effects. | Explicitly specified behavior; require host containment for hostile code. |
| SEC-9 | Availability | An 8 MiB WASM recipe output aborts in transient event JSON allocation before forwarding all bytes. | KB-9 fixed: T012-03 verifies lossless 8 MiB NUL/binary stdout/stderr, bounded failure retention and 64 KiB cache replay on both hosts. All 28 WASM host sanitizer tests pass. T010-22 now verifies static `NO_MEMORY` diagnostics, instance isolation/reuse, host-completion exhaustion, temporary module-heap recovery and unrelated traps. |
| SEC-3 | Verification gap | The compiled CLI leak gate previously lacked trustworthy instrumented results. | Verified: all 102 selected CLI suites pass with ASAN/UBSAN and leak detection enabled. ASAN symbols remain present before and after the run. The binary metadata suite is intentionally excluded from this gate. Earlier broad package coverage passed 359 tests; current focused coverage passes 28 WASM host and 96 program tests, plus Go CLI sanitizer tests. |
| SEC-7 | Memory safety | Returning a definition function from a `let` exposed a freed function and scope to calls and ancestor-store rejection. | Fixed: definition lookups return an owned wrapper retaining the lexical scope. All 65 evaluator sanitizer tests pass, including returned-function calls, result cleanup, and `DEF_ESCAPE` rejection. |
| SEC-8 | Input validation | WASM build-source descriptors accepted incorrectly typed fields and offsets whose source end exceeded the 32-bit span range. | Fixed: require string names/text, integer offsets, and an in-range source end before copying fragments. All 26 WASM host sanitizer tests pass, including malformed descriptors and recovery after rejection. |
| SEC-6 | High when cache directories contain attacker-controlled entries | WASM cache put followed an existing record symlink and overwrote its target. | Fixed: exclusive sibling staging, file sync, and rename; T010-19 preserves a public marker and verifies the published cache hit. |
| SEC-5 | Atomic visibility | JS write requests previously used direct file writes. | Fixed: exclusive staging in a private sibling directory followed by rename; T010-21 covers bytes, zero length, failed publication and cleanup. |
| SEC-10 | Memory safety | An operation-wrapped lazy definition cycle reallocated frames borrowed from an engine diagnostic, leaving a dangling frame pointer for the scheduler. | KB-11 fixed: clone evaluator failure results before adding frames; preserve terminal state after source polling and free discarded results. A pre-feature compiled ASAN reproduction confirms the defect predates scoped definitions. |
| SEC-4 | Low confidentiality limit | Live process streams, collected shell results, and explicit `out` remain observable, even though diagnostic causes omit captured output. | Intended: omission protects diagnostic serialization, not deliberate output. |

The atomic-write fix also removes the old fixed path-length temporary buffer:
the buffer now scales with the destination directory. Root-directory outputs
retain a sibling temporary in `/`. Durable writes sync before rename; directory
fsync is not implemented, so the durability contract depends on filesystem crash
semantics. Atomic visibility is distinct from persistence across power loss.

## Process and capability review

`host/posix/posix.c` uses `execve` for direct argv execution, process groups for
cancellation, TERM followed by KILL escalation, and `waitpid` to reap children.
Pipeline stages use OS pipes and close their known unused endpoints. The JS host
uses explicit argv execution and private pipeline staging directories; its format
preflight rejects Node's implicit ENOEXEC shell fallback. Executable-format
preflight has a race between inspection and launch and must not be presented as
an atomic executable identity guarantee.

`lang/eval/context.go` normalizes path grants before host submission.
`operations/host.go`, `lang/eval/process*.go`, and the session recipe launch path
apply capability checks. The JS wrapper additionally checks process and
redirection grants when forwarding requests. These checks should remain in
regression coverage when adding source forms or new execution paths.

Diagnostic process causes preserve status, signal, and truncation information
while omitting captured bytes. Keep that policy when adding new error paths.
Cache records are local build data; successful records use temporary writes and
rename. Cross-process cache locking and cache eviction are explicitly deferred
by spec 008.

## Verification

- `cd src/go/kame && so test ./host/posix`: 21 tests pass, including staging
  symlink protection, output permissions, durable output, and temporary cleanup
  after a failed rename.
- `cd src/go/kame && CC=clang so test -check=sanitize -panic=abort ./host/posix`:
  21 tests pass with the atomic-write fix.
- Native/WASM public-marker symlink reproduction confirms the documented lexical
  grant boundary.
- `tests/T010-19-wasm-cache.sh`: 9 assertions pass, including record-symlink replacement, marker preservation, readable cache replay, and staging cleanup.
- Signal, pipeline, redirection, grant and cancellation suites pass in the
  completed 102-suite compiled CLI leak gate.

The earlier broad portable-package command `cd src/go/kame && CC=clang so test -check=sanitize -panic=abort ./...` passed all 359 tests across 16 packages before the optional-include and large-stream additions. This includes executable dependency tracking and literal wildcard ownership. The subsequent compiled CLI leak gate passes all 102 selected suites with actual ASAN/UBSAN instrumentation and leak detection. Current stream changes also pass all 28 WASM host and 96 program sanitizer tests, plus Go CLI sanitizer tests.

Go's CLI compatibility tests initially failed AddressSanitizer during repeated
command execution. Symbolization located reclaimed `Script.Items` and
`Definition.Words` slices. Solod 0.4.0 allocates pointer-bearing structs as byte
slices in its Go stubs; Go cannot scan those pointers, and its `slices.Append`
stub bypasses the explicit allocator. `GOGC=off` makes the unchanged CLI tests
pass. The Go compatibility gate now states that setting explicitly. This is a
compatibility-test limitation, not evidence that compiled C ownership is safe;
the separate compiled CLI leak gate still verifies actual frees and retained
allocations.

The CLI harness now preserves sanitizer build flags when refreshing `build/kame.sanitize`. A forced harness rebuild was checked for `__asan_init`/report symbols and passed the 94-assertion parse matrix. The earlier run that replaced the executable with a checks-only binary is discarded as leak-gate evidence.

The current compiled CLI gate completed with exit 0; its log is
`build/review/cli-leak-gate-current.log`. `nm` finds `__asan_init` and
`__asan_report_load1` both before and after the harness run. The command is the
CLI conformance portion of `make test-leaks`, with
`ASAN_OPTIONS=detect_leaks=1:halt_on_error=1`, `UBSAN_OPTIONS=halt_on_error=1`, and
`CLI_BIN=build/kame.sanitize` (absolute path). All 102 selected suites pass;
`T013-03-meta-binary.sh` is intentionally excluded because it checks the debug
artifact's metadata. This closes the compiled CLI verification gap, while the
remaining specification/feature requirements still prevent release completion.

The 2026-10-04 allocator checkpoint and publisher fixes were verified in an
isolated checkout containing the committed changes, excluding unfinished Kash
and source-formatting work in the shared checkout. All 29 WASM host ASAN/UBSAN
tests pass. T010-22 checks allocator exhaustion at compilation and asynchronous
completion, static diagnostics, repeated slot reuse, surviving instances,
temporary module-heap rollback and preservation of unrelated traps. Its real
shell/argv stdout/stderr reproductions additionally require a clean `NO_MEMORY`
CLI diagnostic and reaped children. The eight publication-backpressure checks,
large-output suite and timeout/retry suite pass after the publisher changes.
These focused results do not establish a final full-tree verification.


## Declaration source selection

Declaration predicates run through the portable evaluator in planning phase
with no host and no capability grants. Literal invocation configuration is
supplied as definition overrides; it does not grant general environment reads.
Selected definitions stay lazy, and inactive nested predicates and include
paths are not evaluated or read. Selected inline includes remain unsupported.
Syntax validation covers both branches before execution, and direct evaluator
registration rejects uncomposed markers so embedding callers cannot accidentally
register an inactive declaration. T004-13 verifies rejected shell/read/output
predicates, no resulting effects, inactive missing/cyclic includes, and authored
diagnostic spans on both CLI hosts. Typed WASM descriptor checks pass alongside
all 30 host sanitizer tests; raw registration rejection is included in the
68-case evaluator sanitizer run. This source-selection boundary retains the
existing limits of runtime process grants described above.

## Scoped environment reads and portable target policy

Scoped recipe reads resolve inside the evaluator, so they cannot rely on the
JavaScript host request handler to enforce grants. Primary WASM builds now
forward the CLI policy into portable target compilation. The evaluator checks
name-specific env grants before reading target snapshots, and an explicit empty
embedding policy rejects reads before requests or effects. T006-06 passes all
40 cross-host assertions; native runs use the ASAN/UBSAN CLI. The WASM host
sanitizer suite passes 32 tests including the explicit empty-policy regression.
Missing snapshot names return nil; they do not fall back to the ambient host.
These controls retain the documented child-process and filesystem boundaries.

The committed script constructor is pure: it parses before returning a callable,
retains its lexical scope, and performs effects only when invoked under the
caller's phase and grants. Callable values retain invocation identity; foreign
invocations and persistent JSON serialization reject them. Focused isolated
sanitizer coverage passes 64 core, 71 evaluator, 103 program and 32 WASM host
cases. T017-19 additionally covers restricted executable grants, construction
without effects, parse failure, cancellation of background work, bounded cached
streams and aggregate timeout/retry policy on both CLI hosts.

## Target-scoped definitions and cycle ownership

The scoped-definition change preserves environment grants and the read-only phase
of dynamic prerequisite resolution. KAME_NAME and explicit CLI overrides remain
literal values. Environment namespaces use a digest rather than placing snapshot
values in resource keys. Generated-file reads reached through lazy definitions
bind their producers to the demanding environment before adding the engine edge.

T006-06 passes 56 native/WASM assertions, including denied reads, phase rejection,
cycle failure without effects, cache isolation, override precedence and generated
file inheritance. The native binary retains ASAN instrumentation. Current
ASAN/UBSAN core/evaluator/program/WASM-host suites pass 64/72/104/32 tests, including
the focused operation-cycle ownership regression. The older 102-suite CLI result
predates the latest features; a final current full-tree gate is still required.

## Collected shell snapshot transport

Target-bound collected shell requests retain run grants and phase checks before
submission. Their exact environment travels through the owned process payload;
an empty array remains empty in the WASM forwarding descriptor and JS child
environment. Missing snapshot names cannot silently inherit ambient host values.
Collected shell output remains intentionally readable by its caller. This does
not restrict a granted child process beyond the existing process policy.

T006-06 passes 63 native/WASM assertions, including denied collected shell calls
before effects. Portable payload tests cover explicit empty environments, and an
isolated WASM-host ASAN/UBSAN suite passes 33 tests including transport of the
empty snapshot. The selected JS wrapper passes syntax checking. These focused
checks do not replace the final full-tree security gate.
