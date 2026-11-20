---
name: kame
description: Author, inspect, format, and debug Kame expressions, Kame programs (.km), Kash actions (.kash/.ksh), and document templates (.kmt). Use for the kame CLI, Kamefile and Makefile.km, build graphs, cache-aware tasks, process pipelines, text rendering, and GNU Make migration.
---

# Kame languages

Kame programs declare values and build work. Kash actions run processes and
share Kame values and expressions. Templates render expressions into text.

| Surface | Use it for | Reference |
| --- | --- | --- |
| `expr` | One value expression | [Expressions](./ref/expressions.md) |
| `km` | Kame programs, including declarations and value scripts | [Scripts](./ref/scripts.md) |
| `kash` | Typed command arguments, streaming pipelines, and process control | [Kash](./ref/kash.md) |
| `template` | Inline expansions and structured documents | [Templates](./ref/templates.md) |

Inspect fragments with `kame do parse --lang LANG` and format them with
`kame do fmt --lang LANG`. Run expressions, Kame programs, and Kash actions
with `kame do run`. Render templates using `render`. Check command-specific
help on the executable you intend to use. [CLI reference](./ref/cli.md)

## Values and actions

```kame
project = {name: "app", files: [./src/main.c, ./src/util.c]}
rule ./summary.txt = str project.name ": " (count project.files) "\n"
```

Applications use whitespace. Use `(f x)` to group a call within a larger
expression and `f ()` to call with no arguments. Lists and records use commas.
`nil` and `false` are false in conditionals. `value ?? fallback` takes the
fallback for `nil`. Named predicates include `eq?`, `in?`, `has-key?`,
`nil?`, and `empty?`. Use `|>` to pipe values between expressions and `|`
to connect process streams in Kash.

Kame program definitions are lazy. Kash `let name = expr` evaluates an eager
binding, and `set name = expr` updates an eligible binding. `$name` supplies a
value to a command, `(EXPR)` computes an argument, and `$...items` spreads a
list into arguments. `$(COMMAND)` captures stdout subject to execution policy.
Use `line` to remove a single terminal LF from a capture when the producer
guarantees one. [Kash reference](./ref/kash.md)

## Builds

```kame
task default : ./build/app

rule ./build/{name}.o : ./src/{name}.c
	cc -c $inputs.0 -o $outputs.0

rule ./build/app : ./build/main.o ./build/util.o
	cc $...inputs -o $outputs.0

task clean
	rm -rf ./build
```

- Use explicit paths such as `./build/app` for filesystem targets. Named
  captures such as `{name}` connect matching outputs and inputs.
- Use `task` for work that runs whenever reached, `task cached` for a
  successful result that can be reused, and `service` for a managed process.
- The default recipe language is Kash. Body bindings include `inputs`,
  `outputs`, `changed-inputs`, `captures`, named captures, and target
  arguments. `rule ./path = EXPR` publishes text or bytes to a single output.
- `; {shell: "sh"}` selects a foreign-shell recipe. It renders a document
  template before the shell runs. Use `@{inputs.0}` for Kame interpolation
  and shell variable syntax for values belonging to the shell.
- `include ./rules/common.km` adds declarations in the current scope.
  `include? ./local.km` tolerates a missing file. `import ./lib/c as c`
  binds exported names such as `c/compile`. Literal source paths resolve
  relative to the file containing the statement.
- Inspect dependencies with `kame do plan`, `kame do inputs`, and
  `kame do span --expand`. Use `kame -n TARGET` to preview the work before
  executing. Capabilities and phase constraints apply to lazy definitions
  when they are evaluated.

Discovery looks for `Kamefile`, `Makefile.km`, `make.km`, and
`src/km/main.km` in that order. For a staged GNU Make migration, keep the
existing `Makefile` alongside the Kame program. [Make migration guide](./ref/make-migration.md)

## Templates

Use `${EXPR}` for a lookup or a calculation, such as `${project.name}`
or `@{count project.files}`. Document templates also support directives
including `@if`, `@for`, `@let`, and `@include`. Use
`kame do fmt --lang template --comment STYLE` to format directives embedded
in host-language comments. [Template reference](./ref/templates.md)

## References

- [Language overview](./ref/language.md)
- [Build rules and freshness](./ref/rules.md)
- [Standard library and effects](./ref/library.md)
