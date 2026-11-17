---
revision: 2
---

# Direct Kash and Explicit Value Boundaries

## Purpose and precedence

Kash is a typed process language, not template-generated shell text. A Kash
program has the same parsing and evaluation semantics whether authored as a
`.kash` source, a rule body, a service body, a block-bodied `def`, or a rule
constructed in expression position.

This specification refines `004-language.md`, `005-evaluation.md`,
`006-runtime.md`, `007-library.md`, `008-cache.md`, `016-templates.md`,
`017-kash.md`, `018-source-style.md`, `023-wildcard-sources.md`,
`024-managed-services.md`, `025-generated-declarations.md`, and
`037-build-inspection.md` within the language defined by
`055-unified-kame-kash-language.md`. It defines direct Kash bodies, the value
boundaries inside Kash, executable tracking, scalar text and JSON operations,
interpolated paths, wildcard kinds, and action naming.
`044-value-producing-rule-bodies.md` defines content publication by expression
bodies and Kash `return VALUE`. Foreign-shell recipes use the template contract
of `016-templates.md`.

## Direct bodies

A rule, task or service block body is a Kash action block unless its resolved
`shell` names an external shell (`055` §10.1, §10.2). The body is parsed as
Kash directly; it is never rendered as a document template first.
Interpreter settings keep their pure resolution contract. Because `shell` can be
computed, source loading may retain a body with authored spans until the
interpreter is resolved; it never selects a parser by guessing from body text.

```kame
task authenticate {mode=sso}
	if mode == "sso"
		aws sso login
	else
		printf "Using caller-supplied credentials.\n"
```

The entire selected Kash body is parsed before any of its statements execute.
Syntax errors in unselected runtime branches remain errors; their expressions,
tool lookups, reads, and actions are not evaluated merely by parsing them.
Rules constructed in expression position and registered with `generate` carry
their authored bodies and receive the same direct parsing. Generation still
cannot perform host requests beyond tracked discovery (`025`).

Kash body scope includes the lexical scope of the declaration, module
definitions and imports, target arguments, target captures, and the rule
context. The rule context names `inputs`, `outputs`, `changed-inputs` and
`args` follow the contracts in `005-evaluation.md` and are ordinary expression
references; they are not template expansions. Definitions and branch scopes
keep ordinary lazy semantics.

Authored text, expression results, argv values, and captured output are never
implicitly reparsed as source. There is no template layer: `@` has no meaning
in Kash command words, and template directives such as `@if` are not Kash
statements. Document templates are explicit data passed to `render`. `eval TEXT`
parses one expression only (`055` §13); no operation compiles text into a Kash
program. Foreign-shell recipes use the template contract of `016-templates.md`.

## References, expressions, and commands

Kash has these value/process boundaries:

| Form | Meaning |
| --- | --- |
| `$name`, `${REFERENCE}` | Reference resolved in expression position (`055` §6), including the environment fallback for uppercase names |
| `(EXPR)` | Evaluate an expression, standalone or as an unquoted word fragment |
| `$(COMMAND)` | Run a Kash process graph and capture stdout |
| `$?(COMMAND)` | Run a Kash process graph or function invocation and return its outcome record |

The right-hand sides of `let`, `set` and `return`, and the conditions of `if`,
`elif` and `while`, are expressions; they use expression names, not `$`
wrappers. Parenthesized expressions are legal as typed command arguments,
redirection operands, and stage setup values. Within raw words they can adjoin
other unquoted or quoted fragments, as in
`biome --config-path=(js-biome-config ())`, `emit (name).json`,
`:cwd ./build/(variant) tool` and `tool > ./out/(name).txt`. Parentheses only
group, so `(name)` reads `name` and `(f ())` calls `f` without arguments. Mixed
words require scalar or nil fragments, nil reading as empty text, and produce
one value; only standalone unquoted argv substitutions keep list-splicing
behavior. This applies equally to argv, redirection paths and scalar stage
setup, with ordinary per-context validation. Typed `:env` records do not become
scalar words. Raw path-shaped words accept the same fragments; path atoms inside
expressions keep their distinct interpolation grammar. Kash double-quoted
literal parentheses do not start expression fragments. `017-kash.md` defines
word composition, literal escaping and formatting preservation; Kash delegates
fragments to the shared expression parser.

```kash
let package = get manager packages
printf "%s\n" (str "manager: " manager)
let revision = $(git rev-parse HEAD)

if manager == "auto"
	(fail! "Select a package manager explicitly")

match manager
	"deb" =>
		sudo apt-get install $package
	else =>
		printf "unsupported: %s\n" $manager
```

