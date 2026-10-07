# CLI ANSI and presentation modes

## Purpose and scope

Kame provides three presentations of the same command results, lifecycle events,
and diagnostics: `ansi`, `text`, and `json`. ANSI is the default and provides a
modern inline terminal experience without compromising exact output or automation.

The interface answers:

- What command, source, or target is being requested?
- What is happening now, and which tools are running?
- Why is work being reevaluated or executed instead of reused?
- What completed, failed, or was cancelled, and how long did it take?
- What concrete action can the user take next?

This specification supersedes presentation rules in
[009 CLI](009-cli.md), [011 Diagnostics](011-diagnostics.md),
and [027 Job presentation](027-job-presentation.md) where they differ.
Those specifications continue to own command semantics, stable diagnostic codes,
process-display bounds, and change-reason codes. Execution, capability grants,
freshness, caching, cancellation, and exit statuses are unchanged.

Presentation is observational. It must not request extra dependencies, execute
effects, or alter scheduling merely to populate a display. Native and WASM CLIs
expose equivalent information where their hosts establish the same facts.

The ANSI interface is an inline dashboard, not a fullscreen TUI. It preserves
scrollback, does not enter alternate-screen or raw-input mode, and does not consume
stdin intended for sources or child processes. Menus, prompts, mouse interaction,
custom progress reporting by recipes, and configurable themes are outside scope.

## Global presentation options

```text
-o, --output ansi|text|json       presentation mode (default: ansi)
    --json                      alias for --output json
    --color auto|always|never    styling policy for human output
    --diagnostic-format human|plain
                                diagnostic-only presentation override
```

`-o json`, `--output json`, and `--output=json` are equivalent. In this interface,
`--output` selects presentation, not a destination path; ordinary shell redirection
selects a destination. Every command accepts these controls, including help,
version, parsing, formatting, rendering, and cache actions.

Repeating the same output selection is permitted. Different explicit selections,
including an incompatible `--json`, report `OPT_CONFLICT`. Missing or invalid
values use the existing option diagnostic codes. Help documents these global
options without duplicating incompatible definitions in each command.

Global options may precede the `do` namespace or appear among a command's options.
Recognition respects the command grammar: tokens after `--`, option values,
source text, and program arguments are not scanned as presentation controls.
Presentation selection also governs usage and preflight errors, not just errors
that occur after execution starts. Help/version keep their existing precedence.

### Mode behavior

| Mode | Presentation | Intended use |
| --- | --- | --- |
| `ansi` | Semantic styling, structured messages, live worker rows when supported | Interactive terminal use |
| `text` | Append-only messages and human-readable results; optional styling | Logs, accessibility, copying |
| `json` | Structured data only; no human presentation | Automation and integrations |

The default selection is always `ansi`; its effective behavior degrades according
to the destination's capabilities. Capability checks apply separately to stdout
and stderr. A pipe on stdout does not disable a capable dashboard on stderr.

- Live rendering requires a terminal destination with cursor support.
- Redirected output and `TERM=dumb` use append-only text, without cursor controls.
- Explicit `--output ansi` does not force cursor controls into a pipe.
- Small terminals collapse the layout; they do not cause execution failure.
- `text` never emits cursor movement, erase controls, or carriage-return rewriting.
- JSON ignores color, diagnostic-layout, terminal-width, and animation settings.

### Color and styling

`--color auto` honors the existing environment controls. A nonempty `NO_COLOR`,
`CLICOLOR=0`, or `TERM=dumb` disables automatic styling. Otherwise a nonzero
`CLICOLOR_FORCE` enables styling; without a force setting, styling requires a
terminal. An explicit CLI choice takes precedence over environment controls.

`--color always` permits styling in redirected human output, but does not enable
live cursor manipulation. `--color never` disables decorative SGR styling,
including color and text attributes. Under automatic policy, `NO_COLOR` similarly
selects the unstyled theme. It does not by itself disable live updating.

`--diagnostic-format plain` selects unstyled, deterministic diagnostics without
changing other human output. Without an override, ANSI uses rich diagnostics and
text uses plain diagnostics. An explicit `human` override does not enable cursor
controls or styling on its own.

