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

The end-to-end binary is always the checks-enabled debug build
(`build/kame.debug`), never the release build.

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
  `runtime`=006, `lib`=007, `cache`=008, `cli`=009, `diag`=011, `streams`=012,
  `meta`=013, `patterns`=014.
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

| Spec | Files | Focus |
| --- | --- | --- |
| 003 host | `T003-01`, `T003-02` | streaming, exit codes, retry, shell, environment, cwd, signal cancellation |
| 004 language | `T004-01` … `T004-09` | parse matrix, invalid manifest, stdin/IO, usage, goldens, format idempotence and stability, standalone rule parsing |
| 005 evaluation | `T005-01` … `T005-05` | values, references, special forms, pipes, arguments, failure messages |
| 006 runtime | `T006-01`, `T006-02`, `T006-04`, `T006-05` | freshness, dependency scheduling, yield, deferred effects and dry-run |
| 007 library | `T007-01` … `T007-07` | general, collection, text, path, filesystem, capability and shell operations, error messages |
| 008 cache | `T008-01` … `T008-03` | record creation, hits, force, invalidation, corruption recovery, glob and body fingerprints |
| 009 CLI | `T009-01` … `T009-08`, `T009-11`, `T009-12` | help/version, discovery, targets, JSON, plan, cat, graph, dry-run, usage, case matrix |
| 011 diagnostics | `T011-01`, `T011-02` | layout, notes, human/JSON equivalence, code and message integrity |
| 012 streams | `T012-01` | terminal event uniqueness, process event balance |
| 014 patterns | `T014-01` | placeholder sections as lambda equivalents, section arity, pattern replace match/expand, patterns render as text in rule inputs |
| 013 meta | `T013-01` … `T013-03` | catalog consistency, fixture hygiene, binary contract, determinism |

All currently specified native acceptance bullets have an E2E or package-level
test. Spec 014 coverage is in place: `T014-01`
placeholder sections and pattern replace, plus `expr/section-*.km`,
`expr/pattern-*.km`, and `invalid/expr-pattern-mixed-groups.km` fixtures in the
T004 parse/format suites.

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
