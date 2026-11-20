# Expressions

Kame programs, Kash actions, and templates share the expression grammar.
Use `--lang expr` to inspect or evaluate one expression.

```kame
{name: "app", sources: [./src/main.c, ./src/util.c], release: false}
```

Lists use brackets and records use braces. Separate items and fields with a
comma or newline. Use `[...items, value]` to spread a list into a list and
`{...defaults, name}` to spread a record and add the current `name` value.
Symbols such as `:app` carry a name. Only `nil` and `false` are false.
Use `empty? value` to check whether a container has any contents.

Bare names read their bindings. `project.name`, `project."display name"`,
`files.0`, and `files.-1` select fields or indexes. A grouped expression
can select a field, as in `(parse-json "{\"title\":\"News\"}").title`.
`env/NAME` reads one permitted environment variable. `lib/name` selects
an export from a module, and `std/name` selects a standard operation.
Rule frames bind `inputs`, `outputs`, `changed-inputs`, and `captures`.
`args` supplies the active argument list.
[Rules](./rules.md) · [Program composition](./scripts.md)

## Application, arithmetic, and pipes

```kame
join "," ["a", "b"]
map (name => uppercase name) ["Ada", "Lin"]
["Ada", "Lin"] |> map uppercase |> join ", "
["./main.c", "./site.html"] |> filter (ends? ".c" _)
7 |> gt? _ 3
1 + 2 * 3
```

Use `f a b` to call a function. Parentheses group an inner expression,
as in `f (g a) b`. `f ()` calls with zero arguments. A lambda declares
its parameters before `=>`. Its last parameter can be `rest...`.
`f ...args` and `apply f args` supply the contents of a list as arguments.

Applications bind more tightly than arithmetic. Multiplication, division,
and remainder bind more tightly than addition and subtraction. `/` returns a
float. `%` requires integer operands. Use infix comparisons such as `a >= b`
or a named predicate such as `gte?` when a callback is needed. Comparisons
take left and right operands in that order.

A `|>` stage evaluates the receiving operation with the prior value appended
as its last argument. Top-level `_` placeholders set the placement and reuse
the same value. A parenthesized group such as `(ends? ".c" _)` is a callable
section. Collection operations generally put parameters first and data last.
`map` and `filter` callbacks can receive a list position or a record key as
their second parameter. `reduce` can pass the position third.
[Library](./library.md)

Ordinary operations can be passed, bound, and invoked as callables:

```kame
map uppercase ["a", "b"]
(size => size [1, 2, 3]) count
```

Each call retains its arity, capability, phase, and dependency rules.
Evaluation limits simultaneously active expression depth. Iterative callbacks
release their call depth between items.
[Evaluation](../../../spec/005-evaluation.md)

## Control expressions

| Form | Behavior |
| --- | --- |
| `if test then a elif next then b else c` | Evaluate the selected arm. |
| `left and right`, `left or right` | Short-circuit and return the deciding value. |
| `left ?? right` | Evaluate `right` for nil or a missing lookup. |
| `match value` with one `PATTERN => EXPR` per line | Evaluate the first matching arm. Captures are local to that arm. |
| `eval SOURCE` | Evaluate expression source text. |

`tool "git" ?? nil` allows an unavailable executable. Capability denial and
other failures propagate. Kame programs use lazy definitions and Kash blocks
can use eager `let` and mutable `set` bindings under lexical scoping rules.
[Programs](./scripts.md) · [Kash actions](./kash.md)

## Strings, paths, and patterns

Use `${EXPR}` in ordinary strings and paths. For example,
`"Count: ${count files}"` evaluates a call, and `"Version: ${version ()}"`
calls with zero arguments. In strings, lists render with spaces between
items and records use the standard display format. Use `join` for a
custom separator. Triple-quoted strings preserve raw multiline contents.
[Templates](./templates.md)

An explicit path is a value. A wildcard path such as `./src/**/*.c`
is a path pattern. Use `files ./src/**/*.c` to discover matching files or
`dirs ./*/.jj` to discover matching directories. Discovery requires read
authority and tracks the matches as dependencies. Pattern
groups such as `{name:*}` match one path segment, and `{path:**}` can span
directories. Expansion patterns use `{name}` or `{_N}`. Interpolated path
data stays literal.

```kame
subst ./src/{name:*}.c ./build/{name}.o [./src/main.c, ./src/util.c]
capture ./src/{name:*}.c ./src/main.c
```

`subst` preserves nonmatching subjects and accepts a string or a list of
strings or paths. Use a two-argument `subst` as a callable.
`pattern TEXT` constructs a pattern explicitly. Regex groups
`{name:~REGEX}` use bounded matching.
[Pattern extensions](../../../spec/029-pattern-extensions.md)

## Capture and verification

`$(COMMAND)` captures Kash stdout exactly, including trailing newlines.
`$?(COMMAND)` returns an outcome record, and `$&(COMMAND)` starts owned async
work. These forms need appropriate execution authority and phase.
[Process inspection](../../../spec/049-process-inspection.md)

```sh
kame do run --lang expr -c 'join "," ["a", "b"]'
kame do run --lang expr --allow-read=./src -c './src/*.c'
printf '%s\n' 'join "," ["a", "b"]' | kame do parse --lang expr
```

Pure value sessions require explicit host grants. Reads and globs track
dependencies in builds. Unused lazy definitions and unselected branches run
without effects.
[Kame expression reference](../../../ref-kame-expr.md)