```sh
kame                              # ANSI on capable destinations
kame -o text default              # append-only human output
kame -o text --color never default # unstyled, append-only output
kame --output json default        # execution JSON Lines
kame do plan --json default        # explicit machine-readable inspection
```

## Output ownership and exact data

Command data and presentation are separate. Selecting ANSI must not decorate
every byte the CLI writes.

| Content | ANSI/text destination and policy |
| --- | --- |
| Progress, reasons, lifecycle messages, diagnostics, summaries | stderr |
| Human inspection reports, help, version | stdout; style only under its own destination policy |
| Requested definition/expression values | stdout; existing canonical representation and newline behavior |
| Recipe stdout and stderr | Their original streams, unchanged |
| Artifact bytes, formatted source, rendered documents | stdout, unchanged |

In human modes:

- `do cat` adds no label, styling, or newline to artifact/value output. It retains
  its quiet materialization policy: no routine progress or incidental recipe
  output. Diagnostics remain available on stderr.
- `do fmt` emits canonical source, not highlighted source.
- `do render` emits the rendered document, not a decorated preview.
- Value output retains [005 Evaluation](005-evaluation.md).
- Child streams are not prefixed, recolored, line-reassembled, or truncated by
  the presenter. Existing capture limits do not limit live publication.
- Raw payloads may themselves contain terminal controls. The presenter does not
  promise that arbitrary application output is sanitized or secret-free.

JSON mode writes CLI-controlled output only to stdout and leaves stderr empty,
including during usage, source-loading, and compilation failures. Raw content is
encoded in its command's JSON representation rather than interleaved with JSON.

## Messages, states, and writing

Message kind, lifecycle state, and diagnostic severity are separate concepts.
Informational messages and tips do not introduce new diagnostic severities.

| Message kind | Visible label | Meaning |
| --- | --- | --- |
| Information | `info` | Established fact, change, or execution decision |
| Warning | `warning` | Recoverable problem or qualification |
| Error | `error` | Work cannot complete |
| Fatal | `fatal` | The instance cannot safely continue |
| Tip | `help` | Concrete recovery action |
| Note | `note` | Supporting context |
| Success | `done` | Successful completion |
| Cancellation | `cancelled` | Interrupted work, distinct from failure |

```text
info [./build/app] execute: declared output is missing
warning CACHE_UNUSABLE: saved record cannot be read; continuing without reuse
error TOOL_MISSING: required executable `cc` was not found
help: install `cc` or select it with --tool cc=PATH
done [./build/app] completed in 1.42s
cancelled [./build/app] interrupted
```

Messages lead with the relevant subject and established outcome. They avoid blame,
vague failures, duplicated metadata, and unsupported explanations. A tip is shown
only when a concrete recovery action is known. Optional symbols reinforce written
labels; neither symbols nor colors carry meaning alone. The baseline symbols are
ASCII, not emoji-dependent.

Warnings do not independently change a successful exit status. Change reasons are
information, not warnings. Child stderr is not automatically an error. Cancellation
has its own wording and counts; it is not presented as an ordinary recipe failure.

Diagnostics retain their original code, severity, source/span, notes, related
locations, frames, target stack, tips, and bounded cause metadata. Captured process
stdout/stderr is not retained or replayed inside diagnostic causes.

## Information architecture

### Persistent log

The upper region is an append-only event log preserved in scrollback. It contains:

- Warnings, diagnostics, notes, and recovery help.
- Terminal target outcomes: successful completion, failure, and cancellation.
- Command results appropriate to the command.

ANSI target rows lead with `[target-name]`, without redundant `done` or `error`
prefixes. Successful rows align the measured target duration and `✓` at the right
edge, for example `40.8s - ✓`, matching the tally's `ELAPSED - STATUS` order.
All native ANSI durations use seconds with one decimal place, including worker,
target, cycle, and invocation durations. Duration spans the target lifecycle, including
dependency work and reuse validation, not just a child's runtime. Missing timing
is omitted rather than fabricated. Only the outcome
is green for success or red for failure. Named targets are purple; paths have a
muted directory prefix and only their basename in bold. Worker tools use gold,
and warnings/retries use amber. No whole-line color. Long subjects are clipped at grapheme boundaries rather
than wrapping the outcome. Redirected output keeps unpadded, complete subjects.
Failure details immediately below the target row include the diagnostic code,
message, and established exit status or signal.

