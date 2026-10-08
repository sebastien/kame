---
name: kame
description: Author, inspect, format, and debug Kame expressions, value scripts (.km), rule programs (.kmk), string and document templates, and Kash process scripts (.kash/.ksh). Use for the kame CLI, Makefile.kmk files, target patterns, build graphs, cache-aware tasks, text rendering, process pipelines, and GNU Make migration.
---

# Kame languages

Kame combines a Lisp-like value language, composable scripts, text templates,
declarative build rules, and Kash process orchestration on one incremental
engine. These surfaces share expressions and values, not one interchangeable
outer grammar. They can be used independently; a value or template task does
not need a build file.

## Choose the language first

| Surface | Use it for | Reference |
| --- | --- | --- |
| `expr` | Exactly one value, application, lambda, reference, or value pipe | [Expressions](./ref/expressions.md) |
| `km` / `.km` | Lazy definitions and top-level expression statements | [Scripts](./ref/scripts.md) |
| `script` / `kmk` / `.kmk` | Definitions and build rules; `script` is the general parse/format mode | [Scripts](./ref/scripts.md) |
| `template` | Inline expansions and document directives; parse inspects inline syntax, fmt formats documents | [Templates](./ref/templates.md) |
| `rule` | One rule header and its recipe, including target captures | [Rules](./ref/rules.md) |
| `kash` / `.kash` / `.ksh` | Typed command arguments, streaming pipelines, process control | [Kash](./ref/kash.md) |

Parse/format modes are not all execution modes. `do run` accepts `km`, `kmk`,
`kash`, and `expr`; templates execute through `(render ...)`, and rules through
a rule program. See [CLI](./ref/cli.md) for invocation and capability policy.

## Working procedure

1. **Inspect the source and its caller.** Identify the outer parser, expression
   scope, execution phase, inputs, outputs, and required capabilities. Read the
   matching reference before borrowing syntax from another surface.
2. **Start with a small runnable slice.** Evaluate one expression, run one value
   script, render one template, or inspect one rule before expanding it.
3. **Keep language boundaries explicit.** Use expressions for values, document
   directives for text structure, rule headers for dependencies, and Kash for
   direct process graphs. Recipes default to shell text; `SHELL = kash` or
   `; [shell: kash]` explicitly selects Kash after template expansion.
4. **Compose without erasing context.** File-backed `include ./path.km` or
   `include ./path.kmk` is relative to its containing source. `do run` can share
   definitions across ordered fragments; changing parsers must not add grants.
5. **Validate without effects first.** Use `do parse --lang LANG` and
   `do fmt --lang LANG -n FILE`. For rules, inspect `do plan`, `do inputs`, and
   `do span --expand`, then dry-run with `-n` before materializing. Document
   templates are data: use only `do fmt --lang template --comment STYLE` to
   format their directives while preserving other document bytes.
6. **Check the installed command surface.** Use command-specific help and a
   small smoke test; specifications may describe work not yet implemented on
   the selected backend. Do not substitute a system shell for unsupported Kash.

For GNU Make migration, inventory targets and metaprogramming first, keep the
old `Makefile`, and add `Makefile.kmk`. Discovery tries `Makefile.kmk`,
`make.kmk`, then `src/kmk/main.kmk`, never GNU Make's `Makefile`.

## Essential conventions

Values use whitespace-separated lists and records, not comma-separated items:

```kame
(let [project [name: "app" files: [./src/main.c ./src/util.c]]]
  (cat project.name ": " (count project.files)))
```

`(operation args...)` is an application; `([args...] body...)` is a lambda.
Only `:nil` and `:false` are false. Definitions are lazy, not mutable shell
assignments. Use `@(name)` to render a reference in template text.

Build rules declare resources explicitly:

```kame
SOURCES = (wildcard ./src/*.c)

default : ./build/app

./build/{name}.o : ./src/{name}.c
	cc -c @< -o @>

./build/app : ./build/main.o ./build/util.o
	cc @<* -o @>

clean :
	rm -rf ./build
```

- `./build/app` is a **file target**. The `./` prefix is significant: a token
  such as `build/app` is not an implicit path.
- `default` and `clean` are **bare tasks**, which run whenever reached.
- `{name}` connects corresponding target-template captures in the rule header.
- `@<`, `@<*`, and `@>` render the first input, all inputs, and first output
  in a recipe. They correspond most closely to Make's `$<`, `$^`, and `$@`.
- `@(EXPRESSION)` evaluates and renders a value; `@(reference)` is the reference
  spelling. Do not use the obsolete `@{reference}` form.
- `include ./rules/common.kmk` merges a file-backed source at that location.

Keep these boundaries distinct:

- Expression strings support `{(EXPRESSION)}` and `@(EXPRESSION)`; verbatim
  `"""..."""` strings stay raw until explicitly passed to `render`.
- Document templates use `@if`, `@for`, `@with`, `@let`, `@include`, `@match`,
  `@case`, `@raw`, and `@end` directives, optionally inside host-language comments.
  Blocks can also be inline; labeled endings such as `@end(for)` are supported.
- Kash `$name` looks up a Kame value; `@(EXPRESSION)` computes a typed argument;
  `$(COMMAND)` captures a Kash process's stdout. Lists splice argv only as
  standalone unquoted substitutions; no implicit shell splitting or globbing.
- `$(COMMAND)` is also an expression atom, but requires process authority and
  an allowed execution phase. Plain Kame strings keep it literal; raw recipe
  text leaves it to the selected recipe interpreter.
- `@<` and `@>` need rule context. Target captures `{name}` in rule headers
  are not document interpolation or expression-pattern expansion rules.

Do not assume GNU Make compatibility beyond the documented mappings. In
particular, imports, implicit rules, `eval`, `define`, target-specific variables,
and Make's function language are not interchangeable with Kame features.
Kame does support order-only inputs after `|`; redesign other cases deliberately.

## References

- [Migrating from Make](./ref/make-migration.md): conversion process,
  mappings, and non-equivalences.
- [Kame language reference](./ref/language.md): definitions, rules,
  parser boundaries, and a map to the focused language references.
- [Expressions](./ref/expressions.md): values, calls, functions, references,
  special forms, pipes, patterns, and embedded processes.
- [Scripts](./ref/scripts.md): value/rule programs, lazy definitions, includes,
  and shared execution sessions.
- [Templates](./ref/templates.md): inline expansions, selectors, document
  directives, payloads, styles, and rendering.
- [Rules](./ref/rules.md): target classification, captures, recipes, freshness,
  dynamic dependencies, and effects.
- [Kash](./ref/kash.md): commands, typed argv, capture, pipelines, control,
  recovery, and process ownership.
- [Kame CLI reference](./ref/cli.md): execution, inspection,
  formatting, capabilities, cache behavior, and diagnostics.
- [Standard library](./ref/library.md): expression operations, capabilities,
  and build effects.
