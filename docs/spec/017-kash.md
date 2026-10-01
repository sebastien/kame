# Kash

## Purpose

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

Existing `.kmk` recipe behavior is unchanged. Rule bodies currently render to
one shell script and are not parsed as Kash. A future rule may explicitly opt
into Kash for its body, but no existing recipe is reinterpreted by this
specification.

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
`-c '(out name)'` uses Kame syntax by default and can refer to a definition in
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
`do run`, as is the separate `do expr` execution command.
Async work belongs to the combined invocation: a source boundary does not join
it, later fragments may await shared handles, and normal session completion joins
outstanding graphs. Failure or cancellation stops the remaining session and
reaps owned processes. Appending another source does not implicitly export
branch-local definitions.

`kame do parse --lang kash` and `kame do fmt --lang kash` use the existing
file/stdin conventions. Native and WASM hosts must implement identical language
semantics. `.kash` and `.ksh` do not join build-file discovery or implicitly
change `.kmk` recipes. These invocation forms are a specification contract;
implementations must not claim them until their corresponding runner exists.

## Source and statements

A Kash source is a sequence of comments, blank lines, definitions, statements,
and block control statements. `;` separates statements on a line. Definitions
have the same shape and lazy semantics as Kame definitions:

```kash
name = "John Smith"
(banner WHO) = (cat "Hello, " WHO)
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
VALUE = EXPRESSION | $REFERENCE | ${REFERENCE} | @(BOUNDARY)
RHS = VALUE ("??" RHS)?
```

The expression parser owns each `EXPRESSION` operand, including command
substitution atoms; Kash owns their process contents, the other substitution
wrappers, and recovery separators. `$REFERENCE`, `${REFERENCE}`, Kash's `@(...)`
shorthand, and infix `??` are not added to ordinary Kame expressions: use
`(cat "Hello, " WHO)`, not `(cat "Hello, " $WHO)`. Existing Kame template
expansions and expression-string interpolation retain their own rules. Functions
retain Kame parameter and lazy evaluation semantics. Kash does not inherit
`.kmk` word-list or recipe-template RHS syntax.

Definitions in a statement sequence are registered lazily before that sequence
runs. Duplicate names in one sequence are errors. Each selected control branch
has a child lexical scope; its definitions may shadow outer bindings but do not
escape. Unselected branches are never registered or evaluated. Statements run
in source order; an unhandled failure stops the sequence.

Newlines and unquoted `;` terminate ordinary statements outside nested syntax.
Embedded expressions may span lines using their existing delimiter rules.
Command substitutions may span lines but contain exactly one process expression,
not a statement sequence or control block. Empty statements are ignored. Outside
quotes and nested syntax, `#` at the start of a word introduces a comment through
the end of the line. `//` is literal command text, not a Kash comment.

## Expansion boundary

Kash has four distinct substitutions:

| Form | Parser | Meaning |
| --- | --- | --- |
| `$REFERENCE` | Kame reference parser | Resolve and insert a value from the current Kame scope. |
| `${REFERENCE}` | Kame reference parser | Braced spelling of `$REFERENCE`. |
| `@(EXPRESSION)` | Kame expression parser | Evaluate an arbitrary Kame expression and insert its value. |
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
`$env.PATH` or `@(env.PATH)`.

`env` is a reserved, lazy environment namespace in Kash. Accessing a named field
performs the corresponding Kame environment operation with its capability check
and dependency tracking; it does not eagerly read the whole environment. A
missing variable yields nil, and a denied read fails. Definitions, parameters,
and captures may not bind `env`. No other name has an environment fallback.

### Kame expressions

`@(EXPRESSION)` is Kash's explicit entry into Kame's expression and
meta-programming layer. The boundary supports application shorthand without
introducing new expression operators. Its contents are parsed as a
whitespace-separated sequence of complete Kame expressions:

- One expression evaluates directly: `@(options.release)`, `@([1 2])`, and
  `@((map sources inspect))` evaluate that expression.
- Two or more expressions use Kame's parenthesized-form parser, with parentheses
  supplied by the boundary: `@(map sources inspect)` means `(map sources inspect)`.
  Lambda and section recognition follow that parser's existing rules.
- A top-level value pipe uses Kame's existing parenthesized pipe parser:
  `@(sources | map render)` means `(sources | map render)`.
