# Kash Streaming, Channels, and Multiplexing

## 1. Purpose and scope

Kash extends familiar byte pipelines into bounded, asynchronous process graphs:
explicit fan-in/fan-out, conditional decomposition and rejoin, named channels,
and live stream arguments. It is not a message broker, a graph-editor format,
or a replacement for Kame value pipes.

This specification is normative. It extends [017 Kash](017-kash.md) and
[038 Direct Kash and values](038-direct-kash-and-values.md) for process grouping,
graph connections, redirection ownership, capture, and lifecycle. Other value,
capability, phase, recovery, and argument rules remain unchanged. Process
channels are distinct from [012 reactive streams](012-streams.md) and build
dependency edges. Host process contracts remain subject to
[003 POSIX process](003-posix-process.md) and
[035 Native Windows CLI](035-native-windows-cli.md), with the host boundaries
in §10.

Unsupported hosts must reject unsupported graphs explicitly, before launch or
output truncation. Undecided extensions are confined to §12.

## 2. Graph model and invariants

| Term | Meaning |
| --- | --- |
| Stage | An external executable or a native Kash stream operation. |
| Channel | A bounded, closeable byte or framed-item connection with exactly one writer and one reader. |
| Port | A stage/operator connection point: stdin, stdout, a live argument, or an operator input/output. |
| Graph | Stages, operators, edges, and explicit external/presentation sinks, constructed before launch. |
| Process group | Two or more independent graph operands, not a Kame collection or an OS process group. |
| Selected branch | The graph receiving items selected by a filter. |
| Main remainder | Items not selected by that filter. |
| Next continuation | The immediately following stage/operator in the enclosing pipeline. |
| Otherwise continuation | The first non-filter continuation after a contiguous filter chain at the same lexical depth. |

Every data channel is point-to-point. Sharing a channel requires an explicit
merge, broadcast, or balance operator; multiple endpoint references do not
create subscriptions. Channels cannot escape their owning invocation or become
Kame scalar values. Items retain immutable payload ownership while routed.

Terminal stdout/stderr forwarded to the host are **presentation effects**, not
implicit merge edges or composable shared ports. Separate terminal stages may
present output concurrently without creating a downstream channel. A selected
branch never rejoins implicitly.

The whole graph owns backpressure, cancellation, and completion. Streams must
not be secretly captured, materialized, spooled, or reparsed as code. Stages are
long-running nodes; only an explicit `each` operation starts executables per item.

## 3. Syntax and graph construction

### 3.1 Forms and cardinalities

`A`, `B`, `C`, and `P` below stand for authored processes/graphs and a Kame
predicate, not builtins. N means at least two connected endpoints.

| Form | Cardinality | Contract |
| --- | --- | --- |
| `A \| B` | 1:1 | Connect stdout to stdin by default; bytes remain bytes. |
| `(A, B) \|(merge) C` | N:1 | Merge independent inputs with compatible stream kinds. |
| `A \|(broadcast) (B, C)` | 1:N | Copy input to every destination; lossless by default. |
| `A \|(balance) (B, C)` | 1:N | Assign each framed item to exactly one destination. |
| `A \|(filter P \| B) \| C` | 1:2 | Selected items enter B; only the remainder continues to C. |
| `A \|(frame :lines)` | 1:1 | Convert bytes into bounded line records. |
| `A \|(encode :lines)` | 1:1 | Emit framed payload bytes without private envelopes. |
| `A \|(each) command args` | 1:1 | Execute one prepared command per framed item; output bytes. |

`|(...)` is dedicated graph syntax, neither command substitution nor a Kame
application. Unary adapters compose with subsequent connections; `merge` may
feed another adapter directly. Group-to-group topology must use explicit
operators: a plain pipe never broadcasts or merges. Both `A | (B, C)` and
`(A, B) | C` are static cardinality errors, as are single-input merge and
single-destination fan-out.

A process group has at least two comma-separated process graph operands, all
running concurrently under one owner. It implies no connection among members.
A member beginning with `|(...)` receives input from its owning fan-out; a
standalone group does not invent an upstream connection. For example:

```kash
source |(frame :lines) |(balance) (
    |(each) worker-a > ./left,
    |(each) worker-b > ./right
)
```

Each input port still requires exactly one connection. Acceptance, recovery,
and async execution apply to the enclosing graph, not individual group members.
Any future library stream operator must declare its cardinalities, input/output
kinds, buffering, failure behavior, and pure/effectful/process-starting nature.

