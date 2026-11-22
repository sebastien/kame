# Kame standard-library reference

Use `name argument…` to call an operation. Arguments evaluate before the
call, except for explicitly staged operands such as the replacement in
pattern `subst`. That replacement evaluates with captures in scope after a
match. Invalid arguments report `EXPR_INVALID` and missing capabilities
report `CAP_DENIED`.

Transformations typically take parameters first and data last.
`value |> f params` calls `f params value`. Comparisons take conventional
left and right operands. Variadic operations such as `str`, `concat`,
`merge`, and `joinpath` retain authored operand order.

## Values and calls

| Operation | Signature | Result |
| --- | --- | --- |
| `not` | `(not VALUE)` | Language boolean negation. |
| `bool` | `(bool VALUE)` | Language truth value. |
| `str` | `(str VALUE...)` | Concatenate textual scalars without separators; reject collections. |
| `json` | `(json VALUE)` | Encode JSON-compatible values with stable lexical record key order. |
| `parse-json` | `(parse-json TEXT)` | Parse JSON into typed Kame values; malformed input reports `JSON_INVALID`. |
| `parse-usage`, `render-usage` | `(parse-usage KDL)`, `(render-usage SPEC)` | Convert between Usage KDL and command spec records (`name`, `help`, `aliases`, `flags`, `args`, `commands`). [Completion](../../../spec/058-kash-completion.md) |
| `merge-usage` | `(merge-usage [SPEC...])` | Merge specs, highest precedence first; `nil` items are skipped. |
| `parse-man`, `man-redirect` | `(parse-man PAGE)`, `(man-redirect PAGE)` | A man page's options and synopsis as a spec record (empty when unreadable); the `.so` target of an include-only page, or `nil`. Plain, gzip and bzip2 pages. |
| `parse-fish-completions` | `(parse-fish-completions NAME TEXT)` | The static fish `complete` declarations for `NAME` as a spec record. |
| `fail!` | `(fail! MESSAGE)` | Raise `USER_ERROR` with an explicit string message. |
| `count` | `(count VALUE)` | Number of string code points, bytes, list items, or record fields. |
| `get` | `(get KEY [DEFAULT] RECORD)` | Computed string-key lookup; missing keys return nil or DEFAULT. DEFAULT is evaluated eagerly. |
| `first` | `(first LIST-OR-TEXT)` | Equivalent to `(nth 0 VALUE)`, including Unicode strings and nil for empty input. |
| `nth` | `(nth INDEX LIST-OR-TEXT)` | Indexed item/code point, or `nil` when out of range; negative indexes count from the end. |
| `list` | `(list VALUE...)` | A list containing the arguments. |
| `nop` | `(nop VALUE...)` | Last argument, or `nil` with no arguments. |
| `apply` | `(apply FUNCTION LIST)` | Spreads list elements as positional arguments, regardless of arity. Nest a list once to pass it as one argument. |
| `items` | `(items RECORD)` | Ordered records `{key: STRING, value: VALUE}`, preserving typed nested data. |
| `put` | `put KEY VALUE RECORD` | Replace at the existing position, or append a new string key; nil is a value, not deletion. |
| `merge` | `(merge RECORD...)` | Shallow right-biased merge; first-seen key order; no arguments return an empty record. |
| `nil?`, `bool?`, `int?`, `float?`, `string?`, `bytes?`, `list?`, `record?`, `callable?`, `resource?`, `pattern?`, `process?` | `(PREDICATE VALUE)` | Exact outer-kind boolean for every value, including opaque/callable data. |
| `number?` | `(number? VALUE)` | Whether the value is int or float. |
| `empty?` | `(empty? VALUE)` | Whether text, bytes, a list, or a record has zero length; false for nil and other kinds. |
| `has-key?` | `(has-key? KEY RECORD)` | Whether a string key is present, even when its value is nil. |
| `success?` | `(success? RESULT)` | Whether integer `status` and `signal` fields are both zero. Missing/malformed fields error; failures are not caught. |
| `blank?` | `(blank? VALUE)` | True only for nil or an empty string; false, zero, whitespace, and empty collections are not blank. |
| `digest` | `(digest ALGORITHM BYTES-OR-STRING)` | Raw bytes/UTF-8 SHA-1/256/512 digest, lowercase hex, without build fingerprint framing. SHA-1 is legacy-only. |

Only `false` and `nil` are false. Other values are true.
Truth conversion is not an emptiness test: zero and empty strings, bytes, lists,
and records are true. Use `(not (empty? VALUE))` for known collection/text data;
nil, booleans, and numbers are not valid `count` inputs. A nil-valued list item or
record field still contributes to the count. See [Expressions](./expressions.md).

