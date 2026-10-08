# Kash process scripts

Kash (`.kash`, or equivalent `.ksh`) is a process language, not a system shell
or KornShell. It embeds Kame expressions and values without converting them
to shell text. `.kmk` recipes default to shell scripts; `SHELL = kash` or
header metadata `; [shell: kash]` explicitly selects Kash after template expansion.

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

Definitions/functions are lazy. Their RHS is an expression/substitution, not
`.kmk` whitespace-separated template words; Kash RHSs additionally accept
`??` compositions.
Statements run in source order; newlines or `;` separate them. Word-start `#`
introduces a comment; `//` is literal. Branch-local definitions do not escape.

## Explicit boundaries and argv

| Form | Meaning |
| --- | --- |
| `$name`, `$project.name`, `${name}` | Look up a Kame reference. |
| `@(EXPRESSION)` | Evaluate a Kame value; `@(count files)` is application shorthand. |
| `$(COMMAND)` | Execute a Kash command/pipeline and capture stdout. |
| `$env.NAME` | Explicit lazy environment lookup, requiring an env grant. |

`$PATH` is a Kame binding, never an implicit environment lookup. Use braces to
terminate a reference before a suffix: `${name}.c` appends `.c`, whereas
`$name.c` looks up field `c`.

Kash executes argv directly:

- Scalar expansions contribute one argument even with whitespace. There is no
  implicit splitting, shell reparsing, or globbing; raw `*.c` is literal.
- A standalone unquoted list expansion splices scalar items: `$files` or
  `@(wildcard ./src/*.c)`. Empty lists contribute no arguments.
- Quoted or mixed words require scalars; `"$files"` is invalid for a list.
- Nil, bytes, records, nested lists, callables, and process/results are not argv.
- Double quotes group words and allow substitutions; empty quotes preserve an
  empty argument. Single quotes are reserved and rejected.
- `@NAME` and `@tmpl(...)` are reserved, not implemented macro substitutions.

## Process graphs

```kash
cat < ./input.txt | sort > ./output.txt
echo done >> ./log.txt
:cwd ./build :timeout @(10) :NODE_ENV production npm test
```

Process `|` streams stdout with bounded backpressure; value pipes inside
`@(...)` transform values. `<` provides stdin; `>` replaces stdout's file;
`>>` appends. Redirections belong to a stage and cannot conflict with a pipe:
only the first stage may use `<`, only the last may use `>`/`>>`. Stderr
redirection, descriptor duplication, and heredocs are not defined.

Setup pairs precede the executable. `:cwd` sets stage cwd, `:timeout` is in
seconds (unlike CLI `--timeout` in milliseconds), and other valid keys override
stage environment entries. An ordinary filename argument does not declare a
dependency; an input redirection does. Grants do not sandbox an executable's
internal filesystem access.

Capture preserves valid UTF-8 stdout exactly, including empty output and trailing
newlines; stderr remains live. Capture is limited (default 1 MiB), never silently
truncated. Pipelines fail on any stage's nonzero/signal exit, including SIGPIPE;
aggregate status is the rightmost failure. Failures/cancellation reap owned work.

## Control, recovery, and ownership

```kash
if test -f ./output.txt
	echo exists
elif @(options.release)
	echo release
else
	echo missing

match @(target)
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
- To bind a handle, use `job = @(run :async :true "worker")`, then demand
  `@(await job)` as appropriate. Both definitions remain lazy.
- Expression-level `(run "command" args...)` and `(pipe (run ...) (run ...))`
  use the same graph executor. Synchronous results expose `status`, `signal`,
  `stages`, `stdoutCaptured`, and `stderrCaptured`; streams are live, not retained.
- `await` is repeat-safe. Unobserved async failures fail the final invocation join;
  source failure/interruption cancels and reaps every outstanding graph.
- `&&`, `||`, shell subshell grouping, and single-quoted shell strings are not
  supported. Use Kash control statements or Kame special forms.

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
