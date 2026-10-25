# Kash

## Purpose

`038-direct-kash-and-values.md` refines this contract: Kash recipes parse
directly, parenthesized Kame expressions replace `@(...)` boundaries, and every
reached command head resolves as a tracked tool. Its action names and syntax
take precedence over action spellings in the examples below.

`046-kash-scripting-and-publication.md` adds structured `for`/`while` blocks,
pure script option parsing and token substitution, and host-backed no-clobber
file publication, with explicit scope, replay, and filesystem safety contracts.

Kash is Kame's command and process language. It makes command-oriented programs
read like shell scripts while retaining Kame's typed values, lazy evaluation, and
explicit dependency model.

Kash does not introduce a second expression language. It adds statements,
commands, pipelines, redirections, and process-control forms around the existing
Kame expression language. A Kash implementation composes the Kame expression
parser and evaluator; it does not translate values into shell text and reparse
them.

This specification adds:

- Kash source files containing definitions and command statements.
- Shell-familiar value lookup and command substitution.
- An explicit syntax boundary for Kame expression evaluation.
- Block control statements for conditional and pattern dispatch.
- First-class process pipelines, redirections, asynchronous execution, and
  recovery.

## Relationship to Kame sources

Kame has three related source layers:

| Layer | Responsibility |
| --- | --- |
| `.km` value programs and Kame expressions | Values, references, functions, patterns, templates, and value transformations. |
| `.kmk` rule programs | Declarative definitions, targets, inputs, and recipes. |
| `.kash` process programs | Command statements and process orchestration, using Kame expressions as its computation layer. |

Kash is a new outer syntax, so it is not a literal grammar superset of a Kame
script. Raw command lines are not Kame expressions. It is, however, an
expression extension in the user-facing sense: every Kash value computation
delegates to the existing Kame expression grammar and evaluator rather than a
second value language.

Rule bodies selected for Kash are parsed directly without recipe-template
expansion. External-shell recipes retain their existing template behavior.

### Recipe interpreters and reusable scripts

`SHELL = kash` selects the built-in Kash constructor callable for all recipes.
A rule may override that selection with a metadata record:

```kame
SHELL = kash

build : ./input ; [env: [MODE: mode]]
	printf "%s\n" $mode

legacy : ; [shell: ["/bin/sh" "-c"]]
	printf '%s\n' "$MODE"
```

The `shell` value accepts the Kash constructor, an executable string (with
`-c` implied), or a nonempty list of interpreter argv strings. Per-rule shell
selection does not propagate to prerequisites. An absent `SHELL` uses the
invocation's configured shell, defaulting to `/bin/sh -c`. Bare `kash` as a
complete definition value denotes the constructor; quote it to obtain a string.

Kash recipes use **direct Kash parsing** under `038-direct-kash-and-values.md`.
Template metaprogramming is explicit through `render` and a constructed Kash
callable, not an implicit layer requiring escaped expression boundaries.
The entire authored script is parsed before any of its statements execute.
Statements share rule inputs, outputs, captures, build definitions and invocation
arguments. Each rule joins its asynchronous work before verifying file outputs
or publishing completion. Timeouts cover the whole recipe; retries start a fresh
script invocation. Interpreter identity is an execution dependency, as specified
in `006-runtime.md`. It participates in cache and file freshness only as that
dependency. Selecting Kash or a shell does not make the recipe depend on the
process environment merely by selecting an interpreter. Named Kame environment
reads remain selectively tracked. Once a recipe submits a process, it also
consumes the complete effective child environment after overrides, independently
of those named reads (`006-runtime.md`, `038-direct-kash-and-values.md`).

`(kash TEXT)` parses text without running commands and returns an immutable,
invocation-owned callable that retains its construction scope:

```kame
script = (kash """printf %s @_""")
result = (script "hello")
```

Verbatim strings preserve argument selectors for evaluation when the script is
called. Calls bind arguments through the normal Kame argument selectors, create fresh
script-local definitions and inherit the caller's execution policy, directory
and child environment. Commands stream their output; the call returns its last
statement value (nil for an empty script). Use `$(...)` inside Kash for captured
stdout. Constructed text is already script text and does not undergo an implicit
recipe-template expansion. These callables can be retained by lazy definitions;
ordinary transient closures remain prohibited across engine publication. CLI
displays use opaque `<kash-constructor>` and `<kash-script>` markers.


### Files and invocation

Kash files use canonical `.kash`, with `.ksh` accepted as an equivalent suffix.
Both are Kash sources, not KornShell or system-shell scripts. Execute a file
directly or through the unified runner:

```text
kame [OPTIONS] FILE.kash [INPUT...] [-- ARG...]
kame do run [OPTIONS] FILE.kash [INPUT...] [-- ARG...]
kame do run [OPTIONS] --lang kash -c TEXT [-- ARG...]
kame do run [OPTIONS] --lang kash - [-- ARG...]
```

The file forms also accept `.ksh` and may append other files or repeated `-c`
fragments as defined by `009-cli.md`. Parse all fragments before execution;
share top-level definitions, one engine, and one invocation policy. A trailing
`-c '(out! name)'` uses Kame syntax by default and can refer to a definition in
the Kash source. It is not raw Kash command text unless a forward-scoped
`--lang kash` explicitly selects that parser.

Arguments after `--` use Kame's normal
argument binding. Execution supports the existing directory, capability,
timeout, and diagnostic options. Sessions beginning with Kash have the same
default capability policy as build execution; Kash appended to a value session
inherits that session's more restrictive policy. Relative source filenames
resolve before any directory change. No named entry operands are accepted for
Kash fragments: run statements in source order. Direct
and explicit execution share the same parser, engine, process ownership, and
policy; they are not shell-launching shortcuts. `009-cli.md` owns dispatch and
option validation. The previously specified `do kash` command is replaced by
`do run`, as is the separate `do expr` execution path. Both removed command
names report `CMD_UNKNOWN` with migration guidance; neither remains an alias.
Async work belongs to the combined invocation: a source boundary does not join
it, later fragments may await shared handles, and normal session completion joins
outstanding graphs. Failure or cancellation stops the remaining session and
reaps owned processes. Appending another source does not implicitly export
branch-local definitions.

`kame do parse --lang kash` and `kame do fmt --lang kash` use the existing
file/stdin conventions. Native and WASM hosts must implement identical language
semantics. `.kash` and `.ksh` do not join build-file discovery or implicitly
select the recipe interpreter. Native and WASM runners implement these invocation forms.

## Source and statements

