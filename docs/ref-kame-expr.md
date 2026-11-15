# Kame expression reference

The Kame expression language is the single value language shared by every Kame
layer. It is a small, lazy, Lisp-shaped language: prefix applications, typed
values (lists, records, paths, patterns), and explicit effects that require
capabilities. No other layer adds a second value grammar. Other layers embed
expressions:

| Where | Form |
| --- | --- |
| `.km` / `.kmk` definitions | `NAME = EXPRESSION`, `(NAME PARAM…) = EXPRESSION` |
| Kash words and statements | `(EXPRESSION)`, `name = EXPRESSION` |
| Strings and templates | `"@{ref}"`, `"@(call …)"` |
| CLI | `kame do run --lang expr -c 'EXPRESSION'` |

Key terms:

| Term | Meaning |
| --- | --- |
| Value | Typed data: nil, boolean, number, string, bytes, path, list, record, pattern, callable, resource, process. |
| Reference | A bare name, optionally with fields, indexes, slices or selections: `project.files.0`. |
| Application | `(HEAD ARG…)`: calls a function, operation or special form. |
| Operation | A registered library function (`map`, `read`, …); a first-class value. |
| Special form | Syntax controlling evaluation (`if`, `let`, `and`, …); not a value. |
| Pattern | A path-shaped matcher/expansion: `./src/{name:*}.c` → `./build/{name}.o`. |
| Capability | A grant (read, write, run, env) needed by effectful operations. |

## Running

```sh
kame do run --lang expr -c '(join "," ["a" "b"])'
kame do run --lang expr --allow-read=./src -c './src/*.c'
echo '(str "a" "b")' | kame do parse --lang expr
kame do run ./values.km -c '(out! VERSION)'      # expression after a value program
```

`--lang expr` evaluates exactly one expression and prints its value. Standalone
value sessions deny host capabilities by default: add only the `--allow-*`
grants an expression needs.

## Values

```kame
[name: "app" sources: [./src/main.c ./src/util.c] release: :false]
```

| Kind | Syntax |
| --- | --- |
| Nil, booleans | `:nil`, `:true`, `:false` |
| Symbol | `:app` evaluates to the string `"app"` |
| Number | `42`, `-1`, `3.5` (integers are exact) |
| String | `"text @{ref}"`, verbatim `"""raw, multiline @{kept}"""` |
| Path | `./file`, `../dir/x`, `/abs`; a value, not a file read |
| Glob | `./src/**/*.c`: lazy, tracked scan returning a sorted list (needs read) |
| List | `[a "b" ./c]`: whitespace-separated, no commas |
| Record | `[key: value other: 2]`: keyed entries only, never mixed with list items |
| Spread | `[...prefix "x"]`: splices a list one level into a list literal |
| Lambda | `([x rest...] BODY)` |
| Capture | `$(printf "hi\n")`: run Kash, return stdout exactly (needs run) |

**Truth:** only `:nil` and `:false` are false. `0`, `""`, `[]` and empty records
are true. Test emptiness with `(empty? v)` or `(= (count v) 0)`, absence with
`(= v :nil)`. `and`/`or` return the deciding operand: `(or "" "x")` is `""`.

There are no arithmetic operations: compute numbers with external tools or keep
them as data.

## References

| Form | Meaning |
| --- | --- |
| `name` | Lexical binding, then definition, then registered operation. |
| `p.name`, `files.0`, `files.-1` | Field, index, index from the end. |
| `files.1..3` | Half-open slice. |
| `config.{host,port}` | Record of selected fields. |
| `env.HOME` | One environment variable (needs an env grant); missing is nil. |
| `args`, `inputs`, `outputs`, `inputs/changed` | Execution-context lists, where a frame provides them. |

Bare names are always references: quote text (`"app"`) or use a symbol
(`:app`). Fields start from a name, not from a call: bind
`(let [r (sh! …)] r.stdout)` or use `(get "stdout" …)`.

## Strings and interpolation

- Escapes: `\"`, `\\`, `\n`, `\r`, `\t`; `\@` and `\{` for literal openers.
- `"@{ref}"` inserts a reference; `"@(f x)"` inserts a call's value;
  `"@(version)"` calls with no arguments. Bare `{(…)}` does not interpolate.
- Verbatim strings (three or more `"`) are inert: no escapes, no interpolation.
- Single quotes are not string delimiters.
- Unquoted paths interpolate too: `@{root}/js/{path}.js`, with `{name}` read
  from lexical string bindings. Inserted text stays literal and cannot create
  glob or pattern syntax.

## Applications, functions and pipes

```kame
(join "," ["a" "b"])
(map ([name] (uppercase name)) names)
# placeholder section, value pipe, explicit placement
(map ((str "x-" _)) ["a" "b"])
(names | map uppercase | join ", ")
(x | gt? _ 3)
```

- `(HEAD ARG…)` evaluates arguments then calls; `()` is invalid.
- Lambdas start with a parameter list; a final `rest...` collects the rest.
  Missing arguments bind nil; extra arguments are an error that underlines
  the parameter list.
- `((… _ …))` is a one-argument lambda (placeholder section).
- Collection callbacks receive the item, then its key when they declare a
  second parameter: `(map ([v i] …) list)` gets the position, and
  `(map ([v k] …) record)` gets the field name and returns a record.
  `filter`/`filter-out` keep records too; `reduce` passes the position third.
- `(v | f a)` means `(f a v)`: the subject goes last unless `_` places it.
- Library operations take **parameters first, subject last**:
  `(nth 1 files)`, `(sub FROM TO text)`. Comparisons keep left/right order:
  `(gt? LEFT RIGHT)`.
