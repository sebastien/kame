---
revision: 2
---

# Kash

## Purpose

Kash is Kame's action language: the command and process grammar of the action
context of `055-unified-kame-kash-language.md`. It makes command-oriented
programs read like shell scripts while keeping Kame's typed values, lexical
bindings and explicit dependency model.

Kash does not introduce a second expression language. It adds statements,
commands, pipelines, redirections and process-control forms around the shared
expression grammar of `055` §4. A Kash implementation composes the expression
parser and evaluator; it never converts values into shell text and reparses
them.

This specification defines:

- Kash sources: action blocks holding bindings, functions and command
  statements.
- Command words, `$` references and `(EXPR)` value fragments.
- `$(…)` capture and `$?(…)` outcome inspection.
- Block-bodied functions with value `return`, `yield` and `exit`.
- Block control statements with expression conditions.
- First-class process pipelines, redirections, asynchronous execution,
  recovery and deferred cleanup.

Related specifications refine this contract:

- `038-direct-kash-and-values.md`: direct parsing and tracked command heads.
- `042-kash-streaming.md`: process groups, channels, framing and multiplexors.
- `046-kash-scripting-and-publication.md`: loops, script option parsing and
  no-clobber file publication.
- `047-kash-output-ports-and-command-lists.md`: output ports and command lists.
- `048-kash-practical-scripting.md`: standard-stream routing, structured
  transfers, owned execution scopes and selected exits.
- `049-process-inspection.md`: outcome records and typed capture.
- `050-kash-libraries.md`: modules imported by Kash sources.
- `052-kash-interactive.md`: interactive sessions.

## Relationship to Kame sources

| Source | Top level | Responsibility |
| --- | --- | --- |
| `.km` (alias `.kame`) | Definitions, imports, includes and declarations | Values, functions, patterns, templates, rules, tasks and services. |
| `.kash` (alias `.ksh`) | An action block | Command statements and process orchestration. |

Both share one expression grammar, one value model and one name-resolution
model (`055` §6). Raw command lines are action-context syntax, not expressions;
every value computation inside them delegates to the shared expression grammar.
Which grammar applies is decided syntactically and never depends on bindings,
grants or host availability.

Action context appears in:

- the top level of a `.kash` source;
- a rule, task or service body (`055` §10.1);
- a block-bodied `def` body;
- the content of `$(…)` and `$?(…)`, restricted to one process graph or command
  list.

### Rule bodies

A rule, task or service body is a Kash action block by default. Metadata may
select another interpreter for one declaration:

```kame
task build : ./input ; {env: {MODE: mode}}
	printf "%s\n" $mode

task posix ; {shell: ["/bin/sh", "-c"]}
	printf '%s\n' "$MODE"
```

The `shell` value is the built-in Kash interpreter value `kash` (the default),
an executable string (with `-c` implied) or a nonempty list of interpreter argv
strings. A build-wide `SHELL = "bash"` sets the default for every body that does
not select its own. Per-rule selection does not propagate to prerequisites.
Foreign-shell bodies are document templates rendered to one script (`055`
§10.2); `$` and `$(…)` in them belong to that shell.

A Kash body is parsed directly, without template expansion, and the entire body
is parsed before any statement executes. Statements share the body bindings
`inputs`, `outputs`, the named captures, the program's definitions and the
invocation arguments. If the body ends with `return VALUE` and the value is not
`nil`, the value is published to the single output under `044`. Each body joins
its asynchronous work before verifying file outputs or publishing completion.
Timeouts cover the whole body; retries start a fresh invocation.

Interpreter identity is an execution dependency, as specified in
`006-runtime.md`, and participates in cache and file freshness only as that
dependency. Selecting an interpreter does not make the body depend on the
process environment. Named environment reads are tracked selectively. Once a
body submits a process, it also consumes the complete effective child
environment after overrides, independently of those named reads (`006`,
`038`).

No operation compiles text into Kash. Reusable actions are `def` functions,
imported from modules with `import` (`050`); `eval` parses one expression only
(`055` §13).

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
fragments as defined by `009-cli.md`. All fragments are parsed before
execution; they share module definitions, one engine and one invocation policy.
A trailing `-c 'out! name'` uses Kame expression syntax by default and can refer
to a definition in the Kash source. It is raw Kash command text only when a
forward-scoped `--lang kash` selects that parser.

Arguments after `--` use Kame's normal argument binding. Execution supports the
existing directory, capability, timeout and diagnostic options. A session that
begins with Kash has the same default capability policy as build execution;
Kash appended to a value session inherits that session's more restrictive
policy. Relative source filenames resolve before any directory change. Relative
paths inside a Kash program resolve against the invocation's working directory,
or `-C` (`055` §9.2). Kash fragments accept no named entry operands: statements
run in source order. Direct and explicit execution share the same parser,
engine, process ownership and policy; neither launches a shell. `009-cli.md`
owns dispatch and option validation.

Async work belongs to the combined invocation: a source boundary does not join
it, later fragments may wait on shared handles, and normal session completion
joins outstanding graphs. Failure or cancellation stops the remaining session
and reaps owned processes. Appending another source does not export
block-local bindings.

`kame do parse --lang kash` and `kame do fmt --lang kash` use the existing
file/stdin conventions. Native and WASM hosts implement identical language
semantics and these invocation forms. `.kash` and `.ksh` do not join build-file
discovery or select a rule-body interpreter.

## Source and statements

A Kash source is an action block: a sequence of comments, blank lines, block
statements (`055` §5.2) and command statements. `;` separates statements on a
line.

```kash
let name = "John Smith"
def banner who = "Hello, @{who}!"

git status --short
build | tee ./build.log
watch-assets &
```

A statement is a binding only when it starts with `let` or `set`, and a function
definition only when it starts with `def`. Every other statement is a command
statement, including a pipeline, redirection, asynchronous command or recovery
form. `echo revision=$revision` and `echo a = b` are commands.

| Statement | Meaning |
| --- | --- |
| `let NAME = EXPR` | Evaluate `EXPR` eagerly when reached and bind it immutably for the rest of the block. A later `let` of the same name shadows it. |
| `set NAME = EXPR` | Assign a `let` binding of an enclosing block in the same invocation. Cannot create a binding. Targeting a binding captured by a lambda or nested `def` is `PARSE_ERR`. |
| `set env/NAME = EXPR` | Write the unit's environment store (`055` §5.3). `nil` unsets. |
| `def NAME PARAM… = EXPR` | Expression-bodied function. |
| `def NAME PARAM…` + block | Block-bodied function (below). |
| `if`, `match`, `for`, `while` | Control blocks (below and `046`). |
| `return`, `yield`, `exit`, `break`, `continue` | Control transfer. |
| `defer …` | Deferred cleanup. |
| `(EXPR)` | Expression statement: evaluated for effects; the result is discarded. |
| anything else | Command statement. |

The right-hand side of `let`, `set` and `return` is one expression in value
context (`055` §4), including `$(…)` and `$?(…)` atoms. `$REFERENCE` forms are
command-word syntax and are not expressions: write `let greeting = "Hello, @{who}"`
or `let n = count + 1`, not `let n = $count + 1`.

`def` statements in a block are registered before the block runs, without
evaluating their bodies, so functions in one block may refer to each other in
any order. A `let` binding is visible from the statement after it. Duplicate
`def` names in one block are errors; a `def` or `let` may shadow an outer
binding. Each selected control branch and each loop iteration has a child
lexical scope whose bindings do not escape. Unselected branches are never
evaluated. Statements run in source order; an unhandled failure stops the
sequence.

Module-level definitions of a `.kash` source are its top-level `def` statements;
they are visible to appended fragments and exportable under `050`. A `let`
value is computed once when its statement runs and is retained through the
combined runner invocation, including finalization. Releasing a statement
handle never cancels and recomputes it. Separate function invocations and
iteration callbacks still execute their own fresh calls.