Use native `items`/`put`/`merge` instead of JSON/template round trips and
kind predicates instead of JSON-prefix tests. Remove same-named local wrappers
to expose the native operations. `(digest "sha512" (read ./file))` hashes a tracked
read, including every trailing newline and binary byte.

## Arithmetic and comparisons

| Operation | Infix form | Result |
| --- | --- | --- |
| `add LEFT RIGHT` | `+` | Sum. |
| `sub LEFT RIGHT` | `-` | Difference. |
| `mul LEFT RIGHT` | `*` | Product. |
| `div LEFT RIGHT` | `/` | Floating-point quotient, including integer operands. |
| `mod LEFT RIGHT` | `%` | Integer remainder, with the dividend's sign when nonzero. |
| `neg VALUE` | | Arithmetic negation. |

Addition, subtraction, and multiplication keep the integer kind for integer
operands. Integer overflow, division by zero, remainder by zero, nonfinite
numbers, and invalid operand kinds report `EXPR_INVALID`.

`eq?` and `==` check value equality. `ne?` and `!=` check inequality. Supported values are nil, booleans, numbers, strings, bytes, lists,
and records. Lists compare in order; records compare by key/value regardless of
field order. Bytes compare byte-for-byte and never equal strings. Different
supported kinds are unequal, except exact integer/float numeric equality.
Patterns, resources, processes, callables, duplicate record keys, and values at
depth 256 are invalid, even nested behind an earlier mismatch.

Use `(eq? A B)`, not `(eq? (str A) (str B))`. `filter`/`filter-out` equality shorthand
uses this same contract. Ordering and `unique` remain scalar-only.

Named ordering predicates are `lt?`, `gt?`, `lte?`, and `gte?`; symbolic operators
remain available. Integers order exactly, including beyond 2^53; mixed int/float
ordering does not round the integer to float. NaN is unordered and errors.
Comparisons do not chain.

`get` defaults are eager. Use `if has-key? key record then get key record else compute-default ()`
to defer the default and preserve a present nil.

## Collections

Callbacks run in input order. `map` and `flatmap` require `(FUNCTION SUBJECT)`;
`filter` and `filter-out` require `(PREDICATE-OR-VALUE SUBJECT)`. A unary callback
receives the item alone; a callback declaring two or more parameters receives
the item followed by its key (list position or record field name), with any
further parameters nil. `map`, `filter`, and `filter-out` accept a record and
return a record with the same field order: `map (v k => str k "=" v) {a: 1}`
returns `{a: "a=1"}`. `flatmap` and `reduce` take lists only.

| Operation | Signature | Result |
| --- | --- | --- |
| `map` | `(map FUNCTION LIST-OR-RECORD)` | One callback result per item or field; `(value key => ...)` receives the item then its key. |
| `flatmap` | `(flatmap FUNCTION LIST)` | Maps and flattens one list level; every callback result must be a list. |
| `filter` | `(filter PREDICATE-OR-VALUE LIST-OR-RECORD)` | With a callable, keep items whose predicate result is truthy; otherwise, keep items equal to `VALUE`. |
| `filter-out` | `(filter-out PREDICATE-OR-VALUE LIST-OR-RECORD)` | With a callable, keep items whose predicate result is false or nil; otherwise, keep items unequal to `VALUE`. |
| `in?` | `(in? ITEM LIST)` | Exact membership using value equality, including nested lists/records and exact numeric equality. Validates the full operands before matching. |
| `reduce` | `(reduce FUNCTION [INITIAL] LIST)` | Left fold with `(accumulator item => ...)` callbacks; a third parameter receives the item's position. Without `INITIAL`, the first item is the accumulator; an empty list produces `nil`. |
| `collect!` | `collect! [LIMIT] STREAM` | Pulls a generator's values into a list. A non-negative `LIMIT` closes the source after that many values and runs pending cleanup. |
| `concat` | `(concat VALUE...)` | Flattens list arguments one level and appends scalar arguments. |
| `slice` | `(slice START [END] LIST-OR-TEXT)` | Half-open slice; negative bounds count from the end. |
| `sorted` | `(sorted LIST)` | Ascending sort of one comparable scalar kind. |
| `unique` | `(unique LIST)` | First occurrence of every comparable scalar value. |

`sorted` and `unique` support nil, booleans, integers, floats, and strings; a
mixed or unsupported list is invalid (including integer/float mixtures).

Use `in?` for list membership, `has-key?` for record keys, and
`includes?` for substring membership.

## Text and patterns

