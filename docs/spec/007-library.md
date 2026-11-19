---
revision: 2
---

# Standard Library

## Purpose

`038-direct-kash-and-values.md` refines standard operation contracts and owns
canonical action names, text/JSON separation, and wildcard kind filters. Those
requirements take precedence over the operation descriptions below.

The standard library provides ordinary operations over materialized values.
Reactive lifting, dependency tracking, authorization, and stream scheduling are
provided by the evaluator, not reimplemented by every operation.

The library covers the operations defined here for values, templates and build
programs. Additional operations require an explicit contract; an unspecified
notion of library breadth does not define behavior.

## Operation Contract

Each operation declares:

- Public name.
- Named call function and context pointer.
- Arity.
- Unconditional and argument-dependent capability requirements.
- Direct category: computation, tracked observation, output/publication,
  process action, control effect, or volatile observation.
- Allowed evaluation phases and dependency/effect behavior.
- Stable implementation version.

Standard registrations must have complete, valid descriptions, including aliases
and template primitives. A suffix is not evidence of a category or authority.
Registry validation rejects partial descriptions and category/behavior conflicts.
Unannotated custom registrations retain their existing runtime authorization;
they are not implicitly classified as pure standard operations.

Phase metadata is an upper bound, not permission to bypass contextual checks.
For example, an inline `render` can compute text without a read grant, while a
file template or nested include requires scoped read authority. An environment
snapshot can supply a requested value without a new host request. Pure planning
still cannot submit host requests merely because the enclosing operation permits
inline evaluation in that phase. Tool metadata tracking does not confer user
read authority. Service, dry-run, and publication restrictions remain independent.

Callback-bearing operations (`apply`, `map`, `flatmap`, `filter`, `filter-out`,
`reduce`, `finally!`, `subst`, and template application) explicitly propagate reached
callable behavior. A computation category on the enclosing operation does not
declare its callbacks pure.
`render` also evaluates template expressions under the caller's authority and
phase. Syntax that is not a registry call (`match`, `$(…)`, `$?(…)`, `wait`)
obeys the same contracts.

An operation receives evaluated arguments and an evaluation context. It returns
a value, a host request, or a structured diagnostic. Returned owned values use
the run allocator unless explicitly transferred to the engine.

An explicitly specified staged operand is the exception: pattern `subst`
retains its replacement expression for evaluation after matching, as defined in
`014-patterns.md` and `005-evaluation.md`. This does not make `subst` a reserved
word or change ordinary literal-string replacement.

Pure operations must not read clocks, environment, global mutable state, files,
or processes. Effectful operations register dependencies and effects through
the context.

## General Operations

Operations are called with ordinary application (`055` §4.2): `count xs`,
`join ", " names`, `now ()`. A bare operation name is the operation value and can
be passed to callbacks: `map basename paths`. Every operation is also reachable
as `std/NAME` when a lexical binding shadows it. Operations are not consulted in
command position (`055` §6): in a Kash block, `basename $f` runs the executable,
whereas `(basename f)` in a word or `let b = basename f` calls the operation.

Public transformations are **data-last**: `f PARAMS VALUE`, with all
configuration, including optional operands, before the subject. The value pipe
appends its input as the last argument, so `f PARAMS VALUE` and
`VALUE |> f PARAMS` have identical meaning. Comparisons keep conventional
`gt? LEFT RIGHT` semantics; variadic combiners retain authored operand order.
Keywords (`match`, `if`, `and`, `or`) and internal template primitives are not
operations.

In the signatures below, an uppercase operand followed by `?` is optional and one
followed by `...` is a rest operand. The data-last signatures are
`nth INDEX VALUE`, `slice START END? VALUE`, `join SEPARATOR LIST`,
`split SEPARATOR TEXT`, `includes? PART TEXT`, `starts? PREFIX TEXT`,
`ends? SUFFIX TEXT`, `reduce FUNCTION INITIAL? LIST`, `apply FUNCTION LIST`,
`relpath BASE TARGET`, `put KEY VALUE RECORD`, `get KEY DEFAULT? RECORD`,
`capture PATTERN SUBJECT`, `bind TEMPLATE CAPTURES` and
`sh! OPTIONS? COMMAND`. `map` and `flatmap` accept only `FUNCTION SUBJECT`;
`reduce` callbacks receive the accumulator and the item, plus the item's
position when they declare a third parameter.

```kame
names = sources |> map basename |> sorted
listing = join "\n" names
c-names = names |> filter (ends? ".c" _)
```

The general operations are:

