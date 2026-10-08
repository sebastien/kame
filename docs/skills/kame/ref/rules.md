# Rules and target templates

`rule` parses one header and its indented recipe. `.kmk` programs compose
rules and lazy definitions; see [Scripts](./scripts.md).
`<-` is also accepted as a header separator; formatting canonicalizes it to `:`.

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
| `./output : ./input` | File rule | Reuses a successful record when input/output content digests and recorded dependencies match. |
| `name : dependency` | Bare task | Always stale; runs whenever reached. |
| `task name : dependency` | Cached task | Reuses success when its fingerprint matches. |
| `service name : dependency` | Managed service | Runs while required; readiness, health, restart and cleanup follow [spec 024](../../../spec/024-managed-services.md). |

Paths must start with `./`, `../`, or `/`. `out.o` is not a file target;
`./out.o` is. Quoted output paths must retain an explicit prefix. File rules
can have multiple outputs; logical task/service rules have exactly one.
Do not mix path and name outputs. With no selected target, request literal
`default`, not the first rule.

File freshness uses content, not modification time. Touching unchanged bytes
does not rebuild; changing an input or output's bytes invalidates reuse even
with preserved timestamps. The first run establishes a successful record, and
a file rule without declared or discovered dependencies is always stale.
`always ./output : ./input` and `--force` bypass reuse.

Outputs may also use `@(EXPRESSION)` or interpolated tokens:

```kame
PATH_BUILD = "./build"
@(PATH_BUILD)/{name}.o : ./src/{name}.c

OUTPUTS = [./build/one [./build/two :nil]]
@(OUTPUTS) : ./seed
```

Outputs resolve purely before target selection. Explicit path prefixes are
checked after expansion; `"build"` is not a replacement for `"./build"`.
Resolved capture syntax becomes active. Standalone expressions flatten nested
lists; nil and empty lists contribute nothing, but a rule must retain at least
one output. Interpolated tokens render one target. Computed logical names obey
the ordinary task/service limits. Host reads, processes, writes, and references
to this rule's captures, arguments or selectors are unavailable during output
resolution.

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
Anonymous output captures `{*}`/`{**}` bind positional `_0`, `_1`, etc.; inputs
can reuse them as `{_0}`/`{_1}`. Named captures also occupy positional slots.
Regex captures `{name:~REGEX}` and `{~REGEX}` use the portable bounded matcher;
see [Pattern extensions](../../../spec/029-pattern-extensions.md).

Single named tasks can declare required/defaulted arguments:
`deploy {environment} {region=us-east} :`. Invoke with
`kame deploy environment=production`; these string bindings are distinct from
file-pattern captures and participate in rule-instance/cache identity.

`|` introduces order-only inputs, excluded from normal input selectors and
content freshness. Commas separate input sequence groups: later groups are
requested after earlier groups complete, not a global barrier across targets.

## Recipes and effects

Canonical recipe indentation is one tab; additional shell indentation survives.
The first body line establishes the prefix shared by nonblank body lines.
All rendered lines execute as one shell script, so `cd`, variables, and shell
control flow persist. Shell recipes are opaque text; selecting `[shell: kash]`
uses the Kash parser after template expansion. Interpolation does not
automatically shell-quote values.
Header settings `; [shell: kash env: [MODE: mode]]` resolve purely before
prerequisite execution; `shell` overrides build-wide `SHELL`. The legacy suffix
`; env "MODE=release"` is an alternative, not an additional metadata record.

Use `@<`, `@<*`, and `@>` for first normal input, all normal inputs, and first
output. In file recipes, `@<?` selects normal file inputs whose content changed
since the successful run; missing outputs or records select all normal file
inputs. Order-only prerequisites after `|` are excluded from input selectors.
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