Newlines and unquoted `;` terminate ordinary statements outside nested syntax.
Embedded expressions may span lines under the continuation rules of `055` §4.1.
`$(…)` and `$?(…)` may span lines and contain one synchronous process graph or
command list, not a statement sequence or control block. Empty statements are
ignored. Outside quotes and nested syntax, `#` at the start of a word introduces
a comment through the end of the line. `//` is literal command text.

## Functions

### Definitions

`def NAME PARAMETER...` followed by an indented block defines a function whose
body is a nonempty Kash action block:

```kash
def probe workspace
	if $?(test -f $workspace/ready).ok
		return true
	touch $workspace/ready
	return false

let ready = probe workspace
```

The header occupies its own line, with no trailing `:`, `=`, braces or `end`.
A zero-parameter function is written `def NAME ()`. Parameters follow `005`:
one optional final `rest...` parameter, arity checks and duplicate-parameter
checks. `def NAME PARAMETER... = EXPR` (or `def NAME () = EXPR`) defines an
expression-bodied function with the same call semantics. `def NAME = EXPR`,
with no parameter list, defines a lazy memoized value, not a function (`055`
§5.1).

The body is parsed with its containing source, including unused functions and
unselected branches. Registration performs no body effects, dependency
discovery, process launch or authorization checks. A nested function closes
over its defining block scope and cannot escape existing callable ownership and
publication restrictions.

### Calls

A function is called in expression position, `probe workspace` or `probe ()`
for zero arguments, or as a command (`055` §7.2):

```kash
probe $workspace
compile $source (object-path source) | tee ./compile.log
```

A command whose head resolves to a function passes its words as arguments: bare
and mixed words become strings, `$x` and standalone `(EXPR)` fragments pass typed
values, and `$...xs` spreads a list into separate arguments. At statement level
the return value is discarded; use `let v = probe workspace` to keep it. The
function's stdout and stderr take part in pipelines and redirection like a
process's. `$(f x)` captures what `f` writes, never what it returns.

Arguments evaluate left to right and bind typed parameters and the nearest
`args` frame. The call executes the already-parsed body with fresh call-local
bindings and its defining lexical scope, under the caller's execution policy,
working directory, environment store and limits. It cannot broaden grants. Each
call owns an independent async and cleanup lifetime; branches and loop
iterations do not create further cleanup lifetimes. No source is generated,
reparsed or passed to a shell. Calling the same function again executes a fresh
body; resuming a suspended call continues its retained state without replaying
statements or effects.

`set env/NAME` and `cd` inside a function modify the enclosing process-like
unit, so their effects remain visible after the function returns (`055` §5.3).

### `return`

`return EXPR?` ends the nearest lexically enclosing `def` invocation with the
value of `EXPR`, or `nil` without an operand. It works from nested `if`, `match`,
`for` and `while` bodies. The operand is one expression, evaluated once,
including across suspension. Falling off the end of a body returns `nil`; there
is no implicit last-statement value.

`return` is also valid in a rule, task or service Kash body, where a final
non-`nil` value is published (`044`), and in a top-level block definition
(`055` §5.1). Anywhere else, including the top level of a `.kash` source, it is
`PARSE_ERR`, even in an unused branch; end a script with `exit`.

Return is control flow, not a failure. It skips later body statements, exits
only its own invocation and runs that invocation's deferred cleanup before the
caller resumes. Owned async work is joined as on normal completion. A failure
evaluating the operand unwinds as an ordinary failure. Cleanup failures follow
the primary-failure and LIFO rules below; a successful return cannot hide
failed cleanup. A nested function cannot return from its caller.
`defer return …` is `PARSE_ERR`: cleanup cannot install a return for an
invocation that is already finalizing.

Return does not take `?`, `? ELSE`, pipeline, redirection or async modifiers.
A function reports a condition to its caller through its value:

```kash
def needs-retry workspace
	return not $?(test -f $workspace/ready).ok

if needs-retry workspace
	sleep 1
```

### Derived status and outcomes

Functions do not return integer statuses. Every invocation, of a function or a
process, has the outcome record of `055` §8.1 and `049`: `state`, `ok`,
`status`, `signal`, `value`, `error` and `stages`. When a function's status is
needed, as a pipeline stage, in `&&`/`||`, in a rule body or as a script's
result, it is derived:

| State | Status |
| --- | --- |
| `:completed` | 0 |
| `:failed` | The failing process's status if a process failure caused it, otherwise 1 |
| `:cancelled` | A broken pipe: not a pipeline failure, as with `yes \| head` |

```kash
let check = $?(verify-config ./config.json)
if check.state == :failed
	printf "config check failed with status %s\n" $check.status >&err
```

### `yield`

A `def` whose body contains `yield EXPR` returns a lazy `Stream` when called
(`055` §8.3). The body runs as the consumer pulls elements and is `:suspended`
while waiting. Inside a generator a bare `return` ends the stream and
`return EXPR` is `PARSE_ERR`. When a consumer stops early, the generator becomes
`:cancelled` and its deferred cleanup runs. A function's stdout bytes and its
yielded values are separate channels and never convert into each other.

### `exit`

`exit EXPR?` ends the innermost process-like unit with an integer status from 0
through 255 (default 0); other kinds and out-of-range values fail
`EXPR_INVALID`. Process-like units are the script, a rule body, a `$(…)` or
`$?(…)` capture and, interactively, the current entry (`052`). `exit` runs the
pending cleanup of every invocation it unwinds, innermost first, and joins or
cancels owned async work as for the corresponding completion or failure: status
0 completes the unit and any other status fails it. `exit` does not target a
`def`; use `return`.

### Explicit value construction

List construction and producer framing use ordinary expressions, not word
concatenation or implicit capture conversion:

```kash
let prefix = []
let command = [...prefix, "printf", "%s\n"]
$...command "typed argv"

let workspace = line $(mktemp -d ./kash-example.XXXXXX)
defer rm -rf $workspace
let config = {name: "example", enabled: true}
let config-path = @{workspace}/config.json
std/write! $config-path (json config)

# || runs the fallback after an ordinary unsuccessful process exit.
false || printf "fallback\n"
```

`...EXPR` is list spread from `055` §4.2; `$...command` is argv spread.
`line TEXT` removes one terminal LF under `007-library.md` without changing
capture. `@{workspace}/config.json` is an interpolated path atom in value
context. `std/write!` is an explicit head: command position never consults the
standard library (`055` §6). Use `joinpath` when computed path joining and
normalization are required, not merely to append an authored literal suffix.

## Expansion boundary

Kash has these explicit value and process boundaries:

| Form | Parser | Meaning |
| --- | --- | --- |
| `$REFERENCE` | Reference | Resolve a value and insert it into a word. |
| `${REFERENCE}` | Reference | Braced `$REFERENCE`; required for qualified names. |
| `$...REFERENCE`, `$...{REFERENCE}` | Reference | Spread a flat list into argv entries, including at command position. |
| `(EXPR)` | Expression | Evaluate an expression in value context, standalone or as a word fragment. |
| `$(COMMAND)` | Kash | Run one process graph or command list and capture its stdout. |
| `$?(COMMAND)` | Kash | Run one process graph or command list and return its outcome. |

The right-hand sides of `let`, `set` and `return`, conditions, `match` subjects
and `for` sources are expressions. Path atoms there keep the expression grammar,
including a leading interpolation `@{root}/suffix`. A standalone `@{root}`
without a following path separator is not a path atom. Command words keep their
own substitution rules. Parse, format and parse preserve this distinction
without evaluating code.

### References

`$REFERENCE` inserts a value already in scope. `REFERENCE` is a name followed by
field accesses: `$name`, `$project.name`, `$files.0`, `$record."key"`. Names
resolve as in expression position (`055` §6): lexical bindings, then the
standard library, then, for an `ENV-NAME` only, the environment store.

