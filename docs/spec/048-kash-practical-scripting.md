---
revision: 2
---

# Practical non-interactive Kash

## 1. Purpose and approach

Kash must support command-oriented utilities without generated shell source,
manual subprocess retirement, or knowledge of Kame's build engine. Principal
uses are CI, repository maintenance, file processing, deployment helpers, and
utilities that consume stdin and produce stdout.

This specification extends [Kash](017-kash.md),
[direct execution](038-direct-kash-and-values.md),
[streaming](042-kash-streaming.md), [finalization](043-kame-finalization.md),
[scripting](046-kash-scripting-and-publication.md), and
[output ports and exit fidelity](047-kash-output-ports-and-command-lists.md),
using the surface syntax of [055](055-unified-kame-kash-language.md). Its
clauses take precedence for the additions below. Other contracts are unchanged;
in particular, a top-level `NAME = EXPR` definition is lazy.

The approach is to complete standard-stream handling, define sequenced
bindings and control transfers, and compose scripts through local libraries and
owned execution scopes. Incremental record iteration uses the same execution and
finalization machinery, not capture followed by splitting a whole string.

This is not Bash source compatibility. Interactive facilities, implicit word
splitting/globbing, mutation of the host process's cwd or environment, arbitrary
descriptors, detached jobs, package discovery, implicit retries, and
transactional external effects are outside this contract. The environment store
and working directory of a process-like unit (`set env/NAME`, `cd`) are
`055` §5.3; foreground terminal handoff is `051-kash-session-authority.md`.
Neither mutates the host environment. Separate structured stdout/stderr capture,
byte-valued argv, live graph producers in loop headers, and accepted early-consumer
SIGPIPE policies require separate contracts.

## 2. Execution model and vocabulary

- An **execution boundary** is a process-like unit (`055` §8.4): an ordered
  runner session running a script, one rule-body attempt, one `$(…)` or `$?(…)`
  capture, an embedding execution request, or an interactive entry. It owns the
  environment store and working directory and receives the terminal outcome of
  `exit`.
- An **execution scope** owns a lexical scope, asynchronous work, and deferred
  cleanup. Function invocations and `with` blocks establish such lifetimes;
  ordinary branches and loop iterations do not. A `with` block additionally
  applies a cwd/environment overlay to the work it runs.
- **Invocation input** is the data-input stream supplied at the boundary. It is
  not source text, a filename, a replayable value, or an implicit broadcast.
- An **eager binding** is a value bound by a reached `let` statement; only an
  explicit `set` reassigns it. A **lazy definition** (top-level `NAME = EXPR`)
  keeps the demand/memoization rules of `055` §5.1.
- An **effective outcome** determines failure propagation. Raw process status
  remains inspectable even when explicitly accepted.

Values never become executable source implicitly. Argument boundaries, typed
failures, grants, dependency tracking, and process ownership remain intact.
Resumption must not replay completed effects. A new invocation or explicitly
configured retry is new work, not an exactly-once external-operation guarantee.
Cleanup is bounded finalization, not rollback or proof of resource ownership.

For example, each reached acquisition below runs before its cleanup registration;
the nested scope finishes its work before the enclosing cleanup removes staging:

```kash
let workspace = line $(mktemp -d)
defer rm -rf $workspace
with cwd: $workspace
	generator > ./output 2> ./errors
	verifier ./output
```

`generator` and `verifier` denote application executables, not library operations.

## 3. Standard streams

### Invocation input

File-backed and inline standalone scripts receive supplied data stdin:

```sh
printf 'payload\n' | kame do run --lang kash -c 'cat'
```

This emits `payload\n`. Default input is selected as follows; an explicit file
redirection or graph connection overrides it.

The **primary input root** is the sole source node of the main foreground
dataflow, excluding anonymous live-operand producer subgraphs. A unique source
before fan-out qualifies; file-fed sources still count when determining uniqueness.
A main graph with multiple sources has no implicit primary root. Anonymous
`<(graph)` producers, including nested producers, default to closed input even
when their receiver inherits invocation input; give them explicit input when
needed. Current-stream operands retain `042`'s closed-stdin rule for the receiver.

