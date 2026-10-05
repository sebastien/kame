# End-to-End Test Suite

## Purpose

This specification defines the end-to-end conformance suite for the compiled
`kame` binary. In-process tests (`cd src/go/kame && go test ./cmd/kame`) and
package tests (`cd src/go/kame && so test ./...`) cannot catch Solod-to-C
defects; this suite exercises
the real binary against real files, processes, streams, and signals, and
asserts the behavior promised by specifications `003` through `012`.

The suite is strict: a test encodes specified behavior and must pass. When it
fails, the implementation is fixed in the same change set. There are no
expected-failure markers and no skipped assertions.

## Layers

| Layer | Command | Scope |
| --- | --- | --- |
| Package tests | `cd src/go/kame && so test ./...` | Solod package internals |
| Engine examples tests | `cd examples/engine && so test ./...` | external-consumer engine examples |
| CLI unit tests | `cd src/go/kame && go test ./cmd/kame` | in-process `Run()` behavior |
| End-to-end | `tests/harness.sh`, `make test-cli` | compiled debug binary, real system calls |
| Sanitized package tests | `make test-sanitize` | allocator tracking and native memory safety |
| Leak gate | `make test-leaks` | sanitized package tests plus the compiled CLI end-to-end suite |

General end-to-end conformance uses the checks-enabled debug build
(`build/kame.debug`); the leak gate selects `build/kame.sanitize`. T013-05 also
inspects release artifact version metadata and uses the debug binary to verify
unchanged repository build incrementality.

## Naming and layout

```text
tests/
  harness.sh                       runner
  lib-bootstrap.sh                 shared test-script bootstrap
  lib-testing.sh                   primitives and reporting
  lib-cli.sh                       CLI binary, invocation, assertion helpers
  CATALOG.tsv                      machine-readable test catalog
  T<SSS>-<NN>-<category>-<topic>.sh
  data/cli/<fixture>/              build trees, inputs, scripts
  data/lang/                       shared language fixtures
  data/README.md                   fixture catalog and conventions
```

- `SSS` is the governing spec number and `NN` a two-digit sequence per spec.
- `category` maps one-to-one to the spec: `host`=003, `lang`=004, `eval`=005,
  `runtime`=006, `lib`=007, `cache`=008, `cli`=009, `wasm`=010, `diag`=011,
  `streams`=012, `meta`=013, `patterns`=014, `dist`=015.
- `lib-*.sh` files are support libraries and are never executed as tests.
- Nothing under `tests/data/` is executed as a test.
- Every test script has one `CATALOG.tsv` row; `T013-01` enforces both
  directions plus the naming scheme.

## Harness contract

- `tests/harness.sh [FILE...]` runs every `tests/**/*.sh` except `lib-*.sh`,
  `harness.sh`, and anything under `tests/data/`.
- Each test is a separate process started from the repository root.
- `test-start` creates a scratch directory under `build/tests/`, exports it as
  `TMPDIR`, and changes into it; `test-end` reports and removes it.
- Results are recorded in an append-only file inside the scratch directory, so
  assertions made in subshells or background jobs still reach the report.
- Test scripts source `tests/lib-bootstrap.sh` (which loads `lib-testing.sh`
  and `lib-cli.sh`), call `test-start`, run steps and assertions, and finish
  with `test-end`.

## Binary contract (`tests/lib-cli.sh`)

- `KAME` points at `build/kame.debug`.
- Repository artifacts report `debug`, `sanitize` and `release` according to their
  compiler-bound mode. Shared generated source is target-independent; switching
  mode alone does not change its bytes or timestamp. T013-05 checks real artifacts
  and unchanged Kame file-output timestamps.
- `cli_build` builds the default debug binary through `make
  build/kame.debug` only when it is missing or older than tracked sources,
  under a `flock` guard shared by concurrent runs. A caller-selected `CLI_BIN`
  uses the equivalent direct build.
- Every invocation runs with a controlled environment (`LC_ALL=C`, `TZ=UTC`,
  `HOME=$TEST_PATH`, no leaked `TMPDIR`) and a `timeout` safety bound.
- `CDPATH` is unset so `cd` cannot pollute captured output.
- Background and signal helpers (`cli_spawn`, `wait_for_status`,
  `wait_for_pid_gone`) set globals instead of echoing, because command
  substitution would move the child into a subshell where `wait` cannot reap it.