`${REFERENCE}` has the same evaluation semantics. It ends a reference where
literal text follows and is required for qualified names: `${env/HOME}`,
`${x/git}`, `${c/default-flags}`. It is not a general expression form.

The unbraced form consumes the complete reference, including dot components and
name suffixes, and stops at `/`. `$name.c` looks up component `c`; `${name}.c`
appends the literal suffix `.c`. `$dir/file.txt` is `$dir` followed by
`/file.txt`. A reference's own trailing `?` or `!` belongs to the name, not the
acceptance operator; put whitespace before the operator or brace the reference
to disambiguate.

Environment access:

- `$PATH` resolves a lexical `PATH` binding if one exists, otherwise the
  environment store. An unset variable fails with `ENV_VAR_MISSING`, so
  `$HOME/x` works unbraced and typos are caught.
- `${env/NAME}` reads the store only and yields `nil` when unset; as a word it
  then contributes no argument, like an unset shell variable.
- Lowercase names never fall back to the environment.

Each environment read is a tracked, capability-checked operation on the named
variable; nothing reads the whole environment eagerly, and a denied read fails.
`env` is a namespace in its own table, so a binding named `env` does not
conflict with it.

### Value fragments

A parenthesized group in a command word is a value-context expression parsed by
the shared expression grammar; there is no wrapper shorthand or second value
grammar. Inside it, names are plain expression names, not `$` references:

```kash
emit (subst ".c" ".o" source)
report (map inspect sources)
biome --config-path=(js-biome-config ())
emit prefix-(name)-suffix
```

A word may combine literal fragments, references, captures and value fragments
in any order. The parentheses are owned by the expression parser: `(name)` is
the value of `name`, and `(js-biome-config ())` calls a zero-argument function.
Whitespace, quotes, nested applications, `|>` pipes and captures inside the
fragment do not end the word. After the closing parenthesis, adjacent fragments
continue the same argument. This is general word composition, not special
handling of `--flag=` syntax. A fragment may begin a mixed word, as in
`emit (name).json`.

The same scalar-word composition applies to command heads and arguments,
redirection paths, `:cwd`, `:timeout` and individual environment setup values.
It also applies to path-shaped words: `./out/(name).txt` contains a value
fragment. Inside the fragment, path atoms keep their own `@{EXPR}`
interpolation; word fragments do not change the path grammar. Typed `:env`
records and `let` right-hand sides keep the expression grammar rather than
becoming word concatenation.

Braces introduce no substitution of their own: `{reference}` is literal text,
and `{(name)}` is literal braces around a value fragment. `@` is an
ordinary character in command words, so `tar --mtime=@0` passes the literal
argument `--mtime=@0`. The one exception is an unquoted `@{`, which is
`PARSE_ERR` because it is almost always a misplaced template interpolation.
Quote it (`'@{x}'`) to pass it literally. Interpolate in words with `$`
references and `(EXPR)` fragments.

Values carry their dependencies, capabilities, failures and authored source
spans. A statement-leading `(EXPR)` is an expression statement and never becomes
a command head by adjoining a suffix. Use a reference at the command head for a
computed executable. Mixed words with a literal or reference prefix may supply
computed executable paths, subject to executable validation and dependency
tracking. Argv values and captured output are never compiled as source.

### Capture with `$(…)`

`$(COMMAND)` runs its content, one process graph or command list in action
context, and its output to the enclosing output sink becomes the value:

```kash
let revision = $(git rev-parse --short HEAD)
echo revision=$revision
```

`$(…)` is an expression atom wherever an expression is accepted: applications,
lists, records, lambdas, `let` right-hand sides, top-level definitions of Kame
programs and string interpolation.

```kame
revision = $(git rev-parse --short HEAD)
banner = "Revision: @{$(git rev-parse --short HEAD)}"
```

The expression parser keeps a capture node and delegates its content to the
Kash parser. Embedded Kash may itself contain value fragments and nested
captures; each parser keeps its authored spans. Parsing never depends on grants,
host availability or execution phase. Evaluation requires a context that permits
process execution (Execution context, below).

`"$(git status)"` in a Kame expression string is literal text; only `@{EXPR}`
interpolates there. Kash double-quoted words allow captures, as specified below.
In a foreign-shell body, `$(…)` belongs to that shell.

A failed capture raises in its enclosing expression. Recover inside the capture
with a command list, or inspect the outcome with `$?(…)`:

```kash
let revision = $(git rev-parse --short HEAD || printf unknown)
```

Capture takes stdout directed to its output sink, including explicit `>&out`,
and does not also forward it to the enclosing stdout. Explicit `>&err` stays
live and is excluded from capture; it does not redirect executable stderr.
Sequential command lists share one cumulative byte limit and keep output from
failed operands when `||` selects a fallback. The result is a string: stdout must
be valid UTF-8 and is preserved exactly, including trailing newlines and an
empty result. No newline stripping or whitespace splitting occurs. Leading and
trailing spaces and tabs, embedded line endings, CRLF and newline-only stdout
keep every byte. Identifier and path conversion belongs to an explicit consumer:
`strip` removes all surrounding Unicode whitespace, and `line TEXT` removes
exactly one terminal LF and keeps any preceding CR. A consumer must not infer a
terminator that could be part of the payload. Any newline-removal helper is
opt-in and cannot change capture framing. Binary capture is outside this
specification.

The default capture limit is 1 MiB of stdout per capture. `--capture-limit` sets
a positive byte limit for the invocation. Exceeding it cancels and reaps the
captured graph and fails with `CAPTURE_LIMIT`; invalid UTF-8 fails with
`CAPTURE_ENCODING`. Truncated output is never returned as a successful value.
Async execution is invalid inside a capture.

If the final stdout is redirected to a file, the capture is an empty string.
Capture and redirection do not duplicate the stream.

A capture is a process-like unit: `exit N` inside it ends only the capture,
and `set env/…` or `cd` inside it never reach the caller.

### Outcomes with `$?(…)`

`$?(COMMAND)` runs its content like `$(…)` but returns the outcome record
(`049`) instead of stdout, with output streamed as for a command statement. It
never raises for a process or function failure: an unaccepted nonzero exit, a
signal, a launch failure, a timeout or a failure raised inside a called function
is reported with `state: :failed` and a non-`nil` `error`. Capability denial,
phase violations, parse errors and cancellation of the enclosing invocation
propagate. `$?` must be followed immediately by `(`.

`$?(…)` is the way to test a command in a condition:

```kash
if not $?(git diff --quiet).ok
	printf "tree is dirty\n" >&err
```

## Command values and arguments

A command statement lowers conceptually to a process operation:

```kash
git status --short
```

```kame
run! "git" "status" "--short"
```

Command arguments are typed value positions, not shell word-expansion
positions. A string supplies one argument even when it contains whitespace.
Values are never re-split, implicitly globbed or reparsed as shell syntax. A raw
`*.go` is therefore literal; a filesystem wildcard is an explicit value, such as
`(files ./src/**/*.go)`.

### Command position

The head of a command statement resolves syntactically and then by name
(`055` §6):

1. A reserved word is a statement keyword. To run an executable with a reserved
   name, write `x/NAME`.
2. Lexical callable bindings, including imported names. A lexical binding to a
   non-callable is `EXPR_INVALID`; it never falls through to an executable.
3. The closed set of builtins:

   | Builtin | Available | Meaning |
   | --- | --- | --- |
   | `cd [DIR]` | everywhere | Change the unit's working directory. No operand means `$HOME`, `-` the previous directory; `~`/`~/…` expand to `$HOME`. |
   | `wait [JOB…]` | everywhere | Wait for owned async work and return its outcome. |
   | `kill [-SIGNAL] JOB…` | everywhere | Signal owned async work. |
   | `jobs`, `fg`, `bg`, `alias`, `unalias`, `history`, `option`, `explain` | interactive | Session commands of `052`, `053` and `054`. |

   An interactive-only builtin in a script or rule body is `PARSE_ERR`.
