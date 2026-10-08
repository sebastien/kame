# End-to-End Test Suite

## Purpose

This specification defines the end-to-end conformance suite for the compiled
`kame` binary. In-process tests (`cd src/go/kame && go test ./cmd/kame`) and
package tests (`cd src/go/kame && so test ./...`) cannot catch Solod-to-C
defects; this suite exercises
the real binary against real files, processes, streams, and signals, and
asserts the behavior promised by the language, host, runtime and CLI specifications.

The suite is strict: a test encodes specified behavior and must pass. When it
fails, it signals a conformance defect. There are no expected-failure markers
and no skipped assertions.

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
(`build/kame.debug`); the leak gate selects `build/kame.sanitize`. Artifact tests
inspect release metadata and verify unchanged repository build incrementality.

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

- `tests/harness.sh [-q|-v] [FILE...]` runs every `tests/**/*.sh` except
  `lib-*.sh`, `harness.sh`, and anything under `tests/data/`.
- Each test is a separate process started from the repository root.
- `test-start` creates a scratch directory under `build/tests/`, exports it as
  `TMPDIR`, and changes into it; `test-end` reports and removes it.
- Results are recorded in an append-only file inside the scratch directory, so
  assertions made in subshells or background jobs still reach the report.
- Test scripts source `tests/lib-bootstrap.sh` (which loads `lib-testing.sh`
  and `lib-cli.sh`), call `test-start`, run steps and assertions, and finish
  with `test-end`.

### Human output levels

`KAME_TEST_VERBOSITY` selects the human output level, inherited by nested test
processes; `harness.sh -q` and `-v` are equivalents. The level changes only
human stdout/stderr, never the append-only result log or exit codes.

| Level | Direct suite output |
| --- | --- |
| `quiet` | Same per-step tallies as `compact`; intended for harness invocation |
| `compact` (default) | One line per step with a pass tally (`N✓`), plus every failure with full detail |
| `verbose` | One line per passing assertion (historical behavior) |

- Passing assertions are never dropped from the result log; compact and quiet
  fold them into a per-step tally. A failing assertion always reveals its step
  and prints its message, diff, or diagnostic regardless of level.
- In both `compact` and `quiet`, `harness.sh` runs each test with output captured
  under `build/tests/logs/`
  and prints one line per file (`EOK`/`EFAIL`, elapsed, path). On failure it
  replays the captured log, so a red test remains fully debuggable while a
  passing suite stays small. In `verbose`, it streams suite output instead.
- Incidental helper traces (`test_log_message`, expected-failure command echo,
  CLI build output) are verbose-only; the CLI build log is retained and shown
  if the build fails.

## Binary contract (`tests/lib-cli.sh`)

- `KAME` points at `build/kame.debug`.
- Repository artifacts report `debug`, `sanitize` and `release` according to their
  compiler-bound mode. Shared generated source is target-independent; switching
  mode alone does not change its bytes or timestamp. Artifact tests verify real
  binaries and unchanged Kame file-output timestamps.
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
`lang_fixture`, `tests_data_path`, `set_mtime`, `run_count`, `kill_tree`,
`test_set_verbosity`.

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

## Conformance coverage

`tests/CATALOG.tsv` is the authoritative suite inventory, not a substitute for
clause-by-clause acceptance criteria. Native and WASM must agree on portable
language/runtime behavior; host-specific tests require execution on that host.
Stream tests must cover atom/batch/nested-source lifting, callback replacement,
coalescing, fairness, ownership and retained interest through reactive rebinding.

### Unified-run requirements

Native/WASM conformance covers:

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

Fixtures and advertised examples must remain runnable; regression coverage
continues to apply when test infrastructure changes.

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

The Go CLI unit-test sanitizer command uses `GOGC=off`. Solod 0.4.0's Go
compatibility allocator stores structs in unscanned byte slices, while its Go
`slices.Append` stub uses ordinary Go append. A slice reachable only through such
a struct is invisible to Go's collector and can be reclaimed during sustained
CLI churn. Disabling collection for this short-lived compatibility test preserves
the explicit C lifetime model. It does not replace allocator tracking or the
compiled CLI AddressSanitizer/LeakSanitizer gate, which run with the actual C
allocator and remain mandatory.

The harness preserves `-check=sanitize -panic=abort` and Clang when freshness requires rebuilding `build/kame.sanitize`. A stale sanitized binary must never be replaced by a checks-only executable during the leak gate.

Live-output tests require human and JSON chunks before recipe completion and
watch output before session termination. Embedding fixtures service the public
host request protocol, including canonical paths and 64-bit timestamp strings.
Artifact-reuse checks establish a successful Kame record before asserting an
unchanged build skips; outputs built by another system are not reuse evidence.
The revision stays stable during build-mode gates because the generated build ID
follows Git HEAD.
