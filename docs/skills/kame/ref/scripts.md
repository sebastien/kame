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
FLAGS = ["-Wall" "-Wextra"]
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

Every RHS is one expression: `alias = other` references a value, `number = 42`
is numeric, text requires quotes, and lists require brackets. This applies to
function bodies too. Kash retains its reference wrappers and value recovery.
Parse failures never fall back to text; unused definitions remain unevaluated.

To migrate, quote bare textual values and bracket word lists. Explicit paths
such as `./file` remain valid atoms. Quoted values now use expression-string
escapes/interpolation and pattern syntax; use verbatim strings for inert data.
`SHELL = kash` remains the
built-in constructor reference; use `SHELL = "bash"` or
`SHELL = ["/bin/bash" "-eu"]` for external shells. CLI overrides remain literal.

## Declarative contributions

```kame
CHECKS ?= []
CHECKS += [./tests/unit]
CHECKS += EXTRA_CHECKS
EXTRA_CHECKS = [./tests/smoke]
```

In `.km`/`.kmk`, `+=` lazily concatenates lists in source/include/session order,
without flattening nested items or removing duplicates. An earlier `=`/`?=` is
the base; otherwise the base is `[]`. Later `?=` is skipped, and later `=` is
still a duplicate error. Non-list bases/contributions fail `EXPR_INVALID`.
CLI/environment overrides replace the whole aggregate with literal string data.
Self/indirect references to the final aggregate fail `DEP_CYCLE` when demanded;
there is no previous-accumulator value. Functions and Kash do not support `+=`.

## Value entry points

```kame
name = "Ada"
(out (cat "Hello, " name))
```

```sh
kame ./greet.km
kame do run ./greet.km name
kame do run --print-result -c 'name = "Ada"' -c '(uppercase name)'
```

Without named entries, `.km` runs expression statements in source order but
emits only explicit `out`/`err` effects. `--print-result` opts into final value
display, independent of whether an output operation ran. `expr` retains result
display. Entries display only named definitions, skipping
unrelated statements; selecting a function value does not implicitly call it.
A definitions-only value session falls back to the combined `default` when
there is no other executable work; without it, it reports `TGT_NO_DEFAULT`.
A definitions-only fragment may simply supply bindings for a later fragment.
An implicit `default` is evaluated but not displayed unless `--print-result`
is selected. To migrate machine-readable utilities, use `(out VALUE)` rather
than suppressing an implicit result with `(nop OPERATION "")`.

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
- Arguments after `--` bind the shared `args` list (`@*`), with indexed access
  through `args.0`. Function/Kash-call arguments temporarily supply the nearest
  argument frame. Builds with no operands expose `[]`. Recipe-local `cd`, shell
  variables, or stage environment overrides never mutate the parent session.
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