| Context | Default input |
| --- | --- |
| Foreground graph in an input-inheriting context | Invocation input only at the primary input root; explicit edges downstream |
| Command substitution, including nested helper work | Closed |
| Background graph | Closed |
| Main graph with multiple sources | Closed at unconnected roots; use explicit connections for distribution |
| Deferred cleanup | Closed |
| Recipe or embedding without supplied data input | Closed |
| Session containing a stdin source fragment | Closed; stdin is reserved for loading source |

Function calls and `with` inherit the current input policy; they do not recover
an input stream closed by capture, cleanup, or a record-loop body. `run!` and
`pipe!` obey the same policy as corresponding authored graphs. Closed policies
take precedence over foreground-root selection. A foreground graph inside an
embedding or recipe may inherit explicitly supplied input at its primary root.

Sequential consumers receive the remaining bytes, never copies or restarted
input. Hosts must preserve unread bytes across consumer handoffs, including any
host-side read-ahead. Readers borrow the boundary's stream; completing a command
does not close the caller's original stream. Owned descriptors/relays are released
normally, and cancellation remains serviceable during backpressure.
Native Linux reopens nonblocking pipes for child stdin where available, so a
producer pause does not fail an ordinary blocking reader. This preserves the
shared byte queue and the caller's status flags.

Supplied input requires no filesystem-read grant. `< FILE` retains read grants
and dependency tracking. Opaque input has no reusable content identity: cached
execution supplied such input must not be reused unless the host provides an
explicit stable identity tracked as an input dependency. Stable identity alone
is insufficient: input-consuming execution must not be reused while its remaining
input position is observable by another consumer, including the caller after the
boundary returns, unless an explicit host protocol preserves the same consumption
and unread bytes on a cache hit. Reusing a whole boundary does not exempt borrowed
input from this rule. This specification introduces no such consumption protocol,
implicit input hashing, disk spooling, or persistent stdin replay.

Process requests must distinguish closed, invocation, file, and edge input; an
empty filename cannot encode both inheritance and EOF. An unsupported host
input mode fails before process launch or output truncation, not as empty input.

### Stderr routing and ordering

Add this restricted standard-stream syntax:

| Form | Meaning |
| --- | --- |
| `command 2> PATH` | Replace stderr's file destination. |
| `command 2>> PATH` | Append stderr to a file. |
| `command 2>&1` | Copy stdout's destination at this authored position to stderr. |

Resolve graph-provided default destinations first, then apply redirections left
to right. `command > ./combined 2>&1` sends both streams to the file;
`command 2>&1 > ./stdout` leaves stderr at the preceding stdout destination.
`command 2>&1 | consumer` sends both streams to the consumer. An alias retains
the destination, not a pointer that follows later stdout redirection.

Existing `>&out` and `>&err` still select enclosing presentation sinks for
stdout; they are not aliases for a stage's numbered descriptors. Each stream
accepts at most one explicit disposition. Existing stdout/file/pipe conflicts
remain errors, including intermediate stdout redirection in a linear pipeline;
this subset does not admit every Bash descriptor arrangement. Resolved stderr
aliases participate in topology and named-channel endpoint validation.

File operands use existing scalar redirection grammar, relative cwd, and write
grants. Prepare the whole selected graph, validate routing/operands/grants, and
check host support before opening files or launching stages. Failure while opening
files can still leave an earlier output modified; preparation is not a transaction.

Stderr merged into capture shares its byte limit and UTF-8 validation. A stage's
explicitly aliased stdout/stderr count as one merged output destination for capture
and channel cardinality. Aliasing two independent stages to the capture sink does
not implicitly merge them. Unaliased stderr remains live. Preserve each stream's
byte order without promising cross-stream ordering or record atomicity. Transport
failure, overflow, and cancellation retain graph failure and retirement rules.
Native children receive a direct presentation pipe only if they can write it
in blocking mode without sharing the host writer's flag changes. If the host
relays the output, closure of the public pipe stops the producing invocation.
Batch relays honor the invocation's timeout or interruption while waiting for
the destination to drain.

Only unquoted, unescaped `2` immediately adjacent to the operator selects stderr.
`printf "%s" 2 > ./out` retains the argument `2`; quoted `"2>"` is data.
Other attached numeric descriptor forms, including `3>`, `1>&2`, and `2>&-`, are
`PARSE_ERR`, not an argument silently followed by a different redirection.

