# Scripts and source composition

Scripts compose existing parsers rather than inventing another value language.

| Mode | Source contents |
| --- | --- |
| `km` / `.km` | Comments, includes, lazy definitions, top-level expressions; no rules. |
| `kmk` / `.kmk` | Rule program with definitions and recipes. |
| `script` | General composite parse/format grammar; not a `do run` language name. |

Whole-line `#` and `//` comments are accepted. Balanced expressions may span
lines. Indentation without a preceding rule is invalid. Rule programs execute
selected targets, not top-level expressions as an alternate entry mechanism.

## Definitions are lazy

```kame
VERSION = "1.2.3"
FLAGS = -Wall -Wextra
SOURCES = (wildcard ./src/*.c)
(greet name) = (cat "Hello, " name)
default = (greet "Ada")
```

A value header is `NAME = RHS`; a function header is `(NAME PARAM...) = RHS`.
A final `rest...` parameter collects remaining arguments. Definitions are
demanded on reference, not executed as sequential shell assignments.
`NAME ?= RHS` supplies a lazy default if the name is not already configured;
`?=` must be contiguous. `--define NAME=VALUE` overrides a declared definition
with literal string data, not expression source.

RHS classification matters:

1. Leading `(`, `[`, or `$(`: one expression value; failure does not fall back
   to text.
2. Leading `"`: one quoted string template (or a raw verbatim string).
3. Otherwise: whitespace-separated unquoted string templates; one is a scalar,
   several form a list.

Thus `number = 42` uses textual RHS classification, while the statement `42`
is an integer. Use `number = (nop 42)` when a typed scalar definition is needed.
Kash definitions instead take expression operands, not word-list RHSs.

## Value entry points

```kame
name = "Ada"
(cat "Hello, " name)
```

```sh
kame ./greet.km
kame do run ./greet.km name
kame do run -c 'name = "Ada"' -c '(uppercase name)'
```

Without named entries, `.km` runs expression statements in source order and
returns the last value. Entries request only named definitions, skipping
unrelated statements; selecting a function value does not implicitly call it.
A definitions-only value session falls back to the combined `default` when
there is no other executable work; without it, it reports `TGT_NO_DEFAULT`.
A definitions-only fragment may simply supply bindings for a later fragment.

## File-backed includes

```kame
include ./values.km
include "./rules/common.kmk"

default : ./build/app
```

Paths resolve relative to the containing file, not evaluation cwd. Includes
expand depth-first at their declaration position and share scope/source order.
Active-ancestry cycles are errors. Repeated nonrecursive includes expand again;
ordinary registration still rejects duplicate declarations. Inline `-c` sources
have no include base and cannot include files. Value programs cannot introduce rules through
includes; Kash inclusion is not provided by this directive.

`include? PATH` skips an absent optional file, not other loading errors.
`when PREDICATE` / `otherwise` / `end` selects declarations before registration;
only selected includes are loaded. Predicates use pure configuration values,
not host reads, processes, writes, or rule-local bindings. Typed rule generation
uses `generate NAME = EXPRESSION`; see
[Generated declarations](../../../spec/025-generated-declarations.md).

## Shared execution sessions

```sh
kame do run ./values.km ./functions.km -c '(report version)'
kame do run ./Makefile.kmk check -c '(out VERSION)'
kame do run --lang kmk -f ./custom.build --lang km -c '(out VERSION)'
```

Use the unified runner where supported:

- Repeat/mix files, `-f`, and `-c` in execution order. File suffixes select each
  parser; inline/stdin defaults to `km`, regardless of the preceding file.
- `--lang` is forward-scoped until changed. It does not change authority.
- Parse/register all fragments before effects; retain each source's parser and
  spans. Fragments cannot split an expression, recipe, or control block.
- Share one scope/engine and invocation policy. Forward references are valid;
  duplicate top-level definitions are errors, not reassignment.
- Entries belong to the preceding `km`/`kmk` fragment; use `--entry NAME` for
  an entry resembling a source filename. Kash/expr fragments have no entries.
- Arguments after `--` bind the shared `args` frame. Recipe-local `cd`, shell
  variables, or exports never mutate the parent Kame session.
- Relative source filenames and resource paths resolve under the selected `-C`
  directory, regardless of option order; includes remain source-relative.
  Do not assume execution changes directory to the source file's location.

## Verify

```sh
kame do run ./greet.km
kame do parse --lang script ./Makefile.kmk
kame do fmt --lang script -n ./Makefile.kmk
```

Parse/format tools accept `km`, `kmk`, and `script`, including value-program
expression statements. Use `--lang km` to inspect a value-only program and
`--lang expr` for an individual expression; execution still uses the runner.

For rule execution, follow [Rules](./rules.md); for command statements, follow
[Kash](./kash.md). See [CLI](./cli.md) for defaults and backend support caveats.