### 3.2 Lexing and precedence

The parser must distinguish Kash process syntax from embedded Kame syntax:

| Token/form | Context and meaning |
| --- | --- |
| `\|(` | Maximal-munch configured graph connection. |
| `(A, B)` | Group only in process-operand position with a top-level comma. |
| `<(` | Anonymous live producer in argument position. |
| `<&`, `<&name` | Standalone live argument operands. |
| `&name` | Named reader in process-operand position. |
| `>&name` | Named writer exit, including sink-only group operands. |
| `>&`, `>@` | Final conditional-branch disposition: next/otherwise rejoin. |
| terminal `&` | Async execution of the whole graph, not fan-out. |

Nested commas inside quotes, expressions, collections, arguments, or `<(...)`
do not discriminate groups. A single `(A)` remains a Kame expression, not a
subshell/group; square brackets remain Kame collections. `$reference`,
`${reference}`, `(EXPRESSION)`, `$(graph)`, and typed argv/list splicing retain
their existing meanings. `$(...)` captures; `<(...)` streams.

Binding order, tightest to loosest:

1. Quoted words, embedded expressions, substitutions, live operands, and word composition.
2. Process groups and stage redirections, including writer/rejoin exits.
3. Direct and configured connections, left-to-right, retaining lexical continuations.
4. Terminal acceptance `?`, then right-associative recovery `? ELSE`.
5. Terminal async `&`, covering the recovered graph.
6. Statement separators `;` and newline.

Existing separator, quoting, and `??` value-recovery rules remain authoritative.
`2>&1` must be rejected, never silently interpreted as Bash descriptor
duplication. Output bytes, argument values, and Kame values are never reparsed
as Kash. ASTs must retain operator kind, predicate/branch/substitution spans,
and rejoin kind; formatting must preserve these parser boundaries.

### 3.3 Closed policy grammar

Only merge/broadcast/balance accept the following options. Keys may occur once;
values must be the closed literals shown, never computed expressions. Unknown,
missing, duplicate, out-of-range, or operator-inapplicable options fail before
effects (`PARSE_ERR` for invalid source syntax).

| Option | Operators | Values/default |
| --- | --- | --- |
| `:buffer BYTES` | merge, broadcast, balance | Integer 1..65536; default 16384 for bytes, 65536 per framed input. |
| `:overflow POLICY` | merge, broadcast, balance | `block` by default; only framed broadcast also allows `drop-new`, `drop-old`, `latest`. |
| `:items N` | Explicitly lossy broadcast only | Integer 1..1024, default 64; `latest` defaults to and permits only 1. |
| `:strategy round-robin` | balance | Optional spelling of the sole assignment strategy. |
| `:order ORDER` | merge | `ready` by default; `round-robin` requires framing. |
| `:failure POLICY` | merge, broadcast, balance | `fail-fast` or `collect`; graph-wide inheritance, default `fail-fast`. |

`frame`/`encode` accept exactly `:lines`; `filter` accepts one Kame predicate
expression and a selected branch; `each` has no policy options and consumes a
following command template, not an arbitrary group or implicit pool.

### 3.4 Preparation and effects

Resolve the complete syntax-only topology, including named aliases, branch
continuations, adapters, mergers, cardinalities, kinds, failure policy, and
cycles, before demanding command arguments. Reject all feedback cycles until
an explicit feedback/delay operator defines buffering and initial tokens.

Before launching any executable in the owning graph or creating/truncating its
outputs, prepare and validate every reachable member, branch, anonymous producer,
tool, grant, argument, setup value, and redirection. Explicit input redirections
must demand build-produced inputs through the existing dependency observer and
consume their published signatures before graph launch, including in ordinary
linear pipelines. Opaque filesystem reads and subprocess stdout do not infer
build prerequisites.

Start connected peers together as one graph, not a sequence of independently
awaited `run!` calls. This includes branches receiving no items. Lazy activation
must not change these semantics. Launch-time failure after output files open
does not imply filesystem rollback or atomic output publication.

Stage redirections remain single-owner: stdin cannot also have a graph input,
and stdout cannot also have a graph output. These are port conflicts, not
first/last-position rules for a multi-branch graph. The `<&` exception in §6
claims input as an argument and permits independent stdin redirection. Normal
capabilities, inherited resource ceilings, cwd, environment, tracked executable
identity, and phase restrictions apply to all graph work; nesting grants none.