4. Executables, through the tool resolver, using the `PATH` of the unit's
   environment store.

The standard library is not consulted in command position: `basename $f .c`
runs the executable, while `(basename f)` calls the library function.
`std/NAME`, `x/NAME`, `$REFERENCE`, `${REFERENCE}`, `$...REFERENCE` and path
heads such as `./tool` are explicit heads. In value and declaration contexts,
step 4 is never attempted, so an unknown command fails before any effect.

### Words, quoting and argv conversion

Unquoted spaces and tabs separate words. Adjacent literal and substitution
fragments form one word, as in `revision=$revision`, `${name}.c` and
`--config-path=(js-biome-config ())`. Double quotes group fragments into one word
and allow `$` references, `$(…)` and `$?(…)`. Single quotes group literal text
into one word: everything between the delimiters is literal, including
backslashes, dollar signs, parentheses, operators and double quotes. A single
quote cannot occur inside a single-quoted fragment; end the fragment and use an
escaped or double-quoted single quote. Adjacent quoted and unquoted fragments
form one word. An empty quoted word is one empty argument. An unclosed quote is
`PARSE_ERR`. These are command-word rules only; expression strings follow
`055` §3.3.

Outside quotes, backslash escapes the next non-newline character; backslash plus
newline continues the statement without inserting text. Inside double quotes,
`\"`, `\\`, `\n`, `\r`, `\t` and `\$` are supported; other escapes are errors.
Quoted or escaped operators are literal.

Unescaped `(` outside quotes starts a value fragment, including in the middle of
a word; it is never a subshell or literal punctuation. Escape a literal
parenthesis or quote its text. Parentheses inside double quotes are literal:
`"--config-path=(js-biome-config ())"` calls nothing. References and captures
inside those quotes are unchanged. A fragment outside quotes may adjoin a quoted
fragment in the same word.

A word consisting solely of one unquoted substitution keeps its typed value
until argv conversion:

| Value | argv contribution |
| --- | --- |
| String, path or pattern | One argument containing its text, even when empty. |
| Integer or float | One argument using Kame's canonical numeric spelling. |
| Boolean | One argument, `true` or `false`. |
| List | One argument per scalar item, in order; an empty list contributes none. |
| `nil` | No argument, like an unset shell variable. A `nil` executable is an error. |
| Bytes, record, callable, resource, process handle, outcome or nested list | `EXPR_INVALID`. |

Quoted substitutions and words combining literal and substitution fragments
require scalar values from the first three rows, or `nil`, and produce exactly
one argument; a `nil` fragment contributes empty text. They never splice lists:
`$files` splices a flat list, while `"$files"` fails when `files` is a list. A
standalone `(modules ())` may splice its flat list result, but
`--modules=(modules ())` rejects that result rather than joining or splicing it.
An empty scalar in a mixed word contributes empty text; whitespace in a scalar
stays inside the one argument. A `files` result supplies explicit argv items
without shell globbing. In double quotes, `$` is literal only when escaped or
not followed by substitution syntax; single quotes disable substitutions.

Word fragments evaluate in authored order and keep completed values across
suspension, without replaying calls or effects. Result bytes are inert data:
spaces, operators, quotes, braces, parentheses and substitution-looking text
cannot create words, commands or source. Invalid values fail before launching
the containing process graph, under the existing capability and phase policy.

Parsing and formatting keep fragment boundaries and authored spans. Formatting
must not quote a value fragment into literal text, turn literal parentheses into
an expression, or change standalone typed splicing into mixed-word scalar
conversion. Quoted and escaped literal fragments may be normalized only while
keeping those distinctions.

An explicit `$...REFERENCE` (or `$...{REFERENCE}`) requires a flat list and
contributes one argument per scalar item, keeping order and argument bytes. It
must be a standalone unquoted word; quoted and mixed words, stage setup values
and redirection paths cannot use this form. Empty lists contribute no arguments
after an executable.

At command position, explicit spreading uses the first list item as the
executable and the remaining items as its initial arguments:

```kash
let CMD = ["git", "-C", "./my project"]
$...CMD status --short
```

The command list must be nonempty and its first item a nonempty string or path.
An empty command list fails rather than promoting the next word to executable.
Later authored words append arguments; spread values are never reparsed as setup
keys, operators or source. This works in pipelines, captures, recovery, async
commands and rule bodies.

Without explicit spreading, the executable position requires one nonempty
string or path, not a list even with one item. A `$name` head that resolves to a
callable calls it; any other value names an executable. Executables, arguments,
environment entries and filesystem paths must be free of NUL bytes. Processes
execute argv directly; no shell interprets it.

Executable lookup for a name without a path separator uses the `PATH` of the
unit's environment store, as updated by `set env/PATH`, and the tool overrides
of `038`. It ignores per-stage `:PATH` overrides and lexical bindings named
`PATH`. Choosing a child environment is distinct from reading an environment
variable into a value.

### Stage setup

A command may carry setup keys before its executable, as `:KEY VALUE` pairs:

```kash
:cwd $build-dir :timeout 10 :NODE_ENV production npm test
```

The expression-level equivalent passes the same keys to `run!`:

```kame
run! :cwd build-dir :timeout 10 :NODE_ENV "production" "npm" "test"
```

Except for `:env`, each setup value is one scalar word, never a list splice. It
may combine fragments under the same rules as argv words:

```kash
:cwd ./build/(variant) :MODE release-(variant) deploy
```

`:cwd` is a directory relative to the unit's working directory; in a
[`with` scope](048-kash-practical-scripting.md), relative stage overrides compose
with that scope's directory. `:timeout` is a positive number of seconds: a
literal word such as `10` is read as a number. Boolean settings (`:tty`,
`:foreground`, `051`) read the literal words `true` and `false`; any other word
is an error. Other
keys matching `[A-Za-z_][A-Za-z0-9_]*` are environment overrides. Duplicate keys
are errors. The child environment otherwise inherits the unit's environment
store. Quoted or escaped leading colons are literal. After the executable,
colon-prefixed words are ordinary arguments. Each pipeline stage has independent
setup keys. Reserved setup keys are not environment overrides. Invalid paths,
keys, values and missing executables are errors. The final scalar must satisfy
the setting's contract: a computed `:timeout` must be a positive number of
seconds and `:cwd` a valid directory path. No list splicing or implicit record
conversion occurs.

Setup keys configure one stage only. They never change the environment store,
the working directory, later statements or executable lookup. `set env/NAME` and
`cd` change the unit (`055` §5.3).

### Computed stage environments

`:env VALUE` accepts a typed record of environment assignments:

```kash
let exports = {TOKEN: env/DEPLOY_TOKEN, MODE: "release"}
:env $exports :MODE preview deploy
```

Expression-level process applications share the contract, for example
`run! :env exports "deploy"`. The record is evaluated only when that stage is
reached at execution time, under ordinary capability and phase checks. Parsing,
planning, dry-run, construction and unselected branches neither fetch secrets
nor execute its effects. Reading values through `env/` still requires the
relevant environment grant; supplying a record grants no read or run authority.

Keys must match `[A-Za-z_][A-Za-z0-9_]*`, and values must be strings without NUL.
Empty strings are valid; `nil`, numbers, booleans, collections, bytes and
callables are not converted. The empty record `{}` is a no-op, not an empty
child environment. The whole record is validated before any assignment is used
or the graph launches. Invalid values report `EXPR_INVALID` at the authored
setup boundary, never with evaluated record data.