| Operation | Signature | Result |
| --- | --- | --- |
| `join` | `(join SEPARATOR LIST)` | Joins a list of strings. |
| `split` | `(split SEPARATOR TEXT)` | Splits text on a nonempty literal separator, preserving empty fields; empty text produces `[""]`. |
| `strip` | `(strip TEXT)` | Removes leading and trailing Unicode whitespace. |
| `line` | `(line TEXT)` | Removes one terminal LF, if present; preserves all other whitespace, including a preceding CR. |
| `subst` | `subst FROM TO TEXT` | Replaces all literal `FROM` occurrences. |
| `sub-token` | `(sub-token FROM TO TEXT)` | Replaces literal occurrences outside Unicode letter/number/underscore/hyphen boundaries. Search must be nonempty; replacement is literal and not rescanned. |
| `subst` | `subst MATCH-PATTERN EXPANSION [SUBJECT]` | Pattern substitution. A path pattern subject discovers matches like `wildcard`. Without a subject, returns a one-argument callable suitable for `map`. Nonmatches remain unchanged. |
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
| `capture` | `capture PATTERN SUBJECT` | Anchored typed match that returns a record of named captures or nil. |
| `bind` | `bind TEMPLATE CAPTURES` | Expand a pattern template using named captures. |
| `regex-group` | `regex-group INDEX-OR-NAME MATCH` | Select a regex capture or nil. |
| `resub` | `(resub MATCH-PATTERN EXPANSION SUBJECT)` | Anchored regex-pattern replacement on a string or string list; non-matches yield nil. |

Literal text replacement takes three strings. The two-argument form applies to
pattern substitution.

`strip` is an explicit broad-whitespace conversion, not command-capture framing.
To remove one known terminal LF while preserving other whitespace and earlier
newlines, use `(line TEXT)`. Empty input stays empty; non-strings are invalid.
Do not infer a CRLF terminator or remove all terminal newlines unless the producer's protocol
requires it. Capture itself is lossless; see [Kash](./kash.md).

See [Templates](./templates.md) for document directives, source resolution,
comment styles, and payload scopes. See the
[expression reference](./expressions.md) for conditional evaluation.

Pattern replacement uses Kame pattern literals such as `./src/{name:*}.c` and
expansion references such as `./build/{name}.o`. Invalid matcher/expansion
combinations or missing captures report `PAT_INVALID`.
The subject may be any list of strings, including a filtered wildcard result;
substitution preserves order and retains each nonmatching member unchanged.
Recursive captures backtrack across segments. Two-argument replacement creates
an invocation-owned callable, not printable/serializable definition data.
Regex grammar and matching budgets are defined in
[Pattern extensions](../../../spec/029-pattern-extensions.md).
That grammar rejects lookaround and treats `\w` as literal `w`, not a word class.
`sub-token` supplies global literal replacement with Python-style Unicode
`[\w-]` boundaries using shared Unicode 15.0.0 tables; it is not a CSS parser.
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
| `splitext` | `(splitext PATH)` | `[root, extension]`; a lone leading dot is not an extension. |
| `ext` | `(ext PATH)` | Final extension. |
| `joinpath` | `(joinpath PART...)` | Joined, cleaned path; retains explicit relative anchoring when the first nonempty part is `.` or starts with `./`. Parent traversal still normalizes normally. |
| `relpath` | `(relpath BASE TARGET)` | Target path relative to base. |
| `abspath` | `(abspath PATH)` | Clean absolute path, resolving a relative path from the evaluation directory. |
| Source identity | `module/dir`, `module/file` | Lexical directory or path of the authoring source. |

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
| `files` | `(files PATTERN-OR-PATH-LIST...)` | read | `(wildcard :+files ...)`. Rejects kind flags. |
| `dirs` | `(dirs PATTERN-OR-PATH-LIST...)` | read | `(wildcard :+dir ...)`. Rejects kind flags. |
| `write!` | `(write! PATH VALUE)` | write | Bytes write raw; other supported values use the effect-output encoding described below, not scalar-only `str`. Writes immediately in expression execution; defers an atomic write in direct Kash recipes. Invalid during planning or read-only rule computation/expansion. |
| `publish!` | `(publish! STAGED DESTINATION)` | read + write | Immediately publishes a completed staged regular file without replacing any existing destination entry. Requires read/write on staging and write on destination. Returns `{published: true, stagedRemoved: true}`. |
| Environment read | `env/NAME` | env | Current unit environment value or nil. |
| `sh!` | `(sh! [OPTIONS] COMMAND)` | run | Collected status/stdout/stderr record. Allowed in explicit evaluation and direct Kash. Invalid during planning, shell-template expansion, and value-producing Kame bodies, including through a reached lazy definition. |
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
Interactive/login Kash sessions instead use their current session PATH, while
retaining overrides, executable tracking and capability checks.
`(tool NAME | ?)` (equivalently `(? (tool NAME))`) returns nil for ordinary
unavailability using the same resolver. Empty/NUL names and nonstrings are invalid; capability
and operational failures remain errors. Absence is tracked and refreshed on
resource revalidation, without granting filesystem or environment reads.
`(now)` and `(monotonic)` request host clocks and return integer nanoseconds.
Neither needs a grant. Both are invalid during planning, output resolution,
shell-template expansion, and value-producing Kame bodies.