A Kash source is a sequence of comments, blank lines, definitions, statements,
and block control statements. `;` separates statements on a line. Definitions
have the same shape and lazy semantics as Kame definitions:

```kash
name = "John Smith"
(banner WHO) = (str "Hello, " WHO)
```

A non-definition statement is a Kash command expression, including a pipeline,
redirection, asynchronous command, or recovery form:

```kash
git status --short
build | tee ./build.log
watch-assets &
```

The Kame expression grammar named `EXPRESSION` in `004-language.md` remains the
sole grammar for values, applications, lists, records, lambdas, references,
patterns, and expression pipes. This specification extends that grammar with
one expression atom, `$(COMMAND)`, whose contents delegate to the Kash process
parser. Kash delegates ordinary definition operands to the resulting Kame
grammar. Kash's `$REFERENCE` forms wrap a Kame reference; they do not add an
alternate value grammar. Parser ownership remains visible in diagnostics.

A definition is recognized only when a valid name or function header is followed
by a standalone `=` separator. `echo revision=$revision` is a command, not a
definition. A definition right-hand side is one Kame expression, one Kash
substitution, or a `??` composition of those operands:

```text
VALUE = EXPRESSION | $REFERENCE | ${REFERENCE}
RHS = VALUE ("??" RHS)?
```

The expression parser owns each `EXPRESSION` operand, including command
substitution atoms; Kash owns their process contents, the other substitution
wrappers, and recovery separators. `$REFERENCE`, `${REFERENCE}`,
and infix `??` are not added to ordinary Kame expressions: use
`(str "Hello, " WHO)`, not `(str "Hello, " $WHO)`. Existing Kame template
expansions and expression-string interpolation retain their own rules. Functions
retain Kame parameter and lazy evaluation semantics. All definition surfaces
use expressions rather than word-list or recipe-template RHS syntax.

Definitions in a statement sequence are registered lazily before that sequence
runs. Duplicate names in one sequence are errors. Each selected control branch
has a child lexical scope; its definitions may shadow outer bindings but do not
escape. Unselected branches are never registered or evaluated. Statements run
in source order; an unhandled failure stops the sequence. Completed top-level
statements retain their demanded lazy values through the combined runner
invocation, including finalization. Releasing a statement handle must not cancel
and recompute an effectful definition at the next statement. Separate callable
invocations and iteration callbacks still execute their own fresh calls.

Newlines and unquoted `;` terminate ordinary statements outside nested syntax.
Embedded expressions may span lines using their existing delimiter rules.
Command substitutions may span lines and contain one synchronous process graph
or command list, but not a statement sequence or control block. Empty statements
are ignored. Outside quotes and nested syntax, `#` at the start of a word
introduces a comment through the end of the line. `//` is literal command text,
not a Kash comment.

## Expansion boundary

Kash has these explicit value/process boundaries:

| Form | Parser | Meaning |
| --- | --- | --- |
| `$REFERENCE` | Kame reference parser | Resolve and insert a value from the current Kame scope. |
| `${REFERENCE}` | Kame reference parser | Braced spelling of `$REFERENCE`. |
| `$...REFERENCE`, `$...{REFERENCE}` | Kame reference parser | Explicitly spread a flat argv list, including at command position. |
| `(EXPRESSION)` | Kame expression parser | Evaluate an ordinary parenthesized Kame expression, standalone or as an unquoted word fragment. |
| `$(COMMAND)` | Kash statement parser | Run a Kash command or pipeline and capture its standard output. |

### References

`$REFERENCE` is the convenient form for inserting an already-defined Kame value.
`REFERENCE` uses Kame's complete reference grammar, not a separate shell-variable
grammar. It therefore includes forms such as `$project.name`, `$files.0`, and
`$config.{host,port}` as well as `$name`.

`${REFERENCE}` has exactly the same evaluation semantics. It only establishes the
end of a reference when literal text follows; it is not a general expression form.

The unbraced form consumes the complete reference, including dot components and
name suffixes accepted by Kame. `$name.c` looks up component `c`; `${name}.c`
appends the literal suffix `.c`. A reference's own trailing `?` belongs to the
reference, not the command acceptance operator; use whitespace before the
operator or a braced reference to disambiguate.

A `$` lookup never falls back to the operating-system environment. `$PATH` looks
up the Kame binding `PATH`; it is an unknown reference when no such binding
exists. Environment access remains explicit through `env.`, for example
`$env.PATH` or `(nop env.PATH)`.

`env` is a reserved, lazy environment namespace in Kash. Accessing a named field
performs the corresponding Kame environment operation with its capability check
and dependency tracking; it does not eagerly read the whole environment. A
missing variable yields nil, and a denied read fails. Definitions, parameters,
and captures may not bind `env`. No other name has an environment fallback.

### Kame expressions

Parenthesized expressions delegate directly to the ordinary Kame expression
parser; there is no wrapper shorthand or second value grammar. Use `$value`
for a reference, `(nop [1 2])` for an inline list argument, and ordinary calls
or value pipes for transformations. Expressions use Kame names, not `$` wrappers:

```kash
emit (replace ".c" ".o" source)
report (map inspect sources)
biome --config-path=(js-biome-config)
emit prefix-(name)-suffix
```

An unquoted word may contain literal fragments, references, command captures and
parenthesized expressions in any combination. The complete parentheses are
owned by the Kame expression parser: `(js-biome-config)` is a zero-argument call,
not a wrapper around a reference. Whitespace, quotes, nested applications, value
pipes and command captures inside that expression do not end the command word.
After its closing delimiter, adjacent word fragments continue the same argument.
This is general word composition, not special handling of `--flag=` syntax.
An expression may begin a mixed argument, as in `emit (name).json`.

The same scalar-word composition applies to command heads/arguments, redirection
paths, `:cwd`, `:timeout` and individual environment setup values. It also applies
to path-shaped raw words: `./out/(name).txt` contains a call fragment. Inside a
delegated Kame expression, path atoms keep their own `@{REFERENCE}` / `@(call)`
interpolation grammar; raw-word fragments do not change the Kame path grammar.
An expression in a setup/redirection operand is not a statement-leading
expression. Typed `:env` records and definition right-hand sides retain their
existing value grammar rather than becoming scalar word concatenation.

`$reference` and `${reference}` remain the reference forms. Braces introduce no
substitution boundary: `{reference}` is literal text, while `{(call)}` is literal
braces around an ordinary `(call)` fragment, not an alternative call wrapper.
A definition still requires a valid name or function header followed by a
standalone `=`: both `biome --config-path=(js-biome-config)` and
`echo name=(name)` are commands.