## 4. Eager bindings

```kash
let revision = line $(git rev-parse HEAD)
announce --revision $revision
```

`let NAME = EXPR` evaluates one expression in value context, including `$(…)`,
`$?(…)`, function calls and `??`. It evaluates when reached even if `NAME` is
never read. It retains the result across suspension and subsequent statements;
initializer failure stops execution normally. It does not print its value,
collect a result, or force a fresh filesystem observation from operations with
cached/tracked observation semantics. `set NAME = EXPR` assigns an existing
uncaptured `let` binding under the rules of `055` §5.2; nothing else reassigns
it.

A `let` binding is in scope from the statement after it to the end of its block,
or until a later `let` of the same name in that block shadows it. Its initializer
sees the bindings in effect before the statement, so `let count = count + 1`
reads the previous `count`. Nested `def`s declared in a block are visible
throughout that block and close over the whole block scope. When such a function
reads a name bound by a `let` of the same block that has not yet been reached,
the read reports `BINDING_UNINITIALIZED`. Reading cannot trigger initialization
or fall back to a shadowed outer binding. Each reached function call or
iteration has fresh slots; unselected branches create none. Initializer work
executes once per reached occurrence, not once per resumption. Captured scopes
retain initialized values under normal ownership rules.

`BINDING_UNINITIALIZED` is a nonrecoverable binding-validation failure, not
`REF_MISSING`. Terminal `?`, graph recovery and `??` cannot suppress it or select
a fallback, including through functions. `$?(…)` does not convert it into an
outcome; it propagates.

`let` bindings share name validation and parameter/item collision rules with
other lexical bindings. A `let` and a nested `def` of the same name in one
block, or two nested `def`s of the same name, are errors; a later `let` may
shadow an earlier `let`, and nested blocks may shadow outer names. `--define`,
`--set`, and `KAME_NAME` configure top-level definitions, not `let` bindings;
explicit overrides targeting only a `let` binding are rejected as invalid
configuration. Unrelated environment configuration retains its ignore policy.

Top-level lazy `NAME = EXPR` definitions and configurable defaults are
unaffected. A `let` in an action block evaluates at run time; a `let` in a
declaration-context block (`055` §10.3) evaluates during declaration loading
under declaration-context restrictions. Dry-run does not evaluate an
action-block initializer or invent a value for dependent work.

## 5. Structured transfers

### Loop transfers

`break` exits the nearest lexically enclosing `for` (over a list, a stream or
records) or `while` loop; `continue` proceeds to that loop's next
iteration/condition. Neither takes an operand, numeric depth, label, process
operator, or redirection. Transfers pass through nested branches and execution
scopes, but cannot cross a function boundary. Outside a matching loop they are
`PARSE_ERR`, even in unused code. A nested function cannot transfer to its
caller's loop. Leaving a loop over a generator stream cancels the generator and
runs its cleanup (`012`).

Statements after a transfer do not execute. A crossed `with` scope finishes its
owned work and cleanup before control reaches the loop. Existing invocation-owned
defers outside such a scope do not become per-iteration cleanup. Transfer itself
does not cancel invocation-owned background jobs or turn pending failures into
success. Normal cancellation and deadlines remain enforced.

### Script exit

```kash
if empty? args
	printf "usage: process FILE...\n" >&err
	exit 64
```

`exit EXPR` evaluates its value operand once; it must be an integer from 0
through 255. A missing operand means 0. Invalid values are `EXPR_INVALID`. There
is no implicit last-status operand or terminal acceptance modifier. `return`
ends the nearest `def` invocation with a value and never sets a status.

Exit ends the innermost process-like unit, propagating through function calls,
branches, loops, and execution scopes; functions and `with` scopes do not
create exit boundaries. In a script it stops the complete ordered runner
session, including later fragments. In a rule body it stops that attempt, not
the enclosing build scheduler. In a `$(…)` or `$?(…)` capture it ends only the
capture: its graph completes with status N, so `$(…)` raises for a nonzero N and
`$?(…)` returns an outcome with that status. In an interactive entry it ends the
session (`052`). An embedding receives a terminal outcome, never an
unconditional host-process exit. Outside any process-like unit, such as in
declaration context, exit is `PHASE_INVALID`.