| Name | Contract |
| --- | --- |
| `not` | Boolean negation using language truth rules |
| `bool` | Convert to language truth value |
| `neg` | `neg NUMBER`: arithmetic negation of a computed number (negative literals are written `-1`) |
| `str` | Concatenate zero or more scalar textual values; reject implicit collection serialization |
| `json` | Encode exactly one JSON-compatible value, including quoted scalar strings |
| `count` | Length of string code points, bytes, list, or record |
| `get` | `get KEY DEFAULT? RECORD`: computed string key; missing keys return nil or DEFAULT |
| `first` | First list item or string code point, equivalent to `nth 0 VALUE`; empty input returns nil |
| `nth` | Indexed list or string item with negative indexes |
| `apply` | `apply FUNCTION LIST` always spreads list elements as positional arguments, regardless of callable kind or arity |
| `finally!` | Execution-only `finally! OPTIONS? BODY CLEANUP`: zero-argument callbacks with bounded finalization under `043-kame-finalization.md` |
| `list` | Return arguments as a list |
| `items` | `items RECORD`: ordered `{key: STRING, value: VALUE}` entries |
| `put` | `put KEY VALUE RECORD`: immutable field replacement/insertion |
| `merge` | `merge RECORD...`: shallow, right-biased immutable record merge |
| `nil?` / `bool?` / `int?` / `float?` / `string?` / `bytes?` / `list?` / `record?` / `callable?` / `resource?` / `pattern?` / `process?` | Exact outer-kind tests on any value, without serialization |
| `number?` | True for int or float, false for every other kind |
| `empty?` | True exactly for zero-length strings, bytes, lists, or records; false for nil and all other kinds |
| `has-key?` | `has-key? KEY RECORD` tests presence, including a field whose value is nil |
| `success?` | `success? RESULT` tests integer `status` and `signal` fields for zero; malformed records/fields are `EXPR_INVALID` |
| `digest` | `digest ALGORITHM BYTES-OR-STRING`: raw SHA digest as lowercase hexadecimal |
| `fmt` | `fmt VALUE`: canonical, parseable expression source for a value (`055` §13) |
| `eval` | `eval TEXT`: parse one expression and evaluate it in value context in the current lexical scope and phase (`055` §13) |

`json` uses deterministic JSON for compatible scalars, lists and records with
stable record key order. `str` performs textual conversion/concatenation only;
both are distinct from the Kame value display of `005-evaluation.md`. Bytes
require explicit decoding and are not silently converted or JSON-encoded.

`get` uses byte-exact string keys and returns the selected value with its type
and callable ownership intact. An invalid record/key type is `EXPR_INVALID`.
`DEFAULT` is an ordinary evaluated argument, including when the key exists.
Records can replace families of computed configuration names:

```kame
modes = {debug: "-O0", release: "-O2"}
selected = "release"
flags = get selected modes
```

The operation is pure and works in definitions, input headers, and planning.

Presence and value lookup are separate: `has-key? "key" {key: nil}` is true,
whereas `get` returns nil for both a missing key and a present nil value. Presence
requires a string key and a record with unique keys. It does not evaluate a
fallback. `get` defaults are eager; use a lazy `if` expression with `has-key?`,
or `r.key ?? FALLBACK` for a literal field name (`055` §4.5), when computing a
default may fail or perform effects.

`apply` has one argument-spreading contract for lexical functions, Kash `def`
functions, registered operations, and variadic callables. Thus
`apply uppercase ["hello"]` is `uppercase "hello"`, and
`apply (xs... => xs) [1, 2]` returns `[1, 2]`. To pass a list as one argument,
nest it once, e.g. `apply count [[1, 2]]`, or call `count [1, 2]` directly. Empty
argument lists call with zero arguments and retain normal arity errors. Argument
spreading at a call site is also written directly: `f ...args`.

Public named predicates use `?`; logical `not`, truth conversion `bool`, and
the comparison operators are deliberate exceptions. Predicates retain
their documented domains: `nil?` and the kind tests accept every value, whereas
`in?`, `has-key?`, and `success?` validate their collection/result operands.
`empty?` is not absence or broad whitespace detection; `blank?` keeps its existing
nil-or-empty-string behavior. Kind tests never invoke callables or compare opaque
values. A synchronous process Result is a record, not a `process?` async handle.

### Scoped finalization

`finally!` is a standard control-effect operation with runtime-managed scope
state, not a special form or an ordinary helper around error recovery. Its
optional leading record accepts only `timeout-ms`, an integer from 1 to 60000,
default 5000. It propagates reached callback capabilities and effects without
adding authority. [043-kame-finalization.md](043-kame-finalization.md) defines
callback validation, result/error precedence, shared unwind deadlines,
cancellation, execution-only effect ordering and host requirements. It adds no
`defer!` alias or resource-acquisition/ownership protocol.

### Typed record traversal and updates

`items` returns a list in stored field order, not sorted or serialized order.
Each entry contains exactly `key` and `value`. Empty records (`{}`) yield an
empty list; `[]` is a list and is not accepted as a record. `put` requires a
string key: replacement preserves the field's position; a new key appends. An
empty key is valid, and putting nil stores a present nil field rather than
removing it.

`merge` processes records left to right. The last value for a repeated key wins,
while the key retains its first-seen position. The merge is shallow: a nested
record is replaced, not recursively combined. Zero arguments produce an empty
record. These operations do not mutate their operands and preserve all nested
value kinds, including bytes, resources and callable ownership. Invalid operand
types and records with duplicate keys produce `EXPR_INVALID`. Record literals
with `...` spreads (`{...settings, version: "new"}`) build records directly;
`put` and `merge` serve computed keys and records:

```kame
settings = {version: "old", nested: {enabled: true}}
updated = settings |> put "version" "new"
entries = items updated
combined = merge settings {version: "new", extra: 3}
```

The kind predicates return booleans for every value, including empty data, and
inspect only the outer kind. A pattern is not a string, bytes are not strings,
and a record/list containing callables remains a record/list. No JSON-prefix
test, truth conversion, or implicit parsing occurs. SDK `object?` and `array?`
wrappers delegate to `record?` and `list?` rather than defining
serialization-based equivalents.

### Raw digests

The algorithm is exactly `"sha1"`, `"sha256"`, or `"sha512"`; there is no default,
alias normalization, or platform-dependent selection. Unknown algorithms and
non-string algorithm arguments produce `EXPR_INVALID`. The input is bytes or
the exact UTF-8 bytes of a string, including NUL and trailing newlines. No
decoding, normalization, trimming, value framing, or build-fingerprint salt is
applied. Results have respectively 40, 64, or 128 lowercase hexadecimal digits.

`digest` is pure and identical on native and WASM hosts. Hashing a file composes
with a tracked read: `read ./asset |> digest "sha512"`. The read retains its
ordinary capability, phase, and dependency rules; hashing does not bypass them.
Do not substitute resource/value/build fingerprints for these interoperable
digests. SHA-1 is only for interoperating with existing SHA-1 identifiers, not a
collision-resistant security claim or password derivation.

## Numeric operations

| Signature | Result |
| --- | --- |
| `add LEFT RIGHT` | `LEFT + RIGHT` |
| `sub LEFT RIGHT` | `LEFT - RIGHT` |
| `mul LEFT RIGHT` | `LEFT * RIGHT` |
| `div LEFT RIGHT` | Floating-point quotient, including when both operands are integers |
| `mod LEFT RIGHT` | Integer remainder with the sign of the dividend when nonzero |
| `neg VALUE` | The additive inverse |

`add`, `sub` and `mul` preserve the integer kind when both operands are
integers. They convert to floating-point arithmetic when either operand is a
float. `neg` preserves its operand's kind. `mod` accepts integers only.
Integer overflow, nonfinite operands or results, division or remainder by zero,
and operands of other kinds are `EXPR_INVALID`.

## Comparison Operations

Comparison operations compare materialized values. Each comparison exists as a
named operation and as an infix operator (`055` §4.1) with the identical
contract. The operators are syntax, not names: to pass a comparison as a value,
use the named operation in a section (`filter (eq? "x" _) xs`) or an operator
section (`filter (_ == "x") xs`). `=` is never an operator.

| Name | Operator | Contract |
| --- | --- | --- |
| `eq?` | `==` | Strict, kind-aware equality |
| `ne?` | `!=` | Negation of `eq?` |
| `lt?` | `<` | Ascending order |
| `gt?` | `>` | Descending order |
| `gte?` | `>=` | Order at or after |
| `lte?` | `<=` | Order at or before |

`gt? LEFT RIGHT` is `LEFT > RIGHT`, and likewise for the other names.
Comparison operators do not chain: `a < b < c` is `PARSE_ERR`. `eq?` and `ne?`
support nil, boolean, number, string, bytes, lists, and records. Equality is not
identity. The contract is recursive:

- Nil and booleans compare by value; `nil` and `false` are distinct.
- Integers compare exactly, even outside the exact `float64` integer range.
  Integer/float equality requires an exactly matching integral float:
  `9007199254740993` does not equal `9007199254740992.0`. Floats use IEEE equality:
  NaN is unequal to everything, equal infinities compare equal, and signed zeros
  compare equal.
- Strings compare exact bytes without Unicode normalization. Bytes compare
  length and every byte, including NUL and invalid UTF-8. Bytes and strings are
  different kinds and are never implicitly converted for comparison.
- Lists compare length and corresponding items, recursively and in order.
- Records compare the same key set and recursively equal field values;
  authored/insertion order does not affect equality. Iteration and display order
  are unchanged. Duplicate keys in a host-created record are invalid.
- Different supported kinds are unequal, except numeric int/float equality.
  Empty lists and empty records are distinct.
- Patterns, resources, processes, and all callables (including operation values)
  are unsupported, rather than compared by text or identity. Both complete
  operands must be comparable: nested unsupported values produce `EXPR_INVALID`
  even when lengths, keys, kinds, or an earlier item differ. Values at depth 256
  or deeper are rejected (the operand root has depth 0), consistent with portable
  parsing/signature limits. Cyclic host-created containers are therefore rejected
  rather than compared as cyclic graphs.

Comparison never serializes or mutates the data: `a == b` preserves kinds and
binary data where `str a == str b` would not. Reordered records compare equal,
unlike their printed representations. `ne?`/`!=` negates successful equality;
unsupported-value errors still propagate. Boolean negation is `not`.