Reasons retain specification 027's stable codes, certainty, privacy, and
per-generation deduplication. A reevaluation decision is not a claim that a
process ran. Resuming an attempt or redrawing a frame never repeats a message.

ANSI represents routine start/process/service transitions only in live rows, never
in the persistent log, including when live rendering is unavailable. Routine
execution reasons are omitted in ANSI. Text logs transitions and reasons once
because it has no replaceable region.
Detailed dependency, effect, and process-transition logging belongs under the
existing verbose policy. Both human modes preserve warnings, diagnostics, and
terminal outcome explanations.

Raw child streams remain separate from structured Kame messages. The CLI does not
pretend they have target labels when none were emitted by the child.

### Live dashboard

Below the log, the replaceable region contains:

1. A muted separator naming the invocation and its active worker count,
   for example `build default · workers: 2`.
2. Only active worker slots, with aligned subject, state, tool, and elapsed fields.
   Worker durations use the shared decimal-seconds format and align at the right
   edge, including compact layouts where the tool column is omitted.
   Completed work disappears; historical idle slots never consume visible rows.
   Ready services are summarized separately, not shown as occupied workers.
3. The requested end target/rule immediately above the tally, separate from
   the dependency targets currently occupying workers. Use the same named-target
   or path styling as worker subjects, and keep this row visible in compact layouts.
4. Aggregate build progress below the workers, with `ELAPSED - STATUS` aligned at
   the right edge of that same line. Empty process counts are omitted; global
   `building` status does not claim that a child process is currently running.

The native presenter retains its previous frame and overwrites only changed rows.
Timer updates never erase and rebuild the entire pane. Log batches and their
replacement footer are published together, with synchronized terminal updates
where supported; there must be no timed interval with a blank status pane.

Do not repeat version, working directory, source details, or configuration on
every update. Those details belong in a report or diagnostic when relevant.

### Worker identity and fields

A worker is a presentation slot for an active execution work item, not an OS
thread, process ID, or engine node ID. The slot remains stable while the item is
active and may be reused after completion.

| Field | Required meaning |
| --- | --- |
| Slot | Compact presentation identity |
| Subject | Target, source file, or operation owning the work |
| State | Established running, evaluating, publishing, retrying, or stopping state |
| Progress | Measured units when available; otherwise indeterminate |
| Current tool/operation | Host-established executable, pipeline, or operation |
| Elapsed | Time since the current process/operation started, when known |
| Attempt | Retry attempt when greater than the initial attempt |

A target-started event alone does not establish an occupied execution worker.
The native CLI observes actual scheduler dispatch so synchronous Kame evaluation,
rendering, and declarative effects can occupy a worker even without a child
process. It draws the owning target before dispatch, labels it `evaluating` with
tool `kame`, and releases the slot as soon as that dispatch returns. This observer
does not claim queued parents are running and does not change scheduling.
Waiting parents are not shown as fake running workers. A started target may be
evaluating or validating reuse rather than queued: never infer a waiting tally by
subtracting process workers from started targets. Where execution phase is not observable, use a qualified active state
rather than inventing a tool or phase. Additional phase observations must not
change execution policy.

A pipeline is shown under its owning work item, with bounded stage details. Do not
infer an executable by parsing shell text. Use a shell or operation label when
that is all the host established. Apply specification 027's bounds: at most eight
arguments and 160 UTF-8 bytes per stage, and four stages, with explicit truncation.
No environment values or process input belong in command summaries.

Services are distinct from ordinary worker occupancy: show starting/stopping work
and summarize ready services separately. A ready service is not a completed build
target merely because its readiness value was published. The jobs limit remains
a scheduling policy, not a claim that total child-process or service count equals
the number of worker rows.

### Aggregate fields and counting

Display running work, waiting targets, successful targets, failed targets,
cancelled targets, established reuse, ready services, and total elapsed time when
those facts are available. Hide unavailable optional counts rather than guessing.

`done` includes successfully reused targets; reuse is a subset, not an additional
terminal category:

```text
2 running · 1 waiting · 100 done (84 reused) · 0 failed
```