A statement consisting of a parenthesized expression evaluates that expression
and discards its value. Nil, records, and outcome records are valid results;
they are not treated as executables. Expression statements may span lines while
a delimiter is open and do not implicitly print their value. A command
statement whose head resolves to a lexical function calls it with its words as
arguments (`055` §7.2); the standard library is not consulted in command
position, so a library operation used as a statement is written as a
parenthesized expression or with its `std/NAME` head.

To execute a command whose executable is computed by an expression, bind it and
use a reference at the command head:

```kash
let command = get manager commands
$command --version
```

Parentheses never introduce shell subshells. Conditions use expression truth
(only `nil` and `false` are false); a command is tested through its outcome,
`if $?(test -f ./ready).ok`. `$(…)` and `$?(…)` contain one process graph or
command list, not blocks. Capture, recovery, async ownership, and cancellation
otherwise keep `017-kash.md` and `055` §8 semantics.

Unquoted standalone argument expressions or references splice flat lists only
when spread: `$...flags` or `$...{REFERENCE}`. Scalars contribute one argument
without splitting, globbing, or source parsing. Quoted or mixed words require
scalars. Executable heads require one nonempty string or path without NUL; a
list does not implicitly become a command prefix. `$...command` explicitly
spreads a flat argv list at command position: its first item supplies the
executable and the remaining items precede any following authored arguments.
The list must be nonempty. `$...` words are standalone unquoted argv words, not
scalar interpolation or source generation.

An unbraced `$` reference stops at `/`: `$dir/file.txt` is `$dir` followed by
`/file.txt`. Qualified references need braces: `${env/HOME}`, `${x/aws}`.

Kash quoted words use Kash reference and capture rules, not template
interpolation. Strings inside expressions keep the expression string contract,
including `@{EXPR}` interpolation; raw `'…'` strings and verbatim strings protect
template-looking data. Delimiter balancing accounts for strings, captures,
expressions, and interpolated path atoms; it is never textual parenthesis
matching.

## Automatic executable dependencies

Every reached Kash process stage resolves its executable head through the
existing tool resolver. This includes literal commands, reference-derived
commands, explicit paths, `x/NAME` heads, pipelines, captures, and background
work. An explicit `tool "aws"` call is not necessary to track `aws`:

```kash
aws sts get-caller-identity
let command = "aws"
$command sts get-caller-identity
let identity = $(aws sts get-caller-identity)
```

Bare executable names resolve through the environment store's `PATH` of the
current process-like unit (`055` §5.3) and the `--tool NAME=PATH` overrides.
`set env/PATH = …` therefore changes lookup for later stages in the same unit; a
per-command `:env` override changes only the child environment, not lookup.
Explicit paths resolve against the stage working directory. Resolution records
both tool identity and selected executable file identity before launch.
Missing or non-executable tools report `TOOL_MISSING` at the head. Internal
executable metadata checks do not widen user read capabilities; execution still
requires ordinary run authority.

The `x/NAME` namespace is the explicit way to name a resolved, tracked
executable, bypassing lexical bindings and builtins. At a command head it is
written `x/aws`; as a value it is `x/aws` in expressions and `${x/aws}` in
command words. Bare `aws` and `x/aws` at a command head resolve the same tool
identity and honor the same override. A value obtained through `x/NAME` keeps
its provenance; using it never loses the named override identity or resolves it
under a different `PATH`. `tool NAME` remains available for computed lookup in
expressions.

Only reached stages acquire runtime tool dependencies. Static inspection may
list literal heads in all branches as declared candidates, but distinguishes
them from observed dependencies and does not reject an unavailable tool in an
unreached branch. Dynamic heads are deferred unless their values can be resolved
with read-only authority. Tool-check commands may explicitly validate declared
candidates without running them.

Native hosts may run a resolved system utility in-process instead of
launching it, after resolution and tracking are complete, so recorded tool and
executable identities are unchanged. This applies only to single-stage graphs
whose executable is `/bin/NAME` or `/usr/bin/NAME` for `true`, `false`,
`echo`, `printf`, `test` and `[`, and only to argument forms whose output and
status are identical under GNU coreutils and BSD: `true`/`false` without
option-like operands; `echo` with an optional single leading `-n`, no other
option-like first operand and no backslashes; `printf` with `%s`/`%d`/`%i`
(flags `-`/`0`, width, precision for `%s`), `%%`, the escapes `\n \t \r \\ \"`,
plain decimal integer operands and no unused operands; `test`/`[` with at most
three operands using `!`, `-n -z -e -f -d -s -L -h -p -S -b -c -r -w -x`,
`= !=` and the decimal integer comparisons. Any other form, a program elsewhere
on `PATH`, or a multi-stage graph runs the executable. Redirections, captures,
descriptor inheritance and accepted exit statuses apply as for a child process.

