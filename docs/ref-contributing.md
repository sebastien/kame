# Contributing

Kame's portable runtime is written in Go.
Native and WASM hosts adapt filesystem/process services without moving graph
policy into the host. Use this guide for setup and local checks; the
[architecture](spec/001-architecture.md) and [test contract](spec/013-tests.md)
define the boundaries and acceptance requirements.

## Prerequisites and first build

From the repository root, provide:

- Go 1.26.0 or newer (`src/go/kame/go.mod`), GNU Make, Bash, and Python 3.
- For CLI tests: `jq`, `rg` (ripgrep), `timeout`, `flock`, `sha256sum`, `ps`,
  `touch`, `mktemp`, `diff`, `realpath`, and `readlink`; some platforms need GNU
  utilities on PATH.
- Node.js 18+ for WASM execution and tests. Go builds the module for
  `GOOS=wasip1 GOARCH=wasm`.
- `gofmt` and `shfmt` when using `make fmt`.

Build the native CLI from the checkout:

```sh
make dist
build/kame.debug --version
dist/kame --version
```

`make dist` builds the debug binary, the host-native release CLI, and the
`dist/kash` interpreter link. `make dist-wasm` builds the JS/WASM pair, and
`make release-assets` stages the platform binaries and Windows archive.

## Repository map

| Location | Responsibility |
| --- | --- |
| `src/go/kame/core`, `program` | Reactive engine, resource identity, scheduling, build runtime, cache. |
| `src/go/kame/lang`, `operations` | Parsers, evaluation, templates, Kash, standard operations. |
| `src/go/kame/host`, `cmd`, `cli` | Host adapters, entry points, shared CLI support. |
| `src/js`, `src/py` | WASM wrapper/Node CLI and async Python CLI client. |
| `src/go/kame/cmd/kame-wasm`, `src/go/kame/host/wasm` | WASM reactor entry point and host request forwarding. |
| `src/sh` | Build, provisioning, release and benchmark helpers. |
| `tests` | Black-box CLI catalog plus native/wasm/windows/platform/python harnesses and fixtures. |
| `examples` | Runnable consumer lessons. |
| `docs/spec`, `docs/skills/kame` | Normative contracts and practical agent/user references. |
| `.github/workflows` | Platform/artifact-specific CI; inspect each workflow's actual checks. |

## Choose checks by scope

Start with a relevant test, then run the affected gates. CLI tests can rebuild
binaries and create temporary fixtures; run them from the checkout root.

```sh
make build/kame.debug
tests/harness.sh -q tests/T013-04-meta-layer-examples.sh
# Four workers by default on Bash 5.1+; explicitly serialize when debugging.
KAME_TEST_JOBS=1 make test-cli
```

Published-channel compatibility needs network access and the Go toolchain:

```sh
tests/platform/test-release-build.sh --channel auto
```

After building `dist-wasm`, `node tests/wasm/test-self-build.mjs` checks the
current WASM's self-bootstrap and repeated warm-build memory budget without
using a published binary. `node tests/wasm/test-memory-reuse.mjs` checks freed
block/arena reuse and long-lived watch memory plateaus; it also runs in T010-22.

Concurrency only applies to reviewed entries in `tests/PARALLEL.tsv`, with a reason
for each entry. All unlisted tests run alone after the pool drains; new tests are
exclusive by default. Review helper scripts too before adding a parallel-safe row.

Mixed suites separate their value/format/finite-output checks from lifecycle work:

| Parallel portion | Exclusive companion |
| --- | --- |
| `T004-16` computed outputs | `T004-20` target replacement watch |
| `T006-06` environment/cache values | `T006-10` warm-watch restoration |
| `T007-14` data and digest vectors | `T007-15` dangling-symlink watch |
| `T017-19`, `T017-07`, `T017-17` finite recipes/sessions/graphs | `T017-25` deadlines and failed background work |
| `T017-22` finite deferred cleanup | `T017-24` cleanup deadlines/cancellation |
| `T036-01` byte/JSON presentation | `T036-03` terminals and watch |
| `T038-01` direct recipe values | `T038-06` services and executable watch |

