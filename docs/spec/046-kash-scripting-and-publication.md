# Kash scripting and safe publication

## Status and purpose

Implemented contract for the additions below. Structured iteration and the pure
operations share native/WASM code. Publication is host-backed: native POSIX and
Node implement it; in-memory hosts and Windows reject it explicitly. Tests in
`tests/T046-01-kash-scripting.sh` exercise Linux native and WASM/Node behavior,
not certification of other platforms. This specification extends `017-kash.md` and
`038-direct-kash-and-values.md`, and complements `042-kash-streaming.md` and
`043-kame-finalization.md`.

The goal is to support ordinary file-processing scripts without generated shell
source: iterate over typed paths, parse repeated command-line options, replace
literal tokens, and publish completed files without overwriting another writer.
These are separate contracts, not an implicit filesystem transaction.

Existing support remains valid:

- Kash has `if`/`elif`/`else` and `match` statements.
- `(map ([file] (run! "checker" file)) files)` performs ordered per-item effects
  and stops on failure. An authored Kash callable supports multi-command bodies.
- Streaming `|(each)` receives records on stdin and prepares argv once. It is
  not a list loop with a per-item argument binding.
- The portable regex grammar explicitly rejects lookaround. Backslash quotes
  the next byte, so `\w` is not a word-character class. `resub` is anchored
  pattern substitution, not global regex search and replacement.
- Deferred cleanup does not provide atomic publication or output rollback.

## 1. Structured iteration

### Syntax

```kash
for source in fonts
	converter $source
	verifier $source

while probe --pending
	worker --one

while (pending? queue)
	(process-one! queue)
```

These are Kash block forms. Indentation, comments, source spans, and
whole-script parsing follow existing conditional blocks. `for` takes one binding
name, the keyword `in`, and one Kame expression. A bare reference such as `fonts`
uses expression grammar here, not command-word grammar. `while` takes the same
condition forms as `if`: a command graph or a parenthesized Kame expression.
`$(...)` is not a condition form. Example command and operation names above are
illustrative application code, not new standard-library operations.

Loop headers are reserved statement forms rather than external command names.
An executable named `for` or `while` remains accessible through an explicit path
or a computed command head. No migration rewrites are implicit.

### List iteration

The iterable is evaluated once per reached loop invocation, including across
suspension. It must produce a list; nil, strings, and other values are errors,
not implicit lists. An empty list executes no body. Items retain their types;
ordinary command argument validation still applies when an item is used in argv.

Items are visited sequentially in list order. Each iteration creates a fresh
lexical scope containing its item binding and lazy body-local definitions.
The binding follows existing definition-name validation, including reserved
names. Body definitions cannot redeclare the item binding. An item binding may
shadow an outer name; it does not alter that outer binding. Loop-local names
never escape, and nested loops retain their enclosing lexical scopes.

The loop does not collect body results and returns nil. Failure stops later
statements and iterations unless existing recovery explicitly handles it.

### Conditional iteration and lazy values

`while` evaluates its condition before each iteration and stops before the body
when false. Expression truth follows Kame: only nil and false are false.
Command conditions use the existing `if` success/failure classification;
capability denial, cancellation, and fatal execution failures are not false.

Every condition visit is a fresh evaluation occurrence: command conditions launch
fresh work, and function applications are invoked again. A resumed condition
continues its current occurrence instead of launching it twice. Every true
condition creates a fresh body scope.

This does not make definitions mutable or invalidate memoized outer bindings:

```kash
# The definition is lazy, not an automatically refreshed probe.
pending = $(probe --pending)
```

Repeated lookup of `pending` is not repeated process execution. Likewise,
re-evaluating a filesystem expression does not promise bypassing dependency or
host observation caches. For a live external condition, use a command condition
or an application whose operation explicitly provides fresh runtime observations.
This specification adds no general cache-invalidation operation.

### Ownership, effects, and limits

