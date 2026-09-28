# Kame CLI reference

Kame's primary invocation builds targets:

```text
kame [OPTIONS] [TARGET...]
kame do COMMAND [OPTIONS] [ARG...]
```

With no target, Kame builds `default` when it exists; otherwise the invocation
fails with `TGT_NO_DEFAULT` and reports the available named targets. Every
target-taking command resolves targets this way. A bare invocation with no
source prints help. Source discovery
tries `Makefile.kmk`, `make.kmk`, then `src/kmk/main.kmk` below the selected
working directory.

File-backed Kame sources can use `include PATH` to merge shared definitions and
rules. Relative include paths are resolved from the including source, expanded
depth-first, and reject repeated or cyclic inclusion. Inline `-c` / `--command`
sources cannot use includes.

## Build and run

```sh
kame                         # build default
kame ./build/app             # build one file target
kame clean                   # run one bare task
kame -f Build.kmk test       # select a source explicitly
kame -C subproject default   # build from another directory
kame -j 4 default test       # share one dependency graph across roots
kame -n ./build/app          # plan and render, with no effects or processes
kame --force bundle          # ignore freshness and cached-task hits
```

Primary options:

| Option | Meaning |
| --- | --- |
| `-f`, `--file FILE` | Select one Kame source file. |
| `-c`, `--command TEXT` | Supply inline build source. Mutually exclusive with `--file`. |
| `-C`, `--directory DIR` | Set the working directory. |
| `-j`, `--jobs N` | Limit concurrent graph nodes; `N` must be positive. |
| `-n`, `--dry-run` | Render without writing effects or starting processes. |
| `--force` | Bypass file freshness and cached-task hits. |
| `--json` | Emit JSON Lines events rather than human output. |
| `--verbose` | Report cache decisions and warnings. |
| `--shell SHELL` | Set recipe shell executable/arguments; repeatable. |
| `--env NAME=VALUE` | Add or replace a recipe environment entry; repeatable. |
| `--timeout MS` | Set per-command timeout in milliseconds. |
| `--retry N` | Retry failed commands up to `N` times. |
| `--log-limit N` | Limit captured bytes per process stream. |

Recipes receive an explicit complete environment. The CLI starts from its
environment and applies `--env` replacements; cache fingerprints include the
process environment and other execution inputs that affect results.

## Inspect before executing

Use inspection commands to verify a migration or diagnose a target without
guessing how Kame selected it:

```sh
kame do plan ./build/app
kame do inputs --depth -1 ./build/app
kame do outputs --depth -1 ./build/app
kame do span --depth -1 ./build/app
kame do span --expand --depth -1 ./build/app
kame -n ./build/app
```

| Command | Output and behavior |
| --- | --- |
| `do plan [TARGET...]` | JSON plan(s): selected rule, captures, declared inputs, outputs, and freshness. Does not evaluate body effects or run processes. |
| `do inputs [--depth N] [TARGET]` | JSON array of input edges. |
| `do outputs [--depth N] [TARGET]` | JSON array of output edges. |
| `do span [--expand] [--depth N] [TARGET]` | JSON separating static inputs/outputs from evaluation-dependent inputs. `--expand` evaluates dynamic definitions without executing recipes. |
| `do cat [TARGET]` | Materialize one target, then write its exact file bytes or definition value without a newline. |

Depth `0` returns no edges, `1` returns direct edges, and `-1` traverses without
a depth limit. Use `span` when a rule uses `wildcard`, `read`, or other
expression-driven dependency discovery; use `inputs` for the resolved planned
input graph.

## Format and parse Kame sources

```sh
kame do fmt -n Makefile.kmk       # report files that differ; exit 1 if any
kame do fmt -i Makefile.kmk       # replace formatted files atomically
kame do fmt < Makefile.kmk        # write canonical source to stdout
kame do parse --lang script Makefile.kmk
```

`do fmt` defaults to `script` language. `--lang` also accepts `expr`,
`template`, and `rule`. `do parse` prints a stable JSON AST, including source
spans, which is useful for language tooling rather than ordinary build checks.

## Evaluate a standalone expression

```sh
kame do expr -c '(join ["a" "b"] ",")'
kame do expr --allow-read=./src -c '(wildcard ./src/*.c)'
kame do expr --allow-env=HOME -c '(env "HOME")'
```

`do expr` denies filesystem, environment, and process capabilities by default.
Grant only what the expression needs:

| Option | Grant |
| --- | --- |
| `--allow-read[=ROOTS]` | Filesystem reads and globs, optionally under listed roots. |
| `--allow-write[=ROOTS]` | Filesystem writes, optionally under listed roots. |
| `--allow-run` | Collected shell execution. |
| `--allow-env[=NAMES]` | Reads of named environment variables. |

Arguments after `--` are available to the expression as `args`. These grants
apply to standalone expression execution; normal builds grant recipe execution
and working-directory read/write access, while language-level environment reads
and access outside the working directory remain denied without explicit grants.

## Automation and diagnostics

- `--json` emits one JSON object per line. It includes execution events and
  diagnostics; do not mix it with a human-output parser.
- Human recipe stdout and stderr retain their corresponding streams. Successful
  definition targets print their value; file contents are retrieved with
  `kame do cat`.
- Exit status is `0` for success, `1` for build/parse/evaluation failures (and
  `fmt -n` differences), and `2` for CLI usage errors.
- `--color auto|always|never` and
  `--diagnostic-format human|plain` control diagnostic presentation.
- The first `SIGINT` or `SIGTERM` cancels active roots and their process groups;
  a repeated signal may terminate immediately.

For a migration loop, prefer: format check → `plan` → graph inspection →
dry-run → targeted build. This makes target classification and dependency
mistakes visible before a recipe changes the working tree.
