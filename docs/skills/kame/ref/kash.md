# Kash process scripts

Kash runs process graphs with typed values and explicit argv construction.
Files use `.kash` or `.ksh`. Indented Kame recipes use Kash by default.
Use `; {shell: "sh"}` to select a foreign shell and render the body as a
document template. Native and WASM share the `do run` runner.
[Project Kash reference](../../../ref-kash.md)

```kash
def name = "Ada Lovelace"
def files = [./one.txt, ./two.txt]
let revision = line $(git rev-parse --short HEAD)

echo "Hello, $name"
cat $...files | wc -l
echo revision=$revision
```

A statement beginning with `(EXPRESSION)` evaluates it for effects.
Command words use the typed expansion rules below. Statements may end at
a newline or a semicolon. Word-start `#` begins a comment.
Use `$command` as a computed executable head.

## Eager bindings

```kash
let workspace = line $(mktemp -d)
defer rm -rf $workspace
printf "%s\n" $workspace
```

`let NAME = VALUE` evaluates its initializer when reached and retains the
value across suspension. A subsequent `set NAME = VALUE` updates that
binding within the same invocation. `def NAME = VALUE` supplies a lazy
value. Each function call, selected branch, and loop iteration has fresh
local bindings.

Eager names reserve their binding throughout the lexical scope. Reading
one before initialization reports `BINDING_UNINITIALIZED`, including
through a function or a lazy definition. CLI configuration cannot override
a `let`. Dry-run skips initializer effects. An included definition-only
library may use `let` inside its function bodies.

## Native functions

```kash
def probe workspace
	if $?(test -f "${workspace}/ready").ok
		return true
	return false

let ready = probe ./workspace
```

The header uses `def NAME PARAMETER…`. A zero-parameter header uses `()`.
A final `rest...` collects remaining arguments. An indented action block
or `= EXPRESSION` provides the body. Each call uses its own arguments,
locals, async work, and deferred cleanup.

Functions can be used as command heads or evaluated for their return value.
A command invocation discards an ordinary returned value. Its stdout and
stderr stay connected to the process graph. `return VALUE` ends the function
and runs its deferred cleanup. A function's process status comes from its
completion, failure, or cancellation.