Ordering (`lt?`, `gt?`, `gte?`, `lte?`, and their operators) accepts two numbers or
two strings. Integer/integer ordering is exact across all int64 values, without
float conversion. Integer/float ordering compares mathematical values without
rounding the integer to float64, including fractional floats, signed zero,
infinities, and floats at/outside int64 bounds. Float/float ordering uses numeric
order; NaN is unordered and reports `EXPR_INVALID`. Comparable numbers that are
equal under `eq?` compare at the same position; distinct adjacent large integers
must not collapse into the same position. String ordering compares UTF-8 bytes.
Other kind combinations and non-scalars are `EXPR_INVALID`. Bytes and collections
have equality but no ordering.

## Collection Operations

The collection operations are:

| Name | Contract |
| --- | --- |
| `map` | Apply a function to each list item or record field value, passing the item and its key |
| `flatmap` | Map a list and flatten one level, with the same callback arguments |
| `filter` | Keep list items or record fields for which a predicate is true, passing the item and its key |
| `filter-out` | Remove list items or record fields for which a predicate is true, with the same callback arguments |
| `in?` | `in? ITEM LIST` tests exact membership with the recursive `eq?` contract; full operands are validated before matching |
| `reduce` | Fold a list from left to right, passing the accumulator, the item, and its position |
| `indexed` | `indexed LIST`: `[[0, x0], [1, x1], …]`, pairing each item with its zero-based position |
| `concat` | Concatenate lists or append scalar values |
| `slice` | Half-open list or string slice |
| `sorted` | Stable ascending scalar sort |
| `unique` | Retain the first occurrence of each scalar value |

Callbacks are lexical functions evaluated in item order for deterministic
effects and diagnostics. Their internally discovered host dependencies may run
in parallel, but the result preserves input order. `map`, `flatmap`, `filter`,
and `filter-out` call a unary callback with the item alone and a callable
declaring two or more parameters with the item followed by its key; further
parameters are nil. The key of a list item is its zero-based position and the
key of a record field is its name. `reduce` likewise appends the item's
position for a callable declaring three or more parameters.

`map`, `filter`, and `filter-out` also accept a record with unique keys as
their subject and return a record: `map` keeps every field in order with the
callback result as its value, and `filter`/`filter-out` keep the selected
fields in order. `flatmap`, `reduce`, `indexed`, and the membership operations
accept lists only.

Use `in?` for list membership, `has-key?` for record presence, and `includes?`
for text substrings. The three domains are explicit and do not silently coerce.

`filter` and `filter-out` accept `PREDICATE SUBJECT`, or `VALUE SUBJECT` as an
equality shorthand. The shorthand retains or removes items equal to `VALUE`,
respectively, using the same recursive equality contract as `eq?`, including
bytes, lists, and records. Unsupported values remain errors.

```kame
objects = sources |> map (s => ./build/@{basename s}.o)
headers = files ./include/*.h |> filter-out ./include/config.h
total = sizes |> reduce (acc n => acc + n) 0
```

`blank?` is true exactly for nil or an empty string. False, zero, whitespace-only
strings, empty lists/records, patterns, and other kinds are not blank. This does
not change ordinary Kame truthiness.

`unique` supports nil, boolean, number, and string values. Sorting
mixed kinds is invalid.

## Text Operations

The text operations are:

| Name | Contract |
| --- | --- |
| `join` | Join a list of scalar strings with a separator |
| `split` | Split text by a nonempty literal separator, preserving empty fields (`""` produces `[""]`) |
| `strip` | Trim Unicode whitespace |
| `line` | `line TEXT` removes exactly one terminal LF from a string, if present |
| `includes?` | Literal substring membership |
| `starts?` | Literal prefix test |
| `ends?` | Literal suffix test |
| `subst` | Substitute literal occurrences, or evaluate a capture-scoped expansion while preserving nonmatches per `041-pattern-predicates-and-substitution.md` |
| `match?` | Anchored boolean pattern matching, directly or as a unary predicate |
| `capture` | `capture PATTERN SUBJECT`: anchored match returning a capture record, or nil on no match |
| `bind` | `bind TEMPLATE CAPTURES`: build a value from a template and a capture record |
| `uppercase` | Unicode uppercase conversion supported by Solod |
| `lowercase` | Unicode lowercase conversion supported by Solod |
| `black`, `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`, `white` | Wrap one string in the matching ANSI foreground sequence |
| `bold`, `dim` | Wrap one string in the ANSI bold or dim sequence |

`line` is a pure, one-argument string operation for explicit line framing. It
returns its input unchanged when it has no terminal LF. It preserves leading
and trailing spaces, earlier line endings, and any CR preceding the removed LF;
it does not infer CRLF framing, select the first line, reject embedded newlines,
or strip all trailing newlines. Empty input remains empty. Non-string operands
and incorrect arity report `EXPR_INVALID`; bytes require explicit `text`
conversion first. It is callable and usable in value pipes like other ordinary
text operations.

```kame
line ""             # ""
line "name"         # "name"
line " name \n"     # " name "
line "name\n\n"     # "name\n"
line "name\r\n"     # "name\r"
```

Command capture remains lossless. Consumers opt into framing with, for example,
`line $(mktemp -d ./workspace.XXXXXX)`; `strip` remains the broader Unicode
whitespace conversion and is not a substitute when whitespace is data.