Kash recipe reuse must conservatively fingerprint the full source/configuration
context of runtime predicates and helper closures without running predicates to
discover dependencies. Unchanged contexts may reuse material-input proofs;
changed captured configuration must invalidate them. Runtime-discovered build
dependencies remain engine edges, not process channels or progress events.

## 4. Framing and predicates

### 4.1 Line records

External stdin/stdout/stderr are bytes. Read/write chunks are not records and
must never define item boundaries. `frame :lines` incrementally assembles UTF-8
records ending in LF, retaining the exact payload including LF or CRLF. A
nonempty partial final line is a complete item; empty input produces no items.
No newline is synthesized, normalized, stripped from forwarded payloads, or
added after a partial EOF record.

A record is at most **65536 payload bytes, including its delimiter**. Invalid
UTF-8 fails with `KASH_FRAME_ENCODING`; a larger line fails with
`KASH_RECORD_LIMIT`, never successful truncation. Private framed transport
retains item boundaries, including partial final lines, using a four-byte
envelope in addition to payload. That envelope is neither process input nor
part of payload budgets/counters.

`encode :lines` emits the original payload bytes. External stdin, live argument
operands, terminal presentation, and capture automatically receive payload
bytes through prepared encoding adapters, never private frame headers.
External stdout is always bytes, even if it appears line-shaped. It must be
explicitly reframed before joining a framed remainder. A merge/rejoin requires
compatible kinds and cannot assume subprocess writes are frames. A framer
requires bytes; encode, filter, balance, and each require framed input.

### 4.2 Predicate contract

Evaluate `filter P` once per complete item using the shared Kame evaluator in
the filter's lexical scope, with a fresh `item` binding. `item` is the decoded
string without its terminating LF or CRLF; a callable predicate receives that
same string as its argument. The result must be Boolean, not general Kame
truthiness (`KASH_PREDICATE_BOOLEAN` otherwise).

Predicates must be pure and nonblocking: no host observations, process/tool
execution, effects, dynamic evaluation, or suspension, even when the invocation
has grants. Such attempts report `KASH_PREDICATE_EFFECT`; other evaluation
failures report `KASH_PREDICATE_FAILED`. Predicate failures fail the graph.
Filtering never launches a command per item: the selected branch's ordinary
stages stay alive across all matching records.

## 5. Conditional branches and rejoin

`A |(filter P | B) | C` sends each item to exactly one path: true to B,
false to the main remainder feeding C. Selected items leave the remainder;
B's output is independent. Nested filters may recursively partition a branch.

Each branch output has exactly one disposition: terminal presentation, file
redirection, named writer `>&name`, next rejoin `>&`, or otherwise rejoin `>@`.
Combining dispositions requires an explicit broadcast. Rejoin tokens are legal
only as final exits inside a conditional branch with a valid continuation.
Rejoins merge **branch output**, not original selected input, with the remainder;
they never feed output backwards through the selecting filter.

- **`>&`** joins at the immediately following stage/operator. In
  `A |(filter P | B >&) |(filter Q | C) | D`, Q sees both P's remainder
  and B's output.
- **`>@`** joins at the first non-filter continuation after the maximal contiguous
  filter chain at the same lexical depth. It bypasses subsequent filters, not
  that continuation's downstream stages. An ordinary mux or adapter, including
  `merge` or `encode`, is a boundary. Nested chains resolve only their own
  continuations; a missing destination is `KASH_REJOIN_OUT_OF_SCOPE`.
- **`>&name`** connects branch output to the named writer; its sole reader is
  `&name`. Neither the reader nor writer implicitly rejoins the remainder.

An inserted rejoin merger uses default ready ordering and requires compatible
framing. For example:

```kash
cat ./events.log |(frame :lines)
    |(filter (includes? "ERROR" item) | normalize |(frame :lines) >&)
    |(filter is-invalid | report-invalid)
    |(encode :lines) | archive
```

Normalized records are screened by `is-invalid`; replacing `>&` with `>@`
bypasses that filter and joins at `encode :lines`. A branch without rejoin
remains a terminal side branch. Even a terminal filter retains a separate
remainder output; neither output is silently discarded or concatenated.

## 6. Named channels and live operands

### 6.1 Lexical channel endpoints

`stream name` declares syntax, not an evaluated value, running task, or persistent
queue. Names match ASCII `[A-Za-z_][A-Za-z0-9_-]*`; `in`, `out`, and `err` are
reserved scope-port names, not available external endpoints.