Executable tracking does not reveal opaque subprocesses: `bun x tsc` tracks
Bun, not an inferred npm package/compiler graph. Wrappers, interpreters, and
their scripts require explicit inputs when those files affect freshness.

## Text and JSON operations

`str` accepts zero or more textual/scalar values and concatenates their textual
representations without separators. Strings and paths contribute their text;
numbers use deterministic decimal text, booleans use `true`/`false`, and nil
uses `nil`. Zero arguments return an empty string. Lists, records, bytes,
patterns, resources, and callables are rejected rather than silently serialized.
Binary decoding stays explicit through `text`; collection joining stays
explicit through `join`.

```kame
greeting = str "hello " name
counter = str "count=" 3
manifest = json {name: "example", enabled: true}
```

`json VALUE` accepts exactly one JSON-compatible value and encodes valid,
deterministic JSON. String scalars are quoted and escaped. Lists and records
are recursive; record key order keeps the stable contract. Nil is `null`,
booleans are JSON booleans, and finite numbers are JSON numbers. Nonfinite
numbers, bytes, callables, resource handles, and other non-JSON values are
rejected. There is no implicit action or output. `parse-json` is the inverse for
JSON-compatible value kinds. `fmt VALUE` (`055` §13) is the separate operation
that renders a value as expression source.

`cat` is not a Kame string operation. In Kash command position `cat` is the
ordinary tracked external executable. `concat` keeps collection semantics;
canonical CLI value display is separate from `str`, `json` and `fmt`.

## Interpolated paths and globs

A path atom (`055` §3.4) beginning with `/`, `./`, `../` or `~/` may contain
`@{EXPR}` segments, sharing the interpolation boundary of `016-templates.md`. An
atom beginning with an interpolation followed immediately by `/` is also a path
atom:

```kame
SOURCES = files @{PATH_SRC}/sh/**/*.sh
FILTERED_SOURCES = wildcard :+files :-symlinks @{PATH_SRC}/sh/**/*.sh
OUTPUT = ./build/@{component}/main.js
computed-output = @{output-root ()}/main.js
```

The parser keeps static fragments and expression children in one path AST
node. It does not terminate the path at whitespace inside an interpolation.
Children evaluate through the shared evaluator once per accepted evaluation,
with normal suspension and dependency retention. Their string or path values are
inserted as data without lexing them again. Nil, lists, records, bytes, NUL,
and other non-text values are invalid path segments.

Whitespace, quotes, dollar signs, parentheses, and template-looking bytes in a
value stay path data. Slashes in values contribute path components; `PATH_SRC`
may be a relative or absolute root. The materialized result must be an explicit
filesystem path; a leading value `src` requires an authored `./` if the result
otherwise lacks an explicit root. Relative paths resolve against the root
directory (`055` §9.2), not the including source's directory. Inside Kash
command words `@` has no meaning: `@{ROOT}/x` is literal text there, and the
same path is written `$ROOT/x` or `(ROOT)/x`. Bare `{capture}` groups keep their
pattern/capture meaning.

Path atoms have the kinds of `055` §9.1. An atom with authored glob syntax or
matcher groups is a `PathPattern`; one with only `{name}` groups is a
`PathTemplate`; otherwise it is a `Path`. A `PathPattern` is a value, not a file
list: discovery is explicit through `files`, `dirs` or `wildcard`, which return
tracked path lists (`055` §9.4, `023-wildcard-sources.md`). A `PathTemplate`
produces a path only through `bind TEMPLATE CAPTURES`. Kinds never convert
implicitly.

In an interpolated pattern, glob metacharacters in authored static fragments
keep pattern meaning. Metacharacters contributed by expression values are
literal filename data, not additional glob syntax, and this provenance survives
until matching. Authored groups take precedence over filesystem globs:
`@{ROOT}/{name:*}.@{EXT}` is a matcher pattern. Inserted braces, backslashes,
glob or regex punctuation and template-looking bytes stay literal pattern data
and cannot introduce groups or matching syntax. A path with no authored glob
syntax or groups is a `Path` even if an inserted value contains
metacharacters.