Count terminal target outcomes once per resource identity and generation, not
once per subscriber, redraw, process, or retry. Failed processes that are retried
do not increment final target-failure counts unless their target fails. Define
operation-specific counting units explicitly: formatting counts files, not build
targets. Do not infer reuse from elapsed time or absence of process-start events.

Worker elapsed time follows the current process/operation; invocation elapsed
time covers loading through final cleanup. Watch shows cycle elapsed time during
work and the last cycle's duration while idle. Use monotonic elapsed clocks where
available; do not require extra host queries for every rendered row.

## Honest progress

A progress bar represents measurable units, not elapsed time or visual activity.
Arbitrary recipes normally have no meaningful percentage and use indeterminate
presentation:

```text
0  ./build/main.o     running       cc                  1.2s
```

When a finite total is established, show units and optionally a bar:

```text
0  format sources    [######....]  6/10 files           0.3s
```

- Do not estimate recipe completion from historical runtime.
- Do not parse arbitrary child output for progress percentages.
- Do not show a build percentage while dependency discovery can change the total.
- Show completed counts without a denominator when the total is unknown.
- An optional spinner is decorative; written state is sufficient without motion.
- Show completed progress only after established success, not merely process exit.

## Presentation examples

These are layout examples, not literal ANSI byte sequences. Times, counts, paths,
and tools illustrate established facts; real output omits facts it cannot know.

### Active build

```text
info [./build/main.o] reevaluate: input content changed: ./src/main.c
info [./build/util.o] execute: declared output is missing
done [./build/config.h] reused

build default                                                2.4s
  0  ./build/main.o       running       cc                     1.2s
  1  ./build/util.o       running       cc                     0.8s
-----------------------------------------------------------------
2 running · 1 waiting · 8 done (5 reused) · 0 failed
```

The heading, worker rows, separator, and aggregate line form the replaceable
region. Messages above it remain permanent. A wide or verbose layout may add
bounded argv beneath its worker; the compact layout keeps the tool in the row.

### Completion and no-op

On normal completion, replace the dashboard with one permanent summary:

```text
done build default · 12 targets complete (5 reused) · 3.18s
```

When no execution work was required and that fact is established:

```text
done build default · no work required · 0.04s
```

A result-producing pure expression need not add a routine success message to
stderr. Its canonical value remains the result; empty programs do not invent one.

### Failure

```text
error [./build/app] RECIPE_FAIL: recipe exited unsuccessfully
  required by default -> ./build/app

  Makefile.kmk:18:2
  18 | cc @<* -o @>
     | ^~~~~~~~~~~

  caused by: cc exited with status 1

build failed · 8 done · 1 failed · 2 cancelled · 2.41s
```

The authored location, process identity, and dependency stack are shown only when
available. Notes, frames, related locations, and help follow specification 011's
hierarchy. A failed row cannot disappear before its diagnostic enters the log.
Final success is not printed until owned process/service cleanup has completed.

### Watch

```text
done cycle 3 · 4 targets complete · 0.62s

watch default · waiting for changes
last cycle succeeded · 4 done · Ctrl+C to stop
```

After a failed cycle, retain its outcome until a subsequent cycle supersedes it:

```text
watch default · waiting for changes
last cycle failed · 1 failed · Ctrl+C to stop
```

Changes received during work indicate a pending next cycle rather than falsely
claiming a concurrent rebuild. A source-reload error preserves authored diagnostics
and the waiting-for-repair state. Counters reset per cycle; invocation lifetime
does not masquerade as cycle duration. Idle watch emits no repeated lifecycle
messages or animation frames.

### Text

```text
info [./build/main.o] reevaluate: input content changed: ./src/main.c
started [./build/main.o]
process [./build/main.o] cc -c ./src/main.c -o ./build/main.o
done [./build/main.o] completed in 1.20s
done build default · 12 targets complete · 3.18s
```

Text emits transitions, not dashboard snapshots. It uses no width-dependent
padding or wrapping. Styling changes only attributes, not wording or ordering.

### Inspection

```text
plan ./build/main.o

  rule        ./build/{name}.o : ./src/{name}.c
  source      Makefile.kmk:8:1
  captures    name = "main"
  inputs      ./src/main.c
  outputs     ./build/main.o
  tools       cc -> /usr/bin/cc
  freshness   stale
```

