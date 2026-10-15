# Kash process scripts

Kash (`.kash`, or equivalent `.ksh`) is a process language, not a system shell
or KornShell. It embeds Kame expressions and values without converting them
to shell text. `.kmk` recipes default to shell scripts; `SHELL = kash` or
header metadata `; [shell: kash]` selects direct Kash parsing, without a document-template pass.

Native and WASM execution implement definitions, command words, substitutions,
pipelines, redirections, stage setup, control blocks, recovery, and invocation-owned
async graphs. Both use the unified `do run` runner.

```kash
name = "Ada Lovelace"
files = [./one.txt ./two.txt]
revision = $(git rev-parse --short HEAD)

echo "Hello, $name"
cat $files | wc -l
echo revision=$revision
```

Definitions/functions are lazy. Their RHS uses the same expression grammar as
`.km`/`.kmk`; Kash RHSs additionally accept
`??` compositions.
Statements run in source order; newlines or `;` separate them. Word-start `#`
introduces a comment; `//` is literal. Branch-local definitions do not escape.

A statement beginning with `(EXPRESSION)` evaluates for effects, discards its
result, and propagates failure. Nil, records, and process-result records are not
executables and do not print implicitly. There is no `do (EXPRESSION)` form.
For a computed executable, bind its value and use `$command` at the command head.

## Explicit boundaries and argv

| Form | Meaning |
| --- | --- |
| `$name`, `$project.name`, `${name}` | Look up a Kame reference. |
| `(EXPRESSION)` | Evaluate an ordinary Kame expression, such as `(count files)`. |
| `$(COMMAND)` | Execute a Kash command/pipeline and capture stdout. |
| `$env.NAME` | Explicit lazy environment lookup, requiring an env grant. |

`$PATH` is a Kame binding, never an implicit environment lookup. Use braces to
terminate a reference before a suffix: `${name}.c` appends `.c`, whereas
`$name.c` looks up field `c`.

Kash executes argv directly:

- Scalar expansions contribute one argument even with whitespace. There is no
  implicit splitting, shell reparsing, or globbing; raw `*.c` is literal.
- A standalone unquoted list expansion splices scalar items: `$files` or
  `(wildcard ./src/*.c)`. Empty lists contribute no arguments.
- Quoted or mixed words require scalars; `"$files"` is invalid for a list.
- Nil, bytes, records, nested lists, callables, and process/results are not argv.
- Double quotes group words and allow substitutions; empty quotes preserve an
  empty argument. Single quotes are reserved and rejected.
- Legacy unquoted `@(...)`, `\@(...)`, and template directives are errors.
  Quoted argument data containing `@(...)` remains literal. Expression strings
  and explicit document templates retain their interpolation rules.

The entire body is parsed before execution, including unselected branches.
Generated Kash is explicit: construct `(kash TEXT)`, optionally using `render`
to deliberately generate that text, then call the resulting callable. Argument
values and captured stdout are never reparsed. Use references and typed
expressions directly; JSON serialization and escaping helpers are unnecessary.

### Migrating recipes

Replace `@(name)` and `\@(name)` with `$name`, and expression boundaries such
as `@(join ":" files)` with `(join ":" files)`. Replace document `@if`/`@for`
generation with Kash control or explicit source construction. For a command
represented as an argv list, separate its executable from its arguments:

```kame
SHELL = kash
command = ["printf" "<%s>" """@(literal)""" "two words"]
show :
	executable = (first command)
	arguments = (slice 1 command)
	$executable $arguments
```

Do not serialize argv back into source. Keep `@(...)` in system-shell recipes,
document templates, and Kame expression strings where interpolation is intended.

## Executable dependencies

Every reached Kash stage automatically resolves and tracks its executable,
including literal/dynamic command heads, pipelines, captures, and async work.
Bare names use the driver's startup `PATH` and `--tool NAME=PATH` overrides;
explicit paths resolve from the stage working directory. Stage environment
`PATH` overrides affect the child, not this lookup. Missing/non-executable heads
report `TOOL_MISSING`. Run grants still apply; internal executable checks do
not grant user filesystem-read authority.

`aws ...`, `$x/aws ...`, and `${x/aws} ...` use the same named tool resolver;
`(tool "aws")` obtains a tracked executable value outside command position.
Unreached branches acquire no runtime tool dependencies. External-shell command
names remain shell-resolved unless explicitly rendered through `@(x/NAME)`.

Tracking a wrapper does not reveal its subprocesses: `bun x tsc` tracks Bun,
not an inferred package/compiler graph. Declare scripts and other relevant
files as inputs when their contents affect the result.

## Process graphs

```kash
cat < ./input.txt | sort > ./output.txt
echo done >> ./log.txt
:cwd ./build :timeout (nop 10) :NODE_ENV production npm test
```

Process `|` streams stdout with bounded backpressure; value pipes inside
`(...)` transform values. `<` provides stdin; `>` replaces stdout's file;
`>>` appends. Redirections belong to a stage and cannot conflict with a pipe:
only the first stage may use `<`, only the last may use `>`/`>>`. Stderr
redirection, descriptor duplication, and heredocs are not defined.

Setup pairs precede the executable. `:cwd` sets stage cwd, `:timeout` is in
seconds (unlike CLI `--timeout` in milliseconds), and other valid keys override
stage environment entries. An ordinary filename argument does not declare a
dependency; an input redirection does. Grants do not sandbox an executable's
internal filesystem access.

