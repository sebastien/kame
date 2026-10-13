# Expressions

`expr` parses exactly one Kame expression. It is the shared value language of
scripts, templates, rule expansions, and Kash boundaries.

Evaluation is bounded to 32 simultaneously active expressions, including
function bodies and their operands. Deeper recursion reports `EVAL_DEPTH`
instead of exhausting the native/WASM stack. Iterative `map`/`reduce` callbacks
do not accumulate depth across items.

## Values and references

```kame
[name: "app" sources: [./src/main.c ./src/util.c] release: :false]
```

- Lists and records use whitespace, not commas. Do not mix keyed and unkeyed
  entries. Numbers, strings, explicit paths, booleans, and nil are values.
- Bare names are references; use `"app"` or `:app` for literal text. Symbols
  other than `:true`, `:false`, and `:nil` evaluate to their name without `:`.
- Paths start with `./`, `../`, or `/`; they are not implicit filesystem reads.
  `(resource "file:///absolute/path")` or `(resource "mem://workspace/path")`
  constructs an explicit protocol resource; a quoted URI alone is ordinary text.
- References support fields (`project.name`), indexes (`files.0`, `files.-1`),
  half-open slices (`files.1..4`), and selections (`config.{host,port}`).
- Only `:nil` and `:false` are false; zero, empty strings, and empty lists are true.

Empty records and bytes are also true. For strings, bytes, lists, or records,
use `(= (count VALUE) 0)` for emptiness and `(> (count VALUE) 0)` for nonemptiness;
`count` does not accept nil, booleans, or numbers. Test absence with
`(= VALUE :nil)` and numeric zero with `(= VALUE 0)` instead. Whitespace is data.
`and`/`or` retain the deciding operand, so `(or "" "fallback")` returns `""`.
Source-composition `if` shares this truthiness; test collection emptiness explicitly.

Expression strings use double quotes with `\"`, `\\`, `\n`, `\r`, and `\t`.
Interpolation is `"Count: @(count files)"`. Deprecated `{(...)}` input is still
evaluated but formatting migrates it to `@(...)`, including nested strings.
Use `\@`/`\{` for literal openers; verbatim literals remain untouched.
Single quotes are not string delimiters. Verbatim strings with matching runs
of three or more double quotes preserve raw multiline content.

## Calls, functions, and pipes

```kame
(join ["a" "b"] ",")
([name] (str "Hello, " name))
(let [names ["Ada" "Lin"]]
  (names | map ([name] (uppercase name)) | join ", "))
```

Applications are `(FUNCTION ARG...)`; `()` is invalid. Lambdas begin with a
parameter list; a single final `rest...` parameter collects remaining arguments.
`((str "prefix-" _))` is a placeholder section equivalent to a one-argument
lambda. Outside sections, underscore names remain ordinary names.

`(value | transform a)` becomes `(transform a value)`; `_` on the right chooses
where to insert the value. This is value transformation, not a process pipe.
See [Library](./library.md) for operation signatures; do not infer operand
order from another language.

Named execution context is available where a matching frame exists: `args` is
the nearest argument list, while `inputs`, `outputs`, and `inputs/changed` expose
the rule lists corresponding to `@<*`, `@>*`, and `@<?`. Lexical bindings take
precedence. Unavailable access reports `SEL_NO_CONTEXT`; changed inputs are
runtime-only for file rules. `args.0` and `inputs/changed.0` use normal references.
Names containing slash-separated segments are not implicit filesystem paths.

## Operation values

Ordinary registered operations are first-class callables:

```kame
predicate = exists?
(filter predicate FILES)
(map uppercase ["a" "b"])
(let [size count] (size [1 2 3]))
```

Names resolve lexically before the operation registry. Operation values may be
aliased, passed to functions, or stored in lists/records. Lookup alone performs
no effects; invocation retains ordinary arity, capability, phase, dependency,
version-tracking and suspension behavior. This also applies to plugin operations
and comparison aliases. Callables are invocation-owned, not serialized code.

## Special forms

Special forms are syntax-only, not callable values or plugin operation names.
Use a lambda when adapting one to a callback. In addition to the forms below,
`run!`, `pipe!`, and `await` are process special forms; see [Kash](./kash.md).

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
| `(eval SOURCE)` | Evaluate expression text; not a script loader or replacement for native source composition. |

Comparison heads `=`, `==`, `!=`, `<`, `>`, `<=`, `>=` alias `eq`, `is`, `ne`,
`lt`, `gt`, `lte`, `gte`. Equality is kind-aware and recursive for bytes, lists,
and records (record field order is ignored); `is` remains identical to `eq`.
Opaque values are not comparable. These are prefix
calls, not infix operators.

## Patterns and processes

```kame
(map (sub ./src/{name:*}.c ./build/{name}.o)
  [./src/main.c ./src/util.c])
```

Expression match patterns use `{*}`, `{**}`, `{name:*}`, or `{name:**}`;
expansion patterns use `{name}` or `{_N}`. The pattern form of `sub` with
two arguments produces a callable; nonmatches remain unchanged. `replace` is a
deprecated alias. `(match? PATTERN [TEXT])` tests a full-string match, returning
a unary predicate when `TEXT` is omitted. Use it for filtering. Rule captures
have a related but distinct grammar; see [Rules](./rules.md).
`(pattern TEXT)` constructs and validates a pattern from runtime text. Regex
matcher groups are `{name:~REGEX}` or `{~REGEX}`; `regex-match`, `capture`, and
`regex-replace` expose bounded matching and capture processing. Regex syntax is
portable and byte-oriented, not PCRE; see
[Pattern extensions](../../../spec/029-pattern-extensions.md).

Computed path patterns keep their groups authored while inserting literal data:

```kame
(let [root "./src" output "./build" extension "c"]
  (sub @(root)/{name:*}.@(extension) @(output)/{name}.o "./src/main.c"))
```

Interpolation inside a group is invalid. Inserted braces, backslashes and glob
or regex punctuation stay literal; they cannot create matching syntax. Use
`(pattern TEXT)` when that compilation is intentional. These patterns do not
enumerate files; interpolated strings and raw Kash words remain separate data.

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