- Empty contents are an error. Delimiters, strings, lambdas, patterns, sections,
  and application/pipe rules otherwise remain those of Kame. `$(COMMAND)` is
  allowed as a Kame expression atom; `$REFERENCE`, `${REFERENCE}`, nested Kash
  boundary shorthand, and infix `??` are not Kame expression syntax.

This wrapper composes existing expression and application parsers, not a second
value grammar. It is the form for calls, functions, lists, records,
patterns, templates, and transformations that are more than direct lookup:

```kash
emit @(replace source ".c" ".o")
report @(map sources inspect)
```

`@(...)` does not create a Kash value model or evaluator. It evaluates to an
ordinary Kame value and carries normal Kame dependencies, capabilities, failures,
and source spans.

`@NAME` and `@tmpl(...)` are reserved Kash meta-programming forms for generated
syntax and templates. They are not value substitutions and do not change the
meaning of `$REFERENCE`, `@(EXPRESSION)`, or `$(COMMAND)`. Their quoting and
hygiene rules require a separate macro/template specification.

Until that specification exists, unquoted `@NAME` and `@tmpl(...)` produce a
reserved-syntax diagnostic. Quote or escape a literal `@` to pass it to a command.

### Command substitution

`$(COMMAND)` is command substitution. `COMMAND` is parsed with Kash command and
pipeline syntax, runs as a process graph, and its standard output becomes the
substitution value:

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
banner = (cat "Revision: " $(git rev-parse --short HEAD))
```

The outer expression parser retains a command-substitution node and delegates
the enclosed process expression to Kash. Embedded Kash may itself contain Kame
expressions and nested substitutions; each parser retains its original source
spans. Parsing never depends on grants, host availability, or execution phase.
Evaluation requires a context permitting process execution, as defined below.

Kame quoted strings do not gain implicit command interpolation: `"$(git status)"`
is literal text. Existing expression interpolation can explicitly evaluate a
substitution, for example `"Revision: {(cat $(git rev-parse --short HEAD))}"`.
Kash command-word double quotes still allow substitutions as specified below.
Opaque `.kmk` recipe text remains shell text: a raw recipe `$(...)` is interpreted
by the recipe's shell, not Kash. A substitution inside an explicit Kame recipe
expansion is owned by the expression parser instead.

Substitution captures only the final stage's stdout and does not also forward it
to the enclosing stdout. Stderr remains live. The result is a Kame string:
stdout must be valid UTF-8 and is preserved exactly, including trailing newlines
and an empty result. No newline stripping or whitespace splitting occurs.
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
(run "git" "status" "--short")
```

Kash command arguments are typed value positions, not shell word-expansion
positions. A string expansion supplies one argument even when it contains
whitespace. Expansions are not re-split, implicitly globbed, or reparsed as
shell syntax. A raw `*.go` is therefore literal; a filesystem wildcard is an
explicit Kame value, such as `@(wildcard ./src/**/*.go)`.

### Words, quoting, and argv conversion

Unquoted spaces and tabs separate words. Adjacent literal and substitution parts
form one word, as in `revision=$revision` and `${name}.c`. Double quotes group
parts into one word and allow all four substitutions. An empty quoted word is
one empty argument. Single quotes are reserved and rejected, consistent with
Kame expression syntax.

Outside quotes, backslash escapes the next non-newline character; backslash plus
newline continues the statement without inserting text. Inside double quotes,
`\"`, `\\`, `\n`, `\r`, `\t`, `\$`, and `\@` are supported; other escapes
are errors. Quoted or escaped operators are literal. Command quotes are Kash
word syntax, not expression strings: `{(...)}` has no interpolation meaning.
Inside `@(...)`, the existing Kame string and escape rules apply instead.

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
`files` is a list. A wildcard result supplies explicit argv items without shell
globbing. Quoted/escaped `@` and `$` are literal only when escaped or not followed
by substitution syntax; quotes do not disable valid substitutions.

The executable position requires one nonempty scalar value, not a list even if
it has one item. Executables, arguments, environment entries, and filesystem
paths must be free of NUL bytes. Processes execute argv directly, using host
PATH lookup for an executable without a path separator; no shell interprets it.

Executable PATH resolution uses the stage's configured environment, not a Kame
binding named `PATH`. Choosing a process environment is distinct from reading
an environment variable into a Kame value.

A command may use `run` setup keys for process configuration:

```kame
(run :cwd build-dir :timeout 10 :NODE_ENV "production" "npm" "test")
```

