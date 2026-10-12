# Direct Kash and Explicit Value Boundaries

## Purpose and precedence

Kash is a typed process language, not template-generated shell text. A Kash
program has the same parsing and evaluation semantics whether authored as a
standalone source, a rule recipe, a service recipe, or a constructed callable.

This specification refines `004-language.md`, `005-evaluation.md`,
`006-runtime.md`, `007-library.md`, `008-cache.md`, `016-templates.md`,
`017-kash.md`, `018-source-style.md`, `023-wildcard-sources.md`,
`024-managed-services.md`, `025-generated-declarations.md`, and
`037-build-inspection.md`. Its contracts replace implicit template expansion
of Kash recipes, Kash `@(...)` expression boundaries, implicit collection
serialization through `str`, and unsuffixed action-operation spellings in
those documents. External-shell recipe templating is unchanged.

## Direct recipe parsing

`SHELL = kash` and per-rule `[shell: kash]` select direct Kash interpretation.
The parser delegates the authored recipe body to Kash without first rendering
it as a document template. Interpreter settings retain their existing pure
resolution contract. Because selection can be computed, source loading may
retain a body with authored spans until the interpreter is resolved; it must
not select a parser by guessing from body text.

```kame
SHELL = kash

authenticate {mode=sso} :
	if (= mode "sso")
		aws sso login
	else
		printf "Using caller-supplied credentials.\n"
```

The entire selected Kash body is parsed before any of its statements execute.
Syntax errors in unselected runtime branches remain errors; their expressions,
tool lookups, reads, and actions are not evaluated merely by parsing them.
Generated recipe strings receive the same direct parsing when their resolved
interpreter is Kash. Generation still cannot perform host requests.

Kash recipe scope includes build definitions, target arguments, target captures,
and its rule input/output frame. Definitions and branch scopes retain ordinary
lazy semantics. Rule selectors continue to resolve against that frame when used
as expression atoms; they are not document-template expansions. The named context
aliases `inputs`, `outputs`, `inputs/changed`, and `args` follow the selector
contracts in `005-evaluation.md`.

Authored text, expression results, argv values, and captured output must never
be implicitly reparsed as source. There is no template layer to escape through:
`\@(...)`, template directives such as `@if`, and legacy `@(...)` boundaries
are invalid as Kash structural syntax. Diagnostics identify the authored span
and suggest the corresponding expression or Kash control statement.

Document templates remain explicit data passed to `render`. A caller that
deliberately constructs executable text can pass it to `(kash TEXT)`; this is
an explicit compilation boundary, not an implicit recipe transformation.
Ordinary argument data is never compiled. External-shell recipes still use
the template contract of `016-templates.md`.

## References, expressions, and commands

Kash has three value/process boundaries:

| Form | Meaning |
| --- | --- |
| `$reference` / `${reference}` | Look up a Kame binding; never implicitly read the environment |
| `(function arguments...)` | Evaluate an ordinary Kame application |
| `$(command arguments...)` | Run a Kash process graph and capture stdout |

Parenthesized expressions are legal as statements, typed command arguments,
definition values, setup values, redirection operands, and control operands.
Kash delegates them to the shared Kame expression parser. Expressions inside
the boundary use Kame names, not `$` wrappers:

```kash
package = (get packages manager)
printf "%s\n" (str "manager: " manager)
revision = $(git rev-parse HEAD)

if (= manager "auto")
	(fail! "Select a package manager explicitly")

match $manager
	case "deb"
		sudo apt-get install $package
```

A statement beginning with a parenthesized Kame expression evaluates that
expression and discards its returned value. Nil, records, and process-result
records are valid results; they are not treated as executables. Expression
statements may span lines and do not implicitly print their value.

To execute a command whose executable is computed by a Kame application, use a
reference at the command head:

```kash
command = (get commands manager)
$command --version
```

This avoids interpreting a statement-leading application ambiguously as either
a function call or an executable. Parentheses do not introduce shell subshells.
A parenthesized condition uses Kame truth; a command condition uses process
exit status. `$(...)` contains one process graph, not an arbitrary statement
sequence. Capture, recovery, async ownership, and cancellation otherwise retain
`017-kash.md` semantics.

Unquoted standalone argument expressions or references splice flat lists.
Scalars contribute one argument without splitting, globbing, or source parsing.
Quoted or mixed words require scalars. Executable heads require one nonempty
string/path without NUL; a list does not implicitly become a command prefix.

