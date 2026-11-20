# Operational Kash cookbook

For this checkout, run `make build/kame.debug dist-wasm`, then use
`build/kame.debug` or `node dist/kame.js` in place of `kame`.
See the [Kash reference](skills/kame/ref/kash.md) for syntax and process topology.

A complete, harmless example is
[`examples/kash/04-operations.kash`](../examples/kash/04-operations.kash).
Run it from a disposable writable directory. It creates a private workspace and
removes it with `defer`; no credentials, installation or network are involved.

## Interactive shortcuts and login setup

In `~/.config/kash/init.kash`:

```kash-repl
alias ll = ["ls", "-lah"]
alias gs = ["git", "status", "--short"]
```

Then type `ll` or `gs`, without `$...`. Use `alias` to list shortcuts and
`unalias ll` to remove one. Declarations are typed argv prefixes, not shell text.
Format initialization safely with
`kame do fmt --lang kash -i ~/.config/kash/init.kash`. Only these two files are
read; nothing else in `~/.config/kash/` is loaded.

For login-only environment setup, use `~/.config/kash/profile.kash`:

```kash
set env/PATH = str HOME "/.local/bin:" PATH
```

`kash --login -i` loads the profile before interactive initialization.
This example needs separate grants to read `HOME` and `PATH`.

```sh
kash --login --allow-env=HOME --allow-env=PATH --allow-run -c 'printenv PATH'
```