- Operations are values: `(filter exists? files)`, `(let [size count] (size xs))`.
- Recursion is bounded to 32 active expressions (`EVAL_DEPTH`); `map` and
  `reduce` callbacks don't accumulate depth.

## Special forms

| Form | Behavior |
| --- | --- |
| `(let [n V …] BODY…)` | Sequential bindings in a child scope; returns the last body value. |
| `(def NAME V)`, `(def NAME [P…] BODY…)` | Bind a value or function in the current scope. |
| `(if T THEN [T THEN]… [ELSE])` | Only the selected branch is evaluated; nil without a match. |
| `(and V…)`, `(or V…)` | Short-circuit; return the deciding operand. |
| `(with RECORD BODY…)` | Bind record fields as names. |
| `(match S [PATTERN BODY…]… [:else BODY…])` | First matching clause; captures are local. |
| `(? V…)` | First operand not failing with `REF_MISSING`/`TOOL_MISSING`, else nil. |
| `(eval TEXT)` | Evaluate expression text. |
| `(run! …)`, `(pipe! …)`, `(capture! …)`, `(await JOB)` | Process forms; see [Kash](ref-kash.md). |

Comparisons `=`/`==`, `!=`, `<`, `>`, `<=`, `>=` are prefix aliases of `eq?`,
`ne?`, `lt?`, `gt?`, `lte?`, `gte?`. Equality is deep and kind-aware (records
ignore field order, bytes never equal strings, `1` equals `1.0`).

## Patterns

```kame
# non-matching sources pass through sub unchanged
(sub ./src/{name:*}.c ./build/{name}.o sources)
(filter (match? ./src/{**}.test.js) files)
(match path [./src/{dir:**}/{file:*}.c (str dir "/" file)] [:else :nil])
```

A path containing `{…}` groups is a pattern; without groups, `*` and `**` make
it a glob that scans the filesystem.

- Matchers: `{*}` (one segment), `{**}` (any depth), named `{name:*}`,
  `{name:**}`, regex `{name:~REGEX}`. Expansions: `{name}`, `{_N}`.
- `(sub PATTERN EXPANSION)` without a subject returns a callable for `map`.
- `(pattern TEXT)` builds a pattern from runtime text; `regex-match`, `capture`
  and `resub` handle regex matches (portable, byte-oriented, not PCRE).

## Library at a glance

Full signatures are in the [library reference](skills/kame/ref/library.md).

| Area | Operations |
| --- | --- |
| Values | `str`, `json`, `parse-json`, `count`, `get`, `set`, `merge`, `items`, `first`, `nth`, `list`, `nop`, `apply`, `not`, `bool`, `fail!`, `digest` |
| Predicates | `nil?`, `string?`, `list?`, `record?`, `number?`, … `empty?`, `blank?`, `has-key?`, `in?`, `success?` |
| Collections | `map`, `flatmap`, `filter`, `filter-out`, `reduce`, `concat`, `slice`, `sorted`, `unique` |
| Text | `join`, `split`, `strip`, `line`, `sub`, `sub-token`, `includes?`, `starts?`, `ends?`, `uppercase`, `lowercase`, `text`, `render` |
| Paths (lexical) | `basename`, `dirname`, `ext`, `splitext`, `joinpath`, `relpath`, `abspath`, `source-dir` |
| Filesystem (read) | `read`, `exists?`, `stat`, `lstat`, `readlink`, `wildcard`, `files`, `dirs` |
| Effects | `write!`, `publish!`, `out!`, `err!`, `yield!`, `sh!` |
| Host | `env`, `tool`, `resource`, `now`, `monotonic`, `parse-args` |

A trailing `!` marks an effect. Removed spellings (`write`, `shell`, `cat`, …)
report migration diagnostics rather than acting as aliases.

## Evaluation and capabilities

- **Lazy:** definitions evaluate on first demand and are memoized; unused values
  and unselected branches cause no effects.
- **Tracked:** in builds, reads, globs, tool lookups and environment reads
  become dependencies that invalidate consumers when they change.
- **Granted:** `read`, `write`, `run` and `env` access must be granted. Denials
  are `CAP_DENIED` and can't be recovered with `?`.
- **Phased:** planning and value-producing rule bodies reject writes, processes
  and clocks, whatever the grants.
- Errors carry a code (`EXPR_INVALID`, `REF_MISSING`, `PAT_INVALID`, …) and an
  authored source span.

## Definitions in programs

Value (`.km`) and rule (`.kmk`) programs bind expressions by name:

```kame
VERSION = "1.2.3"
SOURCES = ./src/*.c
(greet name) = (str "Hello, " name)
# default unless configured, then a lazy list contribution
MODE ?= "debug"
CHECKS += [./tests/unit]
```

Comments are whole-line `#` or `//`; there are no trailing comments.

Every right-hand side is exactly one expression: text needs quotes, lists need
brackets. `--define NAME=TEXT` and `--set NAME=LITERAL` override definitions from
the CLI. See [scripts](skills/kame/ref/scripts.md) for includes and sessions.

## References

- [Expressions](skills/kame/ref/expressions.md) and
  [library](skills/kame/ref/library.md) skill references: full detail
- [Templates](skills/kame/ref/templates.md), [Kash](ref-kash.md)
- Specs: [language](spec/004-language.md), [evaluation](spec/005-evaluation.md),
  [library](spec/007-library.md), [patterns](spec/014-patterns.md),
  [pattern extensions](spec/029-pattern-extensions.md)