Terminal style operations are pure string functions. They always include their
ANSI start and reset sequences; they do not inspect whether stdout is a TTY.
Use them only when the resulting string is intended for a terminal. Canonical
value display and ordinary text conversion never add terminal escapes.

Portable regular-expression matching and replacement are specified in
`029-pattern-extensions.md`.

### Pattern Matching, Capture and Substitution

Pattern dispatch over ordered arms is the `match` keyword (`055` §4.5), not a
library operation. The operations below cover single-pattern use. Per
`041-pattern-predicates-and-substitution.md`, `subst` preserves nonmatching
strings and `match?` provides boolean pattern predicates.

`capture PATTERN SUBJECT` matches a `PathPattern` against a `Path`, or a string
pattern against a string, anchored. It returns a record of the named captures,
or nil when there is no match. Repeated named captures must agree.
`bind TEMPLATE CAPTURES` fills a `PathTemplate` (returning a `Path`) or a string
template (returning a string) from a capture record. It fails with
`PAT_INVALID` on absent or incompatible captures. Both keep path kinds
(`055` §9.3); neither converts between paths and text:

```kame
captures = ./src/lib/math/vector.c |> capture ./src/{path:**}/{name}.c
# {path: "lib/math", name: "vector"}
output = captures |> bind ./build/{path}/{name}.o
# ./build/lib/math/vector.o
```

Regex matching and selection of positional or named regex captures
(`regex-match`, `regex-group`) are specified in `029-pattern-extensions.md`.

`subst` accepts two or three arguments. `subst FROM TO TEXT` with plain
strings replaces literal occurrences and retains ordinary eager evaluation.
Wrong literal operand types or a missing literal subject report `EXPR_INVALID`.
A match pattern as the first argument selects anchored matching. Its second
operand is a replacement expression evaluated only after a match, with named
captures bound locally; `./{name}`, `"@{name}"`, `name`, and `(uppercase name)`
can all use `{name:*}`:

```kame
stems = sources |> subst ./src/{name:*}.c name
```

 With two operands it returns a callable of arity one
that defers the replacement until applied by `map`, a pipe, or a direct call.
A string subject produces the replacement string or the original string on no
match; a list subject maps in order and preserves nonmatching entries. Invalid
pattern combinations and missing expansion-pattern capture references are
`PAT_INVALID`; unknown
ordinary names in a selected expression retain `REF_MISSING`. Full semantics
are specified in `014-patterns.md`.

## Path Operations

Path operations are lexical and use slash-separated Kame paths. They accept
`Path` values and strings. A path-valued result keeps the kind of its primary
operand: a `Path` operand yields a `Path`, a string operand yields a string.
`basename` and `ext` always return strings.

| Name | Contract |
| --- | --- |
| `basename` | Final path component |
| `dirname` | Path without final component |
| `splitext` | Two-item list of root and final extension |
| `ext` | Final extension |
| `joinpath` | Join and clean path components, preserving an explicit relative anchor |
| `relpath` | Path relative to a base |
| `abspath` | Path resolved against the root directory (`055` §9.2) |

These operations do not access the filesystem and create no dependencies.
`splitext` treats a leading dot on a basename as part of the name unless another
dot follows.

`joinpath` preserves a leading `./` when its first nonempty part is `.` or
begins with `./`, so a computed file path does not become a bare task name.
For example `joinpath ./sdk "src"` is `./sdk/src`; a result at the anchor
itself is `./`. Parent traversal still normalizes: `joinpath ./sdk ".." ".."`
is `..`, not a fabricated parent prefix. Unanchored and absolute inputs retain
their ordinary cleaned spelling. A module reaches files beside its own source
with `joinpath module/dir "templates"` or the path atom
`@{module/dir}/templates`.

## Filesystem Operations

Filesystem operations issue host requests and require capabilities:

| Name | Capability | Contract |
| --- | --- | --- |
| `read` | `read` | Return file bytes or explicitly decoded text |
| `write!` | `write` | Atomically write bytes or UTF-8 text |
| `exists?` | `read` | Return whether a path exists |
| `stat` | `read` | Return stable metadata record |
| `lstat` | `read` | Inspect the final path component without following its symlink |
| `readlink` | `read` | Return the final symlink's exact UTF-8 target text, without resolving it |
| `wildcard` | `read` | Return the sorted, duplicate-free `List[Path]` union of one or more filesystem patterns |
| `files` | `read` | `wildcard` restricted to regular files: `files PATTERNS...` is `wildcard :+files PATTERNS...` |
| `dirs` | `read` | `wildcard` restricted to directories: `dirs PATTERNS...` is `wildcard :+dir PATTERNS...` |

`read`, `stat`, and `wildcard` register file or glob dependencies before
returning. `exists?` also registers a dependency so creation or removal can
invalidate its consumer.