Values carry ordinary Kame dependencies, capabilities, failures, and authored
source spans. A statement-leading expression evaluates for effects and discards
the result; a leading `(expression)` does not become a command head by adjoining
a suffix. Use a reference at the command head for a computed executable. Mixed
words with a literal or reference prefix may supply computed executable paths,
subject to the ordinary executable validation and dependency tracking.

Legacy unquoted `@(...)`, escaped `\@(...)`, and template directives are invalid
Kash syntax, with no compatibility alias. Quoted interpolation-looking argument
data remains inert. Kame expression strings and document/path templates retain
their separate interpolation contracts. Explicit generated Kash uses `(kash TEXT)`,
optionally with `render`; argv values and captured output are never compiled.

### Command substitution

`$(COMMAND)` is command substitution. `COMMAND` is parsed with Kash command,
pipeline, and sequential-list syntax. Selected process graphs run in order, and
their output to the enclosing output sink becomes the substitution value:

```kash
revision = $(git rev-parse --short HEAD)
echo revision=$revision
```

It never means Kame expression evaluation. A failed command substitution fails
its enclosing expression unless an applicable recovery form handles it.

`$(COMMAND)` is also a Kame expression atom wherever an expression is accepted,
including applications, lists, records, lambdas, and expression-valued `.kmk`
definitions:

```kame
revision = $(git rev-parse --short HEAD)
banner = (str "Revision: " $(git rev-parse --short HEAD))
```

The outer expression parser retains a command-substitution node and delegates
the enclosed process expression to Kash. Embedded Kash may itself contain Kame
expressions and nested substitutions; each parser retains its original source
spans. Parsing never depends on grants, host availability, or execution phase.
Evaluation requires a context permitting process execution, as defined below.

Kame quoted strings do not gain implicit command interpolation: `"$(git status)"`
is literal text. Existing expression interpolation can explicitly evaluate a
substitution, for example `"Revision: @(str $(git rev-parse --short HEAD))"`.
Kash command-word double quotes still allow substitutions as specified below.
Opaque `.kmk` recipe text remains shell text: a raw recipe `$(...)` is interpreted
by the recipe's shell, not Kash. A substitution inside an explicit Kame recipe
expansion is owned by the expression parser instead.

Substitution captures stdout directed to its output sink, including explicit
`>&out`, and does not also forward it to the enclosing stdout. Explicit `>&err`
remains live and is excluded from capture; it does not redirect executable stderr.
Sequential capture lists share one cumulative byte limit and retain output from
failed operands when `||` selects a fallback. The result is a Kame string:
stdout must be valid UTF-8 and is preserved exactly, including trailing newlines
and an empty result. No newline stripping or whitespace splitting occurs.
Leading/trailing spaces and tabs, embedded line endings, CRLF, and newline-only
stdout retain every byte. Identifier/path conversion belongs to an explicit
consumer expression: `strip` removes all surrounding Unicode whitespace, while
an `ends?`/`slice` expression can remove exactly one known terminal LF. The
consumer must not infer a terminator that could be part of the payload. Any
newline-removal helper remains opt-in and cannot change substitution framing.
Binary capture is outside this specification.

The default capture limit is 1 MiB of stdout per substitution. `--capture-limit`
sets a positive byte limit for the invocation. Exceeding it cancels and reaps the
substitution graph and fails with `CAPTURE_LIMIT`; invalid UTF-8 fails with
`CAPTURE_ENCODING`. Truncated output is never returned as a successful value.
Async execution is invalid inside command substitution.

If the final stdout is redirected to a file, substitution captures an empty
string. Capture and redirection do not duplicate the stream.

## Command values and arguments

A raw Kash line is command syntax:

```kash
git status --short
```

It lowers conceptually to a Kame process operation:

```kame
(run! "git" "status" "--short")
```

Kash command arguments are typed value positions, not shell word-expansion
positions. A string expansion supplies one argument even when it contains
whitespace. Expansions are not re-split, implicitly globbed, or reparsed as
shell syntax. A raw `*.go` is therefore literal; a filesystem wildcard is an
explicit Kame value, such as `(wildcard ./src/**/*.go)`.

### Words, quoting, and argv conversion

Unquoted spaces and tabs separate words. Adjacent literal and substitution parts
form one word, as in `revision=$revision`, `${name}.c` and
`--config-path=(js-biome-config)`. Double quotes group
parts into one word and allow reference and command-capture substitutions. An
empty quoted word is one empty argument. Single quotes are reserved and rejected,
consistent with Kame expression syntax.

Outside quotes, backslash escapes the next non-newline character; backslash plus
newline continues the statement without inserting text. Inside double quotes,
`\"`, `\\`, `\n`, `\r`, `\t`, `\$`, and `\@` are supported; other escapes
are errors. Quoted or escaped operators are literal. Command quotes are Kash
word syntax, not expression strings: `{(...)}` has no interpolation meaning.
Inside Kame expressions, the existing Kame string and escape rules apply instead.

For literal at-sign data inside a word, quote the word: `tar "--mtime=@0"`.
The unquoted `--mtime=@0` is reserved syntax, not a template recipe selector.
Direct Kash has no implicit template pass; by contrast, shell quotes in an
external-shell template recipe do not protect `@0` from that recipe's template
parser, where `--mtime=\@0` supplies the literal argument. Canonical Kash
formatting may emit `"--mtime=\@0"`; reparsing preserves the same argument bytes.
This does not change valid standalone selectors or reference/call syntax.

Unescaped `(` outside double quotes starts a Kame expression fragment, including
in the middle of a word; it is not a shell subshell or literal punctuation.
Escape a literal parenthesis or quote its text. Parentheses inside Kash double
quotes remain literal: `"--config-path=(js-biome-config)"` does not call anything.
Reference and process-capture substitutions inside those quotes are unchanged.
An expression outside quotes may adjoin a quoted fragment in the same word.

A word consisting solely of an unquoted substitution preserves its typed value
until argv conversion:

| Value | argv contribution |
| --- | --- |
| String or pattern | One argument containing its text, even when empty. |
| Integer or float | One argument using Kame's canonical numeric spelling. |
| Boolean | One argument, `true` or `false`. |
| List | One argument per scalar item, in order; an empty list contributes none. |
| Nil, bytes, record, callable, resource, process/result, or nested list | Argument-type error. |

