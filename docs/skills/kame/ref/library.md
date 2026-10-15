# Kame standard-library reference

Kame operations are ordinary expression calls: `(name argument...)`. Arguments
are evaluated before the operation runs, except the replacement operand of
pattern `sub`/`replace`: it is evaluated only after matching, with local captures.
Unless noted, invalid arity or value
types produce `EXPR_INVALID`; capability failures produce `CAP_DENIED`.

Transformations take parameters first and their subject last: `(f PARAMS VALUE)`.
Optional parameters also precede the subject, so `(VALUE | f PARAMS)` is the same
call. Only this order is accepted; there is no legacy-order detection. Comparisons
retain `(gt LEFT RIGHT)` and conventional left/right semantics. Variadic operations
such as `str`, `concat`, `merge`, and `joinpath` retain authored operand order;
special forms and internal template-lowering primitives have their own contracts.

### Operation-name migration

| Removed spelling | Replacement |
| --- | --- |
| `write`, `yield`, `out`, `err`, `run`, `pipe`, `fail`, `shell-template` | Add `!`: `write!`, `yield!`, `out!`, `err!`, `run!`, `pipe!`, `fail!`, `shell-template!`. |
| `shell`, `shellrun`, `shell!`, `shellrun!`, `sh` | `sh!` |
| `set-field`, `regex-replace` | `set`, `resub` |
| `cat` | `str` for scalars, `join` for collections, or `json` for serialization. |

Removed standard-library spellings produce migration diagnostics, not aliases.
External command names and user-defined bindings are unaffected. `replace` is
different: it remains a deprecated alias for `sub` with identical behavior.

## Values and calls

| Operation | Signature | Result |
| --- | --- | --- |
| `not` | `(not VALUE)` | Language boolean negation. |
| `bool` | `(bool VALUE)` | Language truth value. |
| `str` | `(str VALUE...)` | Concatenate textual scalars without separators; reject collections. |
| `json` | `(json VALUE)` | Encode JSON-compatible values with stable lexical record key order. |
| `parse-json` | `(parse-json TEXT)` | Parse JSON into typed Kame values; malformed input reports `JSON_INVALID`. |
| `fail!` | `(fail! MESSAGE)` | Raise `USER_ERROR` with an explicit string message. |
| `count` | `(count VALUE)` | Number of string code points, bytes, list items, or record fields. |
| `get` | `(get KEY [DEFAULT] RECORD)` | Computed string-key lookup; missing keys return nil or DEFAULT. DEFAULT is evaluated eagerly. |
| `first` | `(first LIST)` | First item, or `:nil` for an empty list. |
| `nth` | `(nth INDEX LIST-OR-TEXT)` | Indexed item/code point, or `:nil` when out of range; negative indexes count from the end. |
| `list` | `(list VALUE...)` | A list containing the arguments. |
| `nop` | `(nop VALUE...)` | Last argument, or `:nil` with no arguments. |
| `apply` | `(apply FUNCTION LIST)` | Calls a function with list arguments. A one-argument function receives the list itself. |
| `items` | `(items RECORD)` | Ordered records `[key: STRING value: VALUE]`, preserving typed nested data. |
| `set` | `(set KEY VALUE RECORD)` | Replace at the existing position, or append a new string key; nil is a value, not deletion. |
| `merge` | `(merge RECORD...)` | Shallow right-biased merge; first-seen key order; no arguments return an empty record. |
| `record?`, `list?`, `string?` | `(PREDICATE VALUE)` | Exact outer-kind boolean, including empty data; patterns/bytes are not strings. |
| `digest` | `(digest ALGORITHM BYTES-OR-STRING)` | Raw bytes/UTF-8 SHA-1/256/512 digest, lowercase hex, without build fingerprint framing. SHA-1 is legacy-only. |

Only `:false` and `:nil` are false. Other values are true.
Truth conversion is not an emptiness test: zero and empty strings, bytes, lists,
and records are true. Use `(> (count VALUE) 0)` for a nonempty collection/text;
nil, booleans, and numbers are not valid `count` inputs. A nil-valued list item or
record field still contributes to the count. See [Expressions](./expressions.md).

Use native `items`/`set`/`merge` instead of JSON/template round trips and
kind predicates instead of JSON-prefix tests. Remove same-named local wrappers
to expose the native operations. `(digest "sha512" (read ./file))` hashes a tracked
read, including every trailing newline and binary byte.

## Equality

`eq`/`=` and `is`/`==` are identical value-equality operations; `ne`/`!=` negates
their result. Supported values are nil, booleans, numbers, strings, bytes, lists,
and records. Lists compare in order; records compare by key/value regardless of
field order. Bytes compare byte-for-byte and never equal strings. Different
supported kinds are unequal, except exact integer/float numeric equality.
Patterns, resources, processes, callables, duplicate record keys, and values at
depth 256 are invalid, even nested behind an earlier mismatch.