Each referenced channel must bind exactly one `>&name` writer and one reader
(`&name` or `<&name`) **inside the same constructed graph**, under a visible
lexical declaration. Duplicate or missing endpoints are static errors. A
declaration does not make sequential statements concurrent; launching a separate
async graph does not make a channel escape its owner. Control bodies inherit
outer declarations; body-local declarations do not escape.

```kash
stream reports
(
    (producer-a, producer-b) |(merge) >&reports,
    &reports | consumer
)
```

`>&name` may also be a sink-only operand in a fan-out group. Aliases lower to
direct pipes without extra worker processes and preserve framing unchanged;
a terminal reader requires a presentation adapter. Resolve every alias and
reject cycles before argument demands. Multiple writers require merge;
multiple readers require broadcast or balance.

### 6.2 Positional stream arguments

| Form | Contract |
| --- | --- |
| `<&` | Claim exactly one current upstream channel at this argument position, instead of stdin. |
| `<&name` | Consume the named channel's sole reader at this argument position. |
| `<(graph)` | Start an owned anonymous producer with the receiver and use its output as one live argument. |

Live operands are standalone arguments, not command heads, mixed word
fragments, captures, or string interpolation. No eager buffering, trimming, or
materialization occurs. Each consumes one channel; a repeated current claim
is `KASH_DOUBLE_CONSUME`, and repeated named readers violate channel cardinality.

When `<&` claims upstream, stdin is closed unless independently supplied, e.g.
by `< path`. Thus `A | diff -u <& <(B)` is valid, while two `<&` claims are
not. Ordinary `< path` remains stdin redirection; whitespace must not silently
turn positional `<&name` into a different channel operation (see §12).

An anonymous producer must have exactly one unredirected terminal output;
explicit side sinks remain owned. Ambiguous outputs fail rather than being
implicitly concatenated. Its lifetime, failure policy, and cleanup belong to
the receiving graph. A receiver exiting early closes/cancels its producers
under the ordinary closed-reader rules.

Argv list splicing must finish before host mapping of argument ports to actual
inherited descriptors. Static `argumentItem` identifies an authored item slot,
not an expanded argv offset. For native stream operations an argument may be a
typed handle; external commands receive a validated host-accessible pipe path
or equivalent, never a closed/unreadable descriptor advertised as usable.

These streams are non-seekable. Repeated opens, seekability, and path-relative
regular-file metadata are not guaranteed. Commands requiring seekable files
report their own I/O failure (e.g. `ESPIPE`); Kash neither detects all such
requirements nor silently materializes input. Use an explicitly materialized
file when needed.

## 7. Scheduling, bounds, and backpressure

### 7.1 Working storage

Every channel has bounded capacity; full channels backpressure producers.
Lossless operators must not drop data or introduce unbounded queues.
`:buffer` bounds **each operator input's working payload storage**, not OS pipes
or bounded host stream internals. Byte operators transfer at most that many
bytes per turn. Framed operators assemble at most one record per input within
that budget, plus its four-byte envelope. A larger record fails with
`KASH_BUFFER_LIMIT`, never splits or drops silently, including in lossy modes.

Defaults are 16 KiB for byte transfer and 64 KiB per framed input; the fixed
64 KiB ceiling cannot be raised by a nested operator. Explicit merge budgets
still apply when merge feeds an adapter directly. All limits remain bounded by
invocation resource policy; nested configuration cannot broaden caller grants.

### 7.2 Merge

Byte merge preserves each source's byte order, not messages or global order;
interleaved chunks may corrupt application-level records. Framed merge must
forward entire records atomically and preserve each source's item order, with
one bounded assembly buffer per input.

- **`ready` (default):** fairly service ready inputs without starvation or a
  global event-order/deterministic-output promise. A round-robin-ready scheduler
  satisfies this contract. Implicit rejoin mergers use this policy.
- **`round-robin`:** framed inputs only. Starting with the first prepared input
  edge, forward one complete record per still-open input in prepared edge order.
  Wait for the current input even if another is ready; advance only after the
  full record is forwarded, skipping inputs only after EOF. Empty/unequal
  producers drain losslessly. Byte streams fail before effects because read
  chunks are not records.

Strict port turns are not reconstruction of original source identity after
filters/transforms change cardinality. They intentionally stall fast branches
and can expose application demand deadlocks; ready ordering is appropriate
when branches depend on each other's consumption. Neither mode adds reorder
history, spooling, or per-record processes.

### 7.3 Broadcast and overflow

