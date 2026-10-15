# Standard Library

## Purpose

`038-direct-kash-and-values.md` refines standard operation contracts and owns
canonical action names, text/JSON separation, and wildcard kind filters. Those
requirements take precedence over older operation spellings below.

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
`reduce`, and template application) explicitly propagate reached callable
behavior; their computation category does not declare those callbacks pure.
`render` also evaluates template expressions under the caller's authority and
phase. Processes and `await` implemented as syntax obey the same contracts even
though they are not ordinary registry calls.

An operation receives evaluated arguments and an evaluation context. It returns
a value, a host request, or a structured diagnostic. Returned owned values use
the run allocator unless explicitly transferred to the engine.

An explicitly specified staged operand is the exception: pattern `sub`/`replace`
retains its replacement expression for evaluation after matching, as defined in
`014-patterns.md` and `005-evaluation.md`. This does not make `replace` a reserved
special form or change ordinary literal-string replacement.

Pure operations must not read clocks, environment, global mutable state, files,
or processes. Effectful operations register dependencies and effects through
the context.

## General Operations

Public transformations use `(f PARAMS VALUE)`, with all configuration, including
optional operands, before the subject. Value pipes append their input, so direct
calls and `(VALUE | f PARAMS)` have identical meaning. This is a breaking contract:
operations do not detect or accept the former subject-first order. Comparisons
keep conventional `(gt LEFT RIGHT)` semantics; variadic combiners retain authored
operand order. Special forms and internal template primitives are separate.

Canonical changed signatures are `(nth INDEX VALUE)`, `(slice START [END] VALUE)`,
`(join SEPARATOR LIST)`, `(split SEPARATOR TEXT)`, `(includes? PART TEXT)`,
`(starts? PREFIX TEXT)`, `(ends? SUFFIX TEXT)`, `(reduce FUNCTION [INITIAL] LIST)`,
`(apply FUNCTION LIST)`, and `(relpath BASE TARGET)`. `map` and `flatmap` accept only
`(FUNCTION LIST)`; `reduce` callbacks still receive `(accumulator item)`.
`(sh! [OPTIONS] COMMAND)` also puts its subject last.

Migration: rename `set-field` to `set`, `regex-replace` to `resub`, and
`shell!`/`shellrun!` to `sh!`. The old names are removed and report migration
diagnostics; they are not aliases. Other operation names are unchanged.

The general operations are:

| Name | Contract |
| --- | --- |
| `not` | Boolean negation using language truth rules |
| `bool` | Convert to language truth value |
| `str` | Concatenate zero or more scalar textual values; reject implicit collection serialization |
| `json` | Encode exactly one JSON-compatible value, including quoted scalar strings |
| `count` | Length of string code points, bytes, list, or record |
| `get` | `(get KEY [DEFAULT] RECORD)`: computed string key; missing keys return nil or DEFAULT |
| `first` | First list item or nil |
| `nth` | Indexed list or string item with negative indexes |
| `apply` | Call a function with a list of arguments |
| `list` | Return arguments as a list |
| `items` | `(items RECORD)`: ordered `[key: STRING value: VALUE]` entries |
| `set` | `(set KEY VALUE RECORD)`: immutable field replacement/insertion |
| `merge` | `(merge RECORD...)`: shallow, right-biased immutable record merge |
| `record?` / `list?` / `string?` | Exact kind tests on one value, without serialization |
| `digest` | `(digest ALGORITHM BYTES-OR-STRING)`: raw SHA digest as lowercase hexadecimal |

`json` uses deterministic JSON for compatible scalars, lists and records with
stable record key order. `str` performs textual conversion/concatenation only;
both are distinct from the Kame value display of `005-evaluation.md`. Bytes
require explicit decoding and are not silently converted or JSON-encoded.

`get` uses byte-exact string keys and returns the selected value with its type
and callable ownership intact. An invalid record/key type is `EXPR_INVALID`.
`DEFAULT` is an ordinary evaluated argument, including when the key exists.
Records can replace families of computed configuration names:

```kame
modes = [debug: "-O0" release: "-O2"]
selected = "release"
flags = (get selected modes)
```

The operation is pure and works in definitions, input headers, and planning.

### Typed record traversal and updates

`items` returns a list in stored field order, not sorted or serialized order.
Each entry contains exactly `key` and `value`. Empty records yield an empty list;
`[]` remains a list and is not accepted as a record. `set` requires a string
key: replacement preserves the field's position; a new key appends. An empty key
is valid, and setting nil stores a present nil field rather than removing it.

`merge` processes records left to right. The last value for a repeated key wins,
while the key retains its first-seen position. The merge is shallow: a nested
record is replaced, not recursively combined. Zero arguments produce an empty
record. These operations do not mutate their operands and preserve all nested
value kinds, including bytes, resources and callable ownership. Invalid operand
types and records with duplicate keys produce `EXPR_INVALID`. They replace
JSON/template round trips:

```kame
settings = [version: "old" nested: [enabled: :true]]
updated = (set "version" "new" settings)
entries = (items updated)
combined = (merge settings [version: "new" extra: 3])
```

The three predicates return booleans for every value, including empty data, and
inspect only the outer kind. A pattern is not a string, bytes are not strings,
and a record/list containing callables remains a record/list. No JSON-prefix
test, truth conversion, or implicit parsing occurs. SDK `object?` and `array?`
wrappers can delegate to `record?` and `list?`; local serialization-based `items`,
`set`, and `string?` definitions should be removed to expose native calls.

### Raw digests

The algorithm is exactly `"sha1"`, `"sha256"`, or `"sha512"`; there is no default,
alias normalization, or platform-dependent selection. Unknown algorithms and
non-string algorithm arguments produce `EXPR_INVALID`. The input is bytes or
the exact UTF-8 bytes of a string, including NUL and trailing newlines. No
decoding, normalization, trimming, value framing, or build-fingerprint salt is
applied. Results have respectively 40, 64, or 128 lowercase hexadecimal digits.

`digest` is pure and identical on native and WASM hosts. Hashing a file composes
with a tracked read: `(digest "sha512" (read ./asset))`. The read retains its
ordinary capability, phase, and dependency rules; hashing does not bypass them.
Do not substitute resource/value/build fingerprints for these interoperable
digests. SHA-1 is only for legacy compatibility identifiers, not a
collision-resistant security claim or password derivation.

## Comparison Operations

Comparison operations compare materialized values. The spelled names are
canonical; the symbolic aliases are equivalent and parse as expression atoms per
`004-language.md`.

| Name | Alias | Contract |
| --- | --- | --- |
| `eq` | `=` | Strict, kind-aware equality |
| `is` | `==` | Identical to `eq` |
| `ne` | `!=` | Negation of `eq` |
| `lt` | `<` | Ascending order |
| `gt` | `>` | Descending order |
| `gte` | `>=` | Order at or after |
| `lte` | `<=` | Order at or before |

`eq`, `is`, and `ne` support nil, boolean, number, string, bytes, lists, and
records. `is` remains an equality synonym, not reference identity; no new `is?`
spelling is provided. The contract is recursive:

- Nil and booleans compare by value; `:nil` and `:false` are distinct.
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

Comparison never serializes or mutates the data. Replace textual workarounds
such as `(eq (str A) (str B))` with `(eq A B)` to preserve kinds and binary data.
Reordered records now compare equal, unlike their printed representations.
`ne`/`!=` negates successful equality; unsupported-value errors still propagate.
Boolean negation remains `not`.

Ordering (`lt`, `gt`, `gte`, `lte`) is unchanged: only numbers and strings are
accepted; mixed-kind or non-scalar arguments are `EXPR_INVALID`. Number ordering
uses the existing float64 numeric comparison across integer/float, rather than
the exact integer equality contract above; string ordering compares UTF-8 bytes. Bytes
and collections have equality but no ordering.