Exit is control flow, not a failure selected by `?`, `??`, fallback, or outcome
inspection while unwinding within its unit. No synthetic process or executable dependency
is created. Preserve its typed status through finalization and CLI/embedding
adaptation; never infer it from diagnostic text or an arbitrary record.

After its operand produces a valid status, exit irrevocably terminates its
boundary. Retain termination intent separately from the terminal status: a job or
cleanup failure may change a successful exit's status, but cannot turn termination
into an ordinary recoverable failure, select a fallback, or resume later statements
or fragments.

| Pending outcome | Required unwinding |
| --- | --- |
| `exit 0` | Join owned work, then finalize; a job or cleanup failure still fails execution. |
| `exit N`, N nonzero | Cancel/reap owned work, then finalize; preserve N if cleanup also fails. |
| Existing failure, timeout, or cancellation | Exit cannot replace it; preserve existing primary-outcome and interruption rules. |

Every exited scope attempts its registered defers once in LIFO order, including
for nonzero exit. A failed action does not skip remaining actions while budget
remains; exhausted budgets and forced shutdown retain their existing limits.

Nested scopes unwind inside-out under existing shared cleanup budgets. Output
verification and publication requirements still apply to a successfully exited
recipe. Explicit retry policies remain separate and cannot begin another attempt
before teardown finishes. A deliberate exit alone needs no unhandled-error
backtrace; summaries and the terminal status must agree.

Direct `defer break`, `defer continue`, and `defer exit` are `PARSE_ERR`.
An indirect exit reached during finalization is `PHASE_INVALID` before evaluating
its operand; it cannot replace the saved outcome. Cleanup helpers may still use
their own local loops and function returns.

## 6. Local libraries and source identity

```kash
include ./lib/checks.kash
let config = ${module/dir}/defaults.json
check-config $config
```

`include PATH` (`055` §11) is valid at the top level of a file-backed Kash
source. Included files are parsed as Kash regardless of suffix and may contain
comments, `def` definitions, `import`/`from` statements, and further top-level
includes. Top-level `let` bindings, commands, and control blocks are invalid in
a library; bodies of declared functions retain ordinary Kash syntax. This is
definition composition, not execution of another script at load time.
`import` and `from … import` bind a library's exported names as a namespace or
directly without splicing its definitions (`050-kash-libraries.md`).

Resolve includes relative to the containing source, expand depth-first, and parse
all selected sources before execution. Reuse existing source-loading authority,
dependency/provenance tracking, lexical cycle identity, and duplicate-declaration
rules. Repeated nonrecursive inclusion expands again; it is not include-once.
Definitions share the including sequence's namespace and forward visibility.
Diagnostics retain included source spans and include frames. Libraries cannot
broaden grants, change cwd, or supply their own implicit execution policy.

Library functions may perform effects when called under runtime rules;
inclusion neither calls them nor certifies them as pure. `include` is not
permitted inside blocks, in inline/stdin sources, or in any text without a
file-backed loading base. Include operands are literal paths; `include?` skips a
missing file only. Computed paths, search paths, and package discovery are not
provided. Kash library module fields and their exports are specified in
`050-kash-libraries.md`.

`module/dir` is the absolute lexical directory of the file-backed source
containing that authored reference, as a `Path`; `module/file` is that source
file. They use source metadata, not cwd, a caller's source, a filesystem probe,
or physical symlink resolution. An included function therefore resolves assets
beside its own source. Inline, stdin, and other text without file-backed source
metadata report `SEL_NO_CONTEXT`; embedding display labels alone do not
establish a loading base. Reading `module/dir` needs no filesystem grant; later
reads retain normal grants. It does not change root-relative path semantics or
create a script loader.

## 7. Owned execution scopes

```kash
let overrides = {MODE: "release"}
with cwd: ./project env: $overrides
	configure
	compile
	verify
```

`with` introduces a nonempty indented Kash body and may have no setup pairs.
Its header occupies its own line. Accepted pairs are `cwd: VALUE` and `env: VALUE`,
each at most once, in either order. Reuse scalar cwd composition and typed
environment-record grammar from stage setup; no second value parser is added.
An unquoted bare word such as `repo` is a literal directory name. Use
`with cwd: $repo` to take the directory from the `repo` binding.
Unknown or duplicate setup keys are errors. Block `timeout:` and shorthand
arbitrary environment keys are not provided. Unit-wide changes use
`set env/NAME = VALUE` and `cd` (`055` §5.3); `with` confines its overlay to the
block.