Human plans also expose sequence boundaries, order-only inputs, arguments, and
dynamic dependencies when applicable. Distinguish `unknown` freshness from
`stale`; a plan does not claim execution.

```text
inputs ./build/app · depth 1
  ./build/main.o
  ./build/util.o

tools
  cc       /usr/bin/cc    available
  strip    —             unavailable
```

Empty reports say `no inputs`, `no referenced tools`, or `no managed records`,
not unexplained blank output. Listing an unavailable tool remains successful;
`tools check` fails only when the selected work requires unavailable tools.

## Standard value presentation

Human reports use consistent spelling independently of their theme:

| Element | Human representation |
| --- | --- |
| Path or resource URI | Preserve its meaningful prefix, scheme, authority, and spelling; quote whitespace/control characters in report fields |
| Target | Preserve authored classification, including significant `./`; never shorten a file target into a bare task |
| Rule | Authored target/input syntax with captures; keep rule identity distinct from the concrete selected target |
| Symbol or option in prose | Backtick-delimited spelling, such as `SOURCES` or `--force`; dedicated labelled fields need no extra punctuation |
| Literal value | Canonical Kame display: quoted strings/patterns, `:nil`, `:true`, `:false`, lists and records |
| Source location | `source:line:column`, one-based display coordinates; no invented coordinates |
| Count or measured progress | Integer with a named unit or labelled category; `6/10 files`, not an unexplained percentage |
| Duration | Explicit unit, such as `24ms`, `1.42s`, or `2m 04s`; unknown duration is omitted |
| Byte size | Explicit unit; JSON retains integer bytes even when a human table uses a scaled unit |
| Missing information | A labelled `unknown`/`unavailable` state or omitted optional field, never an invented zero |

Report-field quoting is presentation escaping, not a change to resource identity.
Exact payload output does not receive report-field escaping. Disambiguate
reevaluation from execution and freshness from observed completion; these are
different facts even when they concern the same target.

## Semantic design tokens

Tokens describe meaning, not literal colors. Command logic must not request
"red text" directly. The hardcoded initial theme uses terminal-default backgrounds
and a restrained palette: cyan, green, yellow, red, and the default foreground.
No configurable theme loader is required by this specification.

| Token | Applied to | Initial theme |
| --- | --- | --- |
| `text.primary` | Ordinary prose and values | Default foreground |
| `text.secondary` | Supporting context | Default foreground |
| `text.muted` | Times and supplementary counters | Dim where readable |
| `heading` | Invocation and report headings | Bold |
| `message.info` | Information label | Cyan |
| `message.warning` | Warning label | Yellow, bold |
| `message.error` | Error label | Red, bold |
| `message.fatal` | Fatal label | Red, bold |
| `message.tip` | Recovery label | Cyan, bold |
| `message.note` | Note label | Default foreground |
| `status.running` | Active execution/evaluation | Cyan |
| `status.waiting` | Blocked work or idle watch | Default foreground |
| `status.success` | Successful completion | Green |
| `status.reused` | Established reuse | Green |
| `status.failed` | Failed work | Red, bold |
| `status.cancelled` | Cancelled work | Yellow |
| `status.retrying` | Retry or service restart | Yellow |
| `status.ready` | Ready service | Green |
| `value.path` | Filesystem paths and resource URIs | Cyan |
| `value.target` | Target identity | Bold |
| `value.rule` | Rule syntax and target patterns | Default foreground |
| `value.symbol` | Definitions, operations, captures | Bold |
| `value.tool` | Executable names | Bold |
| `value.option` | CLI option spellings | Bold |
| `value.literal` | Strings, numbers, booleans, nil | Default foreground |
| `location` | Source identity and coordinates | Cyan |
| `diagnostic.code` | Stable diagnostic code | Bold |
| `diagnostic.marker` | Caret/span marker | Severity color, bold |
| `progress.complete` | Completed measured units | Cyan |
| `progress.remaining` | Remaining measured units | Default foreground |
| `structure` | Separators, gutters, punctuation | Muted |

Style attributes are independently expressible as foreground/background and
bold, dim, italic, underline, or reverse, but the initial theme uses only the
attributes listed above. Unsupported attributes degrade to readable plain text.