## Collection Operations

The collection operations are:

| Name | Contract |
| --- | --- |
| `map` | Apply a function to each list item |
| `flatmap` | Map and flatten one list level |
| `filter` | Keep items for which a predicate is true |
| `filter-out` | Remove items for which a predicate is true |
| `reduce` | Fold a list from left to right |
| `concat` | Concatenate lists or append scalar values |
| `slice` | Half-open list or string slice |
| `sorted` | Stable ascending scalar sort |
| `unique` | Retain the first occurrence of each scalar value |

Callbacks are lexical functions evaluated in item order for deterministic
effects and diagnostics. Their internally discovered host dependencies may run
in parallel, but the resulting list preserves input order.

`filter` and `filter-out` accept `(PREDICATE LIST)`,
or `(VALUE LIST)` as an equality shorthand. The shorthand retains or removes
items equal to `VALUE`, respectively, using the same recursive equality contract
as `eq`, including bytes, lists, and records. Unsupported values remain errors.

`unique` supports nil, boolean, number, and string values. Sorting
mixed kinds is invalid.

## Text Operations

The text operations are:

| Name | Contract |
| --- | --- |
| `join` | Join a list of scalar strings with a separator |
| `split` | Split text by a literal separator |
| `strip` | Trim Unicode whitespace |
| `includes?` | Literal substring membership |
| `starts?` | Literal prefix test |
| `ends?` | Literal suffix test |
| `sub` | Substitute literal occurrences, or evaluate a capture-scoped expansion while preserving nonmatches per `041-pattern-predicates-and-substitution.md` |
| `replace` | Deprecated alias for `sub`, with identical behavior |
| `match?` | Anchored boolean pattern matching, directly or as a unary predicate |
| `uppercase` | Unicode uppercase conversion supported by Solod |
| `lowercase` | Unicode lowercase conversion supported by Solod |
| `black`, `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`, `white` | Wrap one string in the matching ANSI foreground sequence |
| `bold`, `dim` | Wrap one string in the ANSI bold or dim sequence |

Terminal style operations are pure string functions. They always include their
ANSI start and reset sequences; they do not inspect whether stdout is a TTY.
Use them only when the resulting string is intended for a terminal. Canonical
value display and ordinary text conversion never add terminal escapes.

Portable regular-expression matching and replacement are specified in
`029-pattern-extensions.md`.

### Pattern Substitution

Per `041-pattern-predicates-and-substitution.md`, `sub` is canonical, `replace` is
a deprecated alias, nonmatching strings are preserved, and `match?` provides
boolean pattern predicates.

`sub` accepts two or three arguments. `(sub FROM TO TEXT)` with plain
strings replaces literal occurrences and retains ordinary eager evaluation.
Wrong literal operand types or a missing literal subject report `EXPR_INVALID`.
A match pattern as the first argument selects anchored matching. Its second
operand is a replacement expression evaluated only after a match, with named
captures bound locally; `./{name}`, `"@(name)"`, `name`, and `(uppercase name)`
can all use `{name:*}`. With two operands it returns a callable of arity one
that defers the replacement until applied by `map`, a pipe, or a direct call.
A string subject produces the replacement string or the original string on no
match; a list subject maps in order and preserves nonmatching entries. Invalid
pattern combinations and missing expansion-pattern capture references are
`PAT_INVALID`; unknown
ordinary names in a selected expression retain `REF_MISSING`. Full semantics
are specified in `014-patterns.md`.

## Path Operations

Path operations are lexical and use slash-separated Kame paths:

| Name | Contract |
| --- | --- |
| `basename` | Final path component |
| `dirname` | Path without final component |
| `splitext` | Two-item list of root and final extension |
| `ext` | Final extension |
| `joinpath` | Join and clean path components |
| `relpath` | Path relative to a base |
| `abspath` | Path resolved against evaluation cwd |