Helper surface: `cli_require_tools`, `cli_build`, `cli_run`, `cli_spawn`,
`cli_wait`, `wait_for_file`, `wait_for_status`, `wait_for_pid_gone`,
`cli_expect_status`, `cli_expect_status_nonzero`, `cli_expect_stdout`,
`cli_expect_stdout_file`, `cli_expect_stdout_empty`, `cli_expect_stderr`,
`cli_expect_stderr_empty`, `cli_expect_stderr_contains`,
`cli_expect_stdout_contains`, `cli_expect_printable`, `cli_expect_file`,
`cli_expect_file_bytes`, `cli_expect_no_file`, `cli_expect_dir`,
`cli_expect_jsonl`, `cli_json_types`, `cli_expect_json_query`,
`cli_expect_event`, `cli_expect_diagnostic`, `fixture_copy`, `fixture_path`,
`lang_fixture`, `tests_data_path`, `set_mtime`, `run_count`, `kill_tree`.

## Fixture contract

Fixtures are inputs, never outputs: tests copy them with `fixture_copy` before
use. Fixture scripts are hermetic POSIX `sh`; recipes are indented with one tab
and files use LF/UTF-8. Volatile data never appears in goldens. `tests/data/README.md`
is the fixture catalog.

## Determinism rules

- Execution counts use marker files appended by recipes, not sleeps.
- Freshness edges use `touch -d` with fixed epochs.
- JSON comparisons drop `node`, `request`, `generation`, and `attempt` fields.
- Concurrency tests assert invariants (completion sets, shared-node run counts)
  rather than interleavings.
- Signal tests use bounded polling and reap their process groups.
- Every test passes alone and in any grouping.

## Coverage

`tests/CATALOG.tsv` is the authoritative list; `T013-01` keeps it consistent.
Current coverage:

| Spec | Current CLI suites | Focus |
| --- | --- | --- |
| 003 | 2 suites; sequences 01, 02 | POSIX streams, shell, retries, timeout and process-group cleanup |
| 004 | 14 suites; sequences 01, 02, 03, 04, 05, 06, 07, 08, 09, 09, 10, 11, 12, 13 | Parsers, formatting, headers, continuations, wildcard inputs and declaration selection |
| 005 | 5 suites; sequences 01, 02, 03, 04, 05 | Values, references, lexical forms, selectors and arguments |
| 006 | 8 suites; sequences 01, 02, 03, 04, 04, 05, 06, 07 | Freshness, dependencies, always/order-only rules, deferred effects and target environments |
| 007 | 10 suites; sequences 01, 02, 03, 04, 05, 06, 07, 09, 10, 11 | All standard operations, grants, wildcard unions, record lookup and declared tool policy |
| 008 | 3 suites; sequences 01, 02, 03 | Cached task identity, invalidation and corrupt-record recovery |
| 009 | 13 suites; sequences 01, 02, 03, 04, 05, 06, 07, 08, 11, 12, 13, 14, 15 | Help, discovery, targets, JSON, inspection, configuration, native watch and target arguments |
| 010 | 25 suites; sequences 01, 02, 03, 04, 05, 06, 07, 08, 09, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25 | Public ABI, embedding services, metadata/effect publication, native parity, OOM recovery, retained watch, target arguments and watch ABI conformance |
| 011 | 3 suites; sequences 01, 02, 03 | Code registry, source layout, structured context and process cause policy |
| 012 | 4 suites; sequences 01, 02, 03, 04 | Protocol/event ownership, live output, large captures and public backpressure |
| 013 | 5 suites; sequences 01, 02, 03, 04, 05 | Catalog, fixture hygiene, binary contract, graph growth and artifact build modes |
| 014 | 2 suites; sequences 01, 02 | Sections, pattern replace, bare captures and exact/template precedence |
| 015 | 3 suites; sequences 01, 02, 03 | Launcher integrity, backend choice, provisioning and build metadata |
| 016 | 1 suite; sequences 01 | Template rendering, directives, includes, styles and check mode |
| 017 | 19 suites; sequences 01, 02, 03, 04, 05, 06, 07, 08, 09, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19 | Kash processes/control, unified sessions, capabilities, async ownership and recipe interpreters |
| 018 | 1 suite; sequences 01 | Structural formatting goldens, equivalent ASTs, width boundaries and host parity |