Default `block` is lossless for bytes or framed items: every destination receives
all input, and the slowest destination eventually backpressures the source.
It uses one bounded transfer and no additional pending queue.

Only framed broadcast permits explicit loss. Each output has its own pending
queue, bounded by both `:buffer` payload bytes and `:items` records:

| Policy | Full/pending queue behavior |
| --- | --- |
| `drop-new` | Discard the new record when it cannot fit. |
| `drop-old` | Evict oldest pending records until the new record fits. |
| `latest` | Replace all pending records with the newest; capacity is exactly one pending record. |

Each output may additionally hold **one in-flight record**, at most `:buffer`
payload bytes. It must never be evicted, including when partially written.
Input assembly, each pending queue, and each in-flight record are distinct
bounded allocations. Four-byte headers, bounded queue metadata, kernel pipes,
and bounded Node stream buffers are extra, not counted as payload budgets.
Survivors keep their original order and complete framing; survival is
scheduling-dependent independently per output. EOF drains retained records.
A failed/closed child still fails the graph. Merge and exactly-once balance
have no lossy overflow policy.

### 7.4 Balance

Balance requires framing and assigns each complete record to exactly one child
in strict round-robin destination-group order, starting with the first. Preserve
each child's assigned order; never skip a slow child based on readiness.
Its next assignment backpressures the source. All children receive EOF,
including those assigned no records. No invisible per-item process is spawned.

### 7.5 Explicit per-item workers

`source |(frame :lines) |(each) command args` starts one directly executed
command per complete record, with its exact payload on stdin followed by EOF.
Empty input starts no command. Prepare the template's argv, tracked tool,
grants, cwd, environment, and redirections **once before graph launch**; record
data is never parsed or substituted into argv. Stdin redirection and live
operand claims are invalid on the template. Output redirections open once per
graph, not once per record.

Each operator has at most one active executable, one input record of at most
64 KiB, and bounded byte transport. Stdin and stdout must progress concurrently
to avoid pipe-capacity deadlocks. Output is a byte stream concatenating whole
worker outputs in input order, not guaranteed to retain input cardinality.
Explicit reframing is required before a framed rejoin. Parallelism is explicit:
balance records across multiple `each` branches. No implicit pool, batch capture,
retries, or per-record outcome history is retained. Descendants of authored
executables retain existing process-tree ownership, not a new process quota.

Ordinary nonzero worker exits obey the graph failure policy. Under collection,
remaining records and siblings finish; the worker node retains its last nonzero
exit and the graph uses prepared-node aggregate ordering. Signals and
launch/transport failures remain fatal. Template `:timeout` covers the entire
operator lifetime, including input waits, not each record separately. Workers
remain invocation-owned under async execution and anonymous producers.

## 8. Completion, failure, and capture

### 8.1 EOF and ownership

Writer close yields EOF after queued data drains; reader close propagates the
established process-pipe failure behavior. Merge closes only after all producers
close and its data drains. Broadcast drains retained copies; balance drains
assigned records and closes every output. Filter closes both paths after input
EOF and pending delivery. Rejoin closes only after remainder and joined branch
production complete. Each finishes all workers before closing output.

On normal completion join every owned branch, worker, relay, and anonymous
producer before reporting success. Terminal `&` returns an invocation-owned
async graph handle, not detached subprocesses. Failure/cancellation closes
endpoints, stops remaining owned work, reaps children, and releases buffers,
descriptors, and temporary resources. No implicit daemons remain.

### 8.2 Failure policy and recovery

Default `fail-fast` fails and cancels the graph on a nonzero exit, signal
(including SIGPIPE), launch/capability failure, mux I/O error, predicate/framing/
buffer failure, timeout, or cancellation. There is no implicit acceptance of
early readers or closed children.

`:failure collect` on any merge/broadcast/balance selects an **owner-wide**
policy, not branch isolation. Omitted annotations inherit it; all explicit
annotations in one graph, including anonymous operands, must agree or fail
statically with `KASH_FAILURE_POLICY`.

Collection changes only ordinary nonzero executable exits: preserve sibling
output/EOF/outcomes and wait for them to finish. Any nonzero collected status
still fails the graph. Signals, launch/capability failures, operator errors,
timeouts, and cancellation remain immediately fatal and override collected
ordinary failures. A peer that never closes may require an invocation deadline;
collection adds no detachment, retries, or unbounded error/output history.