These operations do not access the filesystem and create no dependencies.
`splitext` treats a leading dot on a basename as part of the name unless another
dot follows.

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
| `wildcard` | `read` | Return the sorted, duplicate-free union of one or more filesystem patterns |

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

Relative paths resolve against evaluation cwd. `wildcard` supports `*`, `?`,
character classes, and recursive `**`. Results are canonical cwd-relative paths
with `./` prefixes where possible, sorted by raw UTF-8 bytes. String pattern
operands each retain their own glob dependency, including empty members.
For example, `(wildcard ./src/**/*.go ./glue/**/*.c ./glue/**/*.h)`
combines source extensions without shell discovery.

Unquoted path globs are themselves list-valued expressions (`004-language.md`).
`wildcard` also accepts flat lists of path strings as already-expanded members,
so `(wildcard ./journal/*)` remains valid and can be combined with quoted or
computed string patterns. Lists are unioned without globbing their member text
again; filenames containing metacharacters remain data. An empty list is valid
and contributes no members; nested lists and non-string members are
`EXPR_INVALID`. Originating glob dependencies remain tracked, and
entry-kind flags apply to the combined matches under
`038-direct-kash-and-values.md`. This changes neither `map`'s list requirement
nor the literal semantics of quoted strings outside `wildcard`.

Recursive discovery does not descend into directory symlinks encountered among
directory entries. An explicitly named directory symlink in the literal prefix
is traversed, so `./linked/**/*.js` can enumerate its target, while
`./**/*.js` does not discover files by descending through `./linked`. Results
retain the authored lexical path. This prevents recursive symlink cycles; name
linked roots explicitly rather than expanding recursive traversal implicitly.

`write!` is immediate only in explicit expression execution. During build
rendering it becomes a deferred effect committed during execution. It is
invalid during planning.

## Build Effects

| Name | Contract |
| --- | --- |
| `out!` | Defer values for stdout |
| `err!` | Defer values for stderr |
| `yield!` | Append bytes or text to one declarative file output |
| `nop` | Return its final argument, or nil |

During standalone evaluation, `out!` and `err!` both emit their arguments and
return their concatenated output (text unless any argument is bytes). `yield!`
emits to the evaluation output context and returns nil. During rule rendering,
all three retain their nil result so an effect expression contributes no shell
command text; `yield!` remains the declarative file-output effect.

Effect output is not scalar `str` conversion: it accepts raw bytes and text
conversion of nil, booleans, numbers, strings, patterns, lists and records.
Collection output uses the output encoder's JSON-like text, not a guarantee of
JSON compatibility or reparsable Kame display. Callables and resources are
rejected. Use `json` when a consumer requires JSON.

Effects record their source position. Evaluating or rerendering an effect does
not perform it. The runtime commits it once for the accepted generation.
Every build effect is invalid during planning and returns `PHASE_INVALID`.

## Shell Operations

Recipes may refer to an executable with `@(x/NAME)`. Kame resolves `NAME`
against the build driver's startup `PATH` (not the recipe's configured PATH)
only when the reference is reached while rendering a selected rule. Missing or
non-executable tools use `TOOL_MISSING` at the authored reference; tools in
unused rules never block planning or execution. The reference renders the
resolved executable path and records that file as a dynamic dependency of the
using rule. `kame do tools` reports the global tool set and resolved paths,
including an empty path for an unavailable tool. `kame do tools check TARGETS...`
validates tools in selected dependency plans without executing recipes.
Ordinary command names remain shell-resolved.

Normal recipes are the primary shell interface. One explicit `sh!` operation
is provided for expressions that need collected process results. It requires
`run`, submits a process through `003-posix-process.md`, and returns:

```kame
[status: 0 stdout: "..." stderr: "..."]
```

