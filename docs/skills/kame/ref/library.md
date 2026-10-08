# Kame standard-library reference

Kame operations are ordinary expression calls: `(name argument...)`. Arguments
are evaluated before the operation runs. Unless noted, invalid arity or value
types produce `EXPR_INVALID`; capability failures produce `CAP_DENIED`.

## Values and calls

| Operation | Signature | Result |
| --- | --- | --- |
| `not` | `(not VALUE)` | Language boolean negation. |
| `bool` | `(bool VALUE)` | Language truth value. |
| `str` | `(str VALUE)` | Text form; lists and records use stable JSON-like syntax. |
| `parse-json` | `(parse-json TEXT)` | Parse JSON into typed Kame values; malformed input reports `JSON_INVALID`. |
| `fail` | `(fail MESSAGE)` | Raise `USER_ERROR` with an explicit string message. |
| `count` | `(count VALUE)` | Number of string code points, bytes, list items, or record fields. |
| `get` | `(get RECORD KEY [DEFAULT])` | Computed string-key lookup; missing keys return nil or DEFAULT. DEFAULT is evaluated eagerly. |
| `first` | `(first LIST)` | First item, or `:nil` for an empty list. |
| `nth` | `(nth LIST-OR-TEXT INDEX)` | Indexed item/code point, or `:nil` when out of range; negative indexes count from the end. |
| `list` | `(list VALUE...)` | A list containing the arguments. |
| `nop` | `(nop VALUE...)` | Last argument, or `:nil` with no arguments. |
| `apply` | `(apply LIST FUNCTION)` or `(apply FUNCTION LIST)` | Calls a function with list arguments. A one-argument function receives the list itself for legacy compatibility. |

Only `:false` and `:nil` are false. Other values are true.

## Collections

Callbacks run in input order. `map`, `flatmap`, `filter`, and `filter-out`
accept either `(FUNCTION LIST)` or the legacy `(LIST FUNCTION)` order.

| Operation | Signature | Result |
| --- | --- | --- |
| `map` | `(map FUNCTION LIST)` | One callback result per item. |
| `flatmap` | `(flatmap FUNCTION LIST)` | Maps and flattens one list level; every callback result must be a list. |
| `filter` | `(filter PREDICATE LIST)` or `(filter LIST VALUE)` | Items whose predicate is true or equal to `VALUE`. |
| `filter-out` | `(filter-out PREDICATE LIST)` or `(filter-out LIST VALUE)` | Items whose predicate is false or unequal to `VALUE`. |
| `reduce` | `(reduce FUNCTION LIST [INITIAL])` | Left fold. Without `INITIAL`, the first item is the accumulator; an empty list produces `:nil`. |
| `concat` | `(concat VALUE...)` | Flattens list arguments one level and appends scalar arguments. |
| `slice` | `(slice LIST-OR-TEXT START [END])` | Half-open slice; negative bounds count from the end. |
| `sorted` | `(sorted LIST)` | Ascending sort of one comparable scalar kind. |
| `unique` | `(unique LIST)` | First occurrence of every comparable scalar value. |

`sorted` and `unique` support nil, booleans, integers, floats, and strings; a
mixed or unsupported list is invalid (including integer/float mixtures).

## Text and patterns

| Operation | Signature | Result |
| --- | --- | --- |
| `join` | `(join LIST SEPARATOR)` | Joins a list of strings. |
| `split` | `(split TEXT SEPARATOR)` | Splits text on a nonempty literal separator. |
| `strip` | `(strip TEXT)` | Removes leading and trailing Unicode whitespace. |
| `replace` | `(replace FROM TO TEXT)` | Replaces all literal `FROM` occurrences. |
| `replace` | `(replace MATCH-PATTERN EXPANSION [TEXT])` | Pattern replacement. Without `TEXT`, returns a one-argument callable suitable for `map`. A non-match returns `:nil`. |
| `includes?` | `(includes? TEXT PART)` | Literal substring test. |
| `starts?` | `(starts? TEXT PREFIX)` | Literal prefix test. |
| `ends?` | `(ends? TEXT SUFFIX)` | Literal suffix test. |
| `uppercase` | `(uppercase TEXT)` | Unicode uppercase conversion. |
| `lowercase` | `(lowercase TEXT)` | Unicode lowercase conversion. |
| `cat` | `(cat VALUE...)` | Concatenate renderable scalars/lists; nil is empty. Records/bytes require explicit conversion. |
| `text` | `(text VALUE)` | Convert UTF-8 bytes to a string, or return a string unchanged. |
| `render` | `(render SOURCE [PAYLOAD] [STYLE])` | Render a document template; a string second argument is style-only. File sources require read capability; bytes need explicit style. |
| `pattern` | `(pattern TEXT)` | Construct and validate a pattern from runtime text. |
| `regex-match` | `(regex-match PATTERN SUBJECT)` | Regex-pattern match record (`text`, `captures`, `named`), or nil. |
| `capture` | `(capture INDEX-OR-NAME MATCH)` | Select a match capture, or nil when absent. |
| `regex-replace` | `(regex-replace MATCH-PATTERN EXPANSION SUBJECT)` | Anchored regex-pattern replacement on a string or string list; non-matches yield nil. |