Retain independent per-stage outcomes. Aggregate status is the last failing
prepared stage/node, not the last observed exit or last closing producer; signals
retain the existing `128 + signal` convention. An accepted failure retains its
failing node metadata. External exit codes must not be mistaken for reserved
native operator diagnostics.

Existing `?`, graph recovery, value recovery, and owned async observation retain
their contracts at whole-graph scope. Predicate, framing, and buffer failures
participate in graph recovery, not exit acceptance. Capability denial and
cancellation remain unhandleable. Recovery follows cancellation/reaping or
collection, never leaves old graph work detached, and cannot silently convert
an unaccepted nonzero result into success.

### 8.3 Capture and presentation

`$(graph)` captures the graph's terminal stdout and explicit `>&out` presentation
ports under existing UTF-8 and capture bounds; stderr remains live. Explicit
`>&err` and file redirections are noncapture dispositions. More than one output
directed to the capture sink is `KASH_AMBIGUOUS_CAPTURE`: explicitly merge or
redirect unrelated outputs. Never silently concatenate, discard, or bypass
limits. Zero capture outputs produce an empty string, including a multi-branch
graph whose outputs all use noncapture dispositions. The existing redirected
linear-pipeline capture remains empty, without duplicating stdout.

Noncapturing terminal stdout/stderr use the host's public sinks. These effects
do not supply hidden readers/writers to graph channels or retain output in the
Result. Framed terminal/capture outputs must drain their encoding adapters
before completion is reported.

## 9. Inspection and results

### 9.1 Static topology

Inspection must expose syntax-only resolved topology: prepared node IDs, kinds,
ports/edges, stream kinds, inserted adapters/mergers, channel cardinalities,
predicate/branch spans, and lexical rejoin destinations. Show effective buffer
budgets, overflow/blocking policy, scheduling/ordering, and graph failure policy;
`each` reports `maxWorkers: 1`. Commands and executable identities must
distinguish authored candidates from runtime-tracked tools.

Inspection must not evaluate arguments, policy expressions, or predicates,
resolve runtime dependencies by running code, launch processes, retain stream
records/stdout, or fabricate timing-dependent branches. Invalid topology must
remain diagnosable with authored spans.

### 9.2 Terminal graph metadata

One aggregate Result retains independent per-node outcomes and terminal
`graph.nodes`/`graph.edges`, with `graph.terminal: true` and effective
`graph.failure`. Nodes retain prepared `id` (including adapters), `kind`,
tracked executable `program`, terminal `state`, and `status`/`signal`/`outcome`.
Worker nodes are `each`; missing terminal state information remains unknown.
Edges retain `from`/`to`, stream `kind`, and authored `argumentItem` slots.

Operator-owned edges expose named measurements:

- `bytes`: payload bytes only; four-byte frame envelopes are excluded.
- `items`: complete framed records; byte edges have no item count.
- `droppedItems`: framed-output drops; lossy outputs also expose
  `queueHighWaterBytes` and `queueHighWaterItems` for pending queues.
- `measurement`: `completed-write`, `read`, or `unavailable`. Completed-write
  counters exclude partially completed writes; reader-side bytes count bytes
  actually read by an operator. Queue high-water marks are not current occupancy.
- `saturated`: counters saturate at **2^53-1** and set this flag, preserving
  exact portable integers on native and Node hosts.

Direct external-to-external pipes remain uninstrumented. Unavailable counters
are nil, never fabricated zeroes; absent host telemetry is supported explicitly.
Lossless transfers have no pending-queue high-water mark. Terminal counters are
sampled after all children exit. Telemetry allocation failure must leave
measurements unavailable rather than fail work. Malformed supplied telemetry
must be rejected as a host error, not converted into invented measurements.
Metadata is not an implicit output effect and does not change capture values
or failure diagnostics.

### 9.3 Live progress

Expose advisory `process-snapshot` events through `--json` and program/embedding
event streams, with `graph.terminal: false` and effective graph failure policy.
Nodes contain prepared `id`, `kind`, tracked `program`, and `state`; edges
contain endpoints, stream kind, prepared edge-array `id`, expanded `argvIndex`
(zero for stdin, unlike authored `argumentItem`), and the same named measurements
as terminal Results. IDs are scoped by request and node/generation/attempt
identity; stale events cannot identify a newer invocation's work.

Hosts sample no more often than every 100 ms, apart from Node launch/final
samples. Unread snapshots coalesce per request at the native host, program
event queue, and JavaScript embedding collector. Sampling must retain neither
records nor stdout. A Node sampling timer belongs to the request and must be
cancelled on every completion path.