Each capture group, including its name and matcher or regex content, must be
fully authored in one static segment. Interpolation inside a group, such as
`./{@{NAME}:*}` or `./{name:@{MATCHER}}`, is a source error. `pattern TEXT` is
the explicit boundary for compiling constructed pattern text into a
`PathPattern`. Interpolated strings stay strings; raw Kash words keep their
separate grammar. Rule header inputs share expression path parsing, with
output captures supplied as template bindings (`055` §10.1); quoted header
items keep string interpolation semantics. A computed path must still satisfy
the explicit-root, text-only and no-NUL rules above.

`wildcard` accepts `PathPattern` values, complete pattern strings, and
already-expanded path lists, without interpreting list member text again, and
applies its kind flags to the combined matches. Structural path interpolation
applies to expression and Kash path atoms; discovery happens only through the
discovery operations, never implicitly in raw Kash command words.

## Signed symbol flags and wildcard kinds

Symbol grammar admits `:+NAME` and `:-NAME`, with `NAME` following ordinary
name syntax. Like other symbols, their values are their text without the colon:
`+files`, `-files`, and so on. They are ordinary values, not global side
effects or arbitrary keyword arguments. Operations declare which signed flags
they accept. ASTs and formatting preserve their signed spelling.

`wildcard` accepts one or more patterns or expanded path lists and these
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
values follow the same validation as authored flags. Since symbols materialize
as text, whole string arguments beginning with `+` or `-` are reserved for flag
validation; a filename or pattern with that initial character must use an
explicit path such as `./+files`.

Classification uses the entry itself, equivalent to `lstat`, not its target.
A link to a file or directory is a symlink, not also a file or directory;
matched dangling links can be selected. Recursive traversal does not follow
symlink directories. `:+symlinks` selects links but does not enable following
them. Paths explicitly rooted through symlink components still obey host path
resolution and normal filesystem capability policy.

Filtering changes emitted matches, not traversal through ordinary directories.
For example, `:+files` still discovers files under nested directories:

```kame
scripts = files @{PATH_SRC}/sh/**/*.sh
asset-dirs = dirs ./assets/*
assets = wildcard :+files :+symlinks ./assets/**
plain-assets = wildcard :-dir :-symlinks ./assets/**
```

`files PATTERNS...` and `dirs PATTERNS...` are fixed-kind shorthands for
`wildcard` with `:+files` and `:+dir`. They accept the same patterns and
expanded path lists, track the same membership, and reject kind flags. A union
or exclusion uses `wildcard`.

Results keep canonical paths, sorted duplicate-free union semantics, and
tracking for empty patterns. Traversal sources may be shared across filter sets,
but filtered consumer identity includes the normalized mask. Entry-kind and
symlink-existence changes invalidate consumers even when path names are
unchanged. Filtering respects read capabilities and propagates real metadata
failures rather than treating them as absent entries.

## Action names and phase policy

`!` marks an explicit action or control effect in a library operation name. It
is not a substitute for operation metadata, capability checks, phase checks, or
callback effect tracking. Every registered standard operation declares its
category, required capabilities, allowed phases, and dependency/publication
behavior. Registry validation and acceptance coverage account for all
operations, including template primitives, rather than inferring effects from
suffixes.

| Category | Canonical operations | Contract |
| --- | --- | --- |
| Pure computation/construction | `str`, `json`, `fmt`, collections, lexical paths, `capture`, `bind`, `pattern` | No host action merely from construction or evaluation |
| Tracked observations | `read`, `exists?`, `stat`, `wildcard`, `files`, `dirs`, environment reads (`env/NAME`), `tool`, `render`, `await` | Keep dependency/phase contracts; `render` can read templates; `await` joins already-started work |
| File/output actions | `write!`, `out!`, `err!` | Mutation/publication/output under existing authority and publication contracts |
| Process actions | `run!`, `pipe!`, `sh!`, `shell-template!` | Keep existing process grants, results, ownership, and failure semantics |
| Control effect | `fail!` | Raise an authored diagnostic and abort the current computation |
| Volatile observations | `now`, `monotonic` | Unsuffixed; planning/resolution restrictions apply |

`out!` and `err!` are also Kash command builtins (`055` §6). `shell-template!` keeps
the executing tagged-shell-template contract; it is not a pure constructor.
`await` does not launch a new process but keeps joined work's failure and
cancellation semantics. `cat` is not a library operation.

Effectful user callables should use `!`, but effects propagate through arbitrary
callable names, `map`, `reduce`, lazy definitions, and Kash `def` functions
regardless of spelling. Pure functions can still fail validation without
carrying `!`.