Evaluate setup operands left to right once in the enclosing context, retaining
completed work across suspension. Validate all values/grants before entering the
body. Resolve relative cwd from enclosing cwd; merge the validated environment
record over the inherited child environment. Only then establish the child scope.

Direct body work and calls inherit this context; stage setup may override it.
A relative stage `cwd:` override resolves against the enclosing execution scope's
effective cwd, not the boundary's original directory. With boundary cwd `/repo`,
`with cwd: ./project` containing `cwd: ./build pwd` runs `pwd` in
`/repo/project/build`.

The overlay applies to processes started within the block; it does not write the
unit's environment store. `env/NAME` and bare `ENV-NAME` reads, and executable
lookup through the store's `PATH`, are unchanged by `with`. Context does not
refresh a memoized definition, rebase an already materialized resource, change a dependency's established evaluation context, or
alter authored source identity. Newly executed work must retain the effective
context in its execution identity rather than reuse work from another cwd/env.

Each reached block owns a fresh lexical and async/cleanup lifetime. Definitions
do not escape. Reached defers belong to this lifetime, including in its ordinary
branches/iterations; nested function/`with` lifetimes still own their own actions.
Cleanup executes with the scope's effective context, including deferred
expressions, before the enclosing context resumes. The host's global cwd and
environment are never mutated.

Normal completion, `break`, `continue`, `return`, and successful exit join
scope-owned work before LIFO cleanup. Failure, nonzero exit, timeout, and
cancellation cancel/reap it first. Cleanup failure
prevents an otherwise successful completion, loop transfer, or return. A pending
exit remains terminal even when its status changes through finalization.
Borrowing/awaiting an outer handle does not adopt its work. Reuse the finalization
driver, grants, primary-failure rules, and shared unwind deadlines from `043`;
blocks cannot restart an active cleanup budget. Parsing and dry-run acquire no
execution scope or resources.

A `with` block is a statement and produces no value; there is no implicit
last-statement value. Command statement return values are discarded, and cleanup
values are ignored. An accepted nonzero outcome remains effective success; its
raw status does not create a new failure. A pending transfer propagates after
finalization, subject to the cleanup-failure rules above.

## 8. Incremental record iteration

```kash
for line from :stdin :lines
	let event = parse-json line
	if event.kind == "release"
		deploy $event.name

for path from ./paths.list :nul
	inspect $path
```

`for NAME from SOURCE FRAME` is distinct from materialized `for NAME in LIST`.
`SOURCE` is literal `:stdin`, or one expression producing a nonempty local
`Path`, path string, or `file:` resource. It does not accept
`??`, raw command text, live operands, lists, or arbitrary resource protocols.
`FRAME` is literal `:lines` or `:nul`. Headers, binding names, indentation, lexical
visibility, and body/item collision rules follow ordinary list iteration.

Evaluate/open the source once per reached loop, not once per record or resumption.
File sources require read authority, are regular files, and participate in normal
dependency tracking. Opening is not a snapshot against concurrent in-place writes.
Stdin uses the current boundary's remaining data input and requires no file grant.
Its source-loading/capture/cleanup restrictions remain in force. Only one active
record reader may claim that input; nested/concurrent claims report
`KASH_DOUBLE_CONSUME` before consuming more bytes.

| Framing | Binding value |
| --- | --- |
| `:lines` | UTF-8 text without its LF or paired CRLF delimiter; an unpaired CR remains data. |
| `:nul` | UTF-8 text without its NUL delimiter; embedded CR/LF remain data. |

Empty input yields no items. Consecutive delimiters yield empty records; a final
delimiter does not invent another EOF record. A nonempty unterminated final
record is valid. A record is at most 64 KiB including its delimiter when present.
Invalid UTF-8 reports `KASH_FRAME_ENCODING`; excess size reports
`KASH_RECORD_LIMIT`, never successful truncation. Text records do not establish
support for invalid-UTF-8 filenames or bytes in argv. NUL in a line record remains
data and is rejected later if an argv/environment boundary forbids it.