A function with `yield VALUE` returns a lazy stream. `for` and `collect!`
consume its values. A bare `return` ends a generator. An early stop cancels
its invocation and runs pending cleanup. Stream values and stdout travel
through separate channels.
[Generator streams](../../../spec/012-streams.md#generator-streams)

## Deliberate script exit

`exit STATUS` evaluates one typed value and terminates the host-established
execution boundary. Status must be an integer from 0 through 255. There is no
implicit last status, acceptance `?`, process operator, or redirection.

Exit passes through functions, branches,
and loops. In an ordered runner session it also skips later fragments. In a Kash
recipe it ends only that attempt; a successful file recipe still verifies and
publishes its outputs. Embeddings receive a typed termination outcome, never an
unconditional exit of the embedding process.

Exit is not recoverable by `?`, `??`, fallback, or condition truth conversion.
`exit 0` joins owned work before cleanup; nonzero exit cancels/reaps owned work.
Registered defers run once in LIFO order under existing cleanup budgets. A job or
cleanup failure can fail an otherwise successful exit, but cannot resume execution
or select a fallback. A nonzero requested status survives cleanup failure.

Direct `defer exit` is invalid. Indirect exit during finalization reports
`PHASE_INVALID` before evaluating its operand. Exit without an execution boundary
is also invalid. Dry-run does not evaluate exit operands or request termination.

## Owned execution scopes

```kash
let exports = {MODE: "release"}
with cwd: ./project env: $exports
	defer rm -f ./scratch
	configure
	compile
verify-elsewhere
```

`with` starts a nonempty indented body with fresh locals and its own async/cleanup
lifetime. Its header occupies its own line and accepts optional `cwd: VALUE` and
`env: VALUE` pairs, each once, in either order. Values use the same scalar cwd and
typed environment-record grammar as process stage setup. Unknown setup keys and
block timeouts are not supported. Document templates have their own scoped directives.

Setup evaluates left to right, once across suspension, in the enclosing context;
all values are validated before entering the body. Relative cwd resolves against
the enclosing cwd. Environment records override inherited child environment
entries without granting environment-read access or changing startup tool lookup.
Calls and deferred expressions inherit the effective context. A stage override
such as `cwd: ./build pwd` inside `with cwd: ./project` uses `./project/build`.
Neither the host's global cwd nor its environment is mutated.

Each block joins its own work before normal completion, successful return, or
successful exit, then runs its defers in LIFO order. Failure and unsuccessful
exit cancel and reap that work first. Defers reached in ordinary branches
or iterations belong to the enclosing execution scope, not to that iteration.
Nested functions and `with` blocks retain their own lifetimes. Borrowed outer
process handles are not adopted. Cleanup cannot restart an active cleanup budget.

Cleanup finishes before the enclosing context resumes. Failure prevents an
otherwise successful completion or return; a pending exit remains terminal.
Normal completion preserves the body's ordinary final-statement value without
printing it or collecting results. Context does not refresh memoized definitions,
rebase materialized resources, or alter authored source identity. Dry-run evaluates
neither setup nor body and acquires no scope or resources.

## Loop transfers

`break` ends the nearest enclosing `for` or `while`; `continue` skips the
remaining body and starts its next iteration (rechecking a `while` condition).
Both are standalone statements without operands, labels, status acceptance,
redirections, or process operators. They require a lexically enclosing loop in
the same function or script, even in an unreachable branch. They cannot be
deferred or target a caller's loop.

A transfer crossing a `with` scope joins that scope's work and runs its cleanup
before reaching the loop. Join or cleanup failure replaces the transfer with a
failure. Ordinary iteration defers remain owned by their enclosing execution
scope; transfers neither run them early nor cancel that scope's async jobs.
Loops inside cleanup may use their own local transfers. `continue` retains the
normal iteration deadline and interruption checks.

## Regular-file record loops

```kash
for line from ./events.jsonl :lines
	let event = (parse-json line)
	inspect $event.name

for path from ./paths.list :nul
	inspect $path
```

`from SOURCE FRAME` is distinct from materialized `in LIST`. SOURCE is one
literal `:stdin`, or an expression or standalone reference yielding a nonempty local path string or
`file:` resource. FRAME is literal `:lines` or `:nul`. The file is evaluated and
opened once, requires read authority and regular-file storage, and participates
in dependency tracking. It is not snapshotted against concurrent in-place writes.

Records are UTF-8 text, without their LF/paired CRLF or NUL delimiter. Unpaired
CR remains data; NUL framing preserves embedded CR/LF. Empty input has no records,
consecutive delimiters produce empty records, and a final delimiter invents no
extra item. A nonempty unterminated final record is valid. Each record is limited
to 64 KiB including a present delimiter: excess reports `KASH_RECORD_LIMIT`,
invalid UTF-8 reports `KASH_FRAME_ENCODING`, and neither becomes EOF/truncation.

Bodies run sequentially with fresh locals and closed default command stdin;
explicit independent file inputs remain valid. `break` stops without draining,
`continue` advances, and readers close on completion, transfer, failure, or
cancellation. Completed per-record operation and finished cleanup-scope state is
retired. Invocation-owned values, process handles, callables and
outer-scope defers retain their existing lifetimes. Results are not collected.
Use `with` or a callable for cleanup that must finish per record.

`for line from :stdin :lines` reads the invocation's remaining input without a
file grant. Closed input yields no records. One active reader claims that input;
nested readers report `KASH_DOUBLE_CONSUME`, while body commands keep closed
default input. On exit, the claim is released without closing the caller's
descriptor or consuming bytes past the last delimiter. Pending reads obey
cancellation and deadlines, without whole-input capture or per-record processes.
Native hosts poll supplied descriptors. Node supports regular files and Linux
pipes/FIFOs; sockets and other unsupported handles report `FEATURE_UNSUP` before
reading. Interactive terminal input remains closed.

## Local libraries and source identity

```kash
include ./lib/checks.kash
include? ./lib/local.kash
import ./lib/archive.km as archive
def config = joinpath module/dir "defaults.json"
check-config $config
archive/create ./dist/app.tgz
```

Includes bind definitions in the caller's scope. Relative paths resolve
against the containing source. Kash files include Kash libraries. A
loaded library may contain definitions, imports, exports, and includes.
Repeated includes expand again, and conflicting definitions and active
cycles are errors. Imports expose the declared exports of a module.
Choose a namespace with `as`, or bind selected names through `from`.

`module/dir` and `module/file` use the authored source path, including inside
an included function. Lexical symlink spelling is preserved. A relative
include or import in inline or stdin input needs a file-backed source.
Source loading retains the invocation's existing effect grants.
[Modules](../../../spec/050-kash-libraries.md)

## Explicit boundaries and argv

| Form | Meaning |
| --- | --- |
| `$name`, `$project.name`, `${EXPR}` | Substitute a Kame value. |
| `$...name`, `$...{project.command}` | Spread a flat list, including an executable at command position. |
| `(EXPRESSION)` | Evaluate an argument or a scalar fragment of a mixed word. |
| `$(COMMAND)` | Capture exact UTF-8 stdout from a graph or sequential list. |
| `${env/NAME}` | Read a permitted environment variable. |
| `'…'` and `"…"` | Group a literal word or a word with `$` substitutions respectively. |

A scalar contributes one argument regardless of whitespace. A standalone
`nil` contributes no argument. In a quoted or mixed word, `${EXPR}`
renders the value as text, with spaces between list items and empty text
for `nil`. Use a spread to pass a list as separate arguments. Raw `*.c`
stays literal.

Use braces to separate a reference from a suffix. `${name}.c` appends a
suffix and `$name.c` accesses a field. An unbraced reference ends at `/`.
Use `${lib/name}` for a qualified name. A missing uppercase binding can fall
back to a permitted environment read.

```kash
biome --config-path=(js-biome-config)
emit prefix-(name)-suffix (name).json
cwd: ./build/(variant) MODE: release-(variant) tool > ./out/(name).txt
tool < (input-name).txt
```

Mixed words evaluate each fragment once. The same syntax applies to
stage settings and redirection paths. Parentheses inside quotes stay
text, and `"Repos ${(dirs REPOS)}"` renders the directory list in one
argument. Foreign-shell and document templates use `@{EXPR}`.

### Recipe arguments and templates

```kame
command = ["printf", "<%s>", "two words"]
task show
	$...command
```

A default Kash recipe reads `$name` and supplies a computed argument with
`(EXPR)`. Foreign-shell recipes interpolate values with `@{EXPR}` and
apply shell quoting to their rendered text.
[Rules and recipes](./rules.md)

## Executable dependencies

Every reached Kash stage automatically resolves and tracks its executable,
including literal/dynamic command heads, pipelines, captures, and async work.
Bare names use the current unit's `PATH` and `--tool NAME=PATH` overrides.
Explicit paths resolve from the stage working directory. Stage environment
`PATH` overrides affect the child, not this lookup. Missing/non-executable heads
report `TOOL_MISSING`. Run grants still apply; internal executable checks do
not grant user filesystem-read authority.

`aws ...`, `x/aws ...`, and `${x/aws} ...` use the same named tool resolver;
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
cwd: ./build timeout: (nop 10) NODE_ENV: production npm test
```

Process `|` streams stdout with bounded backpressure; value pipes inside
`(...)` transform values. `<` provides stdin; `>` replaces stdout's file;
`>>` appends. Redirections belong to a stage and cannot conflict with a pipe:
only the first stage may use `<`, only the last may use `>`/`>>`. Reserved
`>&out` and `>&err` route command stdout to the current output or error sink;
inside `$(...)`, `out` joins capture and `err` stays live. Neither redirects the
executable's own stderr. `2> PATH` and `2>> PATH` replace or append that stderr
file. `2>&1` copies stdout's destination at that point, so `> file 2>&1` merges
both into the file and `2>&1 > file` leaves stderr on the previous stdout.
`3>`, `1>&2`, and heredocs are not defined.

`accept-exit: [1, 7]` accepts only those ordinary nonzero exits for that stage;
the list must contain distinct integers from 1 through 255. It also works on
`run!`. Empty lists accept nothing extra. Validation precedes graph launch and
output opening. Raw Result status stays unchanged, while `effectiveStatus`,
conditions, joins and failure policy reflect acceptance. Accepted exits never
hide an unaccepted peer/worker failure, signal, deadline, launch or transport error.

Setup pairs precede the executable. `cwd:` sets stage cwd, `timeout:` is in
seconds (unlike CLI `--timeout` in milliseconds), and other valid keys override
stage environment entries. An ordinary filename argument does not declare a
dependency; an input redirection does. Grants do not sandbox an executable's
internal filesystem access.

### Typed capture

```kash
let result = (capture! {stdout: true, stderr: true, stderrLimit: 4096} :accept-exit [1] "checker" "--report")
printf %s (text result.stdout)
```

`capture! POLICY SETUP... EXECUTABLE ARGUMENTS...` runs one typed-argv command
synchronously. Select stdout, stderr or both with booleans; omitted selections
are false and at least one must be true. Positive integer `stdoutLimit` and
`stderrLimit` independently default to the invocation capture limit. Unknown
policy fields, invalid limits and `:async` fail before launch.

Results retain raw status/stages and add exact `stdout`/`stderr` bytes (nil for
unselected streams). Empty captured output is empty bytes. The matching
`stdoutCaptured`/`stderrCaptured` flags indicate selection. Selected streams are
not also published; unselected streams stay live. Use `text` for explicit UTF-8
decoding, which can fail separately. NUL and trailing newlines remain intact.
Each stream is drained concurrently within its own limit; overflow cancels owned
work and reports `CAPTURE_LIMIT`, never successful truncation. Failure acceptance
is explicit through `accept-exit:`; signals and fatal errors remain failures.
Default input is closed. Pipelines, async capture and graph capture are not
supported. See [Process inspection](../../../spec/049-process-inspection.md).

### Invocation stdin

File-backed and inline standalone value/Kash sessions borrow piped or
file-redirected data stdin (interactive terminal input remains closed):

```sh
printf 'payload\n' | kame do run -l kash -c 'cat'
```

Foreground commands, including functions, `with`, `run!`, and `pipe!`, receive
the remaining input at the main graph's unique source. Explicit file inputs and
connections override it. A source before fan-out qualifies; multiple main sources
do not, even if one is file-fed. Anonymous live-operand producers stay closed;
current-stream operand receivers retain their closed default stdin.

Captures (including nested helpers), background work, and cleanup receive closed
default input. A session containing a stdin source fragment reserves all stdin for
source loading instead. Ordinary build recipes and embeddings remain closed
unless their host explicitly supplies data input.

Sequential consumers share the remaining bytes, without copying, restarting, or
parent-side read-ahead. Completing a command does not close the caller's stream.
Supplied input needs no read grant; file redirections still do. Boundaries supplied
opaque input conservatively disable execution reuse, including file freshness,
because replaying output cannot reproduce consumption of a borrowed stream.

### Stage environments

`env: $EXPORTS` or `env: (EXPRESSION)` supplies a typed environment record,
also accepted by `(run! :env EXPORTS ...)`. Keys must match
`[A-Za-z_][A-Za-z0-9_]*`; values must be strings without NUL. Empty strings are
valid, and an empty record (for example `(parse-json "{}")`) leaves inheritance
unchanged. No scalar conversion or list splicing is performed. The entire record
is validated before launch, with diagnostics that omit evaluated values.

Record assignments and ordinary `KEY:` overrides apply in authored order, last
assignment wins. They affect only that stage, not sibling stages, later commands,
the unit's environment store or its tool lookup. Record evaluation
occurs only on reached runtime stages; dry-run defers it, and ordinary read/env/run
grants still apply. Effects and completed values survive suspension without replay.
Computed values are excluded from normal diagnostics/lifecycle traces, but callers
must still avoid placing secrets in authored literals, argv, or explicit output.

### Capture and failure

Capture preserves valid UTF-8 stdout exactly, including empty output and trailing
newlines; stderr remains live. Per captured graph, ordinary terminal stdout and
`>&out` count as capture outputs; more than one is `KASH_AMBIGUOUS_CAPTURE`, while
zero produces an empty string. Capture is limited (default 1 MiB), never silently
truncated.
Pipelines fail on any stage's nonzero/signal exit, including SIGPIPE; aggregate
status is the last failing prepared stage/node. Failures/cancellation reap owned
work.

`A && B` and `A || B` form a synchronous, left-associative list at equal
precedence. `&&` continues after effective success; `||` continues only after an
ordinary process exit or signal. Other failures—launch, timeout, transport/capture,
capability, evaluation, or operator errors—do not select `||`. Lists may appear in
command conditions and `$(...)`, but not as graph members, live producers, direct
deferred commands, or compound async commands. An unhandled failed list stops
later statements.

```kash
compile && verify && publish
probe || use-cache
let message = $(printf "payload" >&out && printf "progress\n" >&err)
printf "captured=%s\n" $message
```

Here `message` is `payload`; `progress` is forwarded to the error sink. Sequential
capture shares one limit and keeps failed-operand bytes if `||` runs a fallback.
`?` accepts an ordinary nonzero exit. An accepted result can continue through
`&&` without erasing its recorded status. For explicit `do run`, unhandled executable exits
preserve their status through nested runners; signal exits use `128 + signal`.
Successful acceptance or recovery exits zero.

Spaces, tabs, embedded line endings, CRLF, and every terminal newline are data.
Use `strip` explicitly only when an identifier's contract permits removing all
surrounding whitespace. For a producer with one terminal LF, use
`(line TEXT)` to remove only that byte, preserving a preceding CR. Captured
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
a slow destination eventually backpressures the source. `|(tee PATH)` copies the
stream to that file and to its continuation; `|(tee :append PATH)` does not
truncate. A failing upstream status is unchanged. Every edge is bounded.
Groups can nest and contain pipelines. Commas inside quoted words or embedded
expressions do not separate members. `(EXPRESSION)` without a process-level
comma remains a Kame expression.

The enclosing invocation owns all stages and native operator workers, including
when the complete graph ends in `&`. Failure cancels peers. Capture rejects
multiple unmerged terminal stdout streams; redirect unwanted outputs or merge
them explicitly. Windows rejects these graph forms before launch;
ordinary linear pipelines remain available. Live stream arguments also require
a host with validated pipe-backed operand paths.

Explicit line framing enables pure Boolean filters and frame-safe merge:

```kash
cat ./events |(frame :lines)
    |(filter (includes? "ERROR" item) | cat > ./errors)
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
    |(filter (includes? "legacy" item) | normalize |(frame :lines) >&)
    |(filter (includes? "invalid" item) | cat > ./rejected)
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

### Explicit merge ordering

```kash
(source-a |(frame :lines), source-b |(frame :lines))
  |(merge :order round-robin :buffer 4096) cat
```

`:order ready` is the default fair ready-input scheduler. `:order round-robin`
waits for each open input's next whole record in port order, beginning with the
first input, and skips inputs only after EOF. Unequal lengths and partial final
lines remain lossless. Strict ordering requires framing and respects the same
per-input byte budget; fast inputs backpressure instead of filling a reorder
buffer. It does not recover original source order when branches change record
counts. Avoid strict ordering when a producer depends on another input being
consumed first. Implicit branch rejoins retain ready ordering.

### Graph failure collection

```kash
(check-a, check-b) |(merge :failure collect) cat
```

The default `:failure fail-fast` cancels peers on failure. `:failure collect`
instead lets siblings finish after ordinary nonzero executable exits. It applies
to the **whole owning graph**, including anonymous producers; contradictory
explicit policies are rejected before launch. It still reports failure, retaining
every stage's outcome and selecting the last failing prepared stage's status.
Use existing `?` or recovery explicitly to handle that failure.

Signals (including SIGPIPE), launch errors, operator/framing/predicate errors,
timeouts, and cancellation still stop the graph. Collection is not branch
isolation, retry, or permission to ignore broken pipes. A peer that never closes
can still wait indefinitely unless the invocation has a deadline. Graph inspection,
snapshots, and Results expose the effective policy.

### Explicit per-record commands

```kash
source |(frame :lines) |(each) command args
```

One directly executed process receives one exact record on stdin and then EOF.
Empty input starts none. The argv/environment/cwd template is prepared once;
record data is never parsed as shell text or interpolated into arguments.
One active worker per operator preserves order and bounds memory. Stdout remains
bytes; explicitly `|(frame :lines)` before a framed rejoin. Redirected stdout is
opened once for the whole graph, not truncated for each record. Live operands and
stdin redirections cannot be used on the template. For parallelism, use explicit
`balance` branches: `|(balance) (|(each) worker-a, |(each) worker-b)`.

Workers use normal grants, failures, recovery, cancellation, and async ownership.
`:failure collect` lets later records run after ordinary nonzero exits, retaining
the last nonzero status for the worker node. Signals and launch/transport errors
are still fatal. A template `timeout:` covers the whole operator lifetime. There
is no unbounded per-record status history or implicit retry.

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

### Graph telemetry

Graph Result records have a `graph` field with terminal nodes and edges:

```kash
let result = $?(printf "a\nb\n" |(frame :lines) |(broadcast) (cat > ./left, cat > ./right))
(out! (json result.graph))
```

Nodes expose prepared IDs, kinds, executable paths, terminal states, and exit
outcomes. Edges expose stream kinds and measured payload `bytes`, framed `items`,
`droppedItems`, `queueHighWaterBytes`, and `queueHighWaterItems`. Envelope headers
do not count. `measurement` distinguishes `completed-write`, `read`, and
`unavailable`; partial failed writes are not completed writes. Byte edges have
nil item counters. Direct external-to-external pipes are not instrumented, so
their measurements are nil, not zero. Non-queue edges have nil queue high-water
marks. Counters saturate at 2^53-1 and set `saturated`.

Telemetry does not capture stdout, infer build dependencies, or change failure
handling. Missing host telemetry is explicitly unavailable. Live progress appears
in `process-snapshot` JSON and embedding events: prepared node IDs carry
running/input-wait/output-wait states, and edge IDs carry sampled counters.
Samples coalesce when unread and are normally at least 100 ms apart. Native
live counters are individually atomic, not transactional across fields; use
terminal Results for final accounting. External processes do not expose internal
waits, and high-water marks are not current queue occupancy. Wait states indicate
I/O interest, not deadlock detection.

## Named execution context

| Name | Value |
| --- | --- |
| `inputs` | Normal rule inputs. |
| `outputs` | Declared output identities. |
| `changed-inputs` | Changed normal file inputs in a file recipe. |
| `captures` | Captured output segments in source order. |
| `args` | Arguments of the current script or function. |

Use ordinary field and index access, such as `inputs.0` or `args.0`.
Named captures and target arguments also bind in a recipe. Lexical bindings
can shadow context names. Order-only prerequisites stay outside the normal
input list.

```kame
rule ./output : ./input
	cat $...inputs > $outputs.0
```

## Control, recovery, and ownership

```kash
if $?(test -f ./output.txt).ok
	echo exists
elif options.release
	echo release
else
	echo missing

match target
	./src/{name:*}.c =>
		echo $name
	else =>
		echo unknown
```

Indent each body consistently. Conditions use Kame truthiness. Test a command
with `$?(COMMAND).ok` or use `.state == :completed` to count accepted exits.
Only selected branches execute. A `match` arm binds captures in its body.

- `CMD ?` accepts an ordinary nonzero exit while leaving live stderr visible.
- `CMD ? FALLBACK` runs a fallback process on recoverable command failure.
- `LEFT ?? RIGHT` uses the fallback when the value is nil or a
  lookup is missing. Inspect `$?(COMMAND)` for process failures.
- Recovery never suppresses capability denial or cancellation.
- Use Kash blocks for action control and Kame conditionals for value logic.

### List and conditional loops

```kash
for source in fonts
	converter $source
	verifier $source
for source in ./src/{name:*}.c
	compile $source ./build/${name}.o

while $?(probe --pending).ok
	worker --one
```

`for NAME in EXPRESSION` visits a list, stream or path pattern in order and
returns nil without collecting results. A runtime path pattern uses tracked
`wildcard` discovery and binds each match's named captures alongside the path.
It matches all entry kinds; use `files` or `dirs` for filtered path lists
without capture bindings. Empty lists and patterns with no matches do nothing.
Each iteration has a fresh lexical scope. The body may use `break` or `continue`
in its own invocation. Use
`set` only for an eligible eager binding.

`while` accepts the same expression conditions as `if` and tests before
each iteration. Each condition visit invokes fresh work, but looking up an outer
lazy definition still returns its memoized value. Re-evaluation does not imply
fresh filesystem observations; use an outcome capture for live external probes.
Even pure loops yield to the host for cancellation and deadline checks.

Failures stop iteration under the ordinary recovery rules. Suspended bodies
resume without repeating completed effects. Dry-run does not execute loop
headers or bodies to discover iteration counts. `defer` remains invocation-owned
and runs LIFO, not at the end of each iteration; call a Kash callable or use
`finally!` for per-item cleanup. Explicit `&` can overlap later iterations and is
still joined by the invocation. `map` remains supported for value-producing or
callback-based iteration; streaming `|(each)` is a separate stdin-record feature.

`for` and `while` are reserved statement headers. Use an explicit executable path
or computed command head to run external programs with those names.

### Async ownership

- Terminal `&` starts the entire graph asynchronously; normal session completion
  joins outstanding work. It is not daemonization or detachment.
- Bind a handle with `let job = $&(worker)`. Read its outcome with
  `$?(wait $job)`.
- Expression-level `(run! "command" args...)` and `(pipe! (run! ...) (run! ...))`
  use the same graph executor. Synchronous results expose `status`, `signal`,
  `stages`, `stdoutCaptured`, and `stderrCaptured`; streams are live, not retained.
- `await` is repeat-safe. Unobserved async failures fail the final invocation join;
  source failure/interruption cancels and reaps every outstanding graph.

### Deferred cleanup

```kash
let directory = line $(mktemp -d ./workspace.XXXXXX)
defer rm -rf $directory
defer (out! "finishing\n")
```

| Form | Registration | Cleanup |
| --- | --- | --- |
| `defer COMMAND` | Snapshot validated argv/setup/redirection operands; do not launch or open output. | Execute the prepared graph. |
| `defer (EXPRESSION)` | Retain the expression and lexical scope. | Evaluate the expression. |

Registration occurs only when reached at runtime, never during dry-run.
Expressions keep normal laziness: an undemanded resource definition may first
run during cleanup; commands can demand it while snapshotting. Demanded
top-level lazy values persist through the invocation, not per statement.

The enclosing recipe, function or runner invocation owns a
LIFO stack; branches do not create a cleanup lifetime. Normal exit joins user work
first. Failure/cancellation cancels and reaps it first. Cleanup expressions also
join async work they start. Deferred commands cannot end in `&` or use a direct
fallback; for compound cleanup defer a call to an authored Kash callable.
Terminal `?` still explicitly accepts unsuccessful exits.

Kame's `(finally! BODY CLEANUP)` is an explicit value-language scope, not ambient
Kash registration. Both use the shared finalization host driver; an enclosing
Kash invocation runs its remaining `defer` actions after reached `finally!`
scopes have unwound during shutdown.

Cleanup gets a separate five-second stack budget, capped by tighter process
limits. Try remaining actions after failure while budget remains. Cleanup failure
fails otherwise successful work; an existing failure/cancellation stays primary.
Finalization retains original grants. A forced second interruption, SIGKILL,
crash or blocking custom host can prevent cleanup.

Deferred `write!` executes during cleanup even in a failed recipe, not as a
successful output-publication effect. `defer` does not establish ownership or make
`>` atomic. Only remove freshly created private resources; don't infer ownership
of shared paths. `if`/fallback are not finalizers, `do clean` is historical
cleanup, and killing a process does not roll back remote operations.
See the [operational cookbook](../../../kash-cookbook.md)
and [SDK migration boundaries](../../../sdk-migration-boundaries.md).

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
Normative details: [CLI](../../../spec/009-cli.md),
[Kash](../../../spec/017-kash.md), [streaming](../../../spec/042-kash-streaming.md),
and [output-port/list spec](../../../spec/047-kash-output-ports-and-command-lists.md).

## Interactive sessions

`option [-l|--local|-g|--global] [NAME [VALUE]]` inspects or changes shell
settings. Local (default) is this running shell; global saves user defaults in
`$XDG_CONFIG_HOME/kash/options` (fallback `~/.config/kash/options`), one
`name: value` per line. Local overrides beat global defaults, then environment
and built-in defaults. Initial options are `history-scope` (`local|global`),
`history-max-age-days`, and `history-max-bytes`. `option` lists effective
settings with their source and bounds; `option -g` lists saved overrides.
Lowering history limits can prune saved entries at the next refresh/write.

Tab completes external commands' subcommands, options and values from specs
built in the background from man pages, static fish files, and Usage KDL in
`$XDG_CONFIG_HOME/kash/completions/NAME.usage.kdl`. `complete --refresh
NAME…|--all` rebuilds, `complete --show NAME` prints a spec, and `complete
--live cobra|clap PATH` asks that executable on each Tab. A rule producing
`./user/NAME.usage.kdl` in `$XDG_CONFIG_HOME/kash/completions.km` runs a
spec-printing tool once per version, e.g. `mise usage > $outputs.0`.
[Command completion](../../../spec/058-kash-completion.md)

```sh
kash                              # REPL when stdin is a terminal
kash -i ./lib.kash                # preload, then read entries
kash --login                     # load login profile, then REPL on a terminal
kash -lc 'echo hello'             # login profile, then noninteractive command
kame do run -i --allow-run
```

Entries share one session and a failure ends only its entry. `let` and
`def` bind names, and `set` updates eligible local bindings. Use `cd`
and `set env/NAME = VALUE` to update session state. Foreground
commands receive the terminal; Ctrl-C reaches only them. A second interrupt
while an interrupted entry settles kills its processes and ends the session
with 130, skipping cleanup. On a terminal, entries are jobs: Ctrl-Z stops one
anywhere, `jobs`, `fg [%N]`, `bg [%N]`, `kill [-SIG] %N` and `wait [%N]`
manage them (`%+`/`%%` current, `%-` previous), and a one-line `cmd &` or a
final `&` line starts one in the background; a background read of the
terminal stops it until `fg`. On a terminal, a command's unsuccessful exit
is not reported beyond its own output and status; `explain` shows the latest
failure's full diagnostic. Use `let x = $(...)` to run a capture now;
`def x = $(...)` is lazy and runs when `x` is used.

The startup file `$KASH_INIT` (default `$XDG_CONFIG_HOME/kash/init.kash` or
`~/.config/kash/init.kash`; empty disables) runs as entries before the first
prompt. A `kash-prompt` function receives a record with `status`, `dir`, `name`, and `jobs` fields and
returns the prompt string; `kash-rprompt` receives the same record and returns
a right prompt, drawn at the right edge of the entry's last line:

```kash
def kash-prompt info = str info.name (if info.status == 0 then "" else " !") " $ "
def kash-rprompt info = if info.jobs == 0 then "" else str "[" info.jobs " jobs]"
```

Login invocation (`-kash` as argv[0], or standalone `kash -l`/`--login`) loads
`$KASH_PROFILE`, default `$XDG_CONFIG_HOME/kash/profile.kash` or
`~/.config/kash/profile.kash`, before supplied sources. Empty disables loading.
Interactive initialization follows only in interactive sessions; ordinary
`kash -c` loads neither file. Other shells' profiles are not interpreted.
Standalone `kash -l` means login; use `--lang` for explicit language selection.

Define interactive shortcuts as typed argv prefixes, invoked by bare name:

```kash-repl
alias ls = x/eza ?? nil
alias ll = ["ls", "-lah"]
ll ./src | head -n 10
alias ll                         # show one; alias alone lists all
unalias ll
```

The expression is evaluated once. A string stays one argument, a nonempty list
of strings supplies separate arguments, and `nil` removes the alias if present.
Prefixes expand once without reparsing; caller arguments retain their boundaries.
Computed command heads and explicit paths bypass aliases. Put declarations in initialization or a login
profile. An alias whose prefix starts with `cd` invokes that builtin.
Format startup files with `kame do fmt --lang kash -i ~/.config/kash/init.kash`.
Alias declarations in ordinary scripts report `PARSE_ERR`.

Use `set env/PATH = VALUE` to update later command and tool lookup or
`set env/PATH = nil` to remove PATH. Relative and empty PATH entries use
the lookup directory. Tool overrides and run grants stay in effect.
Stage-local environment records leave the unit's lookup unchanged.

The editor follows Readline conventions (emacs or vi, `~/.inputrc` with
`$if kash`) and edits a whole multi-line entry; Tab indents at line start.
Tab elsewhere opens a grouped, described completion menu: `$` bindings,
functions after `(`, commands at command position, and paths elsewhere.
The menu has a terminal-width separator and display-only file-type marks:
directory `/`, executable `*`, symlink `@`, FIFO `|`, socket `=`, device `#`.
Type colours are disabled by `NO_COLOR` or `TERM=dumb`; only a directory's
usual trailing slash is included in inserted paths, not the extra type marks.
Entries are syntax-highlighted, with unknown commands in red; `NO_COLOR`
disables it. History defaults to repository/CWD-local recall, 15-day retention,
and 1 MiB per scope. Ctrl-X h toggles local/global recall; `history scope
[local|global]` reports/selects the view, and `history clear local|global`
deletes entries. Likely secrets and space/tab-prefixed commands are not recorded;
detection is best-effort. `KASH_HISTORY_MAX_AGE_DAYS`, `KASH_HISTORY_MAX_BYTES`,
and `KASH_HISTORY_SCOPE` configure defaults. Persistence uses `$KASH_HISTORY`
or the XDG state path; explicitly empty `KASH_HISTORY` disables all history.
Legacy history is discarded without backups. Normative details:
[interactive Kash](../../../spec/052-kash-interactive.md) and
[history](../../../spec/053-kash-history.md).
