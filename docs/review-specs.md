# Specification audit

Reviewed against specs 000–018, their normative contracts and acceptance lists,
portable package tests, CLI conformance fixtures, and the current implementation.
Package paths below are relative to `src/go/kame`.
A numbered suite alone does not establish every requirement. The evidence below
groups related acceptance clauses and identifies the implementation boundary.

## Contracts and evidence

| Spec | Acceptance groups and implementation evidence | Validation boundary |
| --- | --- | --- |
| 000 roadmap | Delivery ordering, Solod pinning and explicit ownership; the module, native runtime, launcher and freestanding WASM artifacts exist. | Governance document; deferred features are excluded from the initial contract. |
| 001 architecture | Core/language imports are portable; explicit Free and owned queued values/diagnostics are exercised by core/diagnostic tracking tests. Fake graph ordering is deterministic. | `wasm-portable` audits transitive program/CLI imports; sanitizer teardown is a separate gate. |
| 002 engine | Lazy/deduplicated roots, static/dynamic scheduling, latest-value subscriptions, bounded coalescing, diamonds, cycles, generation/attempt correlation and last-interest cancellation are covered in core tests. | `core/test/engine.go`, `flow.go`, `order_only.go`, `reactive_restart.go`; 66 current sanitizer tests pass. |
| 003 POSIX | Separate live pipes, one shell per recipe, exact exit status, failed spawn cleanup, group/grandchild cancellation, TERM/KILL timeout, backpressure, truncation and waitpid failures have explicit POSIX fixtures. | 21 process/filesystem sanitizer tests plus T003-01/02. Child execution is authorized authority, not an OS sandbox. |
| 004 language | Independent expression/definition/rule/template/script parsing, literal classification, selectors, pipes, captures, logical forms, continuations, preserved trivia and parse/format equivalence are exercised by package tests and T004-01–13. | Command substitution parsing is context-independent; execution retains phase and grant checks. |
| 005 evaluation | Lazy shared definitions, lexical functions/rest/sections, left-to-right failures, frame selection, scopes, conditionals/match, strict truth, read edges and canonical value display have direct tests. Stream invocation coalescing and stale replacement are now covered by library integration tests. | 72 evaluator sanitizer tests; scoped definitions and operation-cycle diagnostic ownership have independent regressions. |
| 006 runtime | Exact/template precedence, ambiguity, captures, rule-before-definition resolution, diamonds, freshness/always/order-only, dynamic rendering, discarded effects, output parents/verification, target environments, shared outputs and yield conflicts are covered by program tests and T006 suites. | 104 program sanitizer tests. Services are explicitly unsupported under this spec, with FEATURE_UNSUP rather than task approximation. |
| 007 library | General/collection/text/path/filesystem operations, typed errors, grants, legacy core expressions, canonical dependency capture, once-only effects and bounded shell capture are covered by operations tests and T007 suites. | Atom/batch/nested-source lifting and asynchronous callback order/replacement now have 14 operation sanitizer tests. Tools use startup PATH intentionally. |
| 008 cache | Task hits, captures, command/file/glob/env/operation/shell identity, unsafe bare-task inputs, failure/cancellation preservation, separate truncated log replay, malformed records, atomic publication and SHA-256/value-tag vectors have direct tests. | Program runtime_cache/cache encoding tests, T008-01–03 and native/WASM cache tests. |
| 009 CLI | Discovery/direct dispatch, ordered whole-session preflight, shared fragments/args/cwd/policy, selected entries, inline/stdin modes, migration errors, help/version/overview, job bounds, JSON, formatting/cat/graph, dry-run and signal/status contracts have CLI and T009/T017 coverage. | Native and WASM watch reload/repair/active-input behavior is implemented and exercised by T009-14/T010-23; remaining D01 public ABI and grant coverage is tracked in spec 020. |
| 010 WASM | Freestanding linking/import audit, pure execution, buffer/event ownership, memory host forwarding, cancellation/late completion, request pinning, repeated lifecycle/OOM, wrapper disposal and native command/session parity are covered by host tests and T010 suites. | 33 embedding sanitizer tests. T010-05 now uses current batched file-times requests; CLI parity cannot substitute for that embedding boundary. |
| 011 diagnostics | Registered code integrity, preserved severity/span/context, tab/Unicode markers, target/cause order, colour policy, plain/JSON invariance and allocation-free NO_MEMORY formatting have portable/native/ABI fixtures. | T011-01–03, diagnostic tracking tests and explicit failure parity. Captured output is intentionally omitted from diagnostic causes. |
| 012 streams | Atom/batch/empty/final batches, nested sources/collections, protocol errors, copied matching completions, once-only release, independent subscriber lists, fairness and stale cancellation are covered by core and library tests. | Nested teardown and sole-source interest replacement defects are fixed as KB-12. T012-02–04 prove live publication, large captures and public sink backpressure. |
| 013 tests | Catalog/fixture hygiene, compiled-binary contract, controlled environment, sanitizer preservation, public host fixtures and build-mode identity are checked by T013. | Build-mode reuse requires an established Kame context and a stable revision. Full native, leak and WASM gates remain distinct. |
| 014 patterns | Section arity/index/lexical binding, nested sections, higher-order use, anchored match/expand, nil list entries, replacement callables/coercion, invalid pattern combinations and formatting are covered by expression/evaluator/operation tests and T014. | T014-02 also pins bare named captures, exact precedence and template ambiguity. |
| 015 distribution | Launcher dispatch/provisioning, fallback/overrides, checksum and verifier failures, populated/offline caches, version without install, concurrent installation, ordered bootstrap goals and cache-root precedence have local-release fixtures. | T015-01–03. Signing, auto-update, platform package managers and publication are explicitly deferred. |
| 016 templates | Branch/directive removal, elif/loops/rest parameters/separators, with/let/include payload scope, raw text, malformed blocks, style inference, path/bytes rendering, UTF-8, reactive dependencies/cycles, verbatim preservation and CLI render/check are covered. | Portable template tests plus T016-01; permission and include-cycle checks remain part of execution. |
| 017 Kash | References/env separation, full embedded expressions, substitution/argv fidelity, process/value pipes, acceptance/recovery distinctions, control scopes, setup/redirection, capture bytes, SIGPIPE, async joining, grants, preflight and native/WASM session equivalence have T017-01–19 coverage. | Selected Kash recipe interpreters and invocation-owned scripts are implemented; script values cannot escape invocation ownership or persistent JSON. |
| 018 source style | Structural inline/expanded layouts, AST equivalence/idempotence, native/WASM text parity, 80/81-column prefixes, Unicode, unchanged literals/trivia/order and effect-free formatting have explicit goldens. | T018-01 runs 41 golden cases per host; conventions do not rename identifiers. |