The child starts with the unit's environment store. `:env` record entries and
ordinary `:KEY` overrides apply in authored setup order; the last assignment to
a name wins. Duplicate authored setup keys, including two `:env` keys in one
stage, are syntax errors. Record keys such as `cwd` and `timeout` are
environment names, not process settings. Overrides belong to that stage only:
they do not change later statements, other stages, the environment store,
executable lookup or the parent unit. A scope overlay that changes later
children and in-scope lookup is specified in `051-kash-session-authority.md`;
it is not a stage override.

Suspension keeps evaluated assignments and their effects without replay;
dependency tracking keeps the computations and observations used to obtain them
for freshness and watch invalidation. Environment payloads go to the host for
launch, but computed values are omitted from diagnostic messages and normal
human and JSON lifecycle traces, and evaluated records are never embedded in
error causes. Authored source excerpts, explicit output, argv values and child
output are not secret channels and are not redacted automatically; callers must
not print or place secrets in them.

The expression-level `run!` also accepts `:async` with a boolean value; `true`
returns a process handle instead of waiting for completion. This key is reserved
and rejected in raw stage setup: raw async execution uses terminal `&` on the
whole graph. `pipe!` constructs and executes its stages together rather than
waiting on each stage; stage-level async flags are invalid within it.

## Pipelines and redirection

A process graph may be linear or use the groups, channels, framing and
multiplexors of [Kash streaming](042-kash-streaming.md), which refines the
linear-pipeline rules below. Neither form is a shell subshell.

A command pipeline is a first-class process graph:

```kash
generate | transform | publish
compile < sources.txt > output.log
compile >> output.log
```

It lowers conceptually to a streaming graph:

```kame
pipe! (run! "generate") (run! "transform") (run! "publish")
```

A stage may be an executable or a function call. Stages connect through the
graph rather than by buffering intermediate output. `<` supplies standard input,
`>` replaces an output file and `>>` appends to it.

Redirections belong to one stage and take one nonempty scalar path word,
including mixed words such as `tool > ./out/(name).txt` or
`tool < (input-name).txt`. Paths resolve relative to that stage's working
directory. Spaces in a fragment result stay inside the single path; they create
no argv entries or operators. Empty paths, NUL, lists and non-scalars are errors.
The same authored spans, kept operand evaluation and capability checks as argv
words apply. Each stage has at most one stdin and one stdout redirection. A
pipeline edge and a redirection cannot both supply the same stream, so in a
linear pipeline `<` is allowed only on the first stage and `>`/`>>` only on the
last. Conflicts are errors, never silent overrides. `>` creates or truncates;
`>>` creates or appends. Reserved `>&out` and `>&err` route stdout to the
enclosing presentation sinks (`042`, `047`); neither redirects the executable's
own stderr. Redirecting executable stderr, arbitrary descriptor duplication and
here-documents are not defined by this specification.

All stages start as one graph and communicate through bounded streaming pipes
with backpressure. Intermediate stdout is not collected. Every stage's stderr is
forwarded; final stdout is forwarded unless redirected or captured. Commands
inherit invocation stdin. When the source itself is stdin, commands without `<`
receive closed stdin rather than consuming source text. Captures without `<`
also receive closed stdin.

Under the default fail-fast policy, the graph fails if any stage exits nonzero
or by signal, including SIGPIPE, or if a function stage fails. A function stage
cancelled because its consumer closed early is a broken pipe, not a failure.
Owner-wide failure collection follows [Kash streaming](042-kash-streaming.md);
it does not accept failed exits or defer fatal failures. For a linear pipeline,
the aggregate status is the rightmost failing stage's status, or zero if every
stage succeeds; all per-stage outcomes are kept. A signal contributes
`128 + signal` to the aggregate status, and a function stage contributes its
derived status. Launch failures and timeouts are failures, not zero exits. On
launch failure, timeout, cancellation or capture-limit failure, all remaining
stages are terminated and reaped. A stage timeout terminates the whole graph;
the invocation timeout also bounds the complete source execution, including
async joins.

Arguments and capability checks for all executable requests and redirection
paths are validated before any stage starts or any file is created or
truncated. Input redirections record file dependencies; output redirections
record write effects. An ordinary path argument does not declare a dependency.
Capabilities do not sandbox access performed internally by a granted
executable. Launch-time failures may occur after output files have been opened;
no filesystem rollback or atomic-output guarantee is implied.

The value pipeline `|>` is a separate operator, available inside value
fragments and other expressions:

```kash
emit (sources |> map render)
```

`|` between command stages is a process stream; `|>` inside an expression is
the value pipeline of `055` §4.4. Neither converts into the other.

## Process lifecycle and recovery

### Async execution

Appending `&` starts a process graph asynchronously and yields a handle:

```kash
watch-assets &
```

`&` applies to the entire graph or recovery expression. The handle is available
once the initial graph has started, not at completion, and the next statement
may then run. The invocation owns all async work: on normal completion it joins
outstanding work, and an unhandled async failure fails the invocation. On
failure, interruption, `exit` with a nonzero status or shutdown it cancels and
reaps outstanding graphs. There is no implicit detachment or daemonization.

`wait` observes a handle's completion and returns its outcome; repeated waits
are repeat-safe. Handles are invocation-local, cannot be serialized and cannot
be reused by another invocation. For an async recovered expression, the handle
covers the fallback as well as the initial graph. Terminal `&` is statement
syntax. In expression position, including conditions, value operands and `let`
right-hand sides, the group `$&(CMD)` starts the same graph and returns its
handle:

```kash
let job = $&(watch-assets)
let completion = $?(wait $job)
```

`$&(…)` has the same ownership, cancellation and joining rules as `CMD &`, and
its content cannot itself end in `&`.

`let` is eager: the first statement starts the job and the second waits for it.
Waiting observes an async failure for invocation accounting, so an inspected
failure is not reported again by the final join. Unobserved failures remain
invocation failures. Capability denial and cancellation cannot be handled by
Kash recovery.

### Acceptance, command lists and recovery

Commands normally fail on a nonzero exit status. Terminal `?` accepts that exit
while keeping the command's diagnostic output; it does not silence stderr:

```kash
grep needle missing-file ?
```

`A && B` and `A || B` form a synchronous, left-to-right command list at equal
precedence. `&&` runs its right operand after effective success; `||` runs it
only after an ordinary process exit or signal, or after a function failure
caused by one. Launch, timeout, capture, capability, evaluation and operator
failures do not select `||`. A list returns the final executed operand's
outcome; a final unhandled failure propagates. Lists are not process-group
members, live producers or direct deferred commands. Compound lists cannot use
terminal async `&`.

`CMD ? ELSE` runs `ELSE` when the command fails:

```kash
deploy ? echo "deployment failed; continuing"
```

Acceptance handles only nonzero exits, including signal exits, of processes
and of function failures caused by one; it does not accept launch, timeout,
cancellation, argument or capability errors. An accepted invocation is
`:completed` with its raw `status` kept (`049`). Command recovery `? ELSE`
handles nonzero exits, launch failures, timeouts and function failures, but not
capability denial, cancellation, or source, argument or binding-validation
errors. A recovered failure emits no additional unhandled-failure diagnostic;
live stderr is unchanged. A fallback failure propagates.

Value recovery is the expression operator `??` (`055` §4.5). `A ?? B` yields
`A` unless `A` is `nil` or fails with a missing-lookup failure (`REF_MISSING`,
`TOOL_MISSING`, `ENV_VAR_MISSING` or a missing field); then it evaluates `B`.
Other failures, including process failures inside `$(…)`, propagate. No
fallback dependency or effect occurs unless selected:

```kash
let token = DEPLOY_TOKEN ?? ""
let config = env/APP_CONFIG ?? ./config.json
let revision = $(git rev-parse --short HEAD || printf unknown)
```

The three forms are distinct: `?` accepts a process exit, `? ELSE` and `||`
select a fallback command, and `??` selects a fallback value for a missing
lookup. Process failure is inspected as a value with `$?(…)`.