See [Templates](./templates.md) for document directives, source resolution,
comment styles, and payload scopes. Lazy `if`, `and`, `or`, `match`, and `with`
are special forms, not eager library operations; see [Expressions](./expressions.md).

Pattern replacement uses Kame pattern literals such as `./src/{name:*}.c` and
expansion references such as `./build/{name}.o`. Invalid matcher/expansion
combinations or missing captures report `PAT_INVALID`.
Regex grammar and matching budgets are defined in
[Pattern extensions](../../../spec/029-pattern-extensions.md).
`black`, `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`, `white`, `bold`,
and `dim` each wrap one string in ANSI styling unconditionally; they do not
inspect the terminal. Use only for terminal-bound strings.

## Paths

Path operations are lexical: they do not read the filesystem or create
dependencies. Relative paths use the current evaluation directory where needed.

| Operation | Signature | Result |
| --- | --- | --- |
| `basename` | `(basename PATH)` | Final component. |
| `dirname` | `(dirname PATH)` | Parent path. |
| `splitext` | `(splitext PATH)` | `[ROOT EXTENSION]`; a lone leading dot is not an extension. |
| `ext` | `(ext PATH)` | Final extension. |
| `joinpath` | `(joinpath PART...)` | Joined, cleaned path. |
| `relpath` | `(relpath TARGET BASE)` | Target path relative to base. |
| `abspath` | `(abspath PATH)` | Clean absolute path, resolving a relative path from the evaluation directory. |

## Filesystem, environment, and process operations

These operations require the named capability. In normal builds, filesystem
operations also register dependencies so changed files, glob membership, and
environment values can invalidate a consumer.

| Operation | Signature | Capability | Result |
| --- | --- | --- | --- |
| `read` | `(read PATH)` | read | File bytes. |
| `exists?` | `(exists? PATH)` | read | Whether a path exists. |
| `stat` | `(stat PATH)` | read | Stable file metadata. |
| `wildcard` | `(wildcard PATTERN...)` | read | Sorted, duplicate-free union of one or more patterns; each tracks its own membership. |
| `write` | `(write PATH VALUE)` | write | Bytes write raw; other coercible values render as with `str`. Writes immediately in expression execution; defers an atomic write while rendering a build. Invalid while planning. |
| `env` | `(env NAME)` | env | Environment value. |
| `shell` | `(shell COMMAND [OPTIONS])` | run | Collected status/stdout/stderr record; allowed in explicit evaluation, demanded lazy runtime values, and Kash recipe expressions. Invalid during planning or direct build-template rendering. |
| `sh`, `shellrun` | Same as `shell` | run | Aliases with identical result and phase behavior. |
| `shell-template` | `(shell-template FRAGMENTS VALUES)` | run | Collected shell call with POSIX-quoted dynamic strings; FRAGMENTS has one more string than VALUES. Same phase policy as shell. |

`(resource URI)` constructs a validated `file:`/`mem:` resource value for file
operations and rule inputs. Memory resources are host-scoped, not durable disk
artifacts. URI grants use canonical roots; see
[Resource protocols](../../../spec/031-resource-protocols.md).
`(tool NAME)` and `x/NAME` resolve executables against startup PATH or `--tool`
overrides, not recipe PATH, and track the chosen executable. Tool lookup needs
a build/source session; internal metadata checks do not grant user read access.
`(now)` and `(monotonic)` request host clocks and return integer nanoseconds;
neither needs a grant, but both are invalid during planning/output resolution.

Standalone `kame do run --lang expr` denies these capabilities by default; grant only the
needed roots, names, or process access with its `--allow-*` options.

## Build effects

These operations are meaningful in a rule recipe expression. Bytes are
emitted raw; every other coercible value is rendered as with `str`
(nil as `nil`, booleans, integers, floats, text, lists and records in
stable syntax) and emitted. Unrenderable values (callables, resources)
still report `EXPR_INVALID`.

| Operation | Signature | Effect |
| --- | --- | --- |
| `out` | `(out VALUE...)` | In a rule, defers bytes to the target's stdout. Standalone evaluation writes to stdout and returns the combined output. |
| `err` | `(err VALUE...)` | In a rule, defers bytes to the target's stderr. Standalone evaluation writes to stderr and returns the combined output. |
| `yield` | `(yield VALUE...)` | Supplies the bytes for a declared file output. Standalone evaluation writes to stdout and returns nil. It is valid only for a file rule with one output. |

Do not combine a nonempty shell recipe with `yield`; Kame reports an output
conflict rather than choosing a producer.