Quoted substitutions and words combining literal and substitution parts require
scalar values from the first three rows and produce exactly one argument. They
never splice lists: `$files` splices a flat list, while `"$files"` fails when
`files` is a list. A standalone `(modules)` may splice its flat list result,
but `--modules=(modules)` rejects that result rather than joining or splicing it.
An empty scalar in a mixed word contributes empty text; whitespace in a scalar
remains inside the one argument. A wildcard result supplies explicit argv items
without shell globbing. Quoted/escaped `@` and `$` are literal only when escaped
or not followed by substitution syntax; quotes do not disable valid substitutions.

Word fragments evaluate in authored order and retain completed values across
suspension, without replaying calls or effects. Result bytes are inert data:
spaces, operators, quotes, braces, parentheses and substitution-looking text
cannot create words, commands or new source. Nil and nonscalar mixed fragments
are argument-type errors; invalid values fail before launching the containing
process graph, under the existing capability and phase policy.

Parsing and formatting retain expression-fragment boundaries and authored spans.
Formatting must not quote an expression fragment into literal text, turn literal
parentheses into a call, or change standalone typed splicing into mixed-word
scalar conversion. Quoted/escaped literal fragments may be normalized only while
preserving those distinctions.

An explicit `$...REFERENCE` (or `$...{REFERENCE}`) requires a flat list and
contributes one argument per scalar item, preserving order and argument bytes.
It must be a standalone unquoted argv word; quoted/mixed words, definition
values, stage setup values, and redirection paths cannot use this form. Empty
lists contribute no arguments after an executable.

At command position, explicit spreading interprets the first list item as the
executable and the remaining items as its initial arguments:

```kash
CMD = ["git" "-C" "./my project"]
$...CMD status --short
```

The command list must be nonempty, and its first item must be a nonempty
string/path. An empty command list fails rather than promoting the next word to
executable. Subsequent authored words append arguments normally; spread values
are never reparsed as setup keys, operators, or Kash source. This also works in
pipelines, captures, command conditions, recovery, async commands, and recipes.

Without explicit spreading, the executable position requires one nonempty
string/path value, not a list even if it has one item. Ordinary `$files` list
splicing in argument position is unchanged. Executables, arguments, environment
entries, and filesystem paths must be free of NUL bytes. Processes execute argv
directly, using host PATH lookup for an executable without a path separator; no
shell interprets it.

Executable resolution uses the driver's startup PATH and tool overrides, not
stage environment PATH changes or a Kame binding named `PATH`, as specified by
`038-direct-kash-and-values.md`. Choosing a child environment is distinct from
reading an environment variable into a Kame value.

A command may use `run!` setup keys for process configuration:

```kame
(run! :cwd build-dir :timeout 10 :NODE_ENV "production" "npm" "test")
```

The keys configure the process; remaining expressions form its executable and
arguments.

In raw Kash, setup keys precede the executable and use `:KEY VALUE` pairs:

```kash
:cwd $build-dir :timeout (nop 10) :NODE_ENV production npm test
```

Except for `:env`, each setup value is one scalar word or substitution, never a list splice.
It may combine fragments under the same rules as argv words:

```kash
:cwd ./build/(variant) :MODE release-(variant) deploy
```

`:cwd` is a directory relative to the invocation directory, `:timeout` is a
positive number of seconds, and other keys matching `[A-Za-z_][A-Za-z0-9_]*`
are environment overrides. Duplicate keys are errors. The process environment
otherwise inherits the invocation's configured environment; explicit environment
lookup still requires `env` access. Quoted or escaped leading colons are literal.
After the executable, colon-prefixed words are ordinary arguments. Each pipeline
stage has independent setup keys. Reserved setup keys are not environment
overrides. Invalid paths, keys, values, and missing executables are errors.
The final scalar must still satisfy the setting's contract: for example a
computed `:timeout` must be a positive number of seconds and `:cwd` must be a
valid directory path. No list splicing or implicit record conversion occurs.

### Computed stage environments

`:env VALUE` accepts a typed record of environment assignments:

```kash
exports = [TOKEN: (env "DEPLOY_TOKEN") MODE: "release"]
:env $exports :MODE preview deploy
```

Expression-level process applications share the same contract, for example
`(run! :env exports "deploy")`. The record is evaluated only when that stage is
reached at execution time, under ordinary capabilities and phase checks. Parsing,
planning, dry-run, construction, and unselected branches do not fetch secrets or
execute its effects. Reading values through `env` still requires the relevant
environment grant; supplying a record does not grant read or run authority.

Keys must match `[A-Za-z_][A-Za-z0-9_]*`, and values must be strings without NUL.
Empty strings are valid; nil, numbers, booleans, collections, bytes, and callables
are not implicitly converted. An empty record is a no-op, not an empty child
environment; construct one with `(parse-json "{}")`. Validate the entire record
before consuming any assignments or launching the graph. Invalid values report
`EXPR_INVALID` at the authored setup boundary, never with evaluated record data.

The child starts with the inherited invocation/rule environment. Apply `:env`
record entries and ordinary `:KEY` overrides in authored setup order; the last
assignment to a name wins. Duplicate authored setup keys, including two `:env`
keys in one stage, remain syntax errors. Record keys such as `cwd` and `timeout`
are environment names, not process settings. Overrides belong only to that
stage: they do not change later statements, other pipeline stages, Kame's `env`
namespace, executable lookup, or parent/rule context.

Suspension retains evaluated assignments and their effects without replay;
dependency tracking retains the computations/observations used to obtain them
for freshness and watch invalidation. Environment payloads are sent to the host
for launch, but computed values are omitted from diagnostic messages and normal
human/JSON lifecycle traces. Do not embed evaluated records in error causes.
Authored source excerpts, explicit output, argv values, and child output are not
secret channels and are not automatically redacted; callers must not print or
place secrets in those channels.

The expression-level `run!` additionally accepts `:async` with a boolean value;
true returns a process handle instead of awaiting completion. This key is
reserved and rejected in raw stage setup: raw async execution uses terminal `&`
on the entire graph. `pipe!` constructs and executes its stages together rather
than awaiting each stage independently; stage-level async flags are invalid
within it.

## Pipelines and redirection

A process graph may be linear or use the groups, channels, framing and
multiplexors defined by [Kash streaming](042-kash-streaming.md). That specification
refines the linear-pipeline rules below; neither form is a shell subshell.

A raw command pipeline is a first-class Kash process expression:

```kash
generate | transform | publish
compile < sources.txt > output.log
compile >> output.log
```

It lowers conceptually to a Kame streaming graph:

```kame
(pipe! (run! "generate") (run! "transform") (run! "publish"))
```

