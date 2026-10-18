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
| `$...name`, `$...{project.command}` | Explicitly spread a flat argv list, including the executable at command position. |
| `(EXPRESSION)` | Evaluate a parenthesized Kame expression, standalone or as an unquoted scalar word fragment. |
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
- `$...name` requires a flat list. At command position it must be nonempty, with
  a nonempty string/path executable first and initial arguments afterward.
  Following words append arguments; an empty command list is an error. Ordinary
  `$name` still requires a scalar executable. Explicit spreads are standalone,
  unquoted argv words, never definition/setup values or redirection paths.
- Quoted or mixed words require scalars; `"$files"` is invalid for a list.
- Nil, bytes, records, nested lists, callables, and process/results are not argv.
- Double quotes group words and allow references/process captures; parentheses
  inside them stay literal. Empty quotes preserve an empty argument. Single
  quotes are reserved and rejected.
- Legacy unquoted `@(...)`, `\@(...)`, and template directives are errors.
  Quoted argument data containing `@(...)` remains literal. Expression strings
  and explicit document templates retain their interpolation rules.

Mixed words compose directly, without `str` workarounds:

```kash
biome --config-path=(js-biome-config)
emit prefix-(name)-suffix (name).json
:cwd ./build/(variant) :MODE release-(variant) tool > ./out/(name).txt
tool < (input-name).txt
```

Each mixed word requires scalar fragments and produces one value, preserving
spaces and empty text. A standalone `(modules)` may splice its list result in
argv; `--modules=(modules)` rejects a list. The same composition applies to
redirection paths and scalar setup values, including `:cwd` and `:timeout`, with
their ordinary path/type checks. Typed `:env` records and definition RHSs keep
their value grammar. Calls/effects retain completed values across suspension;
results are never reparsed as words or source.

`"--config-path=(js-biome-config)"` is literal, not a call. Quote or escape literal
parentheses outside quotes. `$config` / `${config}` remain the reference forms;
bare braces do not add another substitution syntax. In a delegated Kame
expression, path interpolation still uses `@{ref}` / `@(call)`, not raw-word
fragments. Formatting preserves calls versus literal parentheses, list-splicing
boundaries, and authored value pipes rather than converting them to nested calls.

The entire body is parsed before execution, including unselected branches.
Generated Kash is explicit: construct `(kash TEXT)`, optionally using `render`
to deliberately generate that text, then call the resulting callable. Argument
values and captured stdout are never reparsed. Use references and typed
expressions directly; JSON serialization and escaping helpers are unnecessary.

### Migrating recipes

When switching from external-shell templates, replace `@{name}` with `$name`,
and call expansions such as `@(join ":" files)` with `(join ":" files)`.
Replace document `@if`/`@for`
generation with Kash control or explicit source construction. For a command
represented as an argv list, explicitly spread the executable and arguments:

```kame
SHELL = kash
command = ["printf" "<%s>" """@(literal)""" "two words"]
show :
	$...command
```

Do not serialize argv back into source. Keep `@{ref}` / `@(call)` in system-shell
recipes, document templates and expression strings where interpolation is intended.

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
names remain shell-resolved unless explicitly rendered through `@{x/NAME}`.

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

## Explicit process topology (POSIX)

Process groups run concurrently. A plain `|` is still exactly one stdout to one
stdin; groups require an explicit connection policy:

```kash
(cat ./first, cat ./second) |(merge) sort
cat ./input |(broadcast) (cat > ./audit, wc -c)
```

`merge` preserves each producer's byte order, with no cross-producer ordering or
record-boundary guarantee. `broadcast` delivers all bytes to every destination;
a slow destination eventually backpressures the source. Every edge is bounded.
Groups can nest and contain pipelines. Commas inside quoted words or embedded
expressions do not separate members. `(EXPRESSION)` without a process-level
comma remains a Kame expression.

The enclosing invocation owns all stages and native operator workers, including
when the complete graph ends in `&`. Failure cancels peers. Capture rejects
multiple unmerged terminal stdout streams; redirect unwanted outputs or merge
them explicitly. Windows currently rejects these new graph forms before launch;
ordinary linear pipelines remain available. Live stream arguments also require
a host with validated pipe-backed operand paths.

Explicit line framing enables pure Boolean filters and frame-safe merge:

```kash
cat ./events |(frame :lines)
    |(filter (contains item "ERROR") | cat > ./errors)
    | cat > ./normal
```

Selected items leave the main remainder; the selected branch runs once, not once
per record. `item` is a UTF-8 string without its LF/CRLF terminator, while the
forwarded bytes retain their exact delimiter. Partial EOF lines are valid;
records over 64 KiB (including delimiter) fail. Predicates cannot observe host
state, emit effects, execute processes, dynamically evaluate code, or suspend.
External stages receive payload bytes automatically; `|(encode :lines)` also
provides an explicit byte boundary.

`do parse --lang kash` exposes the resolved syntax-only topology: node IDs,
operators, framing, predicate spans, inserted adapters/mergers, and edges. It
does not evaluate predicates, resolve tools, or run argument substitutions.

A branch exit `>&` rejoins at the next stage/operator. `>@` bypasses contiguous
filters and rejoins at the first non-filter connection in the same lexical
pipeline. Rejoining an external command's output requires explicit reframing:

```kash
cat ./events |(frame :lines)
    |(filter (contains item "legacy") | normalize |(frame :lines) >&)
    |(filter (contains item "invalid") | cat > ./rejected)
    | cat > ./accepted
```

File stdin redirections demand generated prerequisites before graph launch and
record their published signatures for build reuse/invalidation. Process-channel
edges are not build-dependency edges; executable filesystem reads and stdout do
not implicitly declare prerequisites.

### Named process channels

`stream name` declares a lexical channel name, not a Kame value or background
task. Its writer and reader must belong to one simultaneously launched graph:

```kash
stream reports
(
    producer >&reports,
    &reports | consumer
)
```

Each channel has one writer and one reader; use `merge` or `broadcast` explicitly
for multiple producers or consumers. A writer can also be a selected branch's
exit. Missing/duplicate endpoints and feedback cycles fail before launch or
output truncation. Named aliases become ordinary bounded pipes, preserving their
byte/framed data kind. Separate sequential writer/reader statements are invalid.
Use `&name | cat > ./file` for a file sink. Names are ASCII identifiers (hyphens
allowed after the first character); `in`, `out`, and `err` are reserved scope
ports and are not available as general named endpoints.

### Scheduling and working buffers

```kash
source |(frame :lines) |(balance :strategy round-robin) (worker-a, worker-b)
source |(broadcast :buffer 4096) (audit, archive)
```

`balance` requires framed records and assigns each record to exactly one child,
in strict round-robin group order. It preserves each child's assigned order;
slow children apply backpressure, and any failure cancels the graph. Children are
long-running processes, not new processes for each item. Unassigned children
still receive EOF. Merge remains round-robin-ready with per-input ordering, not
a globally ordered rejoin.

`merge`, `broadcast`, and `balance` accept literal `:buffer BYTES` (1..65536).
For bytes it limits each transfer; for records it limits each input's complete
payload assembly, excluding the private four-byte frame header. A larger record
fails with `KASH_BUFFER_LIMIT`. Kernel pipes and bounded host stream buffers are
additional; this is not a total graph memory limit. Defaults are 16 KiB for byte
transfer and 64 KiB per framed input. Invalid or computed policies fail before
execution.

Framed `broadcast` can opt into independent lossy destination queues:

```kash
source |(frame :lines)
  |(broadcast :buffer 4096 :items 8 :overflow drop-old) (audit, monitor)
```

- `drop-new`: discard a new record when its pending destination queue is full.
- `drop-old`: evict oldest pending records until the newest fits.
- `latest`: retain only the newest pending record (`:items 1`).

`:items` bounds pending records per output (1..1024, default 64; latest uses 1).
`:buffer` also bounds each output's pending payload bytes. One additional
in-flight record per output is protected from replacement, so partial writes
cannot corrupt framing. Headers, queue metadata, and bounded host/OS buffers are
additional. Retained records stay ordered; which records survive depends on
timing. EOF drains retained records, and early child closure still fails the
graph. Lossy policies require frames and are forbidden for `merge`/`balance`.
`:overflow block` is the default lossless path, without an extra pending queue.

### Terminal graph telemetry

Graph Result records have a `graph` field with terminal nodes and edges. For
example, inside a Kash source/build session:

```kash
result = ((kash """printf "a\nb\n" |(frame :lines) |(broadcast) (cat > ./left, cat > ./right)"""))
(out! (json result.graph))
```

Nodes expose prepared IDs, kinds, executable paths, terminal states, and exit
outcomes. Edges expose stream kinds and measured payload `bytes`, framed `items`,
`droppedItems`, `queueHighWaterBytes`, and `queueHighWaterItems`. Envelope headers
do not count. `measurement` distinguishes `completed-write`, `read`, and
`unavailable`; partial failed writes are not completed writes. Byte edges have
nil item counters. Direct external-to-external pipes are not instrumented, so
their measurements are nil rather than invented zeroes. Non-queue edges have
nil queue high-water marks. Counters saturate at 2^53-1 and set `saturated`.

Telemetry does not capture stdout, infer build dependencies, or change failure
handling. Missing telemetry from a host remains explicitly unavailable. This is
terminal Result metadata, not live blocked/backpressure inspection. Alternate
ordering/failure policies, per-item workers, and live inspection remain pending.

### Live stream arguments

```kash
diff -u <(generate-old) <(generate-new)
generate | diff -u <& ./baseline
```

`<(graph)` starts a concurrent producer in the receiver's owned graph. `<&name`
consumes a declared channel at that argument position; `<&` claims the current
upstream channel there, leaving stdin closed unless an independent `< path` is
provided. Duplicate claims fail before launch. These are standalone arguments,
not mixed word fragments, executable heads, captured strings, or temporary files.
Argument positions remain correct after ordinary argv list splicing.

Native external programs receive inherited pipe-backed `/dev/fd/N` paths. Node
uses private FIFO paths and one owned POSIX `dd` transport relay per operand,
avoiding FIFO reopen/EOF races. No regular file stores the stream. These inputs are
non-seekable and live only for the graph's lifetime; do not use them with tools
requiring seekable regular files. Producers are joined on success and reaped on
failure, capture overflow, timeout, or cancellation. Unsupported hosts, including
native Windows, fail with `KASH_STREAM_OPERAND_UNSUPPORTED` without launching or
truncating output files.

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