`:env $EXPORTS` or `:env (EXPRESSION)` supplies a typed environment record,
also accepted by `(run! :env EXPORTS ...)`. Keys must match
`[A-Za-z_][A-Za-z0-9_]*`; values must be strings without NUL. Empty strings are
valid, and an empty record (for example `(parse-json "{}")`) leaves inheritance
unchanged. No scalar conversion or list splicing is performed. The entire record
is validated before launch, with diagnostics that omit evaluated values.

Record assignments and ordinary `:KEY` overrides apply in authored order, last
assignment wins. They affect only that stage, not sibling stages, later commands,
Kame's `env` namespace, or tool lookup through startup PATH. Record evaluation
occurs only on reached runtime stages; dry-run defers it, and ordinary read/env/run
grants still apply. Effects and completed values survive suspension without replay.
Computed values are excluded from normal diagnostics/lifecycle traces, but callers
must still avoid placing secrets in authored literals, argv, or explicit output.

Capture preserves valid UTF-8 stdout exactly, including empty output and trailing
newlines; stderr remains live. Capture is limited (default 1 MiB), never silently
truncated. Pipelines fail on any stage's nonzero/signal exit, including SIGPIPE;
aggregate status is the rightmost failure. Failures/cancellation reap owned work.

Spaces, tabs, embedded line endings, CRLF, and every terminal newline are data.
Use `strip` explicitly only when an identifier's contract permits removing all
surrounding whitespace. For a producer with one terminal LF, use
`(if (ends? "\n" TEXT) (slice 0 -1 TEXT) TEXT)` to remove only that byte. Captured
paths may themselves end in whitespace/newlines; do not trim them implicitly.

## Named execution context

| Name | Compact form | Value |
| --- | --- | --- |
| `inputs` | `@<*` | Normal rule inputs, without order-only inputs. |
| `outputs` | `@>*` | Declared output identities, including named task outputs. |
| `inputs/changed` | `@<?` | Changed normal file inputs in a file recipe. |
| `args` | `@*` | Nearest function/Kash-call arguments or script operands. |

These are typed lists with the same frame and availability rules as the compact
forms. Lexical definitions/parameters may shadow the ordinary names. Empty
available lists are valid; unavailable context reports `SEL_NO_CONTEXT`.
`inputs/changed` is unavailable in task/service frames and during planning or
resolution (`PHASE_INVALID`). `args` is empty for a build with no operands; named
target parameters remain their own bindings. Index with `args.0` or
`inputs/changed.0`. Slash-separated references are names, not paths: filesystem
paths require `/`, `./`, or `../`.

```kame
SHELL = kash
./output : ./input
	cat $inputs > (first outputs)
```

## Control, recovery, and ownership

```kash
if test -f ./output.txt
	echo exists
elif (nop options.release)
	echo release
else
	echo missing

match $target
	case ./src/{name:*}.c
		echo $name
	else
		echo unknown
```

Indent nested bodies consistently; keywords occupy their own lines. Command
conditions are true on exit zero; expression conditions use Kame truth. `$(...)`
is not an `if` condition. Only selected branches execute; `match` captures are
scoped to the selected arm.

- `CMD ?` accepts a nonzero process exit but leaves live stderr visible.
- `CMD ? FALLBACK` runs a fallback process on recoverable command failure.
- `LEFT ?? RIGHT` recovers a nil/failed value in Kash RHSs, not raw commands or
  inside Kame expressions. It is distinct from Kame's unknown-reference `?` form.
- Recovery never suppresses capability denial or cancellation.
- Terminal `&` starts the entire graph asynchronously; normal session completion
  joins outstanding work. It is not daemonization or detachment.
- To bind a handle, use `job = (run! :async :true "worker")`, then demand
  `(await job)` as appropriate. Definitions remain lazy.
- Expression-level `(run! "command" args...)` and `(pipe! (run! ...) (run! ...))`
  use the same graph executor. Synchronous results expose `status`, `signal`,
  `stages`, `stdoutCaptured`, and `stderrCaptured`; streams are live, not retained.
- `await` is repeat-safe. Unobserved async failures fail the final invocation join;
  source failure/interruption cancels and reaps every outstanding graph.
- `&&`, `||`, shell subshell grouping, and single-quoted shell strings are not
  supported. Use Kash control statements or Kame special forms.

Cancellation/process reaping is not filesystem or remote rollback. `if` and
fallback handle recoverable process failure, not cancellation or capability
denial; they are not general finalizers. A `clean` target is explicit work, not
an automatic cleanup callback. Ownership-safe temporary files and stale artifact
cleanup need a separate contract before adding structured cleanup syntax. See
[SDK migration boundaries](../../../sdk-migration-boundaries.md) for concrete
use cases and the division between language capabilities and SDK/vendor policy.

## Run and inspect

```sh
kame do run ./publish.kash -- preview
kame do run --lang kash -c 'echo hello'
kame do parse --lang kash ./publish.kash
kame do fmt --lang kash -n ./publish.kash
```

Kash-first sessions use build-like defaults; Kash appended to a value-first
session inherits its restricted policy and needs explicit grants. Processes
inherit scope, cwd, limits, and authority; changing parser cannot broaden them.
Parsing/formatting never executes capture. Named entries are not accepted for
Kash; program arguments follow `--`. Check backend support before claiming a
standalone/process feature works, and never silently fall back to a shell.