Loop evaluation retains the iteration index, current condition/body position,
scopes, completed values, and effects across suspension. Completed process or
file effects must not replay merely because the evaluator resumes.

Loops retain normal grants, read tracking, execution-phase restrictions, dry-run
behavior, deadlines, and cancellation. Dry-run must not execute a probe to discover
an iteration count. Parsing and formatting execute neither headers nor bodies.

`defer` continues to register with the enclosing Kash invocation, in execution
order, for LIFO cleanup. Iteration does not create a new finalization lifetime.
Use an explicitly called Kash callable or `finally!` when cleanup must finish
before the next item. Deferred actions retain their iteration's lexical scope.

Async graphs remain invocation-owned. Sequential iteration orders statement
execution; an explicitly asynchronous body command may overlap later iterations.
Neither loop is an implicit join or concurrency limiter. Long loops can therefore
accumulate deferred actions or explicitly started jobs; they are not advertised
as constant-memory streaming operators. Cancellation must be checked even in
loops whose expressions never suspend.

Initial scope excludes assignment, `break`, `continue`, loop `else`, index
bindings, implicit parallelism, and destructuring.
Eager immutable bindings, loop transfers, and incremental record iteration are
defined separately in [practical scripting](048-kash-practical-scripting.md).

## 2. Pure command-line option parsing

### API

```kame
(parse-args SCHEMA ARGV)
```

The operation is pure, requires no capability, and is available to all expression
surfaces. It does not read process arguments or the environment implicitly.
`ARGV` is a list of strings, normally Kash's `args`.

```kash
schema = [
	fonts: [names: ["-f" "--fonts"] arity: :many repeat: :append]
	css: [names: ["-c" "--css"] arity: :many repeat: :append]
	check: [names: ["--check"] arity: :flag repeat: :replace]
]
options = (parse-args schema args)
fonts = options.options.fonts
stylesheets = options.options.css
```

Each schema field names one result field. Its descriptor requires `names`,
`arity`, and `repeat`. Unknown descriptor fields, empty/duplicate aliases,
aliases shared across fields, and invalid combinations report `EXPR_INVALID`.
Aliases are one ASCII alphanumeric short name (`-f`) or a long name beginning
with `--` and an ASCII letter, followed by ASCII letters, digits, or hyphens.
`--` itself is reserved.

Arity is `:flag` (no value), `:one` (one value), or `:many` (one or more values
per occurrence). Repeat policy is `:reject`, `:replace`, or `:append`; append is
invalid for flags. No abbreviations, bundled short options, implicit coercions,
defaults, required-option validation, or implicit environment reads are supplied.

The result is `[options: RECORD operands: LIST]`. All schema fields are present:
absent flags are false, absent value options are empty lists. Present flags are
true; value options always contain lists, including `:one`. Append preserves
occurrence and value order; replace retains only the latest occurrence's values;
reject fails on a second occurrence.

### Scanning rules

- Scan left to right without shell splitting, globbing, trimming, or reparsing.
- Recognized options terminate a preceding `:many` occurrence and begin another.
- Unrecognized dash-prefixed tokens are errors, not option values. A lone `-`
  is ordinary data. Empty strings are ordinary data.
- `:one` consumes exactly one following non-option token; `:many` consumes all
  following non-option tokens up to an option or `--`. Missing values are errors.
- Long options accept `--name=value`; the attached value may begin with `-`
  or be empty. For `:many`, subsequent non-option tokens are also consumed.
  Flags reject attached values. Short attached values are not supported.
- `--` ends option scanning; remaining tokens become operands. It does not
  satisfy a preceding option's missing value.
- Non-option tokens outside an option become operands. Scanning resumes at later
  options; there is no implicit stop at the first operand.

Consequently, operands immediately following a variadic option are consumed as
values unless separated with `--`. For dash-prefixed paths use `./-name` or an
attached long-option value. This ambiguity is documented rather than guessed.

Malformed user argv reports `ARGS_INVALID` with a zero-based argument index and
the relevant schema field when available. Diagnostics must not dump the argv
list or evaluated option values. The parser does not print help or terminate the
host process; scripts choose their own reporting and application validation.