The catalog is authoritative; these suite counts describe coverage rather than
replacing a clause-by-clause spec audit. Unified-run coverage in T017-05 through
T017-10 includes direct/explicit dispatch, whole-sequence preflight, shared
fragments and includes, JSON, dry-run and invocation policy on both hosts.
T017-19 covers selected recipe interpreters and invocation-owned script values.
Portable operation stream tests additionally verify complete batch lifting,
callback replacement and two-input coalescing; core tests verify protocol
ownership, fairness and retained interest through reactive rebinding.

### Unified-run regression coverage

Expression-command cases have migrated to `do run --lang expr` rather than
dropping their evaluator, argument, capability, or byte-output assertions.
Historical defect descriptions below retain the old command name because they describe the interface at the time of the defect.

Retain native/WASM conformance for:

- Direct `.km`, `.kmk`, `.kash`, and `.ksh` execution versus `do run` equivalence.
- Inline/stdin language selection and unknown-suffix validation without effects.
- Repeated/interleaved files and `-c` fragments: compile the whole sequence before
  execution, then share definitions and run work in input order. Include a rule
  build followed by a value expression reading its definition, and a Kash file
  followed by a default-Kame inline fragment.
- Syntax errors, missing later files, duplicate top-level names, and invalid
  static entries prevent earlier effects; runtime failure stops later fragments
  without replay or rollback. Verify distinct inline source spans and forward
  references across file/inline boundaries.
- Named value entries, forward lazy definitions, top-level expression sequencing,
  definitions-only defaults, empty programs, and final-value presentation.
- Explicit source sequencing, discovered-build append behavior, and literal
  target escapes/`--entry` selection for program-like suffixes.
- Source resolution before `-C`, with no implicit change to the source directory.
- Program arguments after `--`, including empty frames and whitespace-bearing values.
- Language-specific defaults and rejection of inappropriate options before effects.
- First-fragment capability defaults, inherited policy in later languages, shared
  cwd/args/timeout budgets, and whole-session dry-run suppression.
- Kash stdin isolation, recovery, cancellation, async handles across fragments,
  and owned async joining at whole-session completion rather than file boundaries.
- Deterministic migration diagnostics for removed `do expr` and `do kash` names.

Keep fixtures and examples runnable; changes to this specification do not
authorize removing executable regression coverage.

## Defects found and fixed by this suite

1. No-target invocation panicked: a block-scoped C compound literal from a Go
   slice literal in `runBuild`; replaced with an allocator-backed append.
2. Diagnostic messages contained freed memory: `failure` helpers in the
   runtime and evaluator stored stack-allocated concatenations; both now clone
   owned text into the program allocator, and drop sites free what they skip.
3. `splitext` aborted: a literal `[]core.Value` was freed through the
   allocator; it now uses `slices.Make`.
4. `appendExprGrant` returned a slice literal that died with the function, so
   `--allow-read=ROOT` and `--allow-env=NAME` silently became unrestricted.
5. Filesystem and environment operations never checked rooted grants; they now
   call `Allows` before submitting host work.
6. `--file=` and friends with empty values reported `OPT_UNKNOWN` instead of
   `OPT_VALUE_INVALID`.
7. `do cat` failed on an existing file with no rule; it now materializes it.
8. `--json` diagnostics for plan/cat/graph went to stderr; JSON is stdout-only.
9. `shell` never captured output because process requests had no retention
   budget; shell requests now default to the cache log budget.
10. `do expr` re-parsed input as a definition right-hand side, turning `:nil`
    and `0x10` into text; it now evaluates through the `nop` identity wrapper.
11. `relpath` computed common prefixes mid-component; boundaries now align to
    path separators.
12. `Render` built its fallback `Context` as a block-scoped temporary that GCC
    flagged as dangling under `-check=warn`.
13. Cache warnings were unreachable from the CLI; `--verbose` now reports them
    on stderr and they remain JSON `cache-warning` events.
14. `do expr` produced `SEL_NO_CONTEXT` for `@*` with no `--` arguments; the
    evaluator now tracks whether an argument frame was provided.
15. `do span --depth N` ignored depth; non-default depths now expand static
    inputs and outputs transitively through the graph walker.
16. A multi-assignment from two struct slice fields produced a bad pointer in
    the Solod C build; the span command now reads plan fields directly.
17. Recipe failures now have their mapped source span pinned through the JSON
    event stream (`target-failed` carries a non-empty `diagnostic.span`).