Kash quoted words use Kash reference and capture rules, not document
interpolation. Expression strings inside Kame expressions retain the ordinary
Kame string contract; verbatim strings protect template-looking data. Delimiter
balancing must account for strings, captures, expressions, and interpolated
path atoms, not textual parenthesis matching.

## Automatic executable dependencies

Every reached Kash process stage resolves its executable head through the
existing tool resolver. This includes literal commands, reference-derived
commands, explicit paths, pipelines, captures, and background work. An explicit
`(tool "aws")` call is not necessary to track `aws`:

```kash
aws sts get-caller-identity
command = "aws"
$command sts get-caller-identity
identity = $(aws sts get-caller-identity)
```

Bare names use the driver's startup PATH and `--tool NAME=PATH` overrides.
Explicit paths resolve against the stage working directory. Process-stage
environment PATH changes affect the child environment, not executable lookup.
Resolution records both tool identity and selected executable file identity
before launch. Missing/non-executable tools report `TOOL_MISSING` at the head.
Internal executable metadata checks do not widen user read capabilities;
execution still requires ordinary run authority.

The `x/NAME` namespace remains an explicit way to obtain a resolved, tracked
executable value. Kash accepts `$x/aws` as that namespace reference and
`${x/aws}` as its braced spelling. Bare `aws` and `$x/aws` at a command head
resolve the same tool identity and honor the same override. A value already
resolved through `x/NAME` retains its provenance; using it must not lose the
named override identity or resolve it under a different PATH. `(tool NAME)`
remains available for computed lookup outside command position, not as a
requirement for ordinary command tracking.

Only reached stages acquire runtime tool dependencies. Static inspection may
list literal heads in all branches as declared candidates, but must distinguish
them from observed dependencies and must not reject an unavailable tool in an
unreached branch. Dynamic heads are deferred unless their values can be resolved
with read-only authority. Tool-check commands may explicitly validate declared
candidates without running them.

Executable tracking does not reveal opaque subprocesses: `bun x tsc` tracks
Bun, not an inferred npm package/compiler graph. Wrappers, interpreters, and
their scripts require explicit inputs when those files affect freshness.

## Text and JSON operations

`str` accepts zero or more textual/scalar values and concatenates their textual
representations without separators. Strings and paths contribute their text;
numbers use deterministic decimal text, booleans use `true`/`false`, and nil
uses `nil`. Zero arguments return an empty string. Lists, records, bytes,
patterns, resources, and callables are rejected rather than silently serialized.
Binary decoding remains explicit through `text`; collection joining remains
explicit through `join`.

```kame
(str "hello " name)
(str "count=" 3)
(json [name: "example" enabled: :true])
```

`json VALUE` accepts exactly one JSON-compatible value and encodes valid,
deterministic JSON. String scalars are quoted and escaped. Lists and records
are recursive; record key order retains the existing stable contract. Nil is
`null`, booleans are JSON booleans, and finite numbers are JSON numbers.
Nonfinite numbers, bytes, callables, resource handles, and other non-JSON values
are rejected. There is no implicit action or output. `parse-json` remains the
inverse for JSON-compatible value kinds.

`cat` is not a Kame string operation. In Kash command position `cat` remains
the ordinary tracked external executable. `concat` retains collection semantics;
canonical CLI value display remains separate from both `str` and `json`.

## Interpolated paths and globs

An explicit path atom beginning with `/`, `./`, or `../` may contain
`@(EXPRESSION)` segments. An atom beginning with `@(EXPRESSION)` followed
immediately by a path separator is also an interpolated path atom:

```kame
SOURCES = @(PATH_SRC)/sh/**/*.sh
FILTERED_SOURCES = (wildcard :+files :-symlinks @(PATH_SRC)/sh/**/*.sh)
OUTPUT = (nop ./build/@(component)/main.js)
```

The parser retains static fragments and expression children in one path AST
node. It does not terminate the path at whitespace inside an interpolation.
Children evaluate through the shared evaluator once per accepted evaluation,
with normal suspension/dependency retention. Their string/path values are
inserted as data without lexing them again. Nil, lists, records, bytes, NUL,
and nontext values are invalid path segments.

Whitespace, quotes, dollar signs, parentheses, and template-looking bytes in a
value remain path data. Slashes in values contribute path components;
`PATH_SRC` may be a relative or absolute root. The materialized result must be
an explicit filesystem path; a leading value `src` requires an authored `./`
if the result otherwise lacks an explicit root. Relative paths use evaluation
cwd, not the including source's directory. This extension is path-atom syntax,
not a reintroduction of Kash's standalone `@(...)` expression boundary.