- Apply severity styling to labels and markers, not entire paragraphs.
- Keep paths recognizable across information, warning, and error messages.
- A path target combines `value.target` emphasis with `value.path` treatment.
- Literal spelling remains canonical; styling must not change its characters.
- Styling requires typed fragments or known fields, not regex inference over
  arbitrary prose, source text, or child output.
- Reset styles at presentation boundaries so they cannot leak into payloads.
- Avoid blinking, decorative backgrounds, and low-contrast mandatory information.
- Unstyled presentation keeps labels, states, and structure meaningful.
- Validate the basic palette on light and dark terminals; dim is optional.
- Theme changes cannot alter wording, execution, payloads, or message visibility.

## Events and message presentation

Retain existing event names and identity fields. Worker slot numbers are not
runtime identities. Runtime events remain correlated by resource/node,
generation, attempt, and request when available; late events cannot attach to a
reused display slot.

| Event | Human presentation |
| --- | --- |
| `target-started` | Active target state; one text transition |
| `target-reason` | Persistent informational decision and established reason |
| `process-started` | Current tool, bounded argv, and process clock |
| `process-exited` | Process outcome/runtime; not necessarily target completion |
| `dependency` | Known graph state; detailed verbose transition |
| `effect` | Established operation state; detailed verbose transition |
| `target-value` | Requested-value policy; internal dependency values stay internal |
| `target-completed` | Successful count and release of ordinary worker row |
| `target-failed` | Diagnostic, failed count, and row release |
| `target-cancelled` | Separate cancellation count and row release |
| `cache-warning` | Persistent structured warning |
| `service-state` | Readiness, health, restart, and shutdown state |
| `stdout`, `stderr` | Immediate stream publication under command output policy |

CLI-owned observations cover invocation start/finish, watch-cycle boundaries,
idle watch, formatting/check results, cache-clean results, help/version data, and
JSON representations of artifact/document output. Their machine forms are
defined below; do not turn renderer animation ticks into events.

Underlying runtime diagnostics and their CLI rendering must not create duplicate
human diagnostic blocks for the same outcome. Existing JSON lifecycle payloads
remain available even when a diagnostic also has its established separate record.

## JSON contract

Pure data does not require replacing every existing JSON shape. Preserve existing
machine payloads and explicitly document framing:

- Build/run and streaming payload commands use schema-versioned JSON Lines.
- Plan retains schema-1 plan records, one per selected target.
- Graph, tool-list, cache-list, and AST commands retain their established single
  JSON document/array payloads.
- Static-document commands assemble a complete result before publication. On
  failure they emit one diagnostic object, or one array of diagnostic objects
  when several diagnostics are available, never a partial result followed by
  unrelated JSON objects.
- New result types use schema-1 records with stable `type` and payload fields.
- No command mixes human help, raw content, progress, or summary text into JSON.

Existing record example:

```json
{"schema":1,"type":"process-started","target":"./build/main.o","node":7,"generation":1,"attempt":1,"program":"cc","argv":["-c","./src/main.c","-o","./build/main.o"]}
```

New CLI-owned record types have the following required payloads:

| Type | Payload |
| --- | --- |
| `invocation-started` | `command`; selected `targets` or source identities when available |
| `summary` | `command`, `status`, `exitStatus`, `elapsedMS`; established command-specific counts |
| `watch-cycle-started` | Invocation-local `cycle` number |
| `watch-cycle-finished` | `cycle`, `status`, `elapsedMS`, established counts |
| `watch-idle` | Last `cycle`/outcome when available; emitted on transition only |
| `format-result` | `source`, `action` (`format`, `check`, `in-place`), `changed`; formatted `data`/`encoding` only for `format` |
| `render-result` | `source`, `action` (`render`, `check`); `data`/`encoding` only for `render` |
| `artifact` | `target`, `data`, `encoding` for each file-byte chunk |
| `tools-check-result` | Selected `target`, required tool names and availability |
| `cache-clean-result` | Number of managed records actually removed |
| `help` | `topic`, usage forms, command/option metadata, and examples |
| `version` | Version, build ID, build time, and build mode |

