---
name: kame
description: Author, inspect, and migrate Kame build files, especially when converting GNU Make projects. Use for Makefile.kmk files, the kame CLI, Kame rules, target templates, build graph inspection, and cache-aware tasks.
---

# Kame build files

Kame is a modern build system in the spirit of GNU Make, with a Lisp-like
language and a streaming incremental engine that should cover all your needs.
It uses `.kmk` build files; its default discovery order is
`Makefile.kmk`, `make.kmk`, then `src/kmk/main.kmk`.

Use this skill when an agent needs to add or change a Kame build, migrate a
`Makefile`, explain Kame behavior, or diagnose a Kame target.

## Working procedure

1. **Inspect before translating.** Read the existing build file and inventory
   default goals, produced files, always-run commands, generated inputs,
   pattern rules, variables, and GNU Make metaprogramming.
2. **Preserve the old build while migrating.** Add `Makefile.kmk`; do not
   replace a project's `Makefile` unless the user explicitly asks. Kame does
   not discover `Makefile` as a build source.
3. **Model outputs accurately.** Use explicit `./`, `../`, or absolute paths for file
   outputs and inputs. Use a bare target for an always-run task. Use `task NAME`
   only when cacheable task behavior is intended.
4. **Start with an inspectable vertical slice.** Convert one file rule and its
   dependencies, then run `kame do plan TARGET` and `kame do inputs TARGET`
   before expanding the conversion.
5. **Keep shell logic in recipes.** A Kame rule body is one shell script, so
   shell variables and `cd` persist between its rendered lines. Use template
   interpolation only for build-language values.
6. **Format and verify.** Run `kame do fmt -n Makefile.kmk`, inspect plans and
   graph edges, use `-n` for a no-effect render, then materialize the target.
   Use top-level `include ./path.kmk` for shared rules and definitions; the
   path is relative to the source that includes it.

## Essential conventions

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
- `@(EXPRESSION)` evaluates an expression in a recipe; `@{reference}` renders
  a template reference.
- `include ./rules/common.kmk` merges a file-backed source at that location.

Do not assume GNU Make compatibility beyond the documented mappings. In
particular, imports, implicit rules, order-only prerequisites, `eval`,
`define`, target-specific variables, and Make's function language are
not replacements for Kame features. Redesign those cases deliberately.

## References

- [Migrating from Make](./ref/make-migration.md): conversion process,
  mappings, and non-equivalences.
- [Kame language reference](./ref/language.md): definitions, rules,
  templates, expressions, operations, and effects.
- [Kame CLI reference](./ref/cli.md): execution, inspection,
  formatting, capabilities, cache behavior, and diagnostics.
- [Standard library](./ref/library.md): expression operations, capabilities,
  and build effects.
