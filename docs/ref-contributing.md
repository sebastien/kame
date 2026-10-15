# Contributing

Kame's portable runtime is written in Solod (So), a Go subset translated to C.
Native and WASM hosts adapt filesystem/process services without moving graph
policy into the host. Use this guide for setup and local checks; the
[architecture](spec/001-architecture.md) and [test contract](spec/013-tests.md)
define the boundaries and acceptance requirements.

## Prerequisites and first build

From the repository root, provide:

- Go 1.26.0 or newer and Solod 0.4.0 (`src/go/kame/go.mod`).
- GNU Make, Clang with native development headers/linker, Bash, and Python 3.
- For CLI tests: `jq`, `timeout`, `flock`, `sha256sum`, `ps`, `touch`, `mktemp`,
  `diff`, `realpath`, and `readlink`; some platforms need GNU utilities on PATH.
- Node.js 18+ for WASM execution/tests; WASI SDK 34 for building the module.
  Mise's `mise.toml` provisions only that SDK, not Go, Solod, or all host tools.
- `gofmt` and `shfmt` when using `make fmt`.

Install the pinned compiler, matching native CI:

```sh
go install solod.dev/cmd/so@v0.4.0
export PATH="$(go env GOPATH)/bin:$PATH"
CC=clang make dist
build/kame.debug --version
dist/kame --version
```

`dist` builds the checks-enabled debug binary and the host-native release CLI;
it does not build APE or WASM. For WASM, run `mise install` to install the pinned
SDK, then `CC=clang make dist-wasm`. Without Mise, use
`CC=clang make dist-wasm WASM_SDK=/absolute/path/to/wasi-sdk`; the directory
must contain `bin/clang` and `bin/wasm-ld`, and supplies compiler-rt as well.
`WASM_CC`/`WASM_LD` can override the individual tools but do not replace the
SDK directory requirement. APE uses pinned Cosmocc provisioning:
`make dist-ape`, or `src/sh/build-ape.sh` without Make. Provisioning may download
tools. None of these commands publishes a release.

## Repository map

| Location | Responsibility |
| --- | --- |
| `src/go/kame/core`, `program` | Reactive engine, resource identity, scheduling, build runtime, cache. |
| `src/go/kame/lang`, `operations` | Parsers, evaluation, templates, Kash, standard operations. |
| `src/go/kame/host`, `cmd`, `cli` | Host adapters, entry points, shared CLI support. |
| `src/js`, `src/py` | WASM wrapper/Node CLI and async Python CLI client. |
| `src/sh`, `tools` | Build, distribution, platform, ABI, and harness helpers. |
| `tests`, `examples` | Black-box fixtures/catalog and runnable consumer lessons. |
| `docs/spec`, `docs/skills/kame` | Normative contracts and practical agent/user references. |
| `.github/workflows` | Platform/artifact-specific CI; inspect each workflow's actual checks. |

## Choose checks by scope

Start with a relevant test, then run the affected gates. CLI tests can rebuild
binaries and create temporary fixtures; run them from the checkout root.

```sh
CC=clang make build/kame.debug
tests/harness.sh -q tests/T013-04-meta-layer-examples.sh
```

| Command | Coverage |
| --- | --- |
| `CC=clang make test-so` | Solod packages plus native host and CLI lifecycle checks. |
| `CC=clang make test-go` | Go subprocess drivers using the compiled native CLI. |
| `CC=clang make test` | Native package, example, Go-driver, and CLI suites. |
| `CC=clang make test-sanitize` | Sanitized package/example checks. |
| `CC=clang make test-leaks` | Sanitized drivers/packages/examples and CLI leak gate. |
| `CC=clang make test-wasm` | WASM ABI/import checks and explicit cross-backend CLI tests. |
| `make test-all` | `test` plus `test-leaks`; **not** the WASM gate. |

Release validation calls for separate native, leak, and WASM runs at one stable
revision; see [TODO.md](../TODO.md). A workflow that only builds an artifact or
runs `--version` is not evidence that the whole suite passed. In particular,
cross-compiling Windows does not establish real-host process cleanup acceptance.
The [test catalog](../tests/CATALOG.tsv) is an inventory, not a pass report.

## Documentation changes

- Keep specs normative: define intended behavior, not completion dates or test
  histories. Put unfinished implementation work in `TODO.md`.
- When a refining spec changes a contract, update the base spec, practical
  reference, glossary, and affected examples together; link to the refinement.
- Keep one preferred vocabulary: definitions bind names, symbols use `:name`,
  and targets may identify files, bare tasks, cached tasks, or services.
- Smoke-test changed snippets with native and WASM backends where applicable,
  check relative links, and run the examples regression above for lesson changes.
- `make fmt` rewrites tracked Go/shell sources and `Makefile.kmk`; it is not a
  Markdown formatter. Review its diff before retaining unrelated changes.

For installation/trust setup, see [installation](ref-installation.md); for
embedding and ownership-safe disposal, see [embedding APIs](ref-embedding.md).