Byte payloads use UTF-8 when valid and otherwise base64, with an explicit encoding.
Artifact/document publication may use multiple bounded records; chunk boundaries
are transport details, not line or character boundaries. Consumers concatenate
decoded bytes in order. Empty output is represented explicitly. `do cat` definition
values retain typed `target-value` representation, not a fabricated file artifact.

```json
{"schema":1,"type":"artifact","target":"./build/message.txt","data":"hello\n","encoding":"utf-8"}
{"schema":1,"type":"summary","command":"cat","status":"success","exitStatus":0,"elapsedMS":4}
```

```json
{"schema":1,"type":"summary","command":"build","status":"success","exitStatus":0,"completed":12,"failed":0,"cancelled":0,"elapsedMS":3180}
```

Summary status is `success`, `failure`, `cancelled`, or `different`. `different`
represents successful format checking that found differences and exits 1; it is
not a fabricated execution diagnostic. Summary counts use the command's defined
units. Invocation start/summary records belong to execution and new result-stream
commands, not around legacy single-document inspections. Graceful completion
emits one summary after all owned work is reaped; forced termination may prevent
final publication.

JSON is invariant across terminal widths and contains no presenter-generated ANSI
sequences. Application data can legitimately contain escaped control characters.
Omit unavailable optional fields. Preserve existing typed values, stable diagnostic
data, privacy restrictions, and binary encodings. Additive schema-1 fields remain
optional to existing consumers, which ignore unknown fields.

Concurrent event order is causal rather than globally deterministic. Tests must
not impose a total order on independent jobs. Machine output is flushed while
work runs and honors publication backpressure; it is not buffered until completion.

## Command-by-command presentation contract

All command-specific help advertises global presentation selection and the
command's JSON framing. Direct invocation and `do run` remain equivalent. Removed
`do expr` and `do kash` names remain removed and provide structured migration help.

| Command surface | ANSI/text | JSON |
| --- | --- | --- |
| Primary build | Reasons, worker rows/transitions, summary; original recipe streams | Existing execution events, invocation start/summary |
| Direct execution / `do run` | Same policy; exact value/effect output | Values, streams, diagnostics, session completion |
| Build dry-run | Explicit dry-run identity and rendered planned work; no claim of execution | Plans/rendered work explicitly marked dry-run |
| Build watch | Per-cycle dashboard, persistent history, idle state | Cycle boundaries, changes, results |
| `do plan` | Rule, source, captures, inputs/outputs, tools, freshness, sequencing | Existing plan records |
| `do inputs` / `outputs` | Direction, requested depth, edge list, explicit empty state | Existing graph document |
| `do span` | Separate static and evaluation-dependent resources; expansion state | Existing span document |
| `do tools` | Tool/path/status table; unavailable tools do not fail listing | Existing tool array |
| `do tools check` | Required-tool results and established success/failure | Check results and diagnostics |
| `do parse` | Readable AST tree with kinds, values, and spans | Existing stable AST document |
| `do fmt` | Exact canonical source, without syntax highlighting | Per-source formatted payload |
| `do fmt --check` | Unstyled changed-path list on stdout; summary on stderr | Changed/unchanged results; `different` summary when needed |
| `do fmt --in-place` | Per-file results and summary on stderr | Actual replacement results |
| `do render` | Exact rendered bytes | Encoded document payload |
| `do render --check` | Validation result; no rendered document | Validation result |
| `do cat` | Exact artifact/value; quiet materialization | Encoded artifact or typed value; no raw interleaving |
| `do cache list` | Backend/key/size table and explicit empty state | Existing managed-record array |
| `do cache clean` | Actual removals and summary; only managed records | Removal result and summary |
| Help, `do`, command help | Headings, options, examples; no progress | Structured help metadata |
| Version | Existing concise version line; no progress | Version/build metadata |
| Unknown/removed commands | Existing code, specific message, migration help | Diagnostic data only |

Human reports include all established information needed to interpret their
machine counterparts, without printing allocator details or private internal
values. Fast inspection/help/version commands do not create a transient dashboard.
Long finite file operations may show measured progress without inventing parallel
workers when processing is sequential.

Changing inspection commands from implicit JSON to human output is an intentional
compatibility change. Scripts request `--output json` or `--json` explicitly.
Exact-output commands keep their existing human byte contracts. Existing JSON
payload shapes and exit-status meanings remain stable.

