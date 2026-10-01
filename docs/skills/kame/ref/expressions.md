# Expressions

`expr` parses exactly one Kame expression. It is the shared value language of
scripts, templates, rule expansions, and Kash boundaries.

## Values and references

```kame
[name: "app" sources: [./src/main.c ./src/util.c] release: :false]
```

- Lists and records use whitespace, not commas. Do not mix keyed and unkeyed
  entries. Numbers, strings, explicit paths, booleans, and nil are values.
- Bare names are references; use `"app"` or `:app` for literal text. Symbols
  other than `:true`, `:false`, and `:nil` evaluate to their name without `:`.
- Paths start with `./`, `../`, or `/`; they are not implicit filesystem reads.
- References support fields (`project.name`), indexes (`files.0`, `files.-1`),
  half-open slices (`files.1..4`), and selections (`config.{host,port}`).
- Only `:nil` and `:false` are false; zero, empty strings, and empty lists are true.

Expression strings use double quotes with `\"`, `\\`, `\n`, `\r`, and `\t`.
Interpolation is `"Count: {(count files)}"` or `"Count: @(count files)"`.
Single quotes are not string delimiters. Verbatim strings with matching runs
of three or more double quotes preserve raw multiline content.

## Calls, functions, and pipes

```kame
(join ["a" "b"] ",")
([name] (cat "Hello, " name))
(let [names ["Ada" "Lin"]]
  (names | map ([name] (uppercase name)) | join ", "))
```

Applications are `(FUNCTION ARG...)`; `()` is invalid. Lambdas begin with a
parameter list; a single final `rest...` parameter collects remaining arguments.
`((cat "prefix-" _))` is a placeholder section equivalent to a one-argument
lambda. Outside sections, underscore names remain ordinary names.

`(value | transform a)` becomes `(transform a value)`; `_` on the right chooses
where to insert the value. This is value transformation, not a process pipe.
See [Library](./library.md) for operation signatures; do not infer operand
order from another language.

## Special forms

| Form | Behavior |
| --- | --- |
| `(let [name VALUE ...] BODY...)` | Sequential bindings in a child scope; returns the last body value. |
| `(def NAME VALUE)` | Bind a value in the current scope. |
| `(def NAME [PARAM...] BODY...)` | Bind a function. |
| `(if TEST THEN [TEST THEN]... [ELSE])` | Evaluate only the selected branch; without a match/else, return nil. |
| `(and VALUE...)` / `(or VALUE...)` | Short-circuit and return the deciding operand. |
| `(with RECORD BODY...)` | Bind record fields in a child scope. |
| `(match SUBJECT [PATTERN BODY...]... [:else BODY...])` | First matching clause; captures are local to its body. |
| `(? VALUE...)` | First operand not failing with unknown-reference; not general error recovery. |
| `(eval SOURCE)` | Evaluate expression text or an explicitly loaded script; not a replacement for native source composition. |

Comparison heads `=`, `==`, `!=`, `<`, `>`, `<=`, `>=` alias `eq`, `is`, `ne`,
`lt`, `gt`, `lte`, `gte`. Equality is strict and kind-aware. These are prefix
calls, not infix operators.

## Patterns and processes

```kame
(map (replace ./src/{name:*}.c ./build/{name}.o)
  [./src/main.c ./src/util.c])
```

Expression match patterns use `{*}`, `{**}`, `{name:*}`, or `{name:**}`;
expansion patterns use `{name}` or `{_N}`. The pattern form of `replace` with
two arguments produces a callable; a non-match returns nil. Rule captures
have a related but distinct grammar; see [Rules](./rules.md).

`$(printf "hello\n")` is an expression atom whose contents parse as Kash.
It captures UTF-8 stdout exactly, including trailing newlines; it does not
evaluate Kame expressions by itself. Execution needs a run grant and a phase
allowing processes. Plain `"$(command)"` is literal text. Use explicit string
interpolation to demand capture. See [Kash](./kash.md) for process semantics.

## Verify

```sh
kame do run --lang expr -c '(join ["a" "b"] ",")'
kame do run --lang expr --allow-read=./src -c '(wildcard ./src/*.c)'
printf '%s\n' '(join ["a" "b"] ",")' | kame do parse --lang expr
```

Pure value sessions deny host capabilities by default. Grant only required
read/write/run/env access. Reads and globs track dependencies in builds;
unused values and unselected branches must not introduce effects.