The session PATH affects later tool lookup. Ordinary `kash -c COMMAND`
starts without a profile or interactive initialization file.
See [login-shell setup and limitations](ref-kash.md#startup-file-and-prompt)
before changing your account shell.

## Typed command prefixes

```kash
let prefix = []
let command = [...prefix, "printf", "<%s>", "two words", ""]
$...command tail
```

The optional prefix may be empty or contain a wrapper and its arguments; the
final list must start with an executable.
Spreading preserves empty arguments, quotes, newlines and spaces, rejects nested
lists and never interprets argument bytes as source. CLI callers can supply
closed list data with `--set`, not joined shell strings.

Raw `*.c` is literal. Bind `let sources = ./src/*.c` and use `$...sources`
to pass each match as an argument. Quoted/mixed substitutions must
be scalar. `${name}.suffix` appends text; `$name.suffix` performs field lookup.
Double quotes group words and allow reference and capture substitutions.
Single quotes group literal text. Parenthesized expressions compute Kame values.

## Accept selected exit statuses

```kash
let result = $?(accept-exit: [1] tidy -o ./staged.html ./input.html)
if result.state != :completed
	(fail! "Tidy failed; do not publish its output")
```

This policy accepts status 1 and keeps its raw value in `result.status`.
`result.state` is `:completed` for an accepted exit. `result.ok` still tests
raw status zero. Other exits, signals, launch failures, and timeouts fail
this check. Capability denial propagates from the capture.

`CMD ? FALLBACK` runs a fallback on recoverable process failure. Terminal
`CMD ?` accepts an ordinary nonzero exit. Use `A ?? B` for a missing lookup or
nil in an expression. To test a command in a condition, use
`$?(COMMAND).ok` or inspect its state when applying an acceptance policy.

Pipelines account for each stage's failures, including SIGPIPE. Inspect
the aggregate outcome or its stage records before publishing an output.

## Sequence commands and route output deliberately

```kash
compile && verify && publish
probe || use-cache

let message = $(printf "payload" >&out && printf "progress\n" >&err)
printf "captured=%s\n" $message
```

Lists run left to right at equal precedence. `&&` selects the next command after
effective success; `||` handles an ordinary process exit or signal, not launch,
timeout, transport/capture, capability, evaluation, or operator failures. Use `?`
when an exit should be accepted and inspected. Failed operand output remains in
capture if a fallback runs; the capture limit applies cumulatively across the list.

`>&out` routes command stdout to the current output sink, so inside `$(...)` it
is captured. `>&err` sends stdout to the error sink and stays outside capture; it
does not redirect a process's own stderr. These are presentation sinks, not named
channels or descriptor duplication. The example writes `captured=payload` to
stdout and `progress` to stderr.

An unhandled executable exit from explicit `do run` preserves its process status;
signals use `128 + signal`. Successful acceptance or recovery exits zero. For
example, `kame do run --allow-run --lang kash -c 'sh -c "exit 42"'` exits 42.
Build-target failure aggregation remains separate.

## Finite work and bounded retry

For a multi-command body, iterate directly over a typed list:

```kash
for file in files
	checker $file
	verifier $file
```

Items run in order, stop on failure, and have fresh lexical scopes. `defer`
remains invocation-owned, not per-iteration cleanup. For a single callback or
value-producing transformation the existing expression pattern remains useful:

```kash
(map (file => run! "checker" file) files)
```

Callbacks run in order and stop on failure. To collect failures, map an authored
callable that returns `$?(COMMAND)`, then inspect its results. Use `reduce` with a
small accumulator when retaining all callback results is unnecessary.

```kash
def probe command...
	return $?($...command)
let result = reduce (previous number =>
	if previous.ok then previous
	elif previous.signal == 0 and previous.status == 75 then probe "safe-idempotent-probe" number
	else fail! "non-retryable failure"
) {ok: false, status: 75, signal: 0} [1, 2, 3]
if not result.ok
	(fail! "retry budget exhausted")
```

Only the explicitly chosen transient status is eligible; success stops new
attempts. Each call is fresh. Repeatedly referencing one lazy result is not
retry; an unused definition has no effects. Suspension resumes without replay.
CLI recipe retry is a different policy. Never apply either policy to deployments
without explicitly establishing that repeating the operation is safe.

`while $?(probe --pending).ok` checks a fresh command on each visit. Do not put the probe
in a lazy outer definition and expect repeated lookup to refresh it.

## Temporary workspaces and `defer`

```kash
let workspace = line $(mktemp -d ./workspace.XXXXXX)
defer rm -rf $workspace
```

`mktemp` promises one framing LF here: remove only that LF, not arbitrary path
whitespace. Command registration demands/snapshots the workspace path. Demanded
top-level lazy values survive later statements and finalization. The directory
is fresh and private: never substitute a shared directory, glob or existing path.

Within Kame expressions, append an authored filename with
`${workspace}/config.json`; bare `{workspace}/config.json` is a pattern, not a
lookup. `joinpath` remains useful when joining computed parts or normalizing.

`defer (cleanup workspace)` evaluates the whole expression at cleanup, retaining
its lexical scope. An undemanded definition stays lazy until then. Branches share
their invocation's stack; nested Kash callables have independent stacks. Actions
run LIFO after user work joins, or after failing/cancelled work is reaped.
Cleanup-started async work also joins. Deferred commands cannot use `&` or direct
fallback: wrap compound recovery in an authored callable and defer its invocation.
Terminal `?` remains available.

Cleanup failure does not mask an existing failure. Remaining actions are tried
within a separate five-second stack budget; tighter process timeouts still apply.
SIGKILL/crash/forced shutdown can prevent cleanup. Blocking custom hosts are not
preempted by a process timeout. Finalization retains the original grants.

Cleanup does not enforce ownership, make `tool > ./final` atomic, restore a
truncated file, or reverse remote changes. Generate into staging and publish
explicitly after success; `defer` provides no atomic-publication contract for
external artifacts. `do clean` is not a finalizer.

## Environments, captures and tool lookup

```kash
let exports = {TOKEN: $(credential-reader), MODE: "preview"}
env: $exports application
```

Capture is exact UTF-8: empty output, spaces, CRLF and every terminal newline
survive. Normalize only when the producer specifies framing. The default capture
limit is 1 MiB; overflow/invalid UTF-8 fail without successful truncated values.
Stderr remains live. Stream large/binary payloads to files, not captured strings.

Environment names must be valid and values must be strings without NUL. The whole
graph is validated before launch. Computed values are omitted from normal errors
and lifecycle traces, but authored literals, argv, explicit output and child
output can expose secrets. Environment delivery does not hide values from
authorized processes or OS observers. Do not generate exports files or strip
credentials by default.

Three separate boundaries:

1. `$NAME` reads a binding or a permitted uppercase environment variable.
   `${env/NAME}` selects the environment explicitly. Stage assignments grant no reads.
2. Bare tools use the current unit PATH and `--tool NAME=PATH`. Explicit executable paths
   use stage cwd. A stage PATH override changes the child, not Kame's resolver.
   `TOOL_MISSING` differs from `CAP_DENIED`.
3. A wrapper or child shell can do its own lookup. Tracking a wrapper does not
   discover its packages/scripts/subprocesses. Declare relevant file inputs.

When a vendor needs sourcing or an interactive shell, launch an explicitly
authored external shell. Do not interpret its stdout as Kash source.

## Documents are data

```kash
(write! ./literal.txt """Literal $HOME and ${literal-name}.""")
let template = """Hello, @{name}.
"""
(write! ./message.txt (render {name: "Ada"} template))
(write! ./config.json (json {name: "a\"b", lines: ["one", "two\nthree"]}))
consumer < ./config.json
```

Verbatim text remains literal until explicitly rendered. JSON escaping belongs
to `json`, not shell quoting. File-backed rendering and input redirection track
reads; arbitrary filename arguments do not automatically declare dependencies.
`write!` and returned rule content publish individual files.

## Publish a staged file without clobbering

```kash
let workspace = line $(mktemp -d ./staging.XXXXXX)
defer rm -rf $workspace
generator > ${workspace}/output
(publish! ${workspace}/output ./final.txt)
```

Choose private staging on the destination filesystem and finish every writer
before publication. Existing destination entries cause `FS_EXISTS`; there is no
replacing rename or copy fallback. Do not use recipe-deferred `write!` to create
staging for an immediate publication. `FS_PUBLISHED` means the destination was
already published but subsequent cleanup failed: inspect its diagnostic cause,
do not retry or remove the destination as rollback. Only private staging is
eligible for deferred cleanup. This operation assumes trusted parent directories
and stable caller-owned staging; it does not guarantee crash durability or that
original inputs stayed unchanged while processing.

## Checks and debugging

Linux native and WASM/Node checks include:

| Contract | Executable checks |
| --- | --- |
| Prefixes and tool provenance | `T038-01-kash-direct-recipes.sh` |
| Environment scope/grants/traces | `T017-20-kash-context-environment.sh`, `T017-23-kash-operations.sh` |
| Exact capture, encoding and bounds | `T017-02-wasm-capture.sh`, `T017-23-kash-operations.sh` |
| Invocation cleanup, errors and reaping | `T017-22-kash-defer.sh` |
| Background joins and failures | `T017-18-kash-async.sh` |
| Streaming and slow-consumer pressure | `T017-03-eval-pipelines.sh`, `T012-04-streams-backpressure.sh` |

These checks cover Linux native and Node/WASM execution. Native Windows process
containment and filesystem durability require separate validation.

For nonsecret debugging, print typed argv using `json` and retain the authored cwd
and environment *names*. Lifecycle displays may be truncated and are not shell
replay commands. Never dump computed credential environments. Unsupported syntax
must fail at an authored location, never silently fall back to a system shell.
