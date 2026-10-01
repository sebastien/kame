# Rules and target templates

`rule` parses one header and its indented recipe. `.kmk` programs compose
rules and lazy definitions; see [Scripts](./scripts.md).

```kame
default : ./build/app

./build/{name}.o : ./src/{name}.c
	cc -c @< -o @>

./build/app : ./build/main.o ./build/util.o
	cc @<* -o @>

clean :
	rm -rf ./build
```

## Target classification

| Header | Kind | Freshness |
| --- | --- | --- |
| `./output : ./input` | File rule | Current when outputs exist and are at least as new as file inputs. |
| `name : dependency` | Bare task | Always stale; runs whenever reached. |
| `task name : dependency` | Cached task | Reuses success when its fingerprint matches. |
| `service name : dependency` | Service | Parses/plans; do not assume execution support. |

Paths must start with `./`, `../`, or `/`. `out.o` is not a file target;
`./out.o` is. Quoted output paths must retain an explicit prefix. File rules
can have multiple outputs; logical task/service rules have exactly one.
Do not mix path and name outputs. With no selected target, request literal
`default`, not the first rule.

Inputs may be logical targets, paths, target templates, quoted values, or
`@(EXPRESSION)` expansions. Lists from input expressions flatten recursively:

```kame
SOURCES = (wildcard ./src/*.c)

./build/sources.txt : @(SOURCES)
	printf '%s\n' @(SOURCES) > @>
```

Declare actual file inputs; a bare task prerequisite alone does not establish
artifact freshness. For aggregates over computed membership, render the
computed set as well as declaring it, and test additions/removals.

## Captures

```kame
./build/{path:**}/{name:*}.o : ./src/{path}/{name}.c
	cc -c @< -o @>
```

`{name}` and `{name:*}` capture a nonempty non-`/` segment. `{path:**}` may
cross `/`; `?` and character classes are supported. Matching is anchored,
captures cannot be empty, and repeated names must match identical text.
Adjacent variable-width captures use leftmost-shortest matching. Input
templates reuse the captured text; these are not Make stems or expression
pattern expansion semantics.

## Recipes and effects

Canonical recipe indentation is one tab; additional shell indentation survives.
The first body line establishes the prefix shared by nonblank body lines.
All rendered lines execute as one shell script, so `cd`, variables, and shell
control flow persist. Recipes are not parsed as Kash and interpolation does
not automatically shell-quote values.

Use `@<`, `@<*`, and `@>` for first input, all inputs, and first output.
See [Templates](./templates.md) for selectors, inline expressions, and recipe
document directives.

| Expression while rendering | Build behavior |
| --- | --- |
| `(out VALUE...)` / `(err VALUE...)` | Defer stdout/stderr output. |
| `(yield VALUE...)` | Atomically supply one declared file output's content. |
| `(write PATH VALUE)` | Defer an atomic write. |
| `(wildcard PATTERN)` / `(read PATH)` | Discover a dependency. |
| `(render PATH PAYLOAD)` | Read/render a tracked template with tracked expression dependencies. |

`yield` requires exactly one file output. Do not combine it with nonempty
shell commands; Kame reports a producer conflict.

Planning resolves headers, not body effects or commands. Rendering may discover
dependencies that must complete before final freshness and recipe execution.
Do not demand process substitutions or writes during planning, even with grants.

## Verify before materializing

```sh
kame do fmt --lang script -n Makefile.kmk
kame do plan ./build/app
kame do inputs --depth -1 ./build/app
kame do span --expand --depth -1 ./build/app
kame -n ./build/app
kame ./build/app
```

Run again to check freshness; change an input to check invalidation. Use
`--force` only to bypass freshness/cache intentionally. Preserve GNU Make
builds while migrating and follow [Make migration](./make-migration.md).