Kash connects command stages through that graph rather than eagerly buffering
intermediate output. `<` supplies standard input, `>` replaces an output file,
and `>>` appends to it.

Redirections belong to an individual stage and accept one nonempty scalar path
word, including mixed expressions such as `tool > ./out/(name).txt` or
`tool < (input-name).txt`. Paths resolve relative to that stage's working directory.
Expression result spaces stay inside the single path; they do not create argv
or redirection operators. Empty paths, NUL, lists and nonscalar results are
errors. The same authored spans, retained operand evaluation and capability
checks as argv words apply. Each stage may have at most one stdin and one stdout
redirection. A pipeline edge and a redirection cannot both supply the same stream.
In a linear pipeline, `<` is therefore allowed only on the first stage and
`>`/`>>` only on the last. Conflicts are errors, not silently overridden.
`>` creates or truncates; `>>` creates or appends. Reserved `>&out` and `>&err`
route stdout to the enclosing presentation sinks as defined in
[Kash streaming](042-kash-streaming.md); neither redirects the executable's own
stderr. Redirecting executable stderr, arbitrary descriptor duplication, and
here-documents are not defined by this specification.

All stages start as one graph and communicate through bounded streaming pipes
with backpressure. Intermediate stdout is not collected. Every stage's stderr
is forwarded; final stdout is forwarded unless redirected or captured. Ordinary
commands inherit invocation stdin. If the source is stdin, commands without `<`
receive closed stdin rather than consuming source text. Command substitutions
without `<` also receive closed stdin.

Under the default fail-fast policy, the graph fails if any stage exits non-zero
or by signal, including SIGPIPE. Explicit owner-wide failure collection follows
[Kash streaming](042-kash-streaming.md); it does not accept failed exits or defer
fatal failures.
For a linear pipeline, aggregate status is the rightmost failing stage's status,
or zero if every stage succeeds; all per-stage statuses are retained. A signal
contributes `128 + signal` to aggregate exit status. Launch failures and timeouts are
failures, not successful zero exits. On launch failure, timeout, cancellation,
or capture-limit failure, all remaining stages are terminated and reaped. A
stage timeout terminates the whole graph; the invocation timeout additionally
bounds the complete source execution, including async joins.

Arguments and capability checks for all executable requests and redirection
paths are validated before starting any stage or creating/truncating files.
Input redirections record file dependencies; output redirections record write
effects. An ordinary path argument does not implicitly declare a dependency.
Capabilities do not sandbox access performed internally by a granted executable.
Launch-time failures may occur after output files have been opened; no filesystem
rollback or atomic-output guarantee is implied.

Kame's value pipe remains distinct and is used inside an expression boundary:

```kash
emit (sources | map render)
```

The pipe between raw command stages is a process stream; pipes inside embedded
Kame expressions have the value-pipe semantics specified by `004-language.md`.

## Process lifecycle and recovery

Appending `&` starts a process graph asynchronously and yields a `Process` handle:

```kash
watch-assets &
```

`&` applies to the entire process expression. It returns a `Process` handle once
the initial graph has started, not a completion result. The next statement may
then run. The invocation owns every async expression: on normal source
completion it joins outstanding work, and an unhandled async failure fails the
invocation. On source failure, interruption, or shutdown it cancels and reaps
outstanding graphs. There is no implicit detachment or daemonization.

`await` is the process operation that observes a handle's completion, returning
its `Result` or propagating an unhandled failure. Repeated awaits are repeat-safe.
Handles are invocation-local, cannot be serialized, and cannot be reused by
another invocation. For an async recovered expression, the handle covers the
fallback as well as the initial graph. Async syntax is not allowed in conditions
or value operands; terminal `&` is not a definition RHS. A handle can be bound
using an explicit Kame process operation instead:

```kash
job = (run! :async :true "watch-assets")
completion = (await job)
```

Both definitions remain lazy: defining `job` does not start it, and defining
`completion` does not await it until a reference demands its value. Awaiting
observes an async failure for invocation accounting; recovery around that await
can handle recoverable failures without the final invocation join reporting
them again. Unobserved failures remain invocation failures. Capability denial
and cancellation remain unhandleable by Kash recovery.

Commands normally fail on a non-zero exit status. `CMD?` accepts that non-zero
exit while retaining the command's diagnostic output; it does not silence stderr:

```kash
grep needle missing-file?
```

`A && B` and `A || B` form a synchronous, left-to-right command list at equal
precedence. `&&` selects its right operand after effective success; `||` selects
it only after an ordinary process exit or signal. Launch, timeout, capture,
capability, evaluation, and operator failures do not select `||`. Lists return
the final executed operand's outcome; a final unhandled process exit propagates
normally. Command conditions may contain lists, with an eligible final exit
meaning false. Lists are not process-group members, live producers, or direct
deferred commands. Compound lists cannot use terminal async `&`.

`CMD ? ELSE` evaluates `ELSE` when the command fails:

```kash
deploy ? echo "deployment failed; continuing"
```

Acceptance handles only non-zero process exits, including signal exits; it does
not accept launch, timeout, cancellation, argument, or capability errors.
Command recovery handles non-zero exits, launch failures, and timeouts, but not
capability denial, cancellation, or source/argument validation errors. A recovered
failure emits no additional unhandled-failure diagnostic; live stderr is
unchanged. A fallback failure propagates normally.

`LEFT ?? RIGHT` is value-level recovery. It evaluates `RIGHT` when `LEFT` is
`nil` or fails; otherwise it returns `LEFT`:

```kash
config = read-config ?? default-config
revision = $(git rev-parse --short HEAD) ?? "unknown"
```

These forms are deliberately distinct. `CMD?` accepts a process exit;
`??` selects a fallback value or computation. Neither changes Kame's existing
`?` special form, which handles unknown-reference recovery within a Kame
expression.

`??` is valid only in Kash value compositions, including definition RHSs; it is
not a raw-command operator or an operator inside Kame expressions. It handles ordinary
evaluation failures, including unknown references, host read failures, and
substitution exit/launch/timeout/capture failures, but never capability denial,
cancellation, or source/argument validation errors. Waiting for a host result is
not failure. No fallback dependency or effect occurs unless selected.

### Operator binding

From tightest to loosest, process syntax binds as follows:

1. Quoting, substitution, and word composition.
2. Stage redirections.
3. Pipeline `|`, in left-to-right stage order.
4. Terminal acceptance `?`, applying to the complete pipeline.
5. Recovery `? ELSE`, right-associative; `ELSE` is another process expression.
6. Sequential `&&` and `||`, equal precedence and left-associative.
7. Terminal async `&`, applying to one complete graph/recovery expression.
8. Statement separators `;` and newline.