### Operator binding

From tightest to loosest, process syntax binds as follows:

1. Quoting, substitution and word composition.
2. Stage redirections.
3. Pipeline `|`, in left-to-right stage order.
4. Terminal acceptance `?`, applying to the complete pipeline.
5. Recovery `? ELSE`, right-associative; `ELSE` is another process expression.
6. Sequential `&&` and `||`, equal precedence and left-associative.
7. Terminal async `&`, applying to one complete graph or recovery expression.
8. Statement separators `;` and newline.

Outside a reference, a terminal unquoted `?` is acceptance even when attached to
a word; quote or escape it for a literal filename suffix. `?` with a following
fallback must be whitespace-separated. An unquoted `??` word in a command is
`PARSE_ERR`; `??` belongs to expressions. Operators inside quotes or value
fragments are not process operators.

`&` must be followed by a statement terminator: `first & second` requires
`first &; second` or a newline. `A && B &` and `A || B &` are rejected before
effects; separate statements such as `A &; B && C` are valid. Parentheses never
introduce subshells; comma-separated process groups and stream-operator binding
follow [Kash streaming](042-kash-streaming.md).

## Deferred cleanup

`defer COMMAND` registers a process graph or function-call command for cleanup.
`defer (EXPR)` registers one expression. Neither runs at registration. The
statement is parsed with its source before any execution, and formatting keeps
the command/expression boundary. Run an executable named `defer` with
`x/defer`.

```kash
let directory = line $(mktemp -d ./workspace.XXXXXX)
defer rm -rf $directory
defer (out! "finishing\n")
```

Registration occurs only in a reached runtime statement. Parse, format,
planning and dry-run evaluate neither deferred actions nor their operands.
Commands evaluate and validate argv, redirection paths and stage setup at
registration, keeping completed operands across suspension. This can run an
explicit capture needed to obtain an operand. It does not open or truncate
redirection destinations or launch the deferred graph or call. Typed
environments keep their secrecy, authority and validation boundaries. Captured
values are data, never generated source.

Expressions instead keep their AST, lexical scope and execution context and
evaluate the complete expression during cleanup. Block-local bindings survive
until then, and `set` assignments made after registration are visible to the
expression. Use a command operand when a value must be fixed at registration.
Expression results are discarded, never interpreted as executables or printed.

Cleanup belongs to the enclosing invocation: a rule body, a `def` call, a
capture or the top-level runner session. Branch exit does not finalize it;
nested calls have independent stacks. Actions execute once in reverse
registration order. Normal completion, `return` and `exit 0` join owned async
work before cleanup; failure, cancellation and nonzero `exit` cancel and reap
that work before cleanup starts. A deferred expression may start async work, but
the action joins it before advancing. Terminal `&` on a deferred command is
invalid. Direct `? ELSE` recovery is also invalid in this form: defer a call to
a `def` function for compound recovery. A direct `&&`/`||` command list is
likewise invalid; a deferred function may contain a synchronous list. Terminal
acceptance `?` is supported. `defer return`, `defer exit`, `defer yield` and
`defer defer` are `PARSE_ERR`. An action cannot append to its closing stack; a
called function owns a new stack.

Finalization uses a separate five-second budget per invocation stack, starting
after user processes have been reaped. Process timeouts are capped by the
remaining budget and any tighter inherited or stage limit. If the budget is
exhausted, unrun actions are reported rather than claiming successful cleanup.
This is a cooperative runtime and process deadline, not preemption of arbitrary
blocking custom host implementations. Cleanup keeps the original capabilities.

Remaining actions are tried after an action fails, while budget remains. If the
work otherwise succeeded, cleanup failure fails the invocation. Otherwise the
primary error or cancellation is kept and cleanup diagnostics are reported as
supplementary context. `?` on a deferred command accepts only its ordinary
supported exit categories; it cannot suppress timeout, capability denial or
cancellation. SIGKILL, runtime crash, unavailable host services and a forced
second interruption can prevent finalization. Remote effects are not rolled
back.

`defer` attempts authored cleanup; it does not prove filesystem ownership. Only
remove freshly created private resources whose paths were kept safely. Never
infer ownership from a glob, shared directory or predictable `.tmp` suffix.
Deferred `write!` actions execute immediately during cleanup, including in
failed rule bodies, rather than waiting for output publication. Neither deferred
actions nor `yield` publish rule outputs; publication follows `044`. Ordinary
redirection is not atomic output publication. Staged output publication and
`do clean` ownership stay separate.

Forwarded hosts must explicitly begin and pump finalization before freeing a
cancelled runtime. The shared `kame_wasm_finalization_tick` driver services both
Kame `finally!` and Kash `defer`, including cleanup request deadlines. The
`kame_wasm_finalize_begin` entry point is available for Kash-only hosts; ordinary
step and completion calls service its requests under the original grants and a
fresh cleanup cancellation and deadline context. Freeing alone cannot execute
host actions.

## Control statements

A block is introduced by a header line and holds one or more statements indented
deeper than the header (`055` §5.2). Blocks nest, and dedenting to an existing
enclosing level ends the inner block. Block headers are `if`, `elif`, `else`,
`match` and its arms, `for`, `while` and block-bodied `def`. Headers have no
trailing `:`, cannot share a line with another statement and are not
`;`-separated. Function bodies use the same rules.

A block's indentation is the leading whitespace of its first nonblank line; each
later nonblank line must begin with exactly that prefix or end the block. Tabs
and spaces may not be mixed within one block prefix, and a dedent must return to
an existing level. Blank and comment lines do not affect indentation. Empty
blocks, orphan headers, duplicate `else` and branches after `else` are errors.
`elif` and `else` align with their `if`; `match` arms are one level deeper than
`match`, and their bodies one level deeper again.

### `if`

`if`, `elif` and `while` conditions are expressions in value context, evaluated
with Kame truth: only `nil` and `false` are false; even an empty string or a
failed outcome record is true. To test a command, read its outcome:

```kash
if $?(test -f ./build/app).ok
	report "already built"
elif options.release
	gcc -O2 -o build/app src/app.c
else
	gcc -o build/app src/app.c
```

`$?(…)` in a condition reports process exits, signals, launch failures and
timeouts through `ok` without raising or emitting an unhandled-failure
diagnostic; capability denial, phase violations and cancellation propagate.
Its stdout and stderr follow ordinary forwarding and redirection rules. A bare
`$(…)` condition is a string and therefore always true unless it fails;
async syntax is invalid in conditions. Conditions and `match` subjects are
evaluated once per execution, including across suspension and resumption for
host requests.

### `match`

`match EXPR` dispatches a value against ordered arms, using the patterns of
`055` §4.5. Each arm is `PATTERN =>` followed by a block; `else =>` is the
optional fallback:

```kash
match target
	./src/{name:*}.c =>
		cc -c ${name}.c
	else =>
		report unknown
```

The subject is an expression, including `$(…)`. The first matching arm runs and
its named captures bind as names in that arm's scope, read as `$name` in words
and `name` in expressions. Each arm holds exactly one pattern: a path pattern,
string pattern or literal. Subject type rules and capture matching follow
`014`, `029` and `041`. A `let` in an arm that conflicts with a capture is an
error. A `match` without a matching arm or `else` does nothing and continues the
enclosing sequence.

### Loops

`for NAME in EXPR` iterates a list, record or stream, and `while EXPR` repeats
while its condition is true; `break` and `continue` control the innermost loop.
Each iteration has its own lexical scope. `046` and `048` refine iteration over
records and streams.

```kash
for source in files ./src/*.c
	compile $source (object-path source)
while not (ready? server)
	sleep 1
```

### Selection and control transfer

