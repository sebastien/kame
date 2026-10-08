# Kame CLI reference

Kame's primary invocation builds discovered targets or executes explicit sources:

```text
kame [OPTIONS] [TARGET...]
kame [OPTIONS] FILE.km|FILE.kmk|FILE.kash [ENTRY...] [-- ARG...]
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
depth-first, and reject active-ancestry cycles. Repeated nonrecursive includes
expand again; duplicate declarations still fail registration. Inline `-c` /
`--command` sources cannot use includes.

## Execute values, rules, and processes

```sh
kame do run --lang expr -c '(join ["a" "b"] ",")'
kame do run ./values.km result
kame do run ./Makefile.kmk check
kame do run ./publish.kash -- preview
kame do run -c 'name = "Ada"' -c '(uppercase name)'
```

Direct source execution is shorthand for `do run`. Execution accepts ordered,
repeatable file/`-f` and `-c` fragments sharing one scope and engine. Parse all
fragments before effects. Suffixes infer `km`, `kmk`, or `kash` (`.ksh` aliases
`.kash`); inline/stdin defaults to `km`. `--lang km|kmk|kash|expr` overrides
subsequent fragments until changed, not earlier ones. `expr` accepts exactly
one expression. Parse/format modes `template`, `rule`, and `script` are not
execution language names.

Named entries follow the associated `km`/`kmk` source. Use `--entry NAME` if an
entry resembles a source filename. Kash and expr accept no named entries.
Arguments after `--` are program arguments; in discovered build mode they are
literal target operands instead. To build an artifact ending in a program
suffix, use `kame -- ./output.km` or an explicit rule source and `--entry`.

Relative source filenames and evaluation resources resolve beneath the selected
`-C` directory, independent of option order. Includes remain source-relative. Later
fragments share the first fragment's capability policy, not implicit new grants.

The unified runner supersedes the specified `do expr`/`do kash` execution
interfaces. Use `do run --lang expr` for expressions. Native and WASM runner
sessions support `--json` JSON Lines events for values, processes and diagnostics.

## Build targets

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
| `-f`, `--file FILE` | Select a source; repeatable/mixable in execution sessions. Inspection retains a single source. |
| `-c`, `--command TEXT` | Append inline source, defaulting to `km` in the runner; use `--lang kmk` for inline rules. |
| `-C`, `--directory DIR` | Set the working directory. |
| `-j`, `--jobs N` | Limit concurrent graph nodes; `N` must be positive. |
| `-n`, `--dry-run` | Render without writing effects or starting processes. |
| `--force` | Bypass file freshness and cached-task hits. |
| `-o`, `--output ansi|text|json` | Select presentation for every command; ANSI is the default and falls back on redirected or limited terminals. |
| `--json` | Alias for `--output json`; execution uses JSON Lines, static inspection uses its established JSON document. |
| `--verbose` | Report cache decisions and warnings. |
| `--shell SHELL` | Set recipe shell executable/arguments; repeatable. |
| `--env NAME=VALUE` | Add or replace a recipe environment entry; repeatable. |
| `--timeout MS` | Set process timeout in milliseconds; runner sessions also share one invocation deadline across fragments. |
| `--retry N` | Retry failed commands up to `N` times. |
| `--log-limit N` | Limit captured bytes per process stream. |
| `--capture-limit N` | Limit each command substitution's captured stdout; default 1 MiB, independent of retained recipe logs. |
| `--watch` | Rebuild tracked inputs; primary build mode only, not `do run`. |
| `--define NAME=VALUE` | Override a declared definition with literal string data. |
| `--tool NAME=PATH` | Override executable resolution for a declared tool. |

`env.NAME=VALUE` is shorthand for a process environment override. Bare
`NAME=VALUE` overrides a declared global or binds a declared named-target argument.
For example, `deploy {environment} {region=us-east} :` accepts
`kame deploy environment=production`. Arguments are literal strings without
whitespace; program arguments after `--` are a separate mechanism.

Recipes receive an explicit complete environment. The CLI starts from its
environment and applies `--env` replacements; cache fingerprints include the
authored environment assignments and named environment reads. Rules that execute
processes also fingerprint the complete effective child environment after overrides;
pure/declarative rules ignore unread inherited values.

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
| `do plan [--depth N] [TARGET...]` | Recursive resources, producers, bindings, freshness, and dependency-ordered stages; shared graph for multiple roots. |
| `do inputs [--depth N] [TARGET]` | Reachable input files and separately labeled logical/configuration prerequisites. |
| `do outputs [--depth N] [TARGET]` | Reachable declared artifacts and their producers, including intermediate and sibling outputs. |
| `do span [--expand] [--depth N] [TARGET]` | Separate static inputs/outputs from evaluation-dependent inputs; `--json` retains the span document. `--expand` evaluates dynamic definitions without executing recipes. |
| `do cat [TARGET]` | Materialize one target, then write its exact file bytes or definition value without a newline. |

`plan`, `inputs`, and `outputs` default to depth `-1` (unlimited); `span` defaults
to `1`. Depth `0` returns no dependency inventory; `1` reports direct resources.
Read-only computed inputs resolve automatically in the recursive commands:
`span --expand` is not a prerequisite. Inspection never renders recipes, runs
producers, or writes artifacts/cache records. Deferred generated-input,
runtime-discovery, and opaque-process-I/O boundaries are reported explicitly.
Artifact status is `built`, `outdated`, `missing`, or `unknown`; existence alone
does not prove reuse.

Under `--json`, `plan`, `inputs`, and `outputs` emit one schema-2 document with
`targets`, `depth`, `scope`, `truncated`, `resources`, `producers`, `dependencies`,
`stages`, and `deferred`; input/output inventories also have resource-ID `items`.
This replaces old plan records and string edge arrays. `span` retains schema 1.
See [Build inspection](../../../spec/037-build-inspection.md).

## Format and parse Kame sources

```sh
kame do fmt -n Makefile.kmk       # report files that differ; exit 1 if any
kame do fmt -i Makefile.kmk       # replace formatted files atomically
kame do fmt < Makefile.kmk        # write canonical source to stdout
kame do parse --lang script Makefile.kmk
```

`do fmt` defaults to `script` language. `--lang` (`-l`) also accepts `expr`,
`template`, `rule`, `km`, `kmk`, and `kash`. Use `script` for rule programs and `expr` for
individual expressions. Use an explicit mode rather than
assuming filename inference in language tools. `do parse` prints a human AST tree
with spans; add `--json` for the stable AST document. Parsing `template` inspects
inline syntax. Formatting `template` handles document directives with
`--comment STYLE` and preserves non-directive bytes; never format an HTML/config
template in `script` mode.

## Evaluate a standalone expression

```sh
kame do run --lang expr -c '(join ["a" "b"] ",")'
kame do run --lang expr --allow-read=./src -c '(wildcard ./src/*.c)'
kame do run --lang expr --allow-env=HOME -c '(env "HOME")'
```

Value-first (`km`/`expr`) sessions deny filesystem, environment, and process
capabilities by default.
Grant only what the expression needs:

| Option | Grant |
| --- | --- |
| `--allow-read[=ROOTS]` | Filesystem reads and globs, optionally under listed roots. |
| `--allow-write[=ROOTS]` | Filesystem writes, optionally under listed roots. |
| `--allow-run[=ROOTS]` | Process execution, optionally restricted by executable path. Does not override phase restrictions. |
| `--allow-env[=NAMES]` | Reads of named environment variables. |

Arguments after `--` are available to the expression as `args`. These grants
apply to standalone expression execution; normal builds grant recipe execution
and working-directory read/write access, while language-level environment reads
and access outside the working directory remain denied without explicit grants.
Explicit grant options belong to `do run`/explicit source sessions, not discovered
primary builds; `do render` also accepts them. Scoped run grants require explicit
executable paths for direct argv processes; legacy shell-text operations require
an unrestricted run grant.
Grants do not sandbox a launched executable's internal filesystem access.

Render a document with `(render SOURCE [PAYLOAD] [STYLE])`, for example:

```sh
kame do run --lang expr --allow-read -c '(render ./page.html [title: "Hello"])'
```

`do render` renders document files directly and supports parse-only `--check`:
see [Templates](./templates.md) and `kame do render --help` for payload and style
options. Document rendering is not source formatting.

## Automation and diagnostics

- `--json` execution emits JSON Lines events/diagnostics. Static inspection,
  AST, tool, and cache lists emit JSON documents; do not assume one framing for
  every command or parse human output for automation.
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