In a wildcard interpolated path, glob metacharacters in authored static
fragments retain pattern meaning. Metacharacters contributed by expression
values are literal filename data, not additional glob syntax. This provenance
must survive until matching. When evaluated as a Kame expression, an unquoted
path with authored glob syntax produces a tracked path list without requiring a
`wildcard` wrapper (`004-language.md`, `023-wildcard-sources.md`). A path with
no authored glob syntax remains a literal path even if an inserted value
contains metacharacters. Capture-pattern groups retain their distinct grammar.

A caller can deliberately supply a complete string pattern to `wildcard`; that
argument retains its ordinary pattern-string contract. `wildcard` also accepts
already-expanded path lists without interpreting their member text again, and
applies its kind flags to the combined matches. Rule headers retain their
separate template/capture grammar. Structural path interpolation applies to
expression and Kash path atoms; implicit glob evaluation applies to Kame
expressions, not raw Kash command words.

## Signed symbol flags and wildcard kinds

Symbol grammar admits `:+NAME` and `:-NAME`, with `NAME` following ordinary
name syntax. Like other nonboolean symbols, their values are their text without
the colon: `+files`, `-files`, and so on. They are ordinary values, not global
side effects or arbitrary keyword arguments. Operations declare which signed
flags they accept. ASTs and formatting preserve their signed spelling.

`wildcard` accepts one or more string patterns or expanded path lists and these
operation-wide flags:

| Flag | Meaning |
| --- | --- |
| `:+files` / `:-files` | Include / exclude regular-file entries |
| `:+dir` / `:-dir` | Include / exclude directory entries |
| `:+symlinks` / `:-symlinks` | Include / exclude symbolic-link entries |

With no positive flags, selection starts with every entry kind accepted by an
unfiltered wildcard, including other filesystem entry kinds. With positive
flags, selection starts with the union of positively named kinds. Negative
flags subtract named kinds. Flag position does not matter; duplicate identical
flags are idempotent. Opposite flags for the same kind and unknown signed flags
report `EXPR_INVALID`. A call without any pattern or path-list operand is
invalid. Flags are scalar operands, not reinterpreted list members. Computed flag
values follow the same validation as authored flags. Since ordinary symbols
materialize as text, whole argument values beginning with `+` or `-` are reserved
for flag validation; a filename/pattern with that initial character must use
an explicit path such as `./+files`, rather than the flag-shaped value `+files`.

Classification uses the entry itself, equivalent to `lstat`, not its target.
A link to a file or directory is a symlink, not also a file or directory;
matched dangling links can be selected. Recursive traversal does not follow
symlink directories. `:+symlinks` selects links but does not enable following
them. Paths explicitly rooted through symlink components still obey host path
resolution and normal filesystem capability policy.

Filtering changes emitted matches, not traversal through ordinary directories.
For example, `:+files` still discovers files under nested directories:

```kame
(wildcard :+files @(PATH_SRC)/sh/**/*.sh)
(wildcard :+files :+symlinks ./assets/**)
(wildcard :-dir :-symlinks ./assets/**)
```

Results retain canonical paths, sorted duplicate-free union semantics, and
tracking for empty patterns. Traversal sources may be shared across filter sets,
but filtered consumer identity includes the normalized mask. Entry-kind and
symlink-existence changes invalidate consumers even when path names are
unchanged. Filtering respects read capabilities and propagates real metadata
failures rather than treating them as absent entries.

## Action names and phase policy

`!` marks an explicit action or control effect. It is not a substitute for
operation metadata, capability checks, phase checks, or callback effect tracking.
Every registered standard operation must declare its category, required
capabilities, allowed phases, and dependency/publication behavior. Registry
validation and acceptance coverage must account for all operations, including
aliases and template primitives, rather than inferring effects from suffixes.

| Category | Canonical operations | Contract |
| --- | --- | --- |
| Pure computation/construction | `str`, `json`, collections, lexical paths, `kash` | No host action merely from construction |
| Tracked observations | `read`, `exists?`, `stat`, `wildcard`, `env`, `tool`, `render`, `await` | Retain dependency/phase contracts; render can read templates; await joins already-started work |
| File/output actions | `write!`, `yield!`, `out!`, `err!` | Mutation/publication/output under existing authority and publication contracts |
| Process actions | `run!`, `pipe!`, `shell!`, `sh!`, `shellrun!`, `shell-template!` | Preserve existing process grants, results, ownership, and failure semantics |
| Control effect | `fail!` | Raise an authored diagnostic and abort the current computation |
| Volatile observations | `now`, `monotonic` | Unsuffixed; existing planning/resolution restrictions still apply |