`stat` follows the final symlink and returns `name`, `size`, `mode`, and `dir`.
`lstat` returns those fields plus `link` and `regular`; `regular` identifies a
regular-file entry without interpreting platform mode bits. Size and mode describe the link itself
when `link` is true. A symlink to a directory therefore has `dir: false` under
`lstat`. Mode is the host's metadata integer; use `link`, not platform mode bits,
for portable classification. Dangling and looping final links are inspectable
without reading their referent. Parent path components still follow ordinary
filesystem resolution: these calls are not a directory-traversal sandbox.

`readlink` preserves relative segments, spelling and whitespace. It does not
resolve the target against cwd or the link's parent, follow it, or require that
it exists. Missing paths, non-links, host failures, invalid UTF-8 target text,
and targets of 65536 bytes or more report `FS_ERR`; no truncated or
replacement-decoded target is returned. `mem://` resources have no symlinks:
`lstat` classifies ordinary memory resources, while `readlink` fails.

Both operations accept a string or file resource and require read authorization
for the inspected path, not authorization to read the target. They remain
forbidden in pure registration and permitted in read-only input discovery.
Followed metadata, no-follow metadata, and link target are separate accepted
observations. Retargeting invalidates consumers even when referent bytes are
equal, or both referents are missing. Hosts/watchers must retain these views and
promote already-discovered content watches when a later metadata fact arrives.

Inspection does not make deletion safe. Checking `lstat`/`readlink` and then
removing a path has a time-of-check/time-of-use race; it is not proof of producer
ownership. Safe cleanup requires a validated owned-member manifest plus a
separate race-resistant filesystem primitive/descriptor-relative operation.
Never infer ownership from symlink target text or recursively delete shared roots.

Every reached glob pattern is represented by one shared lazy dependency source,
including when its current result is empty. The dependency tracks membership,
so watch invalidation reevaluates the same source and its consumers when a match
is added or removed. See `023-wildcard-sources.md`.

Relative paths resolve against the root directory (`055` §9.2). Pattern
operands are `PathPattern` values written as path atoms; `wildcard` supports
`*`, `?`, character classes, and recursive `**`. A computed pattern is built
with interpolation in a path atom (`./@{dir}/*.c`), whose inserted bytes stay
literal. Strings are not patterns: a string operand is `EXPR_INVALID`. Results
are `Path` values relative to the root, with `./` prefixes where possible, sorted
by raw UTF-8 bytes. Each pattern operand retains its own glob dependency,
including empty members. For example,
`files ./src/**/*.go ./glue/**/*.c ./glue/**/*.h` combines source extensions
without shell discovery.

A `PathPattern` is never enumerated implicitly (`023-wildcard-sources.md`);
discovery is always an explicit `wildcard`, `files` or `dirs` call or a rule
input. `wildcard` also accepts flat lists of `Path` values as already-discovered
members, so `wildcard (files ./journal/*)` is valid and can be combined with
pattern operands. Lists are unioned without globbing their members again;
filenames containing metacharacters remain data. An empty list is valid and
contributes no members; nested lists and non-path members are `EXPR_INVALID`.
Originating glob dependencies remain tracked, and entry-kind flags apply to the
combined matches under `038-direct-kash-and-values.md`. In declaration context
the result is a planning-time snapshot, and a pattern overlapping a declared
output fails with `DISCOVERY_OVERLAP` (`055` §9.4).

Recursive discovery does not descend into directory symlinks encountered among
directory entries. An explicitly named directory symlink in the literal prefix
is traversed, so `./linked/**/*.js` can enumerate its target, while
`./**/*.js` does not discover files by descending through `./linked`. Results
retain the authored lexical path. This prevents recursive symlink cycles; name
linked roots explicitly rather than expanding recursive traversal implicitly.

`write!` is immediate only in explicit expression execution. In Kash rule
bodies it becomes a deferred effect committed during execution. It is invalid
during planning, foreign-shell recipe rendering, and expression rule bodies
(`rule … = EXPR`).

## Build Effects

| Name | Contract |
| --- | --- |
| `out!` | In Kash bodies or standalone evaluation, emit stdout |
| `err!` | In Kash bodies or standalone evaluation, emit stderr |
| `nop` | Return its final argument, or nil |

`out!` and `err!` both emit their arguments' exact text or bytes and return
their concatenated output (text unless any argument is bytes). They are also
Kash command builtins (`055` §6), so in command position they write to the
stage's stdout or stderr and take part in pipes and redirection:
`out! $content > $outputs.0`. Foreign-shell recipe rendering and expression
rule bodies reject them with `PHASE_INVALID`. A rule publishes artifact content
with an expression body or a Kash `return VALUE` (`044-value-producing-rule-bodies.md`),
or by writing its output with a command or `write!`.

Effect output is not scalar `str` conversion: it accepts raw bytes and text
conversion of nil, booleans, numbers, strings, patterns, lists and records.
Collection output uses the output encoder's JSON-like text, not a guarantee of
JSON compatibility or reparsable Kame display. Callables and resources are
rejected. Use `json` when a consumer requires JSON.

Effects record their source position. Kash rule bodies retain their deferred
publication contract. The runtime commits content once for the accepted generation.
Every build effect is invalid during planning and returns `PHASE_INVALID`.

## Shell Operations