## 3. Literal token substitution

### API and boundaries

```kame
(sub-token FROM TO TEXT)
```

All operands are strings and `FROM` must be nonempty. The result replaces every
eligible non-overlapping literal occurrence of `FROM` in the original `TEXT`.
Eligibility requires that each immediately adjacent Unicode scalar, when present,
is not a token character. Start/end of text satisfy the respective boundary.

Token characters are Unicode general categories Letter (`L`) and Number (`N`),
plus underscore and ASCII hyphen-minus. This intentionally targets Python's
default Unicode `(?<![\w-])LITERAL(?![\w-])` boundary behavior rather than ASCII
word classes or a full CSS identifier grammar. Implementations must share a
versioned Unicode classification table across native and WASM; changes to that
table are semantic changes requiring operation-cache invalidation.

Occurrence matching is exact and case-sensitive, without Unicode normalization.
The token itself is literal: punctuation and regex metacharacters need no escaping.
Scan left to right; after accepting a match advance past it, and after rejecting
a candidate continue searching without skipping other possible candidates.
Replacement text is literal and never rescanned.

Boundary characters are neither consumed nor rewritten. Thus replacing `Font`
with `New` transforms `Font,Font` into `New,New`, but leaves `OtherFont`,
`Font-alt`, and `Font_1` unchanged. No eligible matches returns the original text
unchanged. Wrong operand kinds and empty `FROM` report `EXPR_INVALID`.

The operation is pure and does not change regex grammar, `sub`, `resub`, or
anchored pattern semantics. It is textual substitution, not a CSS parser: matching
text in comments, strings, and URLs is not excluded. Mapping multiple replacement
pairs is explicitly sequential unless a future operation specifies simultaneous
replacement semantics.

## 4. Atomic no-clobber publication

### API and minimum guarantee

```kame
(publish! STAGED DESTINATION)
```

This is a runtime file operation for one completed staged regular file. The
caller must finish and join every writer before calling it. It is not an output
redirection, a deferred `write!`, or a multi-file transaction. It executes when
demanded, rejects planning/read-only computation/expansion phases, and must not
run during dry-run. Normal lazy-definition rules apply.

The initial operation supports local `file:` resources only. It requires read
and write authority for the staged path and write authority for the destination,
including the namespace changes performed during publication. Grant validation
must precede mutation and use the actual resolved resource identities.

Native `ProgramHost` providers implement `PublishFile(staged, destination)` and
return `host.PublishResult`. Providers without a safe primitive return
`FS_UNSUPPORTED`. The WASM ABI uses request kind 39 with JSON
`{staged,destination}`, completed with `{code,published,stagedRemoved}`. An empty
code requires both success flags to be true. Hosts must preserve a committed
outcome even if a later action fails. Custom callbacks are subject to the same
grants and must acknowledge the publication outcome before cancellation retires
their work.

On success, it returns `[published: :true stagedRemoved: :true]`. The destination
becomes visible with the completed staged contents at one atomic namespace
operation, and no existing destination entry is overwritten. Existing regular
files, directories, symlinks, and dangling symlinks all cause `FS_EXISTS`.
The staged entry must be a regular file, not a final-component symlink.
Parent directories must already exist; publication does not create them.

Implementations must use a host primitive that actually enforces no replacement;
`exists?` followed by ordinary rename is forbidden. Atomic no-replace rename or
an exclusive hard-link publication followed by staging unlink can satisfy this
contract for supported filesystems. There is no fallback to ordinary replacing
rename or copy-and-delete. Unsupported resources, cross-filesystem publication,
and hosts lacking a safe primitive fail explicitly with `FS_UNSUPPORTED`.

### Ownership and races

Staging should be a freshly created private workspace on the destination
filesystem. The caller owns the staged namespace and must exclude concurrent
mutation of the staged file or its entry through publication. Exclusive
destination creation does not make an attacker-controlled source stable.
Destination contents retain the staged file's metadata; this operation does not
provide a permissions, ownership, timestamp, or extended-attribute policy.