Visit records sequentially with fresh iteration-local bindings, without
collecting body results. Body commands receive closed default stdin so
they cannot steal subsequent records; explicit independent inputs remain valid.
`break` stops without draining the source; file readers close on every loop exit.
Stdin releases its reader claim without closing the caller's original stream or
discarding buffered unread bytes, which remain available to subsequent consumers.
This form does not own or infer an external producer or accept its SIGPIPE.

Reading and body work obey deadlines, grants, recovery, and cancellation. Read,
framing, and decoding failures are not EOF. Retain the current record and statement
position across suspension without repeating completed effects. Reader/framing
buffers are bounded, and completed per-record state not retained by authored work
must be retired rather than kept as an execution history. Authored retained
values, async jobs, and invocation-owned defers can still grow; use a callable or
`with` per record for bounded resource lifetimes. No capture buffer, implicit disk
spooling, per-record process wrapper, or implicit parallelism is introduced.

## 9. Selected ordinary exit acceptance

```kash
accept-exit: [1] grep needle ./input
```

`accept-exit: VALUE` is typed Kash stage setup. `run!` accepts the equivalent
`:accept-exit VALUE` symbol and value pair. This setting is not an
environment override. VALUE is one list expression or standalone reference to a
list of distinct integers from 1 through 255. Empty lists add no accepted status;
zero is always successful. Invalid kinds, duplicates, and out-of-range values
report `EXPR_INVALID` before graph launch or output opening. Evaluate reached
operands once with ordinary runtime grants; skipped stages perform no evaluation.

An ordinary executable exit in the set is effective success. Preserve raw stage
status/signal and established raw aggregate accounting: an accepted nonzero
outcome has `state: :completed` and `ok: false`, with `effectiveStatus` 0
(`049`). `&&`, `||`, fail-fast/collect policy, and the invocation join use
effective outcomes, not those raw numbers. A condition that should treat an
accepted exit as success tests `$?(…).state == :completed`; `$?(…).ok` tests the
raw status.
Acceptance is stage-local and never hides an unaccepted peer failure.

Retain separate typed effective-failure provenance for finalization, summaries,
and CLI status selection. Subject to existing fatal-error/signal/cancellation
precedence, select the last effectively failing node in prepared order, not the
last raw nonzero node. For example, under collection an unaccepted exit `7`
followed by an accepted exit `1` may retain raw aggregate status `1`, but the
unhandled failure and CLI exit remain `7`. If all exits are accepted, execution
is successful and the CLI exits zero regardless of retained raw status.

For `|(each)`, check every worker exit. The node's effective failure retains the
last unaccepted worker outcome in input-record order; a later accepted exit
cannot erase it under collection or replace its terminal failure status.

Signals, launch failures, timeout, transport/framing/capture errors, capability
denial, and cancellation are not accepted by this setup. Existing terminal `?`
retains its separate broader exit-acceptance contract. Selective acceptance
introduces no retry, signal policy, or permission to ignore broken pipes.

## 10. Grammar, inspection, and host boundaries

Statement keyword recognition must not reinterpret ordinary definition headers.
Use `x/NAME` or an explicit path to invoke executables named `let`, `set`,
`with`, `break`, `continue`, `exit`, or any other reserved word. Keywords inside
quoted argument data retain their literal meaning. Whole-source validation covers
unused functions, unselected branches, and libraries before any execution effect.

ASTs/formatting preserve eager versus lazy declarations, scope headers, transfer
targets, record sources/framing, selected-exit setup, and ordered stream aliases.
Inspection never evaluates operands, opens runtime inputs/outputs, or predicts
loop counts by running probes. Dry-run evaluates no eager initializer, scope
setup, record source/body, or exit operand, and fabricates no successful value or
terminal status for dependent work. Existing dry-run planning rules govern work
that remains available without those runtime values.

All operations inherit existing phase restrictions and capabilities. Errors name
the authored construct, expected constraint, and source span without dumping
evaluated argv, environments, captured output, or records. Control transfers
must survive value applications, recovery, and finalization as typed control
outcomes, not forged process Results or ordinary recoverable diagnostics.