Standalone `kame do run --lang expr` denies these capabilities by default; grant only the
needed roots, names, or process access with its `--allow-*` options.
`run!`, `capture!`, `pipe!`, and `await` are process special forms, not registry operations;
see [Kash](./kash.md).

`publish!` supports local files on native POSIX and Node hosts. Memory resources,
Windows, cross-filesystem publication and unavailable no-replace primitives fail
with `FS_UNSUPPORTED`; existing entries, including dangling links, give
`FS_EXISTS`. Staging must be caller-owned, stable and fully written, with trusted
parent directories. Publication is atomic visibility, not crash durability, input
race validation, or a multi-file transaction. `FS_PUBLISHED` means publication
already occurred but subsequent work failed; its diagnostic cause records
`published` and `stagedRemoved`. Never blindly retry or remove the destination.
It is not deferred by recipe output handling and is suppressed during dry-run.
In recipes, create staging with an external command: `write!` to staging may be
deferred until successful recipe completion and is not a staging flush primitive.

## Script option parsing

`(parse-args SCHEMA ARGV)` is pure and takes an explicit list of strings. Each
schema field contains `names`, `arity` (`:flag`, `:one`, `:many`), and `repeat`
(`:append`, `:replace`, `:reject`; flags cannot append):

```kash
let schema = {fonts: {names: ["-f", "--fonts"], arity: :many, repeat: :append}}
let parsed = parse-args schema args
let fonts = parsed.options.fonts
```

The result has `options` and `operands`. All options are present; flags default to
false, value options to empty lists. Value options always return lists. Repeated
append preserves order. `:many` consumes one or more following non-option tokens
until the next option or `--`. Long `--name=value` syntax supplies one value,
including an empty or dash-prefixed value. Short attached/bundled options are not
supported. Unknown dash-prefixed tokens are errors, not paths; use `./-path` or
an attached long value. A lone `-` is data. After `--`, every token is an operand.
Use `--` to separate operands from a preceding multi-value option. Missing values,
unknown options, and rejected repeats give `ARGS_INVALID` with a zero-based index
without disclosing argv values. Invalid schemas give `EXPR_INVALID`.
See [Scripting and publication](../../../spec/046-kash-scripting-and-publication.md).

### Wildcard selection

A glob atom produces a `PathPattern`. Use `files ./src/**/*.c` for discovery,
`wildcard` for unions and kind flags, or `pattern TEXT` to compile a constructed
pattern. All three return sorted, duplicate-free paths. Flat lists of paths
contribute existing members without globbing their filenames again.

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
(files ./src/**/*.c)
(dirs ./assets/*)
(wildcard :+files :+symlinks ./assets/**)
```

Classification does not follow symlinks: `:+files` excludes links to files.
Filtering does not prune traversal through ordinary directories. Recursive
wildcards do not descend into discovered directory symlinks; name a linked
directory in the literal prefix (`./linked/**/*.js`) to traverse it explicitly.
Results retain that lexical prefix. Membership and entry-kind changes can
invalidate consumers even when the set of path names stays unchanged.

## Build effects

These operations emit output during standalone evaluation or direct Kash
execution, not during shell-template expansion. Bytes are emitted raw; other
supported values
are emitted using a separate effect-output encoder (nil as `nil`, booleans,
integers, floats, text, patterns, lists and records in stable syntax).
Unrenderable values (callables, resources) still report `EXPR_INVALID`.

| Operation | Signature | Effect |
| --- | --- | --- |
| `out!` | `(out! VALUE...)` | In direct Kash, emits bytes to the target's stdout. Standalone evaluation writes to stdout and returns the combined output. |
| `err!` | `(err! VALUE...)` | In direct Kash, emits bytes to the target's stderr. Standalone evaluation writes to stderr and returns the combined output. |

Shell-template expansion and rule expression bodies reject effects. A rule with
one file output publishes text or bytes from its expression body or from
`return VALUE` in its Kash body.