## Terminal resilience and rendering

- Rewrite only the owned dashboard region, never persistent messages or unrelated
  terminal content. Do not clear the entire screen.
- Serialize CLI-controlled terminal publication. Clear the dashboard before
  publishing raw child output to the same terminal and redraw only at a safe
  boundary. Suspended live rendering leaves ordinary append-only messages usable.
- For an unterminated chunk, suspend redraw until a safe line boundary. For
  terminal-controlling child output whose position cannot be tracked, disable
  live rewriting for the affected invocation. Never insert a payload newline
  just to restore layout or buffer indefinitely waiting for one.
- Coalesce redraws to at most ten per second. Publish streams, warnings, and
  diagnostics immediately; redraw throttling must not delay them.
- Use bounded pending display state; do not retain a second unbounded copy of the
  event log or bypass sink backpressure to maintain animation.
- Measure terminal display cells, not bytes. Truncate at grapheme boundaries and
  preserve wide/combining text. Do not reverse RTL strings manually.
- Keep diagnostics complete. Truncate only bounded command summaries and live
  rows, with visible truncation; text/JSON retain their defined full data.
- Reserve room for the heading and aggregate line. Collapse tool details and
  optional columns first. If workers exceed terminal height, show a stable subset
  and an explicit hidden-active count. At 80 columns or more use the full layout;
  at 40–79 columns retain subject, written state, and aggregate counts before
  optional tool/time columns. Below 40 columns or four rows use append-only text.
  Unknown terminal dimensions use an 80-column fallback without assuming height
  for an unbounded worker region; unavailable safe cursor/layout capabilities
  select append-only text instead.
- Avoid automatic terminal line wrapping in the replaceable region, including
  accidental wrapping at the final column. Resize recomputes occupied rows and
  safely clears stale owned content.
- Use basic terminal colors/cursor operations; Unicode separators, hyperlinks,
  truecolor, and synchronized updates are not requirements.
- Never enable raw mode or hide the cursor unnecessarily. Restore any changed
  cursor/style state on normal exit, recoverable failure, SIGINT/SIGTERM, and
  supported suspension/resumption paths. Uncatchable termination cannot guarantee
  terminal restoration.
- Emergency `NO_MEMORY` rendering retains its static-allocation contract and must
  not depend on dashboard teardown allocation.

## Acceptance criteria

- Every command surface above accepts ANSI, text, and JSON selection, including
  help/version, usage errors, and preflight failures. Alias, equals spelling,
  conflict, option-value, and `--` behavior are covered.
- Default ANSI adapts independently to each destination. TTY, redirected,
  `NO_COLOR`, `TERM=dumb`, explicit color, and diagnostic-only overrides have
  deterministic behavior. Text never contains cursor/erase controls.
- Exact-byte tests cover requested values, `cat`, source formatting, document
  rendering, child stdout/stderr, zero-byte output, and unterminated chunks.
- Human and machine output preserve codes, severities, source spans, available
  context, reasons, and established outcomes. Diagnostic blocks are not repeated
  by lifecycle handling or redraw.
- Progress never fabricates totals, percentages, current tools, reuse, locations,
  or recovery actions. Target/process/service states remain distinct.
- Concurrent targets, shared dependencies, pipelines, retries, cancellation, and
  service cleanup retain correct ownership and disjoint terminal counts.
- Watch resets cycle counts, retains failed outcomes, reports queued changes and
  source-repair states, and produces no repeated idle output.
- JSON framing and each new record have golden fixtures. Existing payload shapes
  remain stable. Usage failures stay machine-readable with empty stderr. Binary
  payloads decode to their original bytes.
- Live human/JSON output is observable before process completion and remains
  bounded under blocked sinks, cancellation, and cached replay.
- Layout goldens use fixed terminal sizes and an injected clock. Tests include
  shrinking/growing terminals, long subjects, tabs, combining text, wide characters,
  and ASCII fallback. Independent concurrent events are not required to interleave
  identically across runs.
- Native and WASM agree on command grammar, payloads, reason semantics, counting,
  and privacy. Timing is checked for type/non-negativity, not exact equality.
- Rendering failure and signal paths restore terminal state where possible;
  allocation failure retains static emergency diagnostics.