## Suite acceptance criteria

- `tests/harness.sh` exits 0 on a clean checkout after `make test-cli`.
- Every test file has a catalog row and every row resolves to a file.
- No test writes under `tests/data`.
- Normalized event streams and artifacts are identical across fresh copies.
- Fixing a defect never weakens a test: assertions cite their governing spec in
  a header comment.

## Leak verification

`make test-leaks` is the full native leak gate. It runs CLI unit tests with Go
AddressSanitizer and Clang, package and external consumer tests with Clang and
Solod's `-check=sanitize`, then builds the CLI with the same checks and
executes `tests/harness.sh` against that binary.
`ASAN_OPTIONS=detect_leaks=1:halt_on_error=1` and
`UBSAN_OPTIONS=halt_on_error=1` are passed through the harness's otherwise
hermetic command environment. New owning types and lifecycle paths require a
tracker-backed regression test; high-churn paths should repeat construction and
teardown in one process so retained allocations cannot hide behind process exit.
The binary-contract meta test is excluded because it deliberately requires the
normal debug binary path and verifies the harness's non-sanitized rebuild flow.

`T007-11-lib-tools.sh` verifies native/WASM tool resolution, explicit relative
paths in a directory containing spaces, repeated override precedence, missing
tools before effects, plan metadata, and computed names in source sessions.
The program resolver unit test counts lookups and checks tool/file dependency
registration under AddressSanitizer.

`T004-11-lang-wildcard-inputs.sh` covers first-class wildcard rule inputs, recursive expansion, empty matches, quoted literal paths, authored formatting, AST classification and membership invalidation on both hosts.

`T009-15-cli-target-arguments.sh` and `T010-24-wasm-target-arguments.sh` verify
named values, defaults, dependency and recipe scope, distinct task identity,
plan JSON, multiple target requests and invalid assignments on both hosts.

`T010-26-wasm-service-readiness.sh` verifies that forwarded WASM service
probes gate dependents, and that a readiness timeout cancels the service and
reports `SERVICE_READY_TIMEOUT`. It also checks that a configured zero stop
grace period is honored by the JavaScript host.

`T010-27-wasm-generated-declarations.sh` compares native and WASM generated-rule
plan JSON, including generator provenance, and materializes generated tasks on
both hosts.

`T010-28-cache-management.sh` verifies native and WASM cache inspection JSON,
oldest-first eviction at the 1024-record bound, and cleanup, including that
both eviction and cleanup leave symlinks and their targets intact.

`T010-29-cache-locking.sh` starts separate native CLI processes against a cold
task identity, verifies only one recipe execution, and confirms publication
releases the striped lock while the owning process continues other work.

The shared `watch-runtime.py` suite, run by T009-14 and T010-23, also changes an
included module list while watching a generated target family. It verifies the
new generated target runs and the retained root rebuilds against the replacement
set on native and WASM.

The Go CLI unit-test sanitizer command uses `GOGC=off`. Solod 0.4.0's Go
compatibility allocator stores structs in unscanned byte slices, while its Go
`slices.Append` stub uses ordinary Go append. A slice reachable only through such
a struct is invisible to Go's collector and can be reclaimed during sustained
CLI churn. Disabling collection for this short-lived compatibility test preserves
the explicit C lifetime model. It does not replace allocator tracking or the
compiled CLI AddressSanitizer/LeakSanitizer gate, which run with the actual C
allocator and remain mandatory.

The harness preserves `-check=sanitize -panic=abort` and Clang when freshness requires rebuilding `build/kame.sanitize`. A stale sanitized binary must never be replaced by a checks-only executable during the leak gate.

`T004-12-lang-optional-includes.sh` verifies missing and present optional sources, newly registered declarations, value-source fragments, formatting/AST metadata, parse failures before effects, cycles and existing-directory read failures on native/WASM.

`T012-02-streams-live-cli.sh` requires native/WASM human and JSON chunks to reach pipes before allowing the recipe to complete, and verifies native watch output before termination.

T010-05 embedding hosts service the current kind-20 file-times arrays, asserting
canonical paths, absent-before/present-after recipe ordering and 64-bit decimal
nanosecond strings. T013-05 establishes one successful Kame artifact context
before asserting the next unchanged build skips; GNU Make does not publish those
context records. Keep the revision stable during build-mode gates because the
generated build ID follows Git HEAD.
