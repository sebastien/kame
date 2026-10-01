# Standard Library

## Purpose

The standard library provides ordinary operations over materialized values.
Reactive lifting, dependency tracking, authorization, and stream scheduling are
provided by the evaluator, not reimplemented by every operation.

The initial library covers operations needed by practical build files and the
legacy `examples/core-make.example.lm`. Breadth beyond this specification is
deferred.

## Operation Contract

Each operation declares:

- Public name.
- Named call function and context pointer.
- Arity.
- Capability requirements.
- Stable implementation version.

An operation receives evaluated arguments and an evaluation context. It returns
a value, a host request, or a structured diagnostic. Returned owned values use
the run allocator unless explicitly transferred to the engine.

Pure operations must not read clocks, environment, global mutable state, files,
or processes. Effectful operations register dependencies and effects through
the context.

## General Operations

The initial general operations are:

| Name | Contract |
| --- | --- |
| `not` | Boolean negation using language truth rules |
| `bool` | Convert to language truth value |
| `str` | Convert scalar to text; encode lists/records canonically |
| `count` | Length of string code points, bytes, list, or record |
| `first` | First list item or nil |
| `nth` | Indexed list or string item with negative indexes |
| `apply` | Call a function with a list of arguments |
| `list` | Return arguments as a list |

`str` uses canonical JSON-like text for lists and records with stable record key
order; it is distinct from the Kame value display of `005-evaluation.md`. Bytes
require explicit text or hexadecimal conversion and are not silently decoded.

## Comparison Operations

Comparison operations compare materialized scalars. The spelled names are
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

`eq` supports nil, boolean, number, and string. Values of different kinds are
unequal rather than an error; `:nil` and `:false` are distinct, and a number is
never equal to a string. Number equality and ordering are numeric across integer
and float; string equality is exact byte equality and string ordering compares
UTF-8 bytes. Lists, records, bytes, and patterns are `EXPR_INVALID` and must be
converted or compared explicitly. Ordering (`lt`, `gt`, `gte`, `lte`) accepts
only numbers and strings; a mixed-kind or non-scalar argument is
`EXPR_INVALID`. Boolean negation remains the general `not` operation, and
`ne`/`!=` is equality negation, not a synonym for `not`.

## Collection Operations

The initial collection operations are:

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

`filter` and `filter-out` require a callable predicate in the new language; the
legacy equality-value shorthand is deferred.

`unique` initially supports nil, boolean, number, and string values. Sorting
mixed kinds is invalid.

## Text Operations

The initial text operations are:

| Name | Contract |
| --- | --- |
| `join` | Join a list of scalar strings with a separator |
| `split` | Split text by a literal separator |
| `strip` | Trim Unicode whitespace |
| `includes?` | Literal substring membership |
| `starts?` | Literal prefix test |
| `ends?` | Literal suffix test |
| `replace` | Replace literal occurrences, or match and expand pattern arguments per `014-patterns.md` |
| `uppercase` | Unicode uppercase conversion supported by Solod |
| `lowercase` | Unicode lowercase conversion supported by Solod |

Regular-expression operations are deferred.

### Pattern Replace

`replace` accepts two or three arguments. Two plain strings replace literal
occurrences and behave exactly as before. A match pattern as the first argument
selects anchored matching; the second argument is the constant or expansion
replacement. Two pattern arguments return a callable section of arity one, so
`map` and pipes apply it per item. A string subject produces the expanded
string or `:nil` on no match; a list subject maps in order and retains `:nil`
entries. Invalid pattern combinations and missing capture references are
`PAT_INVALID`. Full semantics are specified in `014-patterns.md`.

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
| `write` | `write` | Atomically write bytes or UTF-8 text |
| `exists?` | `read` | Return whether a path exists |
| `stat` | `read` | Return stable metadata record |
| `wildcard` | `read` | Return sorted matching filesystem paths |

`read`, `stat`, and `wildcard` register file or glob dependencies before
returning. `exists?` also registers a dependency so creation or removal can
invalidate its consumer.

Relative paths resolve against evaluation cwd. `wildcard` supports `*`, `?`,
character classes, and recursive `**`. Results are canonical cwd-relative paths
with `./` prefixes where possible, sorted by raw UTF-8 bytes.

`write` is immediate only in explicit expression execution. During build
rendering it becomes a deferred effect committed during execution. It is
invalid during planning.

## Build Effects

| Name | Contract |
| --- | --- |
| `out` | Defer values for stdout |
| `err` | Defer values for stderr |
| `yield` | Append bytes or text to one declarative file output |
| `nop` | Return its final argument, or nil |

During standalone evaluation, `out` and `err` both emit their arguments and
return their concatenated output (text unless any argument is bytes). `yield`
emits to the evaluation output context and returns nil. During rule rendering,
all three retain their nil result so an effect expression contributes no shell
command text; `yield` remains the declarative file-output effect.

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

Normal recipes are the primary shell interface. One explicit `shell` operation
is provided for expressions that need collected process results. It requires
`run`, submits a process through `003-posix-process.md`, and returns:

```kame
[status: 0 stdout: "..." stderr: "..."]
```

Its arguments are a script string and optional option record. Streaming process
events remain available through runtime execution, not as a list accumulated by
the operation. `shell` is allowed in explicit expression execution but is
invalid during planning and build rendering with `PHASE_INVALID`; recipes are the
only process interface for cacheable builds.

Tagged-template shell helpers and multiple aliases such as legacy `sh` and
`shellrun` are deferred.

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
Reading the entire environment at once is not supported initially.

## Acceptance Tests

- Every operation validates arity and value kinds without mutating arguments.
- Pure operations issue no host request and register no dependency.
- Collection callbacks preserve input order.
- The expressions on lines 18-41 of legacy `core-make.example.lm` parse and
  evaluate unchanged against a fixture source tree.
- Path operations handle root, dotfiles, trailing separators, and relative
  normalization without filesystem access.
- Wildcard results are sorted and recursive `**` crosses directories.
- Reads and globs register canonical dynamic dependencies.
- Denied read, write, run, or environment access performs no host action.
- Deferred `out`, `err`, `yield`, and `write` effects commit once after rerender.
- `shell` captures status and separate byte-bounded stdout/stderr.
- Pattern `replace` matches anchored, expands references, returns `:nil`
  without a match, and accepts a section through `map` and pipes.
- Operation tests using `mem.Tracker` leak no returned container or string.
- `eq`/`=` and `is`/`==` share one strict, kind-aware comparison; `ne`/`!=` is
  its negation and `not` remains boolean negation.
- `eq` treats values of different kinds as unequal; `:nil` and `:false` are
  distinct; numbers compare numerically across integer and float.
- `lt`/`gt`/`gte`/`lte` order numbers and strings and reject mixed-kind or
  non-scalar arguments with `EXPR_INVALID`.