Native and Node/WASM hosts must agree on supported behavior. Unavailable host
primitives report `FEATURE_UNSUP` (or the resource contract's `FS_UNSUPPORTED`)
before launching affected work or truncating outputs. Missing supplied input is
ordinary closed input on a capable host, not evidence of unsupported streaming.
Embedders retain explicit streams, cancellation, ownership, and terminal outcomes;
no operation exits the embedding process or relies on an inherited OS descriptor
when the host supplied a different stream. Host support claims require execution
on that host, not parser acceptance, cross-linking, or a test on another platform.

## 11. Conformance

Acceptance requires package and compiled CLI/host checks under
[013 Conformance tests](013-tests.md), with shared native and Node/WASM cases for
supported operations. Unsupported-host cases assert rejection, not weaker results.

| Contract | Required executable checks |
| --- | --- |
| Input ownership | File/inline versus stdin sources; remaining-byte handoffs and host read-ahead; linear/unique-root fan-out, multiple sources, anonymous producers, capture, async, cleanup and embedding defaults; no competing implicit readers, broadcast or cached opaque-input reuse. |
| Stderr | Replace/append, both redirection orders, alias into pipe/capture/channel, single-stage alias cardinality, multi-stage ambiguity, stdout conflicts, numeric-token boundaries and rejection before effects. |
| `let` bindings | Unused initializer ordering, typed values, failures, initializers reading the previous binding, uninitialized reads through nested `def`s, `let` shadowing, `set` assignment, duplicate `def`/configuration rejection, fresh calls/iterations, and suspension without replay. |
| Transfers | Nested loops/branches, loops over generator streams, function boundaries, invalid unused code, cleanup before transfer, successful joins, nonzero cancellation, `exit` with and without an operand, `exit` inside `$(…)`/`$?(…)` ending only the capture, exact status through nested runners, later-fragment suppression and embedding survival. |
| Libraries | Different caller cwd, nested/cyclic/repeated includes, `include?` of a missing file, shared-name duplicates, definition-only rejection, load authority, no load-time effects, include frames, and `module/dir`-relative assets. |
| Execution scopes | Setup evaluation/validation, cwd/env overlay and stage overrides, unchanged `env/` reads, store and tool lookup, nested LIFO cleanup, borrowed versus owned jobs, accepted outcomes, every transfer path, cleanup failure and shared deadlines. |
| Records | Empty/consecutive/trailing delimiters, CRLF/partial EOF, NUL and embedded newlines, UTF-8/size errors, reader conflicts, unread-byte preservation, source failure, bounded working state and per-record cleanup. |
| Accepted exits | Empty/invalid policies, retained raw status and `ok: false` versus effective selection and CLI status, earlier unaccepted/later accepted peer and per-record exits, fail-fast/collect, signals, launch/timeout/capture errors, grants and cancellation. |

Cross-feature checks must also cover:

- Registered defers run on both zero and nonzero exit, including remaining actions
  after one fails while budget remains. A helper's `exit 0` followed by cleanup
  failure stays terminal through enclosing recovery; neither fallback nor later
  statements/fragments execute.
- A stable input identity does not permit cache reuse that changes the remaining
  bytes seen after a record reader breaks. Check handoffs both within a boundary
  and to the embedding caller after it returns.
- Uninitialized reads through nested `def`s retain `BINDING_UNINITIALIZED`
  through `??`, graph recovery, and `$?(…)` without running fallback effects or
  yielding an outcome.
- Relative stage cwd overrides compose with nested `with` scopes for authored
  graphs and `run!`, rather than rebasing at the invocation directory.

Every area also requires effect-free parsing/formatting/dry-run, parse-format-parse
preservation, diagnostics without sensitive values, cancellation under blocked I/O,
and repeated allocator/lifetime checks. Synchronize concurrency tests through
explicit readiness/transport boundaries rather than assuming a sleep establishes
ordering. Representative scripts must run as actual utilities: stdin JSON filtering,
file batches with spaces/newlines and dash-prefixed names, selected-status probes,
private staging cleanup, shared libraries, and scoped background validation.

Release installation, artifact integrity, and host-specific process containment
retain their governing specifications. Performance measurements must identify
artifacts and distinguish startup, external-command overhead, record throughput,
and retained memory; this specification makes no unmeasured Bash speed claim.
