# Kame language reference

Kame scripts combine lazy definitions, Lisp-like expressions, target rules, and
string templates. A script may contain comments, blank lines, includes,
definitions, and rules. Comments begin with `#` or `//` after leading
whitespace.

## Includes

Use a top-level `include` directive to compose a build from file fragments:

```kame
include ./rules/common.kmk
include "./rules/release.kmk"

default : release
```

Paths are resolved relative to the source file containing the directive. Kame
expands includes depth-first at their declaration location, so the included
definitions and rules share one scope and preserve source order. A source may
not include the same file twice, directly or indirectly; Kame reports an
include cycle before compilation. Includes require a file-backed source and are
therefore unavailable with `-c` / `--command`.

## Definitions and expressions

```kame
VERSION = "1.2.3"
SOURCES = (wildcard ./src/*.c)
FLAGS = -Wall -Wextra
MESSAGE = "building @(count SOURCES) sources"

(object source) = (replace ./{**}/{*}.c ./build/{_0}/{_1}.o source)
```

Definitions are lazy. Their right-hand side is classified by its first token:

- `(` or `[` starts one expression value.
- `"` starts one quoted string template.
- Other text is one or more unquoted string templates; whitespace-separated
  values form a list.

Expressions use parentheses for applications and square brackets for lists:

```kame
(join ["-I" ./include] "")
(map (replace ./{**}/{*}.c ./build/{_0}/{_1}.o) SOURCES)
(let [name "app"] (join ["./build/" name] ""))
```

Useful build operations include `wildcard`, `read`, `write`, `exists?`,
`stat`, `basename`, `dirname`, `ext`, `joinpath`, `map`, `filter`, `join`, and
`replace`. `wildcard` returns sorted paths and establishes a dynamic glob
dependency. Filesystem operations are capability-controlled in standalone
expression evaluation and are dependency-aware during builds.

See the [standard-library reference](./library.md) for every registered
operation, signature, capability requirement, and build effect.

## Rules and targets

```kame
default : ./build/app

./build/app : ./build/main.o ./build/util.o
	cc @<* -o @>

./build/{name}.o : ./src/{name}.c
	cc -c @< -o @>

report :
	@(out "building @(count SOURCES) sources\n")

./build/version.txt :
	@(yield "version 1\n")
```

Rule-header classification is significant:

| Header form | Kind | Freshness behavior |
| --- | --- | --- |
| `./output : ./input` | File rule | Skips when all outputs exist and are at least as new as all file inputs. |
| `name : dependency` | Bare task | Always stale; runs whenever reached. |
| `task name : dependency` | Cached task | May reuse a successful cached result when its fingerprint matches. |
| `service name : dependency` | Service | Parses and plans, but execution is not initially supported. |

File paths must begin with `./`, `../`, or `/`. A bare name is a logical target,
not a file path. A rule may declare multiple file outputs, but a task, cached
task, or service has one logical output target.

Each nonblank rule-body line has the rule's indentation; canonical formatting
uses one tab. Kame renders all body lines as one shell script, so shell state
such as `cd`, variables, and control flow persists across lines.

## Templates and recipe selectors

Rule recipes are string templates. The most useful forms are:

| Form | Meaning |
| --- | --- |
| `@(EXPRESSION)` | Evaluate an expression and render its value. |
| `@{reference}` | Resolve and render a template reference. |
| `@<` / `@<*` / `@<#` | First input / all inputs / input count. |
| `@<N` / `@<A..B` | Zero-based input index / half-open input slice. |
| `@>` / `@>*` / `@>#` | First output / all outputs / output count. |
| `@>N` | Zero-based output index. |

Values in an all-input or all-output selector render with single spaces between
items. Missing rule context and invalid selector indexes are diagnostics rather
than empty substitutions.

Target templates use named capture groups:

```kame
./build/{name}.o : ./src/{name}.c
	cc -c @< -o @>

./build/{path:**}/{name:*}.o : ./src/{path}/{name}.c
	cc -c @< -o @>
```

Captures match the requested output and render corresponding input and output
templates. They are not GNU Make's `$*`; prefer declared input/output templates
and recipe selectors over trying to reconstruct a stem in shell code.

## Effects and dynamic dependencies

These operations have build-specific behavior:

| Operation | Use in a rule recipe |
| --- | --- |
| `(out VALUE...)` | Defer output to stdout. |
| `(err VALUE...)` | Defer output to stderr. |
| `(yield VALUE...)` | Atomically provide the content of one declared file output. |
| `(write PATH VALUE)` | Defer an atomic write. |
| `(wildcard PATTERN)` / `(read PATH)` | Discover a dependency while rendering. |

`yield` is valid only for a file rule with exactly one output. Do not combine a
nonempty shell command and `yield` in one rendered rule: Kame reports an output
conflict rather than guessing which producer owns the file.

Planning evaluates rule inputs and outputs but not body effects or shell
commands. Rendering can discover additional dependencies; Kame schedules them
before it decides final freshness and executes the recipe.

## Patterns and path transformations

Pattern literals make source-to-output transformations explicit:

```kame
OBJECTS = (map (replace ./{**}/{*}.c ./build/{_0}/{_1}.o) SOURCES)
```

`./{**}/{*}.c` is a match pattern and `./build/{_0}/{_1}.o` is an expansion
pattern. With two pattern arguments, `replace` returns a one-argument callable,
which makes it suitable for `map` and pipes. A non-matching item produces
`:nil`; it is not an error.

## Rules for agents

- Use expression syntax for computed values; do not paste Make functions into
  definitions or recipes.
- Use `include ./path.kmk` for shared definitions and rules; paths are relative
  to the including source, not the process working directory.
- Use explicit file paths in headers, even when a shell command could infer
  them.
- Prefer a named target template and corresponding input template over shell
  text manipulation.
- Treat recipes as shell text. Kame does not parse shell syntax or quote shell
  arguments for you.
- Use `kame do plan TARGET` and `kame do span --expand TARGET` to distinguish
  declared inputs from dependencies discovered by expressions.