The keys configure the process; remaining expressions form its executable and
arguments.

In raw Kash, setup keys precede the executable and use `:KEY VALUE` pairs:

```kash
:cwd $build-dir :timeout @(10) :NODE_ENV production npm test
```

Each setup value is one scalar word or substitution, never a list splice.
`:cwd` is a directory relative to the invocation directory, `:timeout` is a
positive number of seconds, and other keys matching `[A-Za-z_][A-Za-z0-9_]*`
are environment overrides. Duplicate keys are errors. The process environment
otherwise inherits the invocation's configured environment; explicit environment
lookup still requires `env` access. Quoted or escaped leading colons are literal.
After the executable, colon-prefixed words are ordinary arguments. Each pipeline
stage has independent setup keys. Reserved setup keys are not environment
overrides. Invalid paths, keys, values, and missing executables are errors.

The expression-level `run` additionally accepts `:async` with a boolean value;
true returns a process handle instead of awaiting completion. This key is
reserved and rejected in raw stage setup: raw async execution uses terminal `&`
on the entire graph. `pipe` constructs and executes its stages together rather
than awaiting each stage independently; stage-level async flags are invalid
within it.

## Pipelines and redirection

A raw command pipeline is a first-class Kash process expression:

```kash
generate | transform | publish
compile < sources.txt > output.log
compile >> output.log
```

It lowers conceptually to a Kame streaming graph:

```kame
(pipe (run "generate") (run "transform") (run "publish"))
```

Kash connects command stages through that graph rather than eagerly buffering
intermediate output. `<` supplies standard input, `>` replaces an output file,
and `>>` appends to it.

Redirections belong to an individual stage and accept one nonempty scalar path
word. Paths resolve relative to that stage's working directory. Each stage may
have at most one stdin and one stdout redirection. A pipeline edge and a
redirection cannot both supply the same stream: `<` is allowed only on the first
stage and `>`/`>>` only on the last. Conflicts are errors, not silently overridden.
`>` creates or truncates; `>>` creates or appends. Stderr redirection, descriptor
duplication, and here-documents are not defined by this specification.

All stages start as one graph and communicate through bounded streaming pipes
with backpressure. Intermediate stdout is not collected. Every stage's stderr
is forwarded; final stdout is forwarded unless redirected or captured. Ordinary
commands inherit invocation stdin. If the source is stdin, commands without `<`
receive closed stdin rather than consuming source text. Command substitutions
without `<` also receive closed stdin.

The graph fails if any stage exits non-zero or by signal, including SIGPIPE.
Its aggregate status is the rightmost failing stage's status, or zero if every
stage succeeds; all per-stage statuses are retained. A signal contributes
`128 + signal` to aggregate exit status. Launch failures and timeouts are
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
emit @(sources | map render)
```

The pipe between raw command stages is a process stream; the pipe inside
`@(...)` has the value-pipe semantics specified by `004-language.md`.

## Process lifecycle and recovery

Appending `&` starts a command or pipeline asynchronously and immediately yields
its `Process`/`Result` value:

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
job = @(run :async :true "watch-assets")
completion = @(await job)
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
not a raw-command operator or an operator inside `@(...)`. It handles ordinary
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
6. Terminal async `&`, applying to the complete recovered expression.
7. Statement separators `;` and newline.

Outside a reference, a terminal unquoted `?` is acceptance even when attached to
a word; quote or escape it for a literal filename suffix. `?` with a following
fallback must be whitespace-separated. `??` must be whitespace-separated between
value operands and is an error in raw commands. Operators inside quotes or
embedded Kame syntax are not process operators.

`&` must be followed by a statement terminator: `first & second` requires
`first &; second` or a newline. Parentheses do not introduce shell subshells or
raw process grouping. `&&` and `||` are unsupported and produce diagnostics.

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
expression in `@(...)`, evaluated with Kame's truth rules. A command condition
does not emit a failure diagnostic for a non-zero status; that is an implicit
`CMD?` scoped to the condition. `$(...)` is not a valid condition, because every
non-nil string is true under Kame truth, so it would always be true.

A command condition may be a pipeline with redirections, but not async or
explicit acceptance/recovery syntax. Launch, timeout, capability, or evaluation
failures propagate rather than being treated as false. Its stdout and stderr
follow ordinary forwarding/redirection rules. An expression condition uses
Kame truth: only nil and boolean false are false; even an empty string is true.
Conditions and match subjects are evaluated once per execution, including across
suspension and resumption for host requests.

```kash
if test -f ./build/app
	gcc -o build/app src/app.c