## Boundaries and review scope

Managed services, regex captures, automatic declaration generation, signed release
provenance and other explicitly deferred contracts remain design work. TODO.md's
standalone required/optional target parameters, broader expression wildcard
shorthand, richer job display and zero-copy parser are reviewed proposals rather
than silently implemented language changes. Their current state and recommended
next action are recorded in review-improvements.md. The parser still owns source
copies; the performance review states this directly.

## Current verification

Validation uses Clang with `CFLAGS=-O0` for native debug/sanitizer compilation;
the release target retains its explicit optimization flags. Git HEAD remains
stable throughout metadata and incrementality checks.

| Gate | Result for implementation `9fb24e5d` plus test-script mode corrections |
| --- | --- |
| `CC=clang CFLAGS=-O0 make test-leaks` | Passed: 409 portable tests, two external engine tests, Go CLI ASAN tests and all 113 selected CLI suites with ASAN/UBSAN and LeakSanitizer. |
| `CC=clang CFLAGS=-O0 make test` | Passed: 409 portable tests, two external engine tests, Go CLI tests and all 114 CLI suites. |
| `CC=clang CFLAGS=-O0 make test-wasm` | Passed: all 60 directly invoked suites, portable program/CLI import audit, and freestanding module audit (zero imports, 72 exports). |
| `CC=clang CFLAGS=-O0 make dist-ape` | Passed: actual APE reports release mode; process, signal, freshness and scoped-environment suites pass. Kash recipe/script conformance passes through a shell adapter. |
| `CC=clang CFLAGS=-O0 make -o dist/kame.com dist-release` | Passed: staged the previously verified APE plus WASM, JS and stamped launcher; all four manifest checksums verify. Both launcher backends return the expected value and reuse their cache with downloads disabled. |

`nm build/kame.sanitize` finds `__asan_init`, `__asan_report_load1` and
`__ubsan_handle_type_mismatch_v1` after the complete leak harness. The leak gate
intentionally excludes T013-03, which checks debug artifact metadata. It does not
exclude ownership, source watch, Kash, embedding, process or stream tests.

The first normal full run exposed two stale test conditions: T010-05 serviced an
obsolete output-existence request instead of batched file timestamps; T013-05
expected GNU-produced artifacts to already have successful Kame context records.
The embedding fixture now verifies current metadata transport and ordering. The
incrementality test first establishes the context, then still requires unchanged
artifact timestamps and no process launch on the next build. Both pass in the
complete current leak gate. No runtime assertion was removed to close those
failures.

The first dedicated WASM run passed its portability/import audits and early
suites but stopped at a non-executable test script. The normal Bash harness had
masked that integration defect. T004-11, T004-12 and T012-02 now have executable
modes (commit `7ebfb598`), and the entire dedicated gate passes with those
corrections. These mode-only changes do not alter the compiled runtime.

The APE checks ran on Linux through shell fallback: this host lacks direct APE
binfmt execution, so Python `subprocess` returned `ENOEXEC` when given the binary
itself. The four shell-driven suites passed directly; the Python-owned Kash
suite then passed using a small POSIX-shell adapter that execs the actual APE.
No native ELF substitution was used. The staged launcher also provisions and
executes the real APE. This proves execution on this host; it does not establish
macOS, BSD or Windows conformance. Compiler download pinning and release
provenance remain the security review's explicit supply-chain boundary.

Full logs are local verification artifacts at
`/tmp/kame-review-scoped/current-leak-gate.log`,
`/tmp/kame-review-scoped/final-full-test.log` and
`/tmp/kame-review-scoped/final-wasm-test.log`. Release build/check logs are
`final-ape-build.log`, `final-ape-conformance.log`, `final-ape-kash.log` and
`final-release-stage.log` in the same directory. The APE conformance log preserves
the original Python execution-format failure; the separate Kash log records its
successful shell-adapter run.
