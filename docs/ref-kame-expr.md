# Kame expression reference

Kame programs, Kash actions, and templates share one value language. Expressions
produce typed values and can call library operations. Operations that access
the host require the corresponding capabilities.

| Context | Form |
| --- | --- |
| Kame program | `NAME = EXPRESSION` or `def NAME PARAM… = EXPRESSION` |
| Kash statement or argument | `let name = EXPRESSION` or `(EXPRESSION)` |
| String, path, or document | `${EXPRESSION}` |
| CLI | `kame do run --lang expr -c 'EXPRESSION'` |

## Run or inspect one expression

```sh
kame do run --lang expr -c 'join "," ["a", "b"]'
kame do run --lang expr --allow-read=./src -c './src/*.c'
printf '%s\n' 'str "a" "b"' | kame do parse --lang expr
kame do run ./values.km -c '(out! VERSION)'
```

`--lang expr` evaluates one expression and displays its value. Standalone
value sessions grant host access through the `--allow-*` options. Inline
expressions can also use definitions from earlier sources in the same session.

## Values and references

```kame
{name: "app", sources: [./src/main.c, ./src/util.c], release: false}
```

| Kind or form | Examples and behavior |
| --- | --- |
| Nil and booleans | `nil`, `true`, `false` |
| Symbol | `:app` carries the name `app`. |
| Number | `42`, `-1`, `3.5` |
| String | `"text ${name}"` or verbatim `"""raw ${name}"""` |
| Path | `./file`, `../dir/file`, `/absolute/file`, `~/file` |
| Glob | `./src/**/*.c` scans the filesystem and returns a tracked, sorted list. |
| List | `[a, "b", ./c]`, or `[...items, "tail"]` with a list spread |
| Record | `{name: value, enabled: true}`, or `{...defaults, name}` with a record spread and shorthand field |
| Lambda | `(item => item + 1)`, `(() => now ())`, `(first rest... => rest)` |

Use commas between items on the same line. Multiline lists and records can
use a newline between items. The formatter uses newline separators for expanded
lists. For a call that spans lines inside a collection, group it with
parentheses or end each continued line with `\`.

```kame
settings = {
	name: "Ada"
	active: true
	paths: [
		./input.txt
		./output.txt
	]
}
```

A bare name references its binding. `project.name`, `files.0`,
`files.-1`, and `project."display name"` access fields or indexes.
A computed receiver can also take a field, as in
`(parse-json "{\"title\":\"News\"}").title`.
Use `lib/name` to access a module export, `std/name` for a standard operation,
and `env/HOME` for an environment variable. A missing environment variable can
use a fallback as in `env/BUILD_MODE ?? "debug"`. The environment read still
requires a grant. [Modules and includes](skills/kame/ref/scripts.md)

`args` is the current argument list. Rule bodies bind `inputs`, `outputs`,
`changed-inputs`, and `captures`, along with named captures and target
arguments. [Rules](skills/kame/ref/rules.md)

Only `nil` and `false` are false. Test emptiness with `empty? value` and
absence with `nil? value`. `and` and `or` short-circuit and return the
deciding operand.

## Strings and interpolation

Double-quoted strings accept escapes such as `\"`, `\\`, `\n`, `\r`,
and `\t`. Verbatim strings use matching runs of at least three double quotes
and preserve their contents. `${EXPR}` substitutes values in ordinary strings
and path atoms. In a string, atoms render as text, `nil` renders as empty
text, and lists render their items separated by spaces. Records use Kame's
display format. For example, `"Repos ${(dirs REPOS)}"` displays the matched
directories in one string. Use `join` for a custom separator or `json` for
structured output. Escape a literal substitution opener with `\${`.
Templates use `@{EXPR}`. [Templates](skills/kame/ref/templates.md)

## Calls, arithmetic, and pipelines

