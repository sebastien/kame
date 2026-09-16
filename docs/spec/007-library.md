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
order. Bytes require explicit text or hexadecimal conversion and are not
silently decoded.

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
| `replace` | Replace all literal occurrences |
| `includes?` | Literal substring membership |
| `starts?` | Literal prefix test |
| `ends?` | Literal suffix test |
| `uppercase` | Unicode uppercase conversion supported by Solod |
| `lowercase` | Unicode lowercase conversion supported by Solod |

Regular-expression operations are deferred.

## Path Operations

Path operations are lexical and use slash-separated LittleMake paths:

| Name | Contract |
| --- | --- |
| `basename` | Final path component |
| `dirname` | Path without final component |
| `splitext` | Two-item list of root and final extension |
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

Effects record their source position. Evaluating or rerendering an effect does
not perform it. The runtime commits it once for the accepted generation.
Every build effect is invalid during planning and returns `PHASE_INVALID`.

## Shell Operations

Normal recipes are the primary shell interface. One explicit `shell` operation
is provided for expressions that need collected process results. It requires
`run`, submits a process through `003-posix-process.md`, and returns:

```littlemake
[status: 0 stdout: "..." stderr: "..."]
```

Its arguments are a script string and optional option record. Streaming process
events remain available through runtime execution, not as a list accumulated by
the operation. `shell` is allowed in explicit expression execution but is
invalid during planning and build rendering with `PHASE_INVALID`; recipes are the
only process interface for cacheable builds.

Tagged-template shell helpers and multiple aliases such as legacy `sh` and
`shellrun` are deferred.

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
- Operation tests using `mem.Tracker` leak no returned container or string.