Kash bodies name an executable explicitly with the `x/NAME` head
(`x/git status`), and foreign-shell recipes interpolate it with `@{x/NAME}`.
Kame resolves `NAME` against the build driver's startup `PATH` (not the
recipe's configured PATH) only when the reference is reached while running or
rendering a selected rule. Missing or
non-executable tools use `TOOL_MISSING` at the authored reference; tools in
unused rules never block planning or execution. The reference renders the
resolved executable path and records that file as a dynamic dependency of the
using rule. `kame do tools` reports the global tool set and resolved paths,
including an empty path for an unavailable tool. `kame do tools check TARGETS...`
validates tools in selected dependency plans without executing recipes.
Unqualified command heads in Kash bodies follow command-position resolution
(`055` §6); in foreign-shell recipes they are resolved by that shell.

Interactive/login Kash sessions are the exception to startup-PATH lookup:
their `tool` and `x/NAME` references use the session's environment-store
`PATH` (`055` §5.3), as specified in `052-kash-interactive.md`. Recipe lookup
uses the startup `PATH` as above.

Rule bodies, `$(…)` and `$?(…)` are the primary process interface. One explicit
`sh!` operation is provided for expressions that need collected results from a
POSIX shell script. It requires
`run`, submits a process through `003-posix-process.md`, and returns:

```kame
{status: 0, stdout: "...", stderr: "..."}
```

The signature is `sh! OPTIONS? COMMAND`: COMMAND is a script string and
OPTIONS, when present, must be the empty record `{}`. Nonempty options are
rejected with `EXPR_INVALID`. Streaming process events remain available through
runtime execution, not as a list accumulated by the operation. `sh!` is allowed
in explicit expression execution and Kash bodies, but it is invalid during
planning, resolution, foreign-shell recipe rendering, and expression rule
bodies with `PHASE_INVALID`. A lazy definition inherits
the phase of the computation that demands it. When a Kash recipe is bound to a
target, its collected shell calls inherit that environment and retain the
invocation run policy. Declaration registration never launches them.

`shell-template! FRAGMENTS VALUES` is the tagged-template form for constructing
a shell script from trusted source and dynamic string values. Both arguments
are lists of strings, and `FRAGMENTS` must contain exactly one more item than
`VALUES`. The operation concatenates each source fragment with the
corresponding value enclosed in POSIX single quotes; an embedded single quote
is emitted by closing the quoted value, writing an escaped quote, and reopening
the quote. The final fragment follows the last value. Static fragments remain
executable shell syntax and should come from authored source; dynamic values
cannot introduce shell syntax. Quoting does not prevent a value from being
interpreted as an option or path by the invoked program.

The constructed script is submitted and collected like `sh!`: it requires
`run`, returns the same status/stdout/stderr record, inherits a target's
environment, and is invalid during planning, foreign-shell recipe rendering, and
expression rule bodies. Dry-run submits no process and returns an empty string. Invalid
list types, non-string elements, or fragment/value count mismatch return
`EXPR_INVALID` without submitting a process.

## Clock Operations

Clock operations read the host clocks. They are effectful: they submit host
requests and are invalid during planning, resolution, foreign-shell recipe
rendering, and expression rule bodies with `PHASE_INVALID`, because those phases
must not depend on the time at which they ran. They are allowed in explicit
expression execution and Kash bodies. Both take no arguments: `now ()`,
`monotonic ()`.

| Name | Contract |
| --- | --- |
| `now` | Wall-clock nanoseconds since the Unix epoch |
| `monotonic` | Monotonic-clock nanoseconds from an arbitrary origin |

Both return integer nanoseconds. `monotonic` never decreases and is unaffected
by wall-clock adjustments, so it measures durations; only differences between
two readings are meaningful. Neither requires a capability grant.

## Environment

Environment reads use `env/NAME`, which yields nil when the variable is unset,
or a bare `ENV-NAME` reference, which fails with `ENV_VAR_MISSING` when unset
and no lexical binding of that name exists (`055` §3.2, §6). Either form requires
the `env` capability and reads one named variable from the current unit's
environment store. It registers the variable name and observed value as a
dynamic dependency. Reading the entire environment at once is not a supported
operation. Writes are the `set env/NAME = VALUE` statement (`055` §5.3), not a
library operation.

## Tool configuration

`tool NAME` accepts one nonempty string and returns the resolved executable
path. It requires a build or source session. The operation registers a shared
`tool` dependency and a file dependency for the selected executable; it is not
a pure operation. Resolution performs no process execution and is cached per
name for the invocation. Missing executables report `TOOL_MISSING` before the
using recipe executes. Computed names resolve when reached; literal names also
appear in declaration inspection. Plan JSON includes a `tools` object mapping
names to selected paths. An unavailable inspected declaration has an empty path.

The host resolves explicit paths beneath the selected working directory and bare
names against its startup PATH. Recipe environment changes do not alter this
lookup policy. Repeatable `--tool NAME=PATH` selects the last override. Internal
executable metadata checks do not grant access to user read operations.

## Acceptance Tests