Use `(eq A B)`, not `(eq (str A) (str B))`. `filter`/`filter-out` equality shorthand
uses this same contract. Ordering and `unique` remain scalar-only.

## Collections

Callbacks run in input order. `map` and `flatmap` require `(FUNCTION LIST)`;
`filter` and `filter-out` require `(PREDICATE-OR-VALUE LIST)`.

| Operation | Signature | Result |
| --- | --- | --- |
| `map` | `(map FUNCTION LIST)` | One callback result per item. |
| `flatmap` | `(flatmap FUNCTION LIST)` | Maps and flattens one list level; every callback result must be a list. |
| `filter` | `(filter PREDICATE-OR-VALUE LIST)` | Items whose predicate is true or equal to `VALUE`. |
| `filter-out` | `(filter-out PREDICATE-OR-VALUE LIST)` | Items whose predicate is false or unequal to `VALUE`. |
| `reduce` | `(reduce FUNCTION [INITIAL] LIST)` | Left fold with `(accumulator item)` callbacks. Without `INITIAL`, the first item is the accumulator; an empty list produces `:nil`. |
| `concat` | `(concat VALUE...)` | Flattens list arguments one level and appends scalar arguments. |
| `slice` | `(slice START [END] LIST-OR-TEXT)` | Half-open slice; negative bounds count from the end. |
| `sorted` | `(sorted LIST)` | Ascending sort of one comparable scalar kind. |
| `unique` | `(unique LIST)` | First occurrence of every comparable scalar value. |

`sorted` and `unique` support nil, booleans, integers, floats, and strings; a
mixed or unsupported list is invalid (including integer/float mixtures).

## Text and patterns

| Operation | Signature | Result |
| --- | --- | --- |
| `join` | `(join SEPARATOR LIST)` | Joins a list of strings. |
| `split` | `(split SEPARATOR TEXT)` | Splits text on a nonempty literal separator. |
| `strip` | `(strip TEXT)` | Removes leading and trailing Unicode whitespace. |
| `sub` | `(sub FROM TO TEXT)` | Replaces all literal `FROM` occurrences. |
| `sub` | `(sub MATCH-PATTERN EXPANSION [TEXT])` | Pattern substitution. Without `TEXT`, returns a one-argument callable suitable for `map`. Nonmatches remain unchanged. |
| `replace` | Same signatures as `sub` | Deprecated alias with identical behavior; no runtime warning. |
| `match?` | `(match? MATCH-PATTERN [TEXT])` | Anchored boolean match; without `TEXT`, returns a unary predicate suitable for `filter`/`filter-out`. Subjects must be strings. |
| `includes?` | `(includes? PART TEXT)` | Literal substring test. |
| `starts?` | `(starts? PREFIX TEXT)` | Literal prefix test. |
| `ends?` | `(ends? SUFFIX TEXT)` | Literal suffix test. |
| `uppercase` | `(uppercase TEXT)` | Unicode uppercase conversion. |
| `lowercase` | `(lowercase TEXT)` | Unicode lowercase conversion. |
| `template-cat` | Lowering primitive | Document rendering only: nil is empty and lists join with spaces. Not scalar conversion. |
| `text` | `(text VALUE)` | Convert UTF-8 bytes to a string, or return a string unchanged. |
| `render` | `(render [PAYLOAD] [STYLE] SOURCE)` | Render a document template; in a two-argument call a leading record is payload and a leading string is style. File sources require read capability; bytes need explicit style. |
| `pattern` | `(pattern TEXT)` | Construct and validate a pattern from runtime text. |
| `regex-match` | `(regex-match PATTERN SUBJECT)` | Regex-pattern match record (`text`, `captures`, `named`), or nil. |
| `capture` | `(capture INDEX-OR-NAME MATCH)` | Select a match capture, or nil when absent. |
| `resub` | `(resub MATCH-PATTERN EXPANSION SUBJECT)` | Anchored regex-pattern replacement on a string or string list; non-matches yield nil. |

Literal `sub`/`replace` requires three strings; wrong types or a missing subject
report `EXPR_INVALID` rather than constructing a pattern-substitution callable.

`strip` is an explicit broad-whitespace conversion, not command-capture framing.
To remove one known terminal LF while preserving other whitespace and earlier
newlines, use `(if (ends? "\n" TEXT) (slice 0 -1 TEXT) TEXT)`. Do not infer a
CRLF terminator or remove all terminal newlines unless the producer's protocol
requires it. Capture itself is lossless; see [Kash](./kash.md).

