# Kash Streaming, Channels, and Multiplexing

> This is a proposed extension to Kash, not a description of current Kame behavior. Syntax tagged **proposed** is subject to parser and implementation validation. Sections marked **Open** identify decisions requiring language-design agreement.

## Implementation status

The first implementation slice supports process groups, byte-oriented `merge`
and lossless `broadcast`, explicit point-to-point topology, per-stage file
redirections, whole-graph acceptance/recovery/async ownership, and ambiguous
capture rejection on POSIX native and WASM/Node hosts. Ordinary linear pipelines
retain their existing execution path. This is not full conformance to this draft.

Native byte multiplexors are long-running, invocation-owned worker nodes, each with
one bounded 16 KiB transfer buffer and bounded OS pipes. Node multiplexors use
paused descriptor-backed streams and one bounded transfer per operator. Byte
merges preserve per-source byte order, not records or cross-source ordering.
Multiple terminal outputs are presentation effects, not implicit merge edges.
Acceptance and recovery currently apply only to the enclosing graph, not to
individual members of a process group. Windows rejects the new topology before
launch or output truncation; its existing linear-pipeline support is unchanged.

The next implementation slice adds explicit `frame :lines` / `encode :lines`,
Boolean filters, nested selected branches, and lexical `>&` / `>@` rejoins.
Line records are UTF-8, retain exact delimiter bytes, and are limited to 64 KiB
including the delimiter. A partial final line is a complete item. Predicates
receive `item` without its LF or CRLF terminator; a callable predicate receives
that same string as its argument. Predicates execute in the shared evaluator,
without host observations, process execution, dynamic evaluation, or suspension.
External commands automatically receive payload bytes, but their output must be
explicitly reframed before rejoining a framed remainder. Frame-safe merges keep
one bounded record assembly buffer per input and select ready inputs fairly.
The parse AST retains operator kind, predicate and branch spans, and rejoin kind,
plus a resolved syntax-only topology with node IDs, stream kinds, inserted
adapters/mergers, and edges. Inspection never evaluates predicates or arguments.
Kash recipe reuse conservatively fingerprints the full source/configuration
context of runtime predicates and helper closures; it does not run predicates to
discover build dependencies. Unchanged contexts can reuse material-input proofs,
while changed captured configuration invalidates them.

Named channels use lexical `stream name` declarations and must bind exactly one
`>&name` writer and one `&name` reader inside the same constructed graph. Named
aliases lower to direct pipes, not extra worker processes. The one exception is
a terminal reader, which needs a presentation adapter. Framing crosses aliases
unchanged; all aliases are resolved and cycles rejected before argument demands.
Separate sequential commands do not become concurrent just because they mention
the same declared name. Declarations in a control body remain local to that body.
Names use ASCII letters/underscore initially, then letters, digits, underscore,
or hyphen. The scope ports `in`, `out`, and `err` remain reserved, not generally
available external endpoints.

Stream operands, balance, and runtime topology inspection remain subsequent work. Process channels
are distinct from both reactive-engine streams and build dependency edges.
Explicit stdin redirections demand build-produced inputs through the existing
dependency observer before launching their owning graph, and consume their
published signatures for reuse and invalidation. This also applies to ordinary
linear Kash pipelines. No dependency
declarations are inferred from subprocess stdout or opaque filesystem reads.

## 1. Purpose

Kash is a command-oriented process language. Kame expressions are embedded in Kash for values and computations, but the primary Kash grammar is shell-like.

This specification extends Kash from linear process pipelines to an **asynchronous streaming process-graph language**, without sacrificing the familiar `A | B` case.

It defines:

- Point-to-point, single-producer/single-consumer **channels**.
- Explicit **multiplexing operators** for N-to-1, 1-to-N, N-to-M, and conditional routing.
- A compositional **conditional branch** that removes selected items from the main stream, preserving an unmatched remainder.
- Two explicit branch-rejoin destinations, `>&` (next continuation) and `>@` (otherwise continuation).
- Named channel endpoints using `&name` and `>&name`.
- Stream injection into command arguments via `<&`, `<&name`, and `<(process graph)`.
- Framing, scheduling, buffer, backpressure, completion, cancellation, and diagnostic requirements.

The intended result is a textual process/dataflow language; it is **not** a graph-editor file format, a message broker, or an implicit replacement for Kame's expression-level pipes.

### 1.1 Existing specifications

This draft proposes amendments to:

- [`017-kash.md`](https://github.com/sebastien/kame/blob/main/docs/spec/017-kash.md): current direct process pipelines, redirection, lifecycle, and precedence.
- [`038-direct-kash-and-values.md`](https://github.com/sebastien/kame/blob/main/docs/spec/038-direct-kash-and-values.md): direct Kash parsing, embedded Kame expressions, typed arguments, tool tracking, and ownership.
- [`012-streams.md`](https://github.com/sebastien/kame/blob/main/docs/spec/012-streams.md): existing reactive-engine stream/source protocol, which is **distinct** from the process channel type introduced here.
- [`003-posix-process.md`](https://github.com/sebastien/kame/blob/main/docs/spec/003-posix-process.md) and [`035-native-windows-cli.md`](https://github.com/sebastien/kame/blob/main/docs/spec/035-native-windows-cli.md): host process execution and pipe/handle management.

Where this draft differs from `017-kash.md`, the new behavior is *proposed*, not yet normative in the existing repository. In particular, `017` currently forbids parenthesized process grouping, leaves descriptor duplication undefined, and defines `|` only for linear process stages.

### 1.2 Design principles

1. **Shell first.** `A | B` remains a familiar byte-stream pipe from A's stdout to B's stdin.
2. **Every data channel is point-to-point.** One writer endpoint, one reader endpoint. Multiple parties require a multiplexor.
3. **Routing is a connection operation.** `A OP B`, where either side can denote one endpoint or a process group. `|(...)` configures the connection.
4. **Branches do not automatically rejoin.** Conditional branching retains an unmatched remainder; any return path is explicit.
5. **Streams remain streaming.** No hidden whole-stream capture or eager materialization.
6. **Backpressure and cancellation are properties of the whole graph.** Fan-out and merge are bounded and owned.
7. **Data, code, and executable identity do not get implicitly reparsed.** Kame expressions supply typed values; Kash supplies process syntax.
8. **No invisible process spawning per record.** Stream stages are long-running graph nodes unless a per-item worker operation is explicitly requested.

## 2. Terminology and model

| Term | Definition |
| --- | --- |
| **Process stage** | An external executable or explicitly designated native Kash stream operation. |
| **Channel** | A one-writer/one-reader bounded, closeable data connection. May carry bytes or framed items. |
| **Port** | A process or operator connection point (`stdin`, `stdout`, `stderr`, injected input, or operator input/output). |
| **Graph** | Stages, operators, channel edges, and configured external/presentation sinks, constructed before launch. |
| **Process group** | Multiple independent stages/graphs named together as operands for a multiplexor; **not** a Kame array. |
| **Main remainder** | The output of a conditional filter representing items not selected by that filter. |
| **Selected branch** | The graph consuming items selected by a conditional filter. |
| **Next continuation** | The input to the immediately following stage or routing operator in the enclosing pipeline. |
| **Otherwise continuation** | The first ordinary (non-conditional-branch) stage after a contiguous chain of conditional filters in the same lexical pipeline. |
| **Injected stream argument** | A live channel exposed at a command argument position, often as an inherited FD/handle-backed path for an external executable. |

### 2.1 Channel invariants

Each named or anonymous **data channel** has precisely one writer and one reader. The following are *connections*, not broadcasts or subscriptions:

```kash
stream reports
producer >&reports
&reports | consumer
```

The statements above illustrate endpoint syntax. **They must belong to the same constructed graph**, e.g. a process group or other simultaneous graph context; executing these as three independent synchronous statements is not valid wiring (see §7).

Neither of these is implicitly legal:

```kash
A >&reports
B >&reports       # ERROR: two writers

&reports | C
&reports | D     # ERROR: two readers
```

Connect a merge or broadcast operator instead. A stream declaration does not itself execute or create a background task.

### 2.2 Source channels versus public output

The graph has its own *composable* channels. Terminal stdout/stderr presentation to the CLI is a separate output effect, managed by the host; it must not be mistaken for an implicit channel joining back into the main graph. Multiple terminal processes may log to the host, but they do **not** thereby create a shared downstream data channel. Capture and output-ownership rules are discussed in §10.5 and **Open issue O3**.

## 3. Overview of proposed syntax

```kash
# 1 -> 1: existing shell-style pipeline
A | B

# N -> 1: explicit merge of concurrently running sources
(A, B) |(merge) C

# 1 -> N: explicit broadcast or load balancing
A |(broadcast) (B, C)
A |(balance) (B, C)

# Conditional decomposition; selected records leave the main path
A |(filter P | B) | C

# Transform selected records and merge into the immediate next continuation
A |(filter P | B >&) | C

# Transform selected records and merge into the otherwise continuation
A |(filter P | B >@) |(filter Q | D) | C

# Send selected branch to a named, point-to-point channel
A |(filter P | B >&selected) | C

# Consume a named channel
&selected | D

# Inject live input streams as command operands
diff -u <&left <&right
diff -u <(generate-left) <(generate-right)
A | diff -u <& <(generate-reference)
```

`P`, `Q`, `A`, `B`, etc. above are placeholders, not literal builtins. A predicate may be an embedded Kame expression, e.g. `(contains item "ERROR")`. `filter` requires record framing, as specified in §9.

## 4. Operator family and cardinality

### 4.1 Direct pipeline: `|`

`A | B` connects exactly one output channel to exactly one input channel; specifically, the source's stdout to the destination's stdin by default. It is **1:1 only**.

If A or B is a multi-element process group, a plain `|` without an explicit multiplexing policy is a static error. This differs from implicit broadcast/merge ideas previously considered and protects the single-producer/single-consumer invariant.

### 4.2 Configured multiplexor: `|(...)`

`|(...)` denotes a graph operator, not a command substitution or a Kame expression. It consumes an upstream channel or process group, applies an operator-specific routing/scheduling policy, and connects the result to the subsequent stages or groups.

Initial operator vocabulary:

| Form | Input cardinality | Output cardinality | Contract |
| --- | ---: | ---: | --- |
| `|(merge)` | N | 1 | Combine independent inputs; no global ordering guarantee. |
| `|(broadcast)` | 1 | N | Deliver each complete item to every output. |
| `|(balance)` | 1 | N | Deliver each item to exactly one output (default policy TBD). |
| `|(filter P | branch)` | 1 | 2 | Selected to branch; unmatched to remainder. |
| `|(frame ...)` | 1 | 1 | Establish explicit framing for subsequent item-aware operations. |
| `|(encode ...)` | 1 | 1 | Encode items to bytes for external processes. |

Operators are extensible as library-provided stream transformations, but any extension MUST declare input/output cardinalities, input/output data kinds, buffering contract, failure behavior, and whether it is pure, effectful, or process-starting. Builtin multiplexors use dedicated syntax and AST nodes, not textual rewriting of commands.

### 4.3 Process groups `(A, B, ...)`

A **process group** collects two or more process graph operands without implying a connection between them:

```kash
(cat ./a, cat ./b) |(merge) sort
produce |(broadcast) (consumer-a, consumer-b)
```

All members start concurrently when the enclosing graph runs.

**Parser constraint:** ordinary parenthesized Kame expressions remain valid. Process-group parsing applies only in Kash *process operand* position, and only when the parentheses contain a top-level comma between process expressions. A single `(A)` retains its existing Kame-expression meaning; it is not a process group. This deliberately avoids conflating arrays with process groups. Nested commas inside quotes, expression delimiters, arguments, or `<(...)` do not count.

**Open O1:** Alternative group delimiters can be considered if this syntactic discrimination proves fragile in the existing parser. Square brackets remain Kame collection syntax and should not be commandeered for process grouping.

## 5. Conditional branching and continuation

### 5.1 Default conditional branch: selected stream plus remainder

```kash
A |(filter P | B) | C
```

For every complete input item x:

- If `P(x)` is true, x goes to B's input, and is **removed** from the main remainder.
- Otherwise, x continues to C through the main remainder.
- B and C execute as concurrent parts of one graph.
- B's output **does not** automatically rejoin C.

The predicate is evaluated once per item at this filter. A side branch can contain another filter, creating recursive decomposition.

```
                    matched ───> B ───> branch terminal/sink
                   /
A ──> filter(P) ──┤
                   \ unmatched ───────> C
```

### 5.2 `>&`: merge branch output into the next continuation

```kash
A |(filter P | B >&) |(filter Q | C) | D
```

`>&` is legal only as a branch exit inside a conditional operator. It directs B's **output**, not the original matched input, to a merger at the input of the *immediately following* stage/operator, here `filter Q`.

Thus Q sees (1) items unmatched by P and (2) B's output, interleaved under the merger's framing and scheduling policy. B's output is not fed backwards through P.

For a simpler expression:

```kash
A |(filter P | normalize >&) | consume
```

`consume` receives the merge of normalized P items and the untouched remainder.

### 5.3 `>@`: merge branch output into the otherwise continuation

```kash
A
  |(filter P | transform-a >@)
  |(filter Q | transform-b)
  |(filter R | transform-c)
  | default-command
  | downstream
```

Here `>@` merges `transform-a`'s output directly into the input of `default-command`. It **bypasses** the Q and R filter operators; `downstream` receives only the result of `default-command` as usual.

`default-command` is called the **otherwise continuation**. It is defined lexically: the **first ordinary pipeline stage after the maximal contiguous chain of conditional filter operators** at the same nesting depth. An ordinary `|(merge)`, `|(encode ...)`, etc. counts as a boundary unless explicitly specified otherwise in a future grammar revision.

`>@` does not imply first-match semantics beyond the filters it bypasses; P, Q, and R already partition the stream sequentially.

**Static error:** `>@` without an otherwise continuation in the enclosing pipeline is invalid. Nested filter chains resolve `>@` against their own lexical enclosing chain, never against an unrelated outer chain.

### 5.4 Named branch exit: `>&name`

```kash
stream errors
(
    source |(filter is-error | format-error >&errors) | handle-normal,
    &errors | notify
)
```

`>&errors` connects the output of `format-error` to the **writer endpoint** of the named channel. The channel's sole reader is `&errors`. Neither branch nor consumer implicitly rejoins the remainder.

A branch may terminate in only one exit disposition: local rejoin (`>&`), otherwise rejoin (`>@`), named channel (`>&name`), explicit file/output redirection, or terminal process output. Combining two dispositions on the same output is an error unless an explicit broadcast precedes them.

### 5.5 Example: normalize, reject, process

```kash
source
    |(filter is-legacy | convert-legacy >&)
    |(filter is-invalid | report-invalid)
    | process-events
```

Converted legacy events re-enter immediately after the first filter and are eligible for `is-invalid`. Rejected events do not reach `process-events`.

### 5.6 Example: bypass subsequent filters

```kash
source
    |(filter needs-normalizing | normalize >@)
    |(filter is-duplicate | reject-duplicate)
    |(filter is-invalid | reject-invalid)
    | store
```

Normalized items go straight to `store`; they are deliberately **not** screened by the two subsequent filters. This is a semantic choice by the author, not an optimization. It can be useful but should be apparent in graph inspection.

### 5.7 Branch output semantics (provisional)

If a selected branch has no rejoin or named channel exit, it is a **terminal side branch**. Its final stdout may be forwarded to the invocation's public stdout sink, subject to the same public-output policy as other terminal processes, but is never automatically consumed by the main remainder.

An invocation that requests **capture** and has more than one unconnected terminal stdout must either (a) choose a single designated capture output with explicit sinks for all other terminals, or (b) fail static graph validation. This draft prefers (b) for the first implementation so `$(...)` never silently mixes unrelated outputs. See **Open O3**.

## 6. Named channels and endpoints

### 6.1 Declaration and references

```kash
stream channel-name
```

`stream` declares a graph-scoped channel identifier, not an array or an evaluated Kame value. The channel has one writer and one reader, both resolved during graph construction.

| Token | Proposed meaning |
| --- | --- |
| `&name` | Named channel reader/source endpoint. |
| `>&name` | Named channel writer/sink endpoint. |
| `&in` | Read endpoint representing an enclosing graph's input port where available. |
| `>&out` | Write to the enclosing graph's declared output port. |
| `>&err` | Write to the enclosing graph's declared error port. |
| `>&` | Special branch exit: next continuation (not a named channel). |
| `>@` | Special branch exit: otherwise continuation (not a named channel). |

`&in`, `&out`, and `&err` are reserved **scope ports**, not unrestricted globally shared named channels. The same one-to-one endpoint rules apply to a composable graph port; if multiple sources need to feed it, construct an explicit merger. The CLI's presentation of multiple independent stderr messages remains separate from these graph ports.

`&name` in a process-operand position connects the channel as a source to a pipeline. `>&name` is a writer exit. Bare `&name` is not a shell variable and never triggers expansion of data bytes into argv text.

### 6.2 Explicit merge and broadcast

```kash
stream combined
(
    (A, B) |(merge) >&combined,
    &combined | C
)
```

```kash
stream left
stream right
(
    A |(broadcast) (>&left, >&right),
    &left | B,
    &right | C
)
```

The endpoints `>&left` and `>&right` in group positions represent sink-only graph operands. A reader may be attached to each once. The compiler creates a distinct connection per fan-out edge.

### 6.3 Declaration and execution scopes

Channel references and their endpoints must resolve within the same **graph-construction scope**. A set of independent top-level Kash command statements is sequential unless explicitly grouped or launched asynchronously under an owner; it is not automatically treated as a single graph.

A process group is the minimum proposed way to bind channel users into one concurrently launched graph. Named channels cannot outlive or escape their owning invocation; returning them as Kame scalar values is invalid. Static graph validation rejects missing writers/readers unless a port is explicitly declared externally bound or optional.

**Open O2:** Decide whether to introduce a `flow:` declaration for readability once graph programs become large. It is not required for the initial operator design.

## 7. Stream injection into command operands

### 7.1 Three forms

| Syntax | Role | Example |
| --- | --- | --- |
| `<&` | Inject the current upstream channel at this **argument position**, consuming it once. | `generate | diff -u <& ./baseline` |
| `<&name` | Inject a named channel at this **argument position**, consuming it once. | `diff -u <&old <&new` |
| `<(graph)` | Construct a child producer graph and inject its output as one argument. | `diff -u <(old-version) <(new-version)` |

These are **live stream operands**. They are not `$(...)` capture; no eager buffering, trimming, or string interpolation takes place.

```kash
# Inject upstream stdout into the first file operand, not stdin.
generate | diff -u <& ./reference.txt

# Two independently generated, simultaneously running stream operands.
diff -u <(git show HEAD:settings.json) <(cat ./settings.json)

# Use named streams.
stream before
stream after
(
    old-generator >&before,
    new-generator >&after,
    diff -u <&before <&after
)
```

### 7.2 Input ownership and stdin

`<&, <&name, <(...)` each bind one channel to one receiving operand. Multiple references to the same channel are a static error without `|(broadcast)`.

When `<&` is present, the incoming pipeline channel is *claimed* by that operand rather than also being connected to the process's stdin. The process's stdin is closed unless an independent, explicitly connected stdin source is supplied. Therefore:

```kash
A | diff -u <& <(B)    # Valid: A consumed as operand, B as another operand
A | diff -u <& <&      # ERROR: A claimed twice
```

Existing `< path` remains an **stdin redirection**. `command < &name` may be accepted as an explicit stdin redirection from a channel, whereas `command <&name` is a positional stream argument. This token/whitespace distinction must be specified by the lexer, not inferred from ad-hoc shell text manipulation.

### 7.3 Host realization

For native Kash operations, the injected operand can be a typed stream handle.

For external executables, injected operands are represented by a host-accessible path/handle (on suitable POSIX hosts, often `/dev/fd/N` backed by a pipe with an inherited descriptor). Windows and other backends MUST use a validated equivalent or report a clear unsupported-feature error. Supplying a path that refers to an unreadable or closed handle is not allowed.

An injected stream is usually **non-seekable**. Programs requiring seekable regular files, repeated opens, or path-relative filesystem metadata must not receive a fake regular-file guarantee. Materialization to a temporary file, if supported, must be explicit and bounded/policy-governed.

Process graphs used inside `<(...)` start with their receiving graph; the end of the injected stream closes the associated writer. If the receiving process exits early, the producer is cancelled or receives normal closed-reader propagation under graph ownership rules.

### 7.4 Compatibility with shell descriptor syntax

Kash currently does not define Bash's `2>&1` descriptor duplication. This draft reserves `<&...` for injected stream operands and `>&...` for graph-channel outputs/rejoins. `2>&1` **must not** silently acquire its Bash meaning. A future descriptor-duplication syntax needs its own explicit, unambiguous grammar and tests.

## 8. Framing and stream values

### 8.1 Byte channels are default

External programs expose byte-oriented stdin/stdout/stderr, so `A | B` is a byte pipe. Byte read chunks are **not records**: chunk boundaries vary by host and scheduling and must never be used as filter item boundaries.

An item-aware `filter` requires framing. This draft favors explicit framing initially:

```kash
cat ./events.log |(frame :lines) |(filter (contains item "ERROR") | alert) | archive
```

`|(frame :lines)` assembles newline-delimited items incrementally; the operator retains the underlying byte payload including its delimiter so passing a framed item to an external stage can reproduce its bytes exactly. A partial final line at EOF is a valid final item. Invalid text encoding or overlong records must be handled by declared policy, not silent truncation.

Other framers may include `:jsonl`, `:length-prefixed`, and native typed record sources. Kame predicates evaluate the decoded item according to the selected framer. The frame is not serialized as a Kame expression or parsed as Kash source.

### 8.2 Filter semantics

`filter P` evaluates a pure, nonblocking Kame predicate P over each complete item. The lexical binding `item` refers to the current item. Its result must be Boolean (or obey a specified Kame truth rule, **Open O4**). An item goes to **one** of the two outputs: selected or remainder. A predicate failure fails the graph unless the operator supplies a defined failure policy.

`filter` does not launch an external `grep` process per item. A branch such as `|(filter P | B)` establishes B as one process stage for that branch, with its stdin fed by all matching items.

### 8.3 Interactions with merge, rejoin, and external commands

A byte-channel merge can interleave producer *chunks* and corrupt higher-level messages. To merge framed items, the merge MUST preserve the entire item atomically and keep each producer's local item order. Global order across producers is unspecified unless ordered merge is explicitly requested.

When the branch contains an external program, its stdout is bytes. An item-aware merge must frame those bytes (or have an explicit output framing adapter) before joining framed remainders. A type/framing mismatch must be diagnosed; the compiler must not silently assume that arbitrary subprocess writes correspond to complete frames.

Illustrative framing-aware normalization:

```kash
source |(frame :lines)
    |(filter is-legacy | convert-legacy |(frame :lines) >&)
    |(filter is-invalid | report-invalid)
    |(encode :lines) | process-events
```

The complete design of unary stream transforms inside `|(...)` is **Open O5**. The example is conceptual until parser precedence and adapter placement are finalized. The core requirement is that a rejoin of framed and raw-byte channels cannot bypass framing validation.

## 9. Scheduling, buffering, and backpressure

### 9.1 Concurrent graph startup

The entire reachable graph, including all process-group members, conditional branches, nested producers from `<(...)`, mux operators, and named channel endpoints, is validated and prepared **before launching any executable or truncating output files**. Runtime-conditional node activation may remain lazy when it is provably safe, but this is an optimization, not a change in semantics.

Every process stage runs concurrently with its connected peers. `&` continues to mean *start this whole process graph asynchronously relative to the next Kash statement*. It is not the fan-out operator.

### 9.2 Backpressure is lossless by default

Every channel has bounded capacity. Producer writes wait when its channel is full. A lossless broadcaster must propagate pressure from its slowest destination eventually; no default unbounded queue or implicit item dropping is permitted.

Configured operator policies may alter buffer capacity or overflow behavior; for example:

```kash
A |(broadcast :buffer 64) (B, C)
A |(balance :buffer 256) (B, C)
```

Option spelling above is **proposed**. The policy must distinguish:

- `block` (default, lossless)
- `drop-new`, `drop-old`, or `latest` (explicitly lossy)
- optional bounded disk spooling (future)

Buffers are measured in bytes for byte channels and bytes-plus-item counts for framed channels. Limits inherit invocation resource policy and cannot be relaxed by a nested operator beyond the caller's grants.

### 9.3 Merge ordering and fairness

`|(merge)` and the merge implied by `>&`/`>@` preserve per-input byte order; for framed channels they additionally preserve complete-item boundaries. They do **not** promise global event order or original source order. A merger must avoid starvation of ready inputs; a deterministic round-robin-ready scheduler is a suitable baseline, without promising deterministic output across timing-dependent runs.

**Ordered rejoin**, if added, must be explicit; it requires sequence identity, bounded lookahead, and potentially stalling a fast branch behind a slow one.

### 9.4 Load balancing

`|(balance)` delivers each complete input item to exactly one child. It is invalid on an unframed byte stream unless the operator provides an explicit chunking policy (chunk boundaries cannot be arbitrary read boundaries). Scheduling strategies such as round-robin, weighted, or key-based partitioning are optional future refinements.

### 9.5 Cycles and deadlocks

Feedback cycles can deadlock under bounded backpressure. The initial implementation SHOULD reject cycles in graph validation, including cycles introduced through named channels, unless a future explicit feedback/delay operator defines the required buffering and initial token semantics.

## 10. Completion, failure, and ownership

### 10.1 End-of-stream

Each channel has an explicit end state. A writer close produces EOF after queued data is drained; a reader close makes subsequent writes fail/stop according to the established process-pipe policy.

- `merge` closes after **all** upstream producers close and its queue drains.
- `broadcast` completes each output after the input closes and pending items are delivered.
- `balance` completes all outputs after upstream EOF and queued assignments drain.
- `filter` closes both selected and remainder paths after input EOF and their pending items drain.
- A `>&`/`>@` merger closes only after both the remainder and joined branch production complete.

### 10.2 Error propagation

A failed stage fails the owning graph by default, including subprocess non-zero exit, launch failure, unhandled predicate failure, invalid framing, timeout, or early I/O failure. The existing `017-kash.md` process recovery (`?`, acceptance, and async ownership) is retained at the enclosing graph level; per-branch isolation/retry policies may be introduced explicitly later.

Cancel the remaining reachable graph, close its channel endpoints, reap children and nested `<(...)` producers, and release buffers and temporary resources. On normal completion, the invocation joins all owned branches before reporting a successful result; there are no detached implicit daemons.

### 10.3 SIGPIPE and early consumers

The existing rule treating SIGPIPE as a failing stage remains the compatibility default. A deliberate early-consumer operation may later declare that upstream closed-reader/SIGPIPE is an acceptable completion outcome; such behavior must be explicit and must not mask launch failures or capability denial.

### 10.4 Runtime graph results

The result should retain aggregate status, per-stage outcomes, and graph topology/stream diagnostic metadata. A merged output's status is not merely the status of whichever producer closes last. The graph has one aggregate Result and independent per-node Result entries.

### 10.5 Capture and ambient output

`$(graph)` captures one designated graph output channel into a value under existing capture bounds. It must not silently concatenate unrelated terminal branch stdout streams or bypass capture limits. Initial proposal: refuse ambiguous multi-terminal capture until the author explicitly redirects or merges them into one channel.

Ordinary, noncapturing top-level graph execution may forward terminal-process stdout/stderr to the host's public sinks, but these are **presentation effects** and do not create hidden composable channel readers/writers.

### 10.6 Observability

Static graph inspection and diagnostics SHOULD show:

- Nodes and process commands, with tracked executable identities.
- Port/channel endpoints and exactly-one-reader/writer validation.
- Per-operator kind, cardinality, predicate span, and branch/rejoin destination.
- Active, waiting, blocked-on-backpressure, completed, and failed states.
- Per-edge bytes/items transferred, queue occupancy/high-water mark, and per-branch exits.

Inspection must never evaluate unreached Kame predicates, launch processes, or fabricate timing-dependent branches.

## 11. Lexing and precedence (proposed)

### 11.1 Context sensitivity is explicit

The parser distinguishes **Kash process context** from **embedded Kame expression context**. The following forms are Kash-only at the process level:

| Form | Token/context |
| --- | --- |
| `|` | Direct pipe between process endpoints. |
| `|(` | Configured multiplexor prefix (maximal munch). |
| `(A, B)` | Process group if process-position and top-level comma present. |
| `<(` | Anonymous stream-injection prefix in argument position. |
| `<&` / `<&name` | Live stream-injection operand in argument position. |
| `>&name` | Named channel output redirection. |
| `>&` | Local branch rejoin, only inside conditional branch exit position. |
| `>@` | Otherwise-branch rejoin, only inside conditional branch exit position. |
| `&name` | Named channel source in process/operand positions. |
| terminal `&` | Existing async graph operator. |

Kash's `$reference`, `${reference}`, `(Kame expression)` and `$(process substitution)` keep their existing distinct semantics. In particular, `$(...)` is capturing, `<(...)` is streaming, and `|(...)` is a mux operator.

### 11.2 Precedence

Proposed effective order from tight to loose:

1. Quoted command words, embedded Kame expressions, `$(...)`, `<(...)`, `<&...` stream operands, and ordinary argument composition.
2. Process-group construction and stage redirections, including `>&name` and branch-only `>&`/`>@`.
3. Direct `|` and configured `|(...)` graph connections, evaluated left-to-right; each conditional operator retains lexical access to its following chain.
4. Existing terminal acceptance `?`, recovery `? ELSE`, and async `&`, at whole-graph scope.
5. Statement separators `;` and newline.

The parser MUST retain authored source spans across nested mux bodies, branch graphs, embedded predicates, and substitutions. No output text, argument value, or Kame value is subsequently reparsed as Kash code.

### 11.3 Illustrative AST

```text
Pipeline
  Source: Process("source")
  Connections:
    FilterMux(predicate=P,
      selected=Process("normalize"),
      exit=NextRejoin,
      remainder=Implicit)
    FilterMux(predicate=Q,
      selected=Process("reject"),
      exit=Terminal,
      remainder=Implicit)
    DirectPipe(Process("store"))
```

Graph lowering expands the AST into process nodes, mux nodes, ports, and point-to-point channel edges. An explicit `>&`/`>@` inserts a merge node with exactly one reader and one writer per resulting channel.

### 11.4 Diagnostics (suggested)

| Code | Meaning |
| --- | --- |
| `KASH_CHANNEL_DUP_WRITER` | More than one producer bound to a named channel. |
| `KASH_CHANNEL_DUP_READER` | More than one consumer bound to a named channel. |
| `KASH_CHANNEL_UNBOUND` | Required channel endpoint missing or outside graph scope. |
| `KASH_MUX_ARITY` | Group/endpoint cardinality incompatible with operator. |
| `KASH_REJOIN_OUT_OF_SCOPE` | `>&` or `>@` not attached to a valid branch/continuation. |
| `KASH_DOUBLE_CONSUME` | Current or named channel consumed twice without broadcast. |
| `KASH_FRAME_REQUIRED` | Item-aware operator receives unframed bytes. |
| `KASH_FRAME_MISMATCH` | Incompatible framed/raw inputs at rejoin or merge. |
| `KASH_FRAME_ENCODING` | A line record is not valid UTF-8. |
| `KASH_RECORD_LIMIT` | A line record exceeds the bounded record limit. |
| `KASH_PREDICATE_EFFECT` | A predicate attempts an effect or suspension. |
| `KASH_PREDICATE_BOOLEAN` | A predicate returns a non-Boolean value. |
| `KASH_PREDICATE_FAILED` | Predicate evaluation fails. |
| `KASH_STREAM_OPERAND_UNSUPPORTED` | Host/executable cannot accept a live injected stream. |
| `KASH_GRAPH_CYCLE` | Unsupported feedback cycle. |
| `KASH_AMBIGUOUS_CAPTURE` | Capture would expose more than one unmerged output. |

Existing Kame/Kash capability, phase, timeout, executable, and process errors remain authoritative where applicable.

## 12. Examples and expected topology

All examples in this section are **language-design examples**, not executables that can currently run in Kame.

### 12.1 Decompose a source into independent branches

```kash
cat ./events.log |(frame :lines)
    |(filter (contains item "ERROR") | report-error)
    |(filter (contains item "WARN") | report-warning)
    | archive
```

Each line is delivered to at most one branch. The remainder goes to `archive`. Neither reporting branch's stdout is implicitly fed into `archive`.

### 12.2 Normalize and return to the next filter

```kash
cat ./events.log |(frame :lines)
    |(filter is-legacy | normalize |(frame :lines) >&)
    |(filter is-invalid | reject)
    |(encode :lines) | store
```

Normalized items rejoin the input of `is-invalid`; valid original items take that path directly.

### 12.3 Bypass further filters

```kash
cat ./events.log |(frame :lines)
    |(filter is-trusted | normalize-trusted |(frame :lines) >@)
    |(filter is-duplicate | dedupe-report)
    |(filter is-invalid | invalid-report)
    |(encode :lines) | store
```

The trusted branch rejoins at `encode :lines`, the first non-filter connection,
and then reaches `store`. Dedupe and invalid filters see only the unmatched
original items. The explicit adapter is a chain boundary under §5.3.

### 12.4 Multiple sources and a named sink

```kash
stream errors
(
    (cat ./app.log |(frame :lines), cat ./worker.log |(frame :lines))
      |(merge)
      |(filter (contains item "ERROR") |(encode :lines) | colorize >&errors)
      |(encode :lines) | archive,
    &errors | notify
)
```

Only one process graph writes `&errors`, and only `notify` reads it. The two log sources are merged on frame boundaries; the selected branch never merges into the archive remainder.

### 12.5 Multi-input command via injected channels

```kash
stream before
stream after
(
    render-old >&before,
    render-new >&after,
    diff -u <&before <&after
)
```

`diff` receives two independent live input file operands, not a single concatenated stdin stream. The graph runner owns the producer processes until `diff` completes or the graph fails.

### 12.6 Anonymous producers

```kash
diff -u <(git show HEAD:config.json) <(cat ./config.json)
```

Both producers start concurrently. No whole-file capture is required.

### 12.7 Load balancing is distinct from broadcast

```kash
source |(frame :lines) |(balance :strategy round-robin) (worker-a, worker-b)
source |(frame :lines) |(broadcast) (audit, validate)
```

The first delivers each framed item to exactly one worker; the second delivers each item to both consumers. No implicit fan-out occurs through multiple `&name` readers.

## 13. Implementation outline

1. **Lexer / AST:** Add mux, process-group, branch-exit, channel-reference, channel-declaration, and stream-injection nodes with source spans; preserve ordinary Kame expression parser ownership.
2. **Graph builder:** Resolve named channels and lexical next/otherwise continuations, expand groups/muxes into explicit port and edge nodes, enforce endpoint cardinalities and acyclic topology.
3. **Framing adapters:** Provide byte and framed-item channels, immutable item ownership, record-size limits, and frame-safe mergers.
4. **Host runtime:** Create per-edge bounded queues or OS pipes, asynchronous fair multiplexors, early-reader close behavior, and platform-specific stream operand injection.
5. **Execution ownership:** Start graph atomically after capability validation; maintain cancellation, failure propagation, async joins, output effects, and deterministic resource cleanup.
6. **Inspection:** Expose graph topology, branch destinations, stream types, buffer occupancy, and per-stage status without running unreached effects.

Do not lower a muxed graph into a sequence of independent eager `run!` calls. Launching the whole graph once is essential to concurrency, backpressure, and source-to-sink lifetime.

## 14. Acceptance criteria

### Parsing / compatibility

- [ ] Existing `A | B | C` remains a byte-stream pipeline.
- [ ] Existing `$(...)`, Kame expression parentheses, value pipes, typed arguments, `?`, `??`, and terminal `&` retain semantics.
- [ ] `(A, B)` in process position parses as a process group; `[A B]` remains a Kame collection, not a process group.
- [ ] `A | (B, C)` fails rather than silently broadcasting.
- [ ] `(A, B) | C` fails rather than silently merging.
- [ ] `A |(broadcast) (B, C)` and `(A, B) |(merge) C` construct valid point-to-point edges.
- [ ] `2>&1` is not silently interpreted as Bash descriptor duplication.

### Conditional branches

- [ ] `A |(filter P | B) | C` diverts matched items to B and sends only unmatched items to C.
- [ ] Nested filters can further partition a selected branch.
- [ ] `>&` returns B's output immediately downstream of its enclosing filter; it never re-enters the same filter.
- [ ] `>@` bypasses subsequent contiguous conditional filters and rejoins at the first ordinary stage.
- [ ] Nested `>@` resolves to its lexical chain, not an outer chain.
- [ ] `>@` with no otherwise continuation fails before launching processes.
- [ ] Branches with different output data kinds need not rejoin; explicit rejoin enforces compatible framing.

### Named channels and injection

- [ ] One named channel has exactly one writer and one reader; duplicates are static errors.
- [ ] `&name` and `>&name` endpoints are resolved in one graph-construction scope.
- [ ] `<&` claims the current upstream channel as a positional operand, not stdin; double consumption fails.
- [ ] `<&name` supplies a single live named-channel operand; `<(...)` starts a child producer graph without eager capture.
- [ ] Unsupported OS injection or a seekability requirement produces a diagnostic rather than silent materialization.

### Runtime

- [ ] Every graph is validated for tools, grants, redirections, cardinalities, framing, and cycles before launch.
- [ ] Broadcast is lossless and bounded under a slow consumer; balance delivers each item once.
- [ ] Merge and branch rejoin preserve per-source item order and item boundaries; cross-source ordering is not falsely guaranteed.
- [ ] Closing one of several merge inputs does not prematurely close the merged output.
- [ ] Failures cancel/reap all owned stages and nested process-substitution producers.
- [ ] A top-level `&` returns an owned async graph handle, not detached subprocesses.
- [ ] `$(...)` rejects ambiguous unmerged multiple terminal outputs; no implicit concatenation.
- [ ] Graph inspection reports resolved edges, rejoin targets, and buffer/backpressure states.

## 15. Open design decisions

These are deliberately left explicit rather than presenting untested syntax as settled.

- **O1 — Process-group delimiter.** Should a top-level comma inside `(A, B)` be sufficient to disambiguate a process group from `(Kame expression)`? Alternative: a dedicated grouped-process prefix.
- **O2 — Larger named graph scopes.** Are process groups sufficient to construct all named-channel graphs, or should Kash add a `flow:`/`graph:` block for multi-line declarations and concurrency?
- **O3 — Terminal branch stdout and capture.** Confirm where a side branch's unconnected stdout goes (public sink versus explicit required sink), especially in `$(...)` and build recipes.
- **O4 — Predicate truth and framing defaults.** Require Boolean results? Is line framing implicit for textual `filter`, or must it always be authored as `|(frame :lines)`? This draft chooses explicit framing for correctness.
- **O5 — Unary adapters and operator arguments.** Finalize `|(frame :lines)`, `|(encode :lines)`, and parameter syntax; ensure they compose cleanly with `|(...)` as a connection operator.
- **O6 — Port selection.** Add explicit stderr routing, e.g. `A.err` or `|(from :err)`, without conflicting with existing Kash word and redirection grammar.
- **O7 — `>@` chain boundary.** Confirm that the first non-conditional ordinary stage defines 'otherwise', and specify how nested non-filter mux operators affect this boundary.
- **O8 — Merge and stream types.** Confirm whether framed items retain original raw bytes alongside decoded Kame values, and how mixed byte/framed merges must be adapted.
- **O9 — Process launch strategy.** Eager graph startup is easiest to reason about, but conditionally activated/never-used branches might benefit from lazy launch; exact rules must preserve backpressure and completion.
- **O10 — Portability.** Decide what guarantees `<(...)` and `<&name` can offer across POSIX and native Windows, especially for external commands requiring seekable paths.
- **O11 — Branch terminal defaults.** Whether `|(filter P | B)` without a branch exit should discard B's stdout, display it, or require explicit ownership; reject silent data loss and unexpected capture concatenation.

## 16. Recommended initial implementation slice

Implement in this order, with no need for a visual workflow layer:

**Slice A — Core graph topology:** anonymous point-to-point channels, `(A, B)` grouping, `|(merge)`, `|(broadcast)`, endpoint validation, cancellation and bounded backpressure.

**Slice B — Conditional decomposition:** `|(filter P | branch)`, lexical remainder, `>&` and `>@`, explicit frame handling, and all graph inspection/diagnostics needed to see the constructed topology.

**Slice C — Named channels:** `stream name`, `&name`, `>&name`, multi-pipeline graph construction, one-writer/one-reader validation.

**Slice D — Stream injection:** `<&`, `<&name`, `<(...)`, POSIX FD-backed external operands and portable failure diagnostics, then native Windows support.

**Slice E — Scheduling policies:** `balance`, bounded buffering options, per-item workers, alternate ordering and failure policies, richer graph observability.

This sequencing keeps **Kash syntax, graph topology, and scheduling policy** separate while establishing a foundation for general asynchronous streaming process orchestration.