Only the selected branch runs, so an unselected branch's commands, dynamic
dependencies and effects never occur. Conditions and subjects are evaluated by
the expression evaluator and bodies by Kash, so they carry normal values,
dependencies and diagnostics. Control statements produce no value.

A `return`, `exit`, `break` or `continue` in a selected branch propagates
through control blocks to its target. It is not a branch value and cannot be
swallowed by lowering a branch or iteration to an expression callback.

## Results and lowering

### Execution context

Kash and embedded captures use the evaluation context of `005-evaluation.md`,
not a second ambient context. Its execution components are phase, capability
grants, host services, the unit's working directory and environment store,
timeout and capture policy, execution ownership and cancellation, and the
dependency and effect channels. Lexical scope determines what names mean; it is
not an authorization mechanism.

Embedded Kash inherits the caller's execution context and lexical scope. Calling
a function or demanding a lazy top-level definition introduces no implicit
grants; shared definition evaluation uses the owning program's configured policy
and cannot borrow broader grants from another execution. Unused definitions and
unselected branches perform no authorization checks or effects.

Process execution requires both an evaluation phase that permits it and the
relevant run grant. Planning and resolution reject a demanded capture with
`PHASE_INVALID`, even when a run grant exists. Missing grants produce
`CAP_DENIED`; an authorized but unavailable host service produces `HOST_FAIL`.
Neither parsing nor formatting executes a capture.

Stage setup can change process configuration but cannot broaden grants or relax
caller limits. Paths are authorized after resolving the effective working
directory. Stage timeouts and capture limits stay bounded by caller policy.
Environment reads and redirections also require their environment, read or
write grants. Writing the environment store with `set env/NAME` is not a host
environment mutation and needs no environment-write grant. Capability denial and
cancellation cannot be suppressed by Kash recovery.

A run grant authorizes direct executable requests, not shell-text evaluation.
`$(git status)` runs argv directly. Launching a shell requires an explicit shell
executable request authorized by the caller's run policy; there is no implicit
shell capability or shell fallback.

### Outcomes and lowering

A command statement produces an outcome, not an untyped string. At statement
level the outcome decides success or failure and is otherwise discarded;
`$?(…)` exposes it as a value and `$(…)` converts stdout into a string.

A synchronous graph produces an immutable outcome record; async startup produces
a handle whose `wait` yields that record. The record exposes the fields of
`055` §8.1 and the additional Result fields of `049`: `stdoutCaptured`,
`stderrCaptured`, `effectiveStatus`, `effectiveSignal`, typed capture fields and
the terminal `graph` metadata of [Kash streaming](042-kash-streaming.md). Each
stage exposes `status`, `signal` and `outcome`, in prepared stage/node order
(source order for a linear pipeline); a function stage also exposes `state`,
`value` and `error`. Commands and expression-level `run!`/`pipe!` stream output
and set both capture flags to false; the record keeps no stdout or stderr.
Streams are never converted to strings or kept without a capture request.
Outcomes are not argv scalars. The concrete core representation is
implementation-defined, but these observable distinctions are required and do
not depend on the backend.

The Kash parser owns statement, pipeline, redirection, async, recovery and
control nodes. Every embedded expression and reference keeps its expression AST
node and source span. Lowering commands to `run!`, `pipe!` and associated
process operations, and function-call commands to calls with their stdout and
stderr bound to the stage, preserves laziness of top-level definitions,
capability checks, dynamic dependencies and diagnostics. Control statements
lower to conditional and match evaluation over expression conditions and
subjects, with each branch body a nested Kash block.

Function definitions keep their parameter and body ASTs and authored spans and
are never lowered through generated source text. `return`, `exit`, `break` and
`continue` are control outcomes distinct from values, failures and suspension.
They survive branch and loop lowering and host suspension until their target
boundary performs finalization and exposes the value, outcome or failure.

Pipeline lowering is descriptive, not eager evaluation of independent `run!`
calls: the whole graph is constructed before any stage starts. Execution state
survives evaluator suspension without repeating completed statements, captures,
effects or process launches. Request correlation includes the owning execution
and generation; stale completions cannot satisfy a newer run.

## Formatting and diagnostics

Formatting preserves the parser boundaries:

- `$REFERENCE`, `${REFERENCE}`, `$...REFERENCE`, `(EXPR)`, `$(COMMAND)` and
  `$?(COMMAND)` keep their distinct forms.
- Block headers stay on their own line without a trailing `:`, and each nested
  block is one tab deeper than its header. Block indentation is always tabs,
  regardless of the generic formatter's requested space style.
- Function headers keep parameter order and rest syntax. `return`, `yield` and
  `exit` keep their operands. Formatting, cloning and AST inspection preserve
  function bodies and never convert them to source strings or external commands.
- Value fragments, `let`/`set`/`return` operands, conditions and subjects are
  formatted by the expression formatter, keeping the fragment boundary.
- `$(…)` and `$?(…)` contents are formatted by the Kash formatter, including
  when embedded in Kame expressions, and keep their enclosing expression spans.
- Formatter output is idempotent and reparses to an equivalent Kash AST.
- Command words use bare spelling when safe; quotes remain for empty words,
  whitespace, literal syntax characters, grammar-sensitive words and scalar
  substitutions whose unquoted spelling would enable list splicing.

Formatting preserves comments, scalar and list-splice distinctions, literal
operator quoting and complete reference boundaries. Recovery chains are spaced,
and terminal acceptance is emitted with preceding whitespace so it is not read
as a name suffix.

Malformed references are reference errors, malformed value fragments are
expression errors, and malformed `$(…)`/`$?(…)` contents are Kash command
errors. Diagnostics identify the enclosing source and the precise embedded span,
including a fragment inside a command word inside a rule body inside a
declaration loop (`055` §14). Process diagnostics carry status, signal, stage
and source information but do not embed captured stdout or stderr in causes;
live forwarding and explicit capture stay separate channels.

## Acceptance tests

Functions and control transfer:

- `def` supports zero, fixed and rest parameters, lexical capture, forward
  references among block functions, shadowing, nested definitions, and calls in
  both expression and command position. Registration and unused bodies have no
  effects and need no grants.
- Command position resolves lexical callables, then builtins, then executables;
  the standard library is never consulted; a lexical non-callable head is
  `EXPR_INVALID`; `x/NAME` and `std/NAME` heads bypass resolution;
  interactive-only builtins are `PARSE_ERR` outside a session.
- A function called as a command receives bare words as strings and `$x`/`(EXPR)`
  as typed values; its stdout joins pipelines, redirection and `$(…)`; its return
  value is discarded at statement level and never written to stdout.
- Each invocation gets fresh locals, argument frames and cleanup/async
  ownership. Nested and repeated calls share no local state; resumed calls do not
  replay statements or accept stale completions.
- `return VALUE` returns the value, bare `return` and fall-through return `nil`,
  and the operand evaluates once across suspension. Return from nested branches
  and loops skips remaining body work, runs call-local cleanup once in LIFO
  order, joins owned work and resumes only the immediate caller. Unselected
  returns do not evaluate their operands. `return` outside a `def`, rule body or
  block definition, `return EXPR` in a generator, and `defer return` are
  `PARSE_ERR`.
- `$?(f x)` yields the same outcome fields as a process. Derived status is 0 for
  completed calls, the failing process's status or 1 for failed calls; a
  cancelled function stage does not fail its pipeline and runs its cleanup.
- A generator yields elements lazily, is `:suspended` while waiting, and is
  cancelled with cleanup when its consumer stops early.
- `exit N` ends the innermost process-like unit with status `N`, runs every
  unwound cleanup stack innermost first, and inside `$(…)` or `$?(…)` ends only
  the capture. Out-of-range statuses fail `EXPR_INVALID`.
- Invalid `def` headers, empty or misindented bodies, duplicate bindings and
  misplaced control transfers produce precise authored-source diagnostics.
  Parse/format/clone/AST inspection preserves function blocks, parameters,
  comments and control statements on both hosts.