See [Templates](./templates.md) for document directives, source resolution,
comment styles, and payload scopes. Lazy `if`, `and`, `or`, `match`, and `with`
are special forms, not eager library operations; see [Expressions](./expressions.md).

Pattern replacement uses Kame pattern literals such as `./src/{name:*}.c` and
expansion references such as `./build/{name}.o`. Invalid matcher/expansion
combinations or missing captures report `PAT_INVALID`.
The subject may be any list of strings, including a filtered wildcard result;
substitution preserves order and retains each nonmatching member unchanged.
Recursive captures backtrack across segments. Two-argument replacement creates
an invocation-owned callable, not printable/serializable definition data.
Regex grammar and matching budgets are defined in
[Pattern extensions](../../../spec/029-pattern-extensions.md).
Use `match?`, not substitution, as a filtering predicate: unchanged strings are
truthy. `resub` retains its separate nil-on-nonmatch contract. See
[Pattern predicates and substitution](../../../spec/041-pattern-predicates-and-substitution.md).
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
| `relpath` | `(relpath BASE TARGET)` | Target path relative to base. |
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
| `lstat` | `(lstat PATH)` | read | No-follow final-component metadata, with a portable `link` boolean; parents still follow links. |
| `readlink` | `(readlink PATH)` | read | Exact UTF-8 target text; relative targets stay relative, dangling links work; invalid text/absent paths/non-links report `FS_ERR`. |
| `wildcard` | `(wildcard [FLAG...] PATTERN-OR-PATH-LIST...)` | read | Sorted, duplicate-free union; at least one pattern/path-list operand is required. Each pattern tracks membership. |
| `write!` | `(write! PATH VALUE)` | write | Bytes write raw; other supported values use the effect-output encoding described below, not scalar-only `str`. Writes immediately in expression execution; defers an atomic write while rendering a build. Invalid while planning. |
| `env` | `(env NAME)` | env | Environment value. |
| `sh!` | `(sh! [OPTIONS] COMMAND)` | run | Collected status/stdout/stderr record; allowed in explicit evaluation, demanded lazy runtime values, and Kash recipe expressions. Invalid during planning or direct build-template rendering. |
| `shell-template!` | `(shell-template! FRAGMENTS VALUES)` | run | Collected shell call with POSIX-quoted dynamic strings; FRAGMENTS has one more string than VALUES. Same phase policy as sh!. |

`sh!` accepts only an empty OPTIONS record, such as `(parse-json "{}")`, until
options are supported; nonempty records report `EXPR_INVALID`. COMMAND is last.

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
`run!`, `pipe!`, and `await` are process special forms, not registry operations;
see [Kash](./kash.md).

### Wildcard selection

Unquoted glob paths evaluate to tracked lists before the call. `wildcard` accepts
these expanded lists without reinterpreting their filenames as patterns.

| Flags | Entry kind |
| --- | --- |
| `:+files` / `:-files` | Include / exclude regular files. |
| `:+dir` / `:-dir` | Include / exclude directories. |
| `:+symlinks` / `:-symlinks` | Include / exclude symbolic links, including dangling links. |

Without positive flags, selection starts with all entry kinds. Positive flags
select their union; negative flags subtract kinds. Flags may appear anywhere;
identical duplicates are harmless, opposite flags for one kind and unknown flags
report `EXPR_INVALID`. Pass flags as scalar operands, not list members.

```kame
(wildcard :+files ./src/**/*.c)
(wildcard :+files :+symlinks ./assets/**)
```

Classification does not follow symlinks: `:+files` excludes links to files.
Filtering does not prune traversal through ordinary directories. Recursive
wildcards do not descend into discovered directory symlinks; name a linked
directory in the literal prefix (`./linked/**/*.js`) to traverse it explicitly.
Results retain that lexical prefix. Membership and entry-kind changes can
invalidate consumers even when the set of path names stays unchanged.

## Build effects

These operations are meaningful in a rule recipe expression. Bytes are
emitted raw; other supported values use a separate effect-output encoder
(nil as `nil`, booleans, integers, floats, text, patterns, lists and records in
stable syntax) and emitted. Unrenderable values (callables, resources)
still report `EXPR_INVALID`.

| Operation | Signature | Effect |
| --- | --- | --- |
| `out!` | `(out! VALUE...)` | In a rule, defers bytes to the target's stdout. Standalone evaluation writes to stdout and returns the combined output. |
| `err!` | `(err! VALUE...)` | In a rule, defers bytes to the target's stderr. Standalone evaluation writes to stderr and returns the combined output. |
| `yield!` | `(yield! VALUE...)` | Supplies the bytes for a declared file output. Standalone evaluation writes to stdout and returns nil. It is valid only for a file rule with one output. |

Do not combine a nonempty shell recipe with `yield!`; Kame reports an output
conflict rather than choosing a producer.