Both portions remain in the full CLI and explicit WASM gates. The Python helpers
still default to `all` when called directly; wrappers select their portion. Timing
budgets and failure checks are unchanged. Digest vectors batch successful pure
evaluations to avoid a CLI/WASM startup for each value; rejection cases stay isolated.

| Command | Coverage |
| --- | --- |
| `make test-unit` | Go packages plus native host and CLI lifecycle checks. |
| `make test-go` | Go subprocess drivers using the compiled native CLI. |
| `make test` | Native package, example, Go-driver, and CLI suites. |
| `make test-sanitize` | Go `checkptr` package and example checks. |
| `make test-leaks` | Go `checkptr` drivers, packages, examples, and CLI suites. |
| `make test-wasm` | WASM ABI/import checks and the curated cross-backend CLI suites. |
| `make test-all` | Runs `test` and `test-leaks`. |

Run `kame bench` from the repository root on Linux (with PTYs and
`/proc` memory accounting). It runs three independent Kash sessions, reporting
first-key, typing, and Enter-to-next-prompt p50/p99 and process RSS at 0, 100,
1,000, and 2,000 submitted commands. The typing probe uses a line that doesn't
match any history entry. It uses private history with a 128 KiB cap so the last two
checkpoints measure steady state after eviction begins. Compare runs on the
same machine: timing and RSS are reported, not enforced as pass/fail
thresholds. The probe does not include terminal emulator rendering or measure
peak memory.

For retained build graphs, run the following from the repository root:

| Target | Measurements |
| --- | --- |
| `kame bench-graph` | 128, 1,024 and 8,192 nodes. Collected heap above an empty engine per bare node, additional bytes per edge, and settled bytes per node. First evaluation nodes/s and latency and allocations for editing a three-node chain amid settled roots. Separate cases vary join width from 4 to 256 branches and measure first dispatch in consumer-first chains up to 4,096 nodes. Compiled programs measure single, duplicate and distinct input batches against an in-memory host. |
| `kame bench-watch` | On POSIX, time 100 atomic edits on both native and Node/WASM CLIs with zero and 100 ms debounce. JSON reports edit-to-start, start-to-idle and end-to-end percentiles. The cancellation workload distinguishes edit-to-cancelled-idle from edit-to-replacement-start. The source reload workload is also separate. Every successful cycle must publish the matching file contents. Linux reports process RSS by watch cycle. |
| `kame bench-soak` | 10,000 cycles with only one input selected at once. Every cycle checks the value, three retained graph nodes and removal of the obsolete watch. Every 1,000 cycles report post-GC heap, object count, heap change since warm-up and average update latency. |

Compare several runs on the same machine. Heap deltas are process-wide estimates;
the smallest graph is especially sensitive to runtime noise. RSS includes reserved
memory and need not shrink. Nightly artifacts include the runner CPU and toolchain;
hosted runners of the same OS version need not have identical hardware. Use the
existing WASM memory-reuse gate for collected module heap and caller-buffer bounds;
compare samples with the same input size.

Release validation calls for separate native, leak, and WASM runs at one stable
revision; see [TODO.md](../TODO.md). A workflow that only builds an artifact or
runs `--version` is not evidence that the whole suite passed. In particular,
cross-compiling Windows does not establish real-host process cleanup acceptance.
The [test catalog](../tests/CATALOG.tsv) is an inventory, not a pass report.
`make test-unit` covers `TestCachedTaskUsesPortableMemoryBackend`.
Native and WASM artifacts rebuild only when declared inputs change. Use
`kame --force TARGET` to force rebuilding after a compiler or build-flag change.

## Documentation changes

- Keep specs normative: define intended behavior, not completion dates or test
  histories. Put unfinished implementation work in `TODO.md`.
- When a refining spec changes a contract, update the base spec, practical
  reference, glossary, and affected examples together; link to the refinement.
- Keep one preferred vocabulary: definitions bind names, symbols use `:name`,
  and targets may identify files, phony tasks, cached tasks, or services.
- Smoke-test changed snippets with native and WASM backends where applicable,
  check relative links, and run the examples regression above for lesson changes.
- `make fmt` formats tracked Go and shell sources and the root `Kamefile`.
  Review the diff for unrelated changes.

For installation/trust setup, see [installation](ref-installation.md); for
embedding and ownership-safe disposal, see [embedding APIs](ref-embedding.md).