Outside a reference, a terminal unquoted `?` is acceptance even when attached to
a word; quote or escape it for a literal filename suffix. `?` with a following
fallback must be whitespace-separated. `??` must be whitespace-separated between
value operands and is an error in raw commands. Operators inside quotes or
embedded Kame syntax are not process operators.

`&` must be followed by a statement terminator: `first & second` requires
`first &; second` or a newline. `A && B &` and `A || B &` are rejected before
effects; separate statements such as `A &; B && C` remain valid. Parentheses do
not introduce shell subshells; comma-separated process groups and stream-operator
binding follow [Kash streaming](042-kash-streaming.md).

## Deferred cleanup

`defer COMMAND` registers a process graph for cleanup. `defer (EXPRESSION)`
registers one parenthesized Kame expression. Neither runs the deferred action at
registration. The statement is parsed with its source before any execution, and
formatting retains the command/expression boundary.
Quote an external executable named `defer` at statement position; a lazy
definition named `defer` remains an ordinary definition.

```kash
created = $(mktemp -d ./workspace.XXXXXX)
directory = (if (ends? "\n" created) (slice 0 -1 created) created)
defer rm -rf $directory
defer (out! "finishing\n")
```

Registration occurs only in a reached runtime statement. Parse, format,
planning and dry-run do not evaluate deferred actions or their operands.
Commands evaluate and validate argv, redirection paths and stage setup at
registration, retaining completed operands across suspension. This can demand a
lazy definition or run an explicit capture needed to obtain an operand. It does
not open/truncate redirection destinations or launch the deferred graph. Typed
environments retain their ordinary secrecy, authority and validation boundaries.
Captured values are data, never generated source.

Expressions instead retain their AST, lexical scope and execution context and
evaluate the complete expression during cleanup. Branch-local bindings survive
until then. Ordinary laziness remains: an undemanded definition may first execute
during cleanup. Use an explicit demand or command operand when creation must
precede registration. Expression results are discarded, not interpreted as
executables or implicitly printed.

Cleanup belongs to the enclosing Kash invocation: a recipe, constructed callable,
or the top-level ordered runner session. Branch exit does not finalize it; nested
Kash callables have independent stacks. Actions execute once in reverse
registration order. Normal completion joins owned async work before cleanup;
failure/cancellation cancels and reaps that work before cleanup starts. A deferred
expression may start async work, but the action joins it before advancing.
Terminal `&` on a deferred command is invalid. Direct `? ELSE` command recovery
is also invalid in this form: defer a call to an authored Kash callable for
compound recovery. A direct `&&`/`||` command list is likewise invalid; a deferred
Kash helper may contain a synchronous list. Terminal exit acceptance `?` remains
supported. An action
cannot append to its closing stack; a nested callable may own a new stack.

Finalization uses a separate five-second budget per invocation stack, starting
after user processes have been reaped. Process timeouts are capped by the
remaining budget and any tighter inherited/stage limit. If the budget is
exhausted, report unrun actions rather than claiming successful cleanup. This is
a cooperative runtime/process deadline, not preemption of arbitrary blocking
custom host implementations. Cleanup retains original capabilities.

Try remaining actions after an action fails, while budget remains. If work
otherwise succeeded, cleanup failure fails the invocation. Otherwise preserve the
primary error/cancellation and report cleanup diagnostics as supplementary
context. `?` on a deferred command explicitly accepts only its ordinary supported
exit categories; it cannot suppress timeout, capability denial or cancellation.
SIGKILL, runtime crash, unavailable host services and forced second interruption
can prevent finalization. Remote effects are not rolled back.

`defer` attempts authored cleanup; it does not prove filesystem ownership. Only
remove freshly created private resources whose paths were retained safely. Never
infer ownership from a glob, shared directory or predictable `.tmp` suffix.
Deferred `write!` actions execute immediately, including in failed recipes, rather
than waiting for successful artifact publication. `yield!` is not a deferred
artifact-publication mechanism. Ordinary redirection is not atomic output
publication. Keep staged artifact publication and historical `do clean` ownership
separate.

Forwarded hosts must explicitly begin and pump finalization before freeing a
cancelled runtime. The shared `kame_wasm_finalization_tick` driver services both
Kame `finally!` and Kash `defer`, including cleanup request deadlines. The additive
`kame_wasm_finalize_begin` entry point is available for Kash-only hosts; ordinary
step and completion calls service its requests under original grants and a fresh
cleanup cancellation/deadline context. Free alone cannot execute host actions.

## Control statements

Kash adds block control statements around its commands. A block is introduced by
a keyword line and holds one or more statements indented deeper than the keyword.
Blocks nest, and dedenting to an existing enclosing level ends the inner block.
Block keywords are `if`, `elif`, `else`, `match`, and `case`; a block statement
is not `;`-separated.

Input indentation may use tabs or spaces, but a source must use one style;
spaces use a consistent positive unit. Each nested block is one unit deeper,
and a dedent must return to an existing level. Blank and comment lines do not
affect indentation. Empty blocks, orphan keywords, duplicate `else`, and branches
after `else` are errors. `elif`/`else` align with their `if`; `case`/`else` are
one level deeper than `match`, and their bodies one level deeper again. Keyword
headers cannot share a line with another statement.

`if` selects a branch. Its condition is either a raw command — evaluated for its
exit status, where zero is true and non-zero is false — or an explicit Kame
parenthesized expression, evaluated with Kame's truth rules. A command condition
does not emit a failure diagnostic for a non-zero status; that is an implicit
`CMD?` scoped to the condition. `$(...)` is not a valid condition, because every
non-nil string is true under Kame truth, so it would always be true.

A command condition may be a pipeline or sequential list with redirections, but
not async or explicit acceptance/graph-recovery syntax. The final eligible process
exit means false; launch, timeout, capability, or evaluation failures propagate
rather than being treated as false. Its stdout and stderr follow ordinary
forwarding/redirection rules. An expression condition uses
Kame truth: only nil and boolean false are false; even an empty string is true.
Conditions and match subjects are evaluated once per execution, including across
suspension and resumption for host requests.

```kash
if test -f ./build/app
	gcc -o build/app src/app.c
elif (nop options.release)
	gcc -O2 -o build/app src/app.c
else
	report "nothing to build"
```