The operation guarantees no-clobber at its publication point. It does not prevent
another authorized writer from later removing or replacing the destination.

Stable directory handles and no-follow traversal are required to claim resistance
to malicious parent-directory substitution. A final-component `lstat` check alone
does not provide that guarantee. The initial contract assumes trusted parent
directories; hosts and documentation must state this limitation. It does not
constitute a filesystem sandbox for external commands.

### Failure and cleanup

Failures before publication leave the staged entry available and the destination
unchanged by this operation. After a successful publication, staging unlink or
other post-publication work may fail. Such failures report `FS_PUBLISHED` with
structured `published: :true` and `stagedRemoved` state, never an ordinary
pre-publication failure suggesting retry is safe. If cancellation arrives during
the publication step, the host must reconcile its outcome before reporting it;
it must not report definitely unpublished when publication may have occurred.

No rollback removes a published destination. Cleanup may remove only staging
resources still owned by the invocation; it must not unlink a shared destination
based on a prior existence check. Existing recipe output verification and output
ownership rules still apply. Publishing one file does not commit sibling outputs
or make invocation failure transactional.

Atomic visibility is not crash durability. The initial API makes no persistence
guarantee across power loss and adds no durability option. A future durable mode
must specify file and directory synchronization and post-publication sync errors.

### Input consistency is separate

No-clobber publication does not establish that original inputs were unchanged
during processing. Before/after metadata checks can detect some changes, not all;
cached `stat` observations are not fresh race checks, and a check immediately
before publication still leaves a race window.

Scripts needing stable processing bytes should process a private snapshot or a
stable opened object, with an explicitly documented snapshot/locking protocol.
An open handle stabilizes object identity, not bytes against in-place writers;
copying a concurrently modified source is not automatically a coherent snapshot.
Comparing an unrelated input and publishing an output cannot be advertised as
one atomic compare-and-publish transaction against arbitrary external writers.

## Acceptance and delivery

The features have independent contracts. Documentation and backend support claims
must remain tied to executable checks; parsing alone is not execution evidence.

Required checks include:

- Nested and empty `for`, invalid iterables, lexical shadowing, fresh body-local
  definitions, ordered effects, failure, and suspension without effect replay.
- Initially false and repeated command/expression `while` conditions, memoized
  outer bindings, fresh calls, deadline/cancellation in non-suspending bodies,
  invocation-owned async work, and iteration-specific deferred captures.
- Parse/format/AST coverage for loops in files, constructed callables, and direct
  recipes; malformed headers/indentation fail before any effect executes.
- Repeated mixed aliases, multiple values, replace/reject policies, attached
  values, empty values, spaces/newlines, missing values, unknown options, `--`,
  and variadic-option/operand boundaries. Verify diagnostic value redaction.
- Token replacement at both text boundaries, punctuation, shared delimiters,
  overlapping candidates, Unicode letters/numbers, underscores/hyphens, literal
  metacharacters, unchanged text, and non-recursive replacement output.
- Two synchronized competing publishers: exactly one succeeds, the loser reports
  conflict, and readers never observe partial published data. Cover existing
  entries including dangling symlinks, staged symlinks, cross-filesystem errors,
  denied grants, pre-publication failure, post-publication cleanup failure, and
  cancellation outcome reporting. Race tests must synchronize rather than sleep.
- Native and WASM hosts agree on supported behavior; unsupported publication
  primitives fail explicitly instead of weakening guarantees. Unicode tables,
  operation semantics, and relevant cache identities stay aligned.

Strong hostile-directory protection, durability, and input compare-and-publish
remain separate future contracts, not implied acceptance claims. Publication
failures expose structured outcome fields in the diagnostic's `cause` record.
In direct recipes, `write!` retains its existing deferred-output contract; use a
completed external writer for staging before an immediate `publish!`.