- List spread, `line` framing, interpolated path construction, boolean probes
  and `false || printf "fallback\n"` need no implicit or explicit shell.
  Ordinary capture still preserves every stdout byte.

Bindings and environment:

- `let` evaluates once when reached; `set` assigns only existing, uncaptured
  `let` bindings of the same invocation; `echo a = b` and
  `echo revision=$revision` are commands.
- `set env/NAME` and `cd` are visible to later statements, lookups and processes
  in the same unit, including after the setting function returns, and never
  reach the parent unit; `set env/NAME = nil` unsets; executable lookup follows
  the updated `PATH`; stage setup keys change none of these.
- `$PATH` resolves a lexical binding first, then the environment store; an unset
  bare `$NAME` fails with `ENV_VAR_MISSING`; `${env/NAME}` yields `nil` and
  contributes no argument when unset.

Deferred cleanup:

- Deferred commands snapshot operands once; deferred expressions keep lexical
  bindings and evaluate only during cleanup. Neither opens an output at
  registration, executes in an unselected branch or runs during dry-run.
- Top-level lazy definitions demanded by successive statements are computed
  once; distinct calls and retry attempts stay fresh.
- Cleanup is invocation-scoped, LIFO and once-only across suspension, on
  success, `return`, `exit`, failure, denied capabilities, timeout and supported
  interruption paths.
- User processes are reaped before cleanup; cleanup-started async work is
  joined. Failed cleanup does not skip later actions or mask the primary
  failure, and exhausted budgets and host limitations are reported.
- Temporary-workspace examples keep unrelated files. No automatic ownership,
  atomic-redirection, crash-finalization or remote-rollback guarantee is
  implied.

Words and fragments:

- `$name`, `$project.name` and `${name}` resolve the same bindings;
  `$name.c` resolves a field while `${name}.c` appends a suffix; `$dir/file`
  stops the reference at `/`; `${ns/name}` resolves qualified names.
- Value fragments use the expression grammar and keep values without string
  splitting. `biome --config-path=(js-biome-config ())`, `echo name=(name)` and
  arguments with fragment prefixes and suffixes parse as command words. Nested
  calls, whitespace, strings, `|>` pipes and captures keep one word and their
  original spans.
- Mixed fragment words produce one scalar argument with exact whitespace and
  empty-text behavior. Standalone list results splice; mixed and quoted list
  results, records and other invalid values fail before the containing graph
  launches.
- Mixed fragments work consistently in path-shaped words, redirection paths,
  `:cwd`, `:timeout` and individual environment setup values, including words
  that start with a fragment. Per-setting validation, stage-relative redirection
  paths, dependency tracking, no-splice rules and prelaunch checks hold; invalid
  values cannot start stages or truncate files. Typed `:env` records and `let`
  values keep the expression grammar.
- Quoted and escaped literal parentheses stay literal; references and captures
  inside double quotes keep their semantics; bare braces and `@` are literal in
  words; statement-leading `(EXPR)` is an expression statement.
- Fragments keep completed values across suspension, obey capabilities, phases,
  dependencies and executable tracking, and never reparse result bytes.
  Parse/format/parse is idempotent without turning fragments into quoted
  literals or changing typed splicing on either host.
- Command arguments containing whitespace stay one argument. A raw wildcard is
  literal; `(files PATTERN)` is an explicit discovery value.
- Quoted and escaped operators are literal, empty quotes keep an empty argument,
  and invalid escapes and unclosed quotes are rejected. Single-quoted
  substitutions, backslashes and operators stay literal, including in adjacent
  fragments; parse/format/parse keeps argument bytes and substitution
  boundaries.
- Flat lists splice only in standalone unquoted substitutions; mixed and quoted
  lists, unsupported values, empty executables and NUL bytes fail before launch.
- `$...CMD` spreads a nonempty command list; `$...args` spreads a flat argument
  list, including empty lists. Formatting keeps the explicit marker, and a plain
  `$CMD` list in executable position is rejected.

Captures and outcomes:

- `$(COMMAND)` captures stdout and never parses its content as an expression;
  `>&out` joins the capture, while `>&err` stays live and excluded.
- Capture preserves empty stdout and trailing newlines, forwards stderr once,
  rejects invalid UTF-8 with `CAPTURE_ENCODING` and fails with `CAPTURE_LIMIT`
  rather than returning truncated output.
- `$(…)` raises on failure; `$?(…)` returns an outcome and does not raise for
  process or function failures, while capability denial, phase violations and
  cancellation propagate.
- `$(…)` and `$?(…)` parse as expression atoms in applications, lists, records,
  lambdas, definitions and `@{…}` interpolation; their contents always parse as
  Kash. Kame strings containing `$(…)` stay literal, and foreign-shell bodies
  keep `$(…)` for their shell.
- The same capture parses with or without grants and host services; demanded
  execution reports `PHASE_INVALID`, `CAP_DENIED` or `HOST_FAIL` as applicable.
- Embedded processes inherit scope and execution policy without broadening
  grants, escaping resolved path restrictions or relaxing caller limits.

Pipelines, recovery and control:

- Command pipelines stream process data; `|>` inside expressions has value
  pipeline behavior; neither converts into the other.
- Function stages take part in pipelines with derived statuses.
- Terminal `?` keeps diagnostics for accepted nonzero exits.
- `A ?? B` evaluates the fallback only for `nil` or a missing-lookup failure;
  process failures propagate through it. An unquoted `??` command word is
  `PARSE_ERR`.
- `&&`/`||` are left-associative with equal precedence and select only on
  effective success or eligible exits. Skipped operands have no effects; a
  failed unhandled list stops the sequence.
- Acceptance, command recovery and value recovery obey their distinct failure
  categories; none suppresses capability denial or cancellation.
- Output-port disposition, broadcast, error-only capture, output capture,
  zero and multiple capture sinks, and process-exit status agree on native and
  WASM.
- Pipelines exceeding pipe-buffer capacity stream without deadlock; failures in
  every stage, including SIGPIPE, contribute to aggregate failure.
- Conflicting redirections fail; denied capabilities neither launch stages nor
  truncate files; launch failures and cancellation reap every stage.
- Setup keys configure only their stage; duplicates fail, timeouts are in
  seconds, and stage cwd governs relative redirection paths.
- Async execution does not block the next statement, is joined on completion,
  and is cancelled on failure, nonzero `exit` or interruption; `wait` returns
  the handle's outcome and is repeat-safe.
- `if` and `while` conditions use Kame truth over expressions; `$?(CMD).ok` is
  `false` for eligible process exits, launch failures and timeouts without an
  unhandled-failure diagnostic.
- `match` selects the first matching arm, binds named captures in that arm, and
  falls to `else =>` or does nothing when no arm matches.
- Only the selected branch runs; unselected commands and effects never occur.
  Branch bindings and captures do not escape; unused definitions and fallback
  operands cause no effects or dynamic dependencies.
- Nested control blocks parse and format idempotently. Mixed indentation,
  invalid dedents, empty blocks, misplaced headers, trailing `:` on headers and
  `;`-separated block headers produce precise diagnostics.
- Multiple host requests and suspended sequences execute each process and
  effect once; stale completions cannot satisfy newer executions.

Hosts and invocation:

- Rule, task and service bodies are Kash by default; `shell` metadata or
  `SHELL` selects a foreign shell whose body is a rendered template.
- Native and WASM agree on argv, captures, outcomes, statuses, capabilities,
  source spans and formatting; `.kash` and `.ksh` do not affect build-file
  discovery.
- Direct Kash execution and `do run` agree for files, program arguments,
  capability defaults, cancellation and async joining; `.ksh` is the same
  language, never a system shell. Inline and stdin forms use explicit
  `--lang kash`.