elif @(options.release)
	gcc -O2 -o build/app src/app.c
else
	report "nothing to build"
```

`match` selects a branch by pattern, reusing the pattern clauses of the Kame
`match` form in `004-language.md`. Its subject is a value — `@(...)` or
`$(...)` — and each `case` names a match pattern. The first matching case runs
and its named captures bind as `$name` references within that arm; `else` is the
optional fallback.

```kash
match @(target)
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

The Kash parser may own statement, pipeline, redirection, async, and recovery
AST nodes. Every embedded expression and reference must retain the normal Kame
AST node and source span. Lowering Kash nodes to `run`, `pipe`, and associated
process operations must preserve Kame's laziness, capability checks, dynamic
dependencies, and diagnostics. Block control statements lower to the Kame `if`
and `match` special forms: a command condition lowers to a process-status
predicate, an `@(...)` condition to the Kame expression, a `match` subject to a
value, and each branch body to a nested Kash statement sequence.

Pipeline lowering is descriptive, not eager evaluation of independent `run`
calls: the whole graph must be constructed before any stage starts. Execution
state survives evaluator suspension without repeating completed statements,
substitutions, effects, or process launches. Request correlation includes the
owning execution and generation; stale completions cannot satisfy a newer run.

## Formatting and diagnostics

Formatting must preserve the parser boundary:

- `$REFERENCE`, `${REFERENCE}`, `@(EXPRESSION)`, and `$(COMMAND)` retain their
  distinct forms.
- Block keywords `if`, `elif`, `else`, `match`, and `case` remain on their own
  line, and each nested block is one tab deeper than its keyword.
- A Kame expression inside `@(...)` is formatted by the Kame expression
  formatter, preserving the single-expression versus parenthesized-form boundary.
- A nested command substitution is formatted by the Kash formatter.
- Command substitutions embedded in Kame expressions also delegate their
  contents to the Kash formatter and preserve their enclosing expression spans.
- Formatter output is idempotent and reparses to an equivalent Kash AST.

Malformed references are Kame reference errors, malformed `@(...)` contents are
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

- `$name`, `$project.name`, and `${name}` resolve the same Kame bindings.
- `$PATH` does not read the environment, while `$env.PATH` does.
- `@(EXPRESSION)` accepts the complete Kame expression grammar and preserves
  values without string splitting.
- `$(COMMAND)` captures stdout and does not parse its contents as Kame syntax.
- Command arguments containing whitespace remain one argument.
- A raw wildcard is literal; `@(wildcard PATH)` is evaluated as a Kame wildcard.
- Raw command pipelines stream process data; expression pipes inside `@(...)`
  use Kame value-pipe behavior.
- `CMD?` retains diagnostics for accepted non-zero exits.
- `LEFT ?? RIGHT` evaluates the fallback only for a nil or failed left side.
- An `if` command condition is true on exit zero and false on a non-zero exit
  without a failure diagnostic; an `@(...)` condition uses Kame truth.
- `match`/`case` selects the first matching pattern, binds named captures as
  `$name` in that arm, and falls to `else` when nothing matches.
- Only the selected control-statement branch runs; an unselected branch's
  commands and effects never occur.
- Nested control blocks parse and format idempotently.
- Existing `.kmk` recipes retain their shell-text behavior unless an explicit
  future Kash recipe mode is selected.
- Definition detection distinguishes `name = VALUE` from `echo name=value`;
  function RHSs use Kame expressions, including command-substitution atoms, not
  shell-style infix concatenation or Kash reference wrappers.
- Single-expression, shorthand application, lambda, and value-pipe boundaries
  agree with their corresponding ordinary Kame expressions.
- `$name.c` resolves a component; `${name}.c` appends a literal suffix.
- Quoted/escaped operators are literal, empty quotes preserve an empty argument,
  and invalid escapes, single quotes, `&&`, and `||` are rejected.
- Flat lists splice only in standalone unquoted substitutions; mixed/quoted
  lists, unsupported values, empty executables, and NUL bytes fail before launch.
- Setup keys configure only their stage; duplicates fail, timeout units are
  seconds, and stage cwd governs relative redirection paths.
- Substitution preserves empty stdout and trailing newlines, forwards stderr
  once, rejects invalid UTF-8, and fails rather than returning truncated capture.
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