`match` selects a branch by pattern, reusing the pattern clauses of the Kame
`match` form in `004-language.md`. Its subject is a value — a Kame expression or
`$(...)` — and each `case` names a match pattern. The first matching case runs
and its named captures bind as `$name` references within that arm; `else` is the
optional fallback.

```kash
match $target
	case ./src/{name:*}.c
		cc -c ${name}.c
	else
		report unknown
```

Only the selected branch runs, so an unselected branch's commands, dynamic
dependencies, and effects never occur. `if`/`elif`/`else` and `match`/`case`
delegate their tests and subjects to the Kame expression evaluator and their
bodies to Kash statements, so they carry normal Kame values, dependencies, and
diagnostics.

Each `case` header contains exactly one pattern expression accepted by Kame's
`match` form. Subject type rules and capture matching are unchanged. Captures
belong to the selected arm's scope; an arm definition conflicting with a capture
is an error. A match without a matching case or `else` returns nil and continues
the enclosing sequence. A control statement returns its selected branch's last
statement value, or nil when no branch is selected.

## Results and lowering

### Execution context

Kash and embedded command substitutions use the Kame evaluation context defined
in `005-evaluation.md`, not a second ambient context. Its execution components
are phase, capability grants, host services, working directory and process
environment, timeout and capture policy, execution ownership/cancellation, and
dependency/effect channels. Lexical scope determines what names mean; it is not
an authorization mechanism.

Embedded Kash inherits the caller's execution context and lexical scope. Calling
a function or demanding a lazy definition must not introduce implicit grants;
shared definition evaluation uses the owning program's explicitly configured
policy and cannot borrow broader grants from another execution. Unused lazy
definitions and unselected branches perform no authorization checks or effects.

Process execution requires both an evaluation phase permitting that effect and
the relevant run grant. Planning and resolution reject a demanded substitution
with `PHASE_INVALID`, even when a run grant exists. Missing grants produce
`CAP_DENIED`; an authorized but unavailable host service produces `HOST_FAIL`.
Neither parsing nor formatting executes a substitution.

Nested setup can change process configuration but cannot broaden capability
grants or relax caller limits. Paths are authorized after resolving the effective
working directory. Stage timeouts and capture limits remain bounded by caller
policy. Explicit environment lookups and redirections additionally require their
respective environment/read/write grants. Capability denial and cancellation
cannot be suppressed by Kash recovery.

A run grant authorizes direct executable requests, not implicit shell-text
evaluation. `$(git status)` runs argv directly. Launching a shell requires an
explicit shell executable request authorized by the caller's run policy; there
is no additional implicit shell capability or shell fallback.

### Process values and lowering

A command or pipeline returns a `Process`/`Result` value rather than an untyped
string. It carries process completion, status, streams, and related execution
information. `$(COMMAND)` is the explicit conversion point that captures
standard output as a value.

More precisely, a synchronous graph returns an immutable `Result`; async startup
returns a `Process` handle whose completion yields that result. A result exposes
aggregate `status`, per-stage `stages` (status, signal, and outcome), and stream
identity/capture metadata. Streams are not automatically converted to strings
or retained without a capture request. Results are not argv scalars. The concrete
core representation is implementation-defined, but these observable distinctions
are required and must not depend on the backend.

The synchronous record interface exposes `status`, `signal`, `stages`,
`stdoutCaptured`, and `stderrCaptured`. Each stage exposes `status`, `signal`,
and `outcome`, in prepared stage/node order (source order for a linear pipeline).
Streaming graph Results additionally expose the terminal `graph` metadata defined
by [Kash streaming](042-kash-streaming.md). Raw commands and expression-level
`run!`/`pipe!` stream output and set both capture flags to false; the result does
not contain retained stdout or stderr. A result may be inspected through ordinary
Kame references, but cannot be passed directly as an argv argument.

The Kash parser may own statement, pipeline, redirection, async, and recovery
AST nodes. Every embedded expression and reference must retain the normal Kame
AST node and source span. Lowering Kash nodes to `run!`, `pipe!`, and associated
process operations must preserve Kame's laziness, capability checks, dynamic
dependencies, and diagnostics. Block control statements lower to the Kame `if`
and `match` special forms: a command condition lowers to a process-status
predicate, a parenthesized condition to the Kame expression, a `match` subject to a
value, and each branch body to a nested Kash statement sequence.

Pipeline lowering is descriptive, not eager evaluation of independent `run!`
calls: the whole graph must be constructed before any stage starts. Execution
state survives evaluator suspension without repeating completed statements,
substitutions, effects, or process launches. Request correlation includes the
owning execution and generation; stale completions cannot satisfy a newer run.

## Formatting and diagnostics

Formatting must preserve the parser boundary:

- `$REFERENCE`, `${REFERENCE}`, `(EXPRESSION)`, and `$(COMMAND)` retain their
  distinct forms.
- Block keywords `if`, `elif`, `else`, `match`, and `case` remain on their own
  line, and each nested block is one tab deeper than its keyword.
- A parenthesized Kame expression is formatted by the Kame expression
  formatter, preserving the single-expression versus parenthesized-form boundary.
- A nested command substitution is formatted by the Kash formatter.
- Command substitutions embedded in Kame expressions also delegate their
  contents to the Kash formatter and preserve their enclosing expression spans.
- Formatter output is idempotent and reparses to an equivalent Kash AST.
- Command words use bare spelling when safe; quotes remain for empty words,
  whitespace, literal syntax characters, grammar-sensitive words, and scalar
  substitutions whose unquoted spelling would enable list splicing.

Malformed references are Kame reference errors, malformed parenthesized expressions are
Kame expression errors, and malformed `$(...)` contents are Kash command errors.
Diagnostics must identify the enclosing Kash source and the precise embedded
span.

Formatting preserves comments, scalar/list-splice distinctions, literal operator
quoting, and complete reference boundaries. Recovery chains are spaced, and
terminal acceptance is emitted with preceding whitespace to avoid confusing a
reference suffix. Block indentation is always tabs, regardless of the generic
formatter's requested space style. Process diagnostics carry status, signal,
stage, and source information but do not embed captured stdout/stderr in causes;
live forwarding and explicit capture remain separate channels.

## Acceptance tests

- Deferred commands snapshot operands once; expressions preserve lexical bindings
  and evaluate only during cleanup. Neither opens an output at registration,
  executes in an unselected branch or runs during dry-run.
- Demanded top-level lazy effect values survive successive statement handles;
  distinct calls and retry attempts remain fresh.
- Cleanup is invocation-scoped, LIFO and once-only across suspension, on success,
  failure, denied capabilities, timeout and supported interruption paths.