`fail!` is usable in pure validation, declaration registration, planning, and
runtime evaluation. It requires no host authority and is not deferred to
publication. An unselected branch containing `fail!` does not fail. Other
actions keep their phase restrictions: a suffix grants no permission to execute
processes or publish files during read-only inspection.

Kash commands, `$(…)` and `$?(…)` are process actions through syntax; no `!` is
added to external executable names. Calling a Kash `def` function runs its body
under the caller's phase and authority.

## Inspection, freshness, and publication

Direct Kash ASTs keep authored spans across nested expressions, word fragments,
and captures. Diagnostics never point into serialized argument text. Static
parse and format perform no host requests or actions.

Planning and dry-run do not launch processes, emit authored output effects,
mutate files, or evaluate a diagnostic merely because it occurs in an unreached
runtime branch. Read-only-resolvable definitions and tool candidates may be
inspected. Conditions dependent on process results or runtime-only facts produce
explicit deferred boundaries, not fabricated values that select a branch.
Ordinary capability and validation failures are not hidden.

Recipe identity covers interpreter selection, authored Kash structure, reached
helper definitions, configuration and target arguments read, file and glob
facts, environment reads, and resolved executable identities.
Runtime-discovered dependencies remain available for persisted freshness and
watch invalidation. Typed argv does not replace opaque process environment
tracking.

Effects and completed operands survive suspension without replay. File effects
keep atomic publication and failure cleanup. Async graphs belong to their
rule, service or call invocation and are joined or cancelled under the existing
process and service contracts. There is no template or evaluation pass during
publication or retry.

## Acceptance criteria

- `.kash` sources, `def` functions, authored and generated rules, and services
  interpret equivalent source with the same typed semantics.
- Expression conditions and `fail!` in unselected branches need no escaping;
  only reached branches run or acquire runtime dependencies. Commands are tested
  through `$?(…)` outcomes.
- Kash bodies are never template-expanded; `@` in command words is literal
  text; foreign-shell templates still interpolate `@{…}` and pass `$` through.
- Expression statements accept nil, record and outcome results without executing
  a result or printing an unsolicited value; library operations are not resolved
  in command position.
- Multiple arguments, spaces, newlines, quotes, `$`, `@{…}`, and `$(…)` in
  runtime data survive without serialization helpers or source injection.
- Mixed Kash expression fragments preserve one scalar argument, authored spans,
  retained operands, phase/capability checks and dependencies. Explicit `$...`
  list spreading, quoted literal parentheses and expression statements keep
  their distinct semantics across parse/format round trips.
- `(name)` reads a value and `(f ())` calls; `$dir/file` stops the reference at
  `/`; `${env/HOME}` and `${x/aws}` resolve their namespaces.
- Redirection paths and scalar setup values use the same composition as argv,
  including path-shaped raw words and leading expression operands. Validation
  keeps the destination's type/path constraints, prelaunch grants and file
  safety; typed environment records keep their non-conversion rules.
- Literal, dynamic, explicit-path, and `x/NAME` command heads track executable
  identity; overrides, the environment store's `PATH` after `set env/PATH`,
  per-command `:env` overrides, scoped authority, missing tools, and unreached
  branches behave consistently on native and WASM hosts.
- Changing a used executable invalidates freshness without making an unavailable
  unused branch tool an observed dependency.
- `str` concatenates scalar text and rejects collection serialization; `json`
  quotes scalars and round-trips nested JSON-compatible values.
- Interpolated paths stay one atom, preserve spans and dependencies, accept roots
  containing spaces, and distinguish literal dynamic glob metacharacters.
- Path interpolation uses `@{EXPR}` consistently with strings, templates and
  headers, `@{f ()}` for a zero-argument call. Bare capture groups stay authored
  patterns; `@{…}` is not a Kash word form.
- Glob path atoms are `PathPattern` values; `files`, `dirs` and `wildcard`
  return tracked lists with union/kind-filter behavior without re-globbing
  dynamic filename bytes; `{name}`-only atoms are `PathTemplate` values used
  with `bind`; raw Kash words stay literal.
- Filter tests cover positive/negative combinations, contradictory/unknown flags,
  duplicates, empty matches, symlink targets/dangling links, traversal, entry-kind
  changes, and shared-source filtered watch invalidation.
- Action names keep grants, phase restrictions, once-only publication,
  callback effects, async lifetime, cancellation, and failure cleanup.
- Parse/format round trips preserve semantics and are idempotent on both hosts,
  including multiline expressions and interpolated path atoms.