The signature is `(sh! [OPTIONS] COMMAND)`: COMMAND is a script string and
OPTIONS, when present, must be an empty record (for example `(parse-json "{}")`).
Nonempty options are rejected with `EXPR_INVALID` until options are supported.
Streaming process
events remain available through runtime execution, not as a list accumulated by
the operation. `sh!` is allowed in explicit expression execution but is
invalid during planning and direct build-template rendering with `PHASE_INVALID`.
Demanded lazy values and Kash recipe expressions evaluate in their runtime phase;
when bound to a target, their collected shell calls inherit its environment and
retain the invocation run policy. Declaration registration never launches them.

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
environment, and is invalid during planning and direct build-template
rendering. Dry-run submits no process and returns an empty string. Invalid
list types, non-string elements, or fragment/value count mismatch return
`EXPR_INVALID` without submitting a process.

## Clock Operations

Clock operations read the host clocks. They are effectful: they submit host
requests and are invalid during planning and resolving with `PHASE_INVALID`,
because a plan must not depend on the time at which it was computed. They are
allowed in explicit expression execution and in build rendering.

| Name | Contract |
| --- | --- |
| `now` | Wall-clock nanoseconds since the Unix epoch |
| `monotonic` | Monotonic-clock nanoseconds from an arbitrary origin |

Both return integer nanoseconds. `monotonic` never decreases and is unaffected
by wall-clock adjustments, so it measures durations; only differences between
two readings are meaningful. Neither requires a capability grant.

## Environment

An `env` operation requires the `env` capability and reads one named variable.
It registers the variable name and observed value as a dynamic dependency.
Reading the entire environment at once is not a supported operation.

## Tool configuration

`(tool NAME)` accepts one nonempty string and returns the resolved executable
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

- Every operation validates arity and value kinds without mutating arguments.
- Pure operations issue no host request and register no dependency.
- Collection callbacks preserve input order.
- `filter` and `filter-out` accept `(PREDICATE LIST)` and the `(VALUE LIST)`
  equality shorthand, including mixed-kind inequality and numeric int/float
  equality, binary equality, and recursive list/record equality; opaque values
  remain `EXPR_INVALID`, including behind an earlier mismatch.
- Composed path, collection and filesystem expressions parse and evaluate
  against a fixture source tree with tracked resource dependencies.
- Path operations handle root, dotfiles, trailing separators, and relative
  normalization without filesystem access.
- Wildcard results are sorted and recursive `**` crosses directories.
- Unquoted glob expressions and explicit `wildcard` calls agree for empty,
  singleton and multiple matches; unioning expanded lists never re-globs their
  filenames or loses their membership dependencies.
- Reads and globs register canonical dynamic dependencies.
- Denied read, write, run, or environment access performs no host action.
- Deferred `out!`, `err!`, `yield!`, and `write!` effects commit once after rerender.
- `sh!` captures status and separate byte-bounded stdout/stderr, enforces run
  capability and phase restrictions, and rejects nonempty options records.
- `shell-template!` requires one more trusted fragment than dynamic string
  values, quotes dynamic values safely under POSIX `sh`, preserves the
  collected result, and is verified against command-substitution injection on
  native and WASM hosts.
- Terminal style functions wrap strings with their documented ANSI sequences,
  compose using selective reset codes, reject nonstrings, and leave canonical
  value display unchanged.
- Pattern `sub` (and its deprecated `replace` alias) matches anchored, expands
  references, preserves the original string without a match, and accepts a
  callable through `map` and pipes. `resub` retains its separate
  nil-on-nonmatch contract in `029-pattern-extensions.md`.
- Pattern replacement expressions resolve captures as local names, skip all
  replacement dependencies/effects on nonmatches, and leave literal-string
  replacement unchanged.
- Operation tests using `mem.Tracker` leak no returned container or string.
- `eq`/`=` and `is`/`==` share one strict, kind-aware comparison; `ne`/`!=` is
  its negation and `not` remains boolean negation.
- `eq` treats values of different kinds as unequal; `:nil` and `:false` are
  distinct; numbers compare numerically across integer and float.
- `lt`/`gt`/`gte`/`lte` order numbers and strings and reject mixed-kind or
  non-scalar arguments with `EXPR_INVALID`.