- User processes are reaped before cleanup; cleanup-started async work is joined.
  Failed cleanup does not skip later actions or mask the primary failure, and
  exhausted budgets/host limitations are reported.
- Temporary-workspace examples preserve unrelated files. No automatic ownership,
  atomic-redirection, crash-finalization or remote-rollback guarantee is implied.

- `$name`, `$project.name`, and `${name}` resolve the same Kame bindings.
- `$PATH` does not read the environment, while `$env.PATH` does.
- Parenthesized expressions use the Kame expression grammar and preserve
  values without string splitting.
- `biome --config-path=(js-biome-config)`, `echo name=(name)` and arguments with
  expression prefixes/suffixes parse as command words, not definitions. Nested
  calls, whitespace, strings, value pipes and process captures retain one word
  and original spans; zero-argument calls need no extra parentheses.
- Mixed expression words produce one scalar argument with exact whitespace and
  empty-text behavior. Standalone list results splice; mixed/quoted list results,
  nil, records and other invalid values fail before the containing graph launches.
- Mixed expressions work consistently in path-shaped words, redirection paths,
  `:cwd`, `:timeout` and individual environment setup values, including operand
  words that start with an expression. Per-setting validation, stage-relative
  redirection paths, dependency tracking, no-splice rules and prelaunch checks
  remain unchanged; invalid values cannot start stages or truncate files.
  Typed `:env` records and definition values keep their distinct value grammar.
- Quoted and escaped literal parentheses stay literal. References/captures inside
  quotes keep their existing semantics, bare braces do not become substitutions,
  and statement-leading expressions keep their expression-statement meaning.
- Mixed fragments retain completed values across suspension, obey capabilities,
  phases, dependencies and executable tracking, and never reparse result bytes.
  Parse/format/parse is idempotent without turning calls into quoted literals or
  changing typed splicing on either host.
- `$(COMMAND)` captures stdout and does not parse its contents as Kame syntax;
  `>&out` joins that capture, while `>&err` remains live and excluded.
- Command arguments containing whitespace remain one argument.
- A raw wildcard is literal; `(wildcard PATH)` is evaluated as a Kame wildcard.
- Raw command pipelines stream process data; parenthesized expression pipes
  use Kame value-pipe behavior.
- `CMD?` retains diagnostics for accepted non-zero exits.
- `LEFT ?? RIGHT` evaluates the fallback only for a nil or failed left side.
- An `if` command condition, including a sequential list, is true on effective
  success and false on an eligible final process exit; other failures propagate.
  A parenthesized condition uses Kame truth.
- `match`/`case` selects the first matching pattern, binds named captures as
  `$name` in that arm, and falls to `else` when nothing matches.
- Only the selected control-statement branch runs; an unselected branch's
  commands and effects never occur.
- Nested control blocks parse and format idempotently.
- Existing `.kmk` recipes retain their shell-text behavior unless explicit
  Kash recipe metadata or `SHELL = kash` selects the portable interpreter.
- Definition detection distinguishes `name = VALUE` from `echo name=value`;
  function RHSs use Kame expressions, including command-substitution atoms, not
  shell-style infix concatenation or Kash reference wrappers.
- Single-expression, shorthand application, lambda, and value-pipe boundaries
  agree with their corresponding ordinary Kame expressions.
- `$name.c` resolves a component; `${name}.c` appends a literal suffix.
- Quoted/escaped operators are literal, empty quotes preserve an empty argument,
  and invalid escapes and unsupported single-quote syntax are rejected. `&&` and
  `||` are recognized only as sequential process-list operators.
- Quoted `"--mtime=@0"` in direct Kash remains one literal argument through
  repeated formatting; unquoted reserved at-sign syntax receives Kash quoting
  guidance, not an unrelated template selector diagnostic.
- Flat lists splice only in standalone unquoted substitutions; mixed/quoted
  lists, unsupported values, empty executables, and NUL bytes fail before launch.
- `$...CMD` explicitly spreads a nonempty command list; `$...args` spreads a flat
  argument list, including empty lists. Formatting preserves the explicit marker,
  and ordinary `$CMD` still rejects a list in executable position.
- Setup keys configure only their stage; duplicates fail, timeout units are
  seconds, and stage cwd governs relative redirection paths.
- Substitution preserves empty stdout and trailing newlines, forwards stderr
  once, rejects invalid UTF-8, and fails rather than returning truncated capture.
- `&&`/`||` are left-associative with equal precedence and select only on effective
  success or eligible process exits. Skipped operands have no effects; a failed
  unhandled list stops the statement sequence.
- Output-port disposition, broadcast, error-only capture, output capture,
  zero/multiple capture sinks, and process-exit status agree on native and WASM.
- Pipelines exceeding pipe-buffer capacity stream without deadlock; failures in
  every stage, including SIGPIPE, contribute to aggregate failure.
- Conflicting redirections fail; denied capabilities do not launch stages or
  truncate files; launch failures and cancellation reap every stage.
- Acceptance, command recovery, and value recovery obey their distinct failure
  categories; none suppresses capability denial or cancellation.
- Async execution does not block the next statement, is joined on source
  completion, and is cancelled on source failure or interruption.
- Branch definitions and captures do not escape; unused lazy definitions and
  fallback operands cause no effects or dynamic dependencies.
- Multiple host requests and suspended sequences execute each process and effect
  once; stale completions cannot satisfy newer executions.
- Mixed indentation, invalid dedents, empty blocks, misplaced keywords, and
  semicolon-separated block headers produce precise diagnostics.
- Native and WASM agree on argv, captures, statuses, capabilities, source spans,
  and formatting; `.kash`/`.ksh` do not affect build-file discovery.
- Direct Kash execution and `do run` agree for files, program arguments, capability
  defaults, cancellation, and async joining; `.ksh` is the same language, not a
  fallback to a system shell. Inline/stdin forms use explicit `--lang kash`.
- `$(...)` parses as a Kame atom in applications, lists, records, lambdas, and
  expression-valued definitions; its contents always parse as Kash processes.
- The same substitution parses with or without grants and host services; demanded
  execution reports `PHASE_INVALID`, `CAP_DENIED`, or `HOST_FAIL` as applicable.
- Embedded processes inherit scope and execution policy without broadening
  grants, escaping resolved path restrictions, or relaxing caller limits.
- Plain Kame strings containing `$(...)` remain literal; explicit expression
  interpolation can evaluate it, while raw `.kmk` recipe text stays shell-owned.