The old action names (`write`, `yield`, `out`, `err`, `run`, `pipe`, `shell`,
`sh`, `shellrun`, `shell-template`, `fail`) and expression concatenator `cat`
are not canonical aliases. They produce migration diagnostics when used as
library operations; external command names are unaffected. `shell-template!`
retains the existing executing tagged-shell-template contract, not a new pure
constructor. `await` does not launch a new process but retains joined work's
failure and cancellation semantics.

Effectful user callables should use `!`, but effects propagate through arbitrary
callable names, `map`, `reduce`, lazy definitions, and Kash callables regardless
of spelling. Pure functions can still fail validation without carrying `!`.

`fail!` is usable in pure validation, declaration registration, planning, and
runtime evaluation. It requires no host authority and is not deferred to
publication. An unselected branch containing `fail!` does not fail. Other
actions retain their phase restrictions: a suffix grants no permission to
execute processes or publish files during read-only inspection.

Kash commands and `$(...)` are process actions through syntax; no bang is added
to external executable names. Calling a constructed Kash callable executes its
program under the caller's phase and authority.

## Inspection, freshness, and publication

Direct Kash ASTs retain authored spans across nested expressions and captures.
Diagnostics do not point into serialized argument text. Static parse/format
performs no host requests or actions.

Planning and dry-run do not launch processes, emit authored output effects,
mutate files, or evaluate a diagnostic merely because it occurs in an unreached
runtime branch. Read-only-resolvable definitions and tool candidates may be
inspected. Conditions dependent on process results or runtime-only facts produce
explicit deferred boundaries, not fabricated values that select a branch.
Ordinary capability and validation failures are not hidden.

Recipe identity covers interpreter selection, authored Kash structure, reached
helper definitions, configuration/target arguments read, file/glob facts, and
resolved executable identities. Runtime-discovered dependencies remain available
for persisted freshness and watch invalidation. Typed argv does not replace
opaque process environment tracking.

Effects and completed operands survive suspension without replay. File effects
retain atomic publication and failure cleanup. Async graphs belong to their
recipe/service/call invocation and are joined or cancelled under the existing
process and service contracts. There is no extra template/eval pass during
publication or retry.

## Acceptance criteria

- Standalone Kash, callables, authored/generated rules, and services interpret
  equivalent source with the same typed semantics.
- Expression conditions and `fail!` in unselected branches need no escaping;
  only reached branches run or acquire runtime dependencies.
- Direct recipes reject template directives/legacy escaped boundaries with
  authored-span migration diagnostics; external-shell templates still work.
- Expression statements accept nil/record/process results without executing a
  result or printing an unsolicited final value.
- Multiple arguments, spaces, newlines, quotes, `$`, `@(...)`, and `{(...)}` in
  runtime data survive without serialization helpers or source injection.
- Literal, dynamic, explicit-path, and `x/NAME` command heads track executable
  identity; overrides, PATH, scoped authority, missing tools, and unreached
  branches behave consistently on native and WASM hosts.
- Changing a used executable invalidates freshness without making an unavailable
  unused branch tool an observed dependency.
- `str` concatenates scalar text and rejects collection serialization; `json`
  quotes scalars and round-trips nested JSON-compatible values.
- Interpolated paths remain one atom, preserve spans/dependencies, accept roots
  containing spaces, and distinguish literal dynamic glob metacharacters.
- Unquoted interpolated globs evaluate to tracked lists without a wrapper;
  explicit `wildcard` retains union/kind-filter behavior without re-globbing
  dynamic filename bytes, and raw Kash words remain literal.
- Filter tests cover positive/negative combinations, contradictory/unknown flags,
  duplicates, empty matches, symlink targets/dangling links, traversal, entry-kind
  changes, and shared-source filtered watch invalidation.
- Action renaming preserves grants, phase restrictions, once-only publication,
  callback effects, async lifetime, cancellation, and failure cleanup.
- Parse/format round trips preserve semantics and are idempotent on both hosts,
  including multiline expressions and interpolated path atoms.