Operators distinguish `running`, `waiting-input`, and `waiting-output`, and
terminal states when known. Wait states describe pending I/O interest, not a
deadlock diagnosis: lossy broadcast may wait for output while accepting input.
External processes expose running/exit state only; internal waits and OS pipe
occupancy remain unknown. Native counters are independently sampled relaxed
atomics, not a transaction. Cross-field conservation is guaranteed only for
completed successful graphs. Allocation failure may omit live samples; terminal
Results remain authoritative. Progress infers no new build prerequisites.

## 10. Host boundaries

POSIX native and WASM/Node hosts must preserve the same graph, payload,
backpressure, failure, and ownership semantics. Native muxes are invocation-owned
long-running workers; byte muxes each have one transfer buffer and bounded OS
pipes. Node byte muxes use paused descriptor-backed streams and one bounded
transfer per operator. Default byte transfer storage is 16 KiB; framed and
configured budgets follow §7. Native telemetry uses an invocation-owned shared
mapping with one writer per edge; allocation failure must not prevent execution.

Native POSIX live operands use inherited descriptors with validated usable
operand paths. Node uses one owned bounded `dd` relay per argument into a
private FIFO pathname. The relay must perform a real reader/writer open
rendezvous: reopening an existing FIFO descriptor after a fast producer exits
must not hang. A fixed POSIX shell opens the FIFO and acknowledges readiness
before becoming `dd`; user commands and payload bytes are never shell source.
An early receiver exit must unblock/close even a never-opened operand without
a permanent dummy writer. Join/reap relays with the graph and remove FIFO names
on completion and cancellation. This is pipe transport, not regular-file
materialization.

Native Windows rejects this streaming topology before launch/output truncation;
existing linear pipelines remain unchanged. Hosts without validated descriptor
paths/FIFO support must reject live operands with
`KASH_STREAM_OPERAND_UNSUPPORTED`. A host may not advertise an unreadable path
as a supported equivalent. Host-side graph request validation must independently
enforce kinds, cardinalities, policy values/bounds, argument slots, cycles, tools,
grants, and redirection conflicts before effects; malformed forwarded requests
must not silently downgrade an authored policy.

## 11. Diagnostics and acceptance

| Code | Required meaning |
| --- | --- |
| `KASH_CHANNEL_DUP_WRITER` | Multiple named-channel producers. |
| `KASH_CHANNEL_DUP_READER` | Multiple named-channel consumers. |
| `KASH_CHANNEL_UNBOUND` | Missing/out-of-scope endpoint or unbound current input. |
| `KASH_MUX_ARITY` | Operator/group endpoint cardinality or host topology incompatible. |
| `KASH_REJOIN_OUT_OF_SCOPE` | Invalid branch exit or missing lexical continuation. |
| `KASH_DOUBLE_CONSUME` | Current channel/port claimed twice or conflicting redirection. |
| `KASH_FRAME_REQUIRED` | Item-aware operation receives bytes. |
| `KASH_FRAME_MISMATCH` | Incompatible framed/raw kinds at an adapter, merge, or rejoin. |
| `KASH_FRAME_ENCODING` | Line record is not valid UTF-8. |
| `KASH_RECORD_LIMIT` | Line record exceeds 64 KiB including delimiter. |
| `KASH_BUFFER_LIMIT` | Complete record exceeds configured working payload budget. |
| `KASH_PREDICATE_EFFECT` | Predicate attempts an effect, host observation, or suspension. |
| `KASH_PREDICATE_BOOLEAN` | Predicate returns a non-Boolean. |
| `KASH_PREDICATE_FAILED` | Other predicate evaluation failure. |
| `KASH_STREAM_OPERAND_UNSUPPORTED` | Host cannot provide a valid live operand. |
| `KASH_GRAPH_CYCLE` | Unsupported feedback cycle. |
| `KASH_FAILURE_POLICY` | Conflicting explicit owner failure policies. |
| `KASH_AMBIGUOUS_CAPTURE` | Multiple unmerged capture/producer terminal outputs. |

Existing capability, phase, timeout, executable, capture, and process diagnostics
remain authoritative where applicable. Errors must identify authored spans,
including nested predicates/branches/substitutions, without embedding stream
payloads in diagnostic causes.

Acceptance must cover the following contracts:

| Area | Required checks | Related tests |
| --- | --- | --- |
| Compatibility/topology | Ordinary byte pipelines, Kame expressions/value pipes, typed argv, captures, recovery and async retain semantics; group discrimination/cardinality, no Bash duplication; invalid graphs launch/truncate nothing. | `tests/T042-01-kash-topology.sh`, `T017-*`, parser/evaluator suites |
| Framing/branches | Exact LF/CRLF/partial EOF, empty input, UTF-8 and exact byte limits; Boolean host-free predicates; nested lexical next/otherwise joins, missing destinations and framed/raw mismatch; changed predicate context invalidates build reuse. | `tests/T042-02-kash-framing.sh`, stream-predicate tests |
| Channels | One lexical writer/reader in one graph, inherited/body-local declarations, alias framing, terminal readers, duplicate/missing endpoints and cycles. | `tests/T042-03-kash-channels.sh` |
| Operands | Positional ownership distinct from stdin, list-spliced slots, concurrent anonymous/named producers, no capture/materialization; visible seek failure, unsupported-host preflight, early receiver/producer exit, cancellation and resource cleanup. | `tests/T042-04-kash-operands.sh` |
| Scheduling/bounds | Slow-consumer lossless pressure, exactly-once round-robin balance, EOF for unassigned children; per-input payload budgets including delimiters; independent bounded lossy queues and non-evictable partial writes; strict merge turns, unequal/empty/slow producers. | `tests/T042-05-kash-scheduling.sh`, `T042-06-kash-lossy.sh`, `T042-09-kash-ordering.sh` |
| Ownership/failure | Fatal/default failures cancel/reap all owned work, including anonymous producers and async graphs; collection preserves ordinary failed siblings and deterministic aggregate ordering but never masks fatal failures; explicit graph recovery/acceptance. | `tests/T042-01-kash-topology.sh`, `T042-10-kash-failure-policy.sh` |
| Inspection/telemetry | Effect-free resolved topology and policies, sampled waits without payload retention, terminal authoritative payload-only counts/high-water marks, nil unavailable measurements, saturation and malformed host data. | `tests/T042-07-kash-metrics.sh`, `T042-08-kash-live.sh`, AST/snapshot suites |
| Workers | One prepared executable per record, one active worker per node, exact stdin/EOF, concurrent bidirectional I/O, ordered byte output, once-opened redirections, ordinary grants, collection and cancellation; no later worker after fatal failure. | `tests/T042-11-kash-each.sh` |

Streams larger than capture limits must remain bounded when only a small final
output is captured. Missing host telemetry does not imply unsupported execution.
Acceptance makes no claim of seekability detection, implicit disk spooling,
transactional live measurements, or native Windows streaming support.

## 12. Reserved presentation ports and remaining extensions

`>&out` and `>&err` are reserved terminal presentation writers, not named
channels or data edges. `out` inherits the current output sink (the capture sink
inside `$(...)`); `err` inherits the current error sink and is excluded from
capture. They may terminate selected branches or serve as sink-only graph
writers. Framed payloads are encoded before presentation. A stdout port has one
disposition, so conflicting graph/file/output-port destinations fail before
launch. `in` remains reserved and unavailable; `<&name` remains a positional
channel operand. Arbitrary descriptor duplication such as `2>&1` is unsupported.

The zero-output rule is explicit: no capture-directed outputs yields an empty
string, while more than one is `KASH_AMBIGUOUS_CAPTURE`. Each terminal stdout and
explicit `>&out` counts as one output. Error ports, files, and internal channels
do not count as capture outputs; named channels are counted at their resolved
terminal destinations.

The following features remain extensions requiring separate design:

- Larger `flow:`/`graph:` construction blocks; lexical declarations in one
  constructed graph are already defined.
- Whether whitespace-separated `command < &name` should mean channel stdin
  remains undecided; `<&name` already means a positional operand.
- Anonymous live producers require exactly one output.
- Other framers (`:jsonl`, `:length-prefixed`, typed sources), explicit chunking,
  weighted/key/readiness scheduling, worker pools, and bounded explicit disk
  spooling/materialization.
- Original-source ordered rejoin after cardinality-changing branches: it would
  require explicit sequence identity and bounded lookahead, unlike defined
  port-turn merge ordering. Feedback requires explicit delay/initial tokens.
- Branch isolation/retries and deliberate accepted early-consumer/SIGPIPE
  outcomes; collection is not isolation or acceptance.
- Additional host realizations, including native Windows streaming/operands,
  optional lazy node activation preserving eager graph semantics, and
  stronger live diagnostics such as current occupancy or deadlock analysis.