```kame
join "," ["a", "b"]
map (name => uppercase name) ["Ada", "Lin"]
["Ada", "Lin"] |> map uppercase |> join ", "
7 |> gt? _ 3
1 + 2 * 3
```

Juxtaposition calls the function with arguments in left-to-right order.
Parentheses group nested expressions. `version ()` invokes a function with
zero arguments. A final `rest...` parameter collects the remaining arguments.
Use `apply operation args` or `operation ...args` to supply a list as arguments.

Applications bind more tightly than infix operators. `*`, `/`, and `%`
bind more tightly than `+` and `-`. Addition, subtraction, and multiplication
preserve integers when both operands are integers. Division returns a float.
Remainder takes integers. Comparisons are infix and cannot chain. Use `==`,
`!=`, `<`, `>`, `<=`, and `>=`, or the predicates `eq?`, `ne?`, `lt?`,
`gt?`, `lte?`, and `gte?` when a callable is needed.
[Arithmetic and comparisons](spec/007-library.md#numeric-operations)

`|>` evaluates its left operand once. A stage appends it as the last argument
unless top-level `_` placeholders mark where it belongs. A parenthesized
placeholder section such as `(ends? ".c" _)` creates its own callable.
Collection operations generally put parameters before the data. A callback
that declares a second parameter can receive an item index or record key,
and `reduce` can pass the index to its third parameter.
[Library](skills/kame/ref/library.md)

## Conditionals and patterns

`if test then value else fallback` evaluates the selected arm. Add
`elif test then value` for additional cases. `left ?? right` evaluates the
right side when the left side is nil or has a missing lookup. Other failures
propagate. The fallback also works with a missing executable lookup, as in
`tool "git" ?? nil`.

```kame
def object-path source = match source
	./src/{path:**}.c => ./build/${path}.o
	./gen/{name}.c => ./build/gen/${name}.o
	else => nil
```

`match` tries arms in order, binding the captures in the selected arm.
Each arm occupies its own line. A file matcher can use `{*}`, `{**}`,
`{name:*}`, `{name:**}`, or a bounded regex group `{name:~REGEX}`. Expansion
patterns refer to captures with `{name}` or `{_N}`. `subst` accepts a string or
a list as its subject, replacing matches and preserving nonmatches.
Call `pattern TEXT` to compile a pattern from a dynamic string.
[Patterns](spec/014-patterns.md) · [Pattern extensions](spec/029-pattern-extensions.md)

## Effects and process expressions

- `$(COMMAND)` runs a Kash graph and captures its exact UTF-8 stdout.
- `$?(COMMAND)` returns an invocation outcome with status, captured output,
  and failure information. Capability denial and cancellation of the owning
  invocation propagate.
- `$&(COMMAND)` starts a graph and returns an invocation-owned async handle.
  Inspect the terminal outcome with `$?(wait $job)` in Kash.
- `read`, `write!`, `publish!`, `out!`, `err!`, `sh!`, `tool`, and
  `env/NAME` follow their capability and phase rules. Build-time reads, globs,
  tool resolutions, and environment observations can invalidate dependent
  work.
- `eval SOURCE` evaluates expression text. Source composition uses authored
  `include` and `import` statements.

[Kash reference](ref-kash.md) · [Capabilities and the library](spec/007-library.md)

## Definitions in programs

```kame
VERSION = "1.2.3"
SOURCES = ./src/*.c
def greet name = str "Hello, " name
MODE ?= "debug"
CHECKS += [./tests/unit]
```

Definitions are lazy and memoized. A default `?=` can be configured before
evaluation. `+=` contributes list items in source order. Use
`--define NAME=TEXT` for literal strings and `--set NAME=LITERAL` for typed data.
[Programs and source composition](skills/kame/ref/scripts.md)

## References

- [Unified language specification](spec/055-unified-kame-kash-language.md)
- [Evaluation](spec/005-evaluation.md)
- [Authoring skill expression reference](skills/kame/ref/expressions.md)