- Canonical named predicates end in `?`, including `in?`; comparison operators
  and their named operations agree in behavior and phase contracts, and only the
  named forms are callable values. `not` and `bool` retain logical/conversion
  names without pretending to be kind tests.
- Numeric operators agree with their named operations. Integer addition,
  subtraction, multiplication and negation detect overflow. Division of
  integers yields a float. Remainder requires integers and follows the
  dividend's sign. Zero divisors and nonfinite operands or results fail.
- `apply` spreads argument lists regardless of arity, with normal arity errors,
  typed/rest argument binding, callable ownership, and once-only host suspension.
- `first` and `nth 0` agree on empty/nonempty lists and Unicode strings.
- Every data-last operation gives the same result called directly and through
  `|>`, including with `_` placement.
- `put` replaces in place or appends, stores nil as a present field, and never
  mutates its operand.
- Kind tests and `nil?` accept every core value kind, including opaque/callable
  values. `empty?` detects empty text/bytes/lists/records only. `blank?` is unchanged.
- `in?` preserves recursive membership validation. `has-key?` detects present nil
  fields without conflating absence; invalid keys/records are rejected. `success?`
  requires integer status/signal fields, rejects malformed Results, and has no
  effect or recovery behavior.
- Integer ordering is exact at int64 limits and beyond 2^53. Mixed int/float
  ordering agrees with exact equality and handles fractions/infinities safely;
  NaN is unordered rather than spuriously equal under ordering.

- Every operation validates arity and value kinds without mutating arguments.
- Pure operations issue no host request and register no dependency.
- Collection callbacks preserve input order.
- `filter` and `filter-out` accept `PREDICATE LIST` and the `VALUE LIST`
  equality shorthand, including mixed-kind inequality and numeric int/float
  equality, binary equality, and recursive list/record equality; opaque values
  remain `EXPR_INVALID`, including behind an earlier mismatch.
- Composed path, collection and filesystem expressions parse and evaluate
  against a fixture source tree with tracked resource dependencies.
- Path operations handle root, dotfiles, trailing separators, and relative
  normalization without filesystem access.
- `joinpath` retains explicit relative anchoring across empty leading parts,
  `.` / `./` roots, cleaning and parent traversal, without adding an anchor to
  ordinary unanchored paths. Joined concrete artifact paths retain file
  classification as computed outputs/prerequisites in planning/materialization,
  including basenames that would otherwise be valid logical task names.
- Wildcard results are sorted `Path` values and recursive `**` crosses
  directories.
- `files` and `dirs` match `wildcard` with `:+files` and `:+dir`. They reject kind flags; unions and exclusions stay on `wildcard`.
- `wildcard` rejects string patterns; a `PathPattern` value is never enumerated
  outside a discovery call or rule input. Empty, singleton and multiple matches
  keep list shape; unioning discovered lists never re-globs their filenames or
  loses their membership dependencies.
- `capture` and `bind` round-trip path captures, keep `Path`/`PathPattern`/
  `PathTemplate` kinds, return nil on nonmatch and fail on missing captures.
- Reads and globs register canonical dynamic dependencies.
- Denied read, write, run, or environment access performs no host action.
- Kash-body `out!`, `err!` and `write!` effects retain their accepted-generation
  publication contract. Expression rule bodies and foreign-shell recipe
  rendering reject output actions; they never rerender actions into command text.
- `sh!` captures status and separate byte-bounded stdout/stderr, enforces run
  capability and phase restrictions, and rejects nonempty options records.
- `shell-template!` requires one more trusted fragment than dynamic string
  values, quotes dynamic values safely under POSIX `sh`, preserves the
  collected result, and is verified against command-substitution injection on
  native and WASM hosts.
- Terminal style functions wrap strings with their documented ANSI sequences,
  compose using selective reset codes, reject nonstrings, and leave canonical
  value display unchanged.
- `line` removes one terminal LF only, covering empty/no-LF input, leading and
  trailing spaces, multiple terminal LFs, embedded newlines, CR-only and CRLF
  input. Arity and non-string errors, callable aliases, and value pipes agree on
  native and WASM; capture and `strip` behavior remain unchanged.
- Pattern `subst` matches anchored, expands
  references, preserves the original string without a match, and accepts a
  callable through `map` and pipes. `resub` retains its separate
  nil-on-nonmatch contract in `029-pattern-extensions.md`.
- Pattern replacement expressions resolve captures as local names, skip all
  replacement dependencies/effects on nonmatches, and leave literal-string
  replacement unchanged.
- Operation tests using `mem.Tracker` leak no returned container or string.
- `eq?`/`==` share one strict, kind-aware comparison; `ne?`/`!=` is its
  negation and `not` remains boolean negation. `=` is not an operator.
- `eq?` treats values of different kinds as unequal; `nil` and `false` are
  distinct; numbers compare numerically across integer and float.
- `lt?`/`gt?`/`gte?`/`lte?` and `<`/`>`/`>=`/`<=` order numbers and strings and
  reject mixed-kind or non-scalar arguments with `EXPR_INVALID`.
- `fmt` output round-trips through `eval` with value kinds preserved; `eval`
  rejects statements, declarations and commands.
