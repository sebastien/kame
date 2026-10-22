# Diagnostics and Error Reporting

## Purpose

This file defines Kame's user-facing failure model, machine-readable
representation, and terminal reporting. It is the authoritative registry of
stable diagnostic codes. Codes use uppercase ASCII words separated by
underscores, and a code's meaning must not change after release.

A useful diagnostic answers, in order:

1. What failed.
2. Where it failed, when Kame knows an authored location.
3. Why Kame believes it failed, including the relevant evaluation or
   target context.
4. What the user can do to recover, when a concrete action is known.
5. What an underlying host process reported, when applicable.

Diagnostics are not ad-hoc strings assembled at failure sites. Parser,
evaluator, planner, runtime, host, and CLI code construct or enrich one
structured diagnostic. Human, plain, and JSON output render that same value.

New and revised messages should identify established constraints: arity failures
give expected and actual counts, type failures give the actual kind, and missing selector contexts
name the required input, output or argument context. An EOF delimiter failure
names the missing delimiter without an additional whitespace error. A recognized
outer-language mismatch names the appropriate language rather than suggesting
an unrelated separator fix. Recovery actions use structured `tips` where the
diagnostic representation supports them; changing wording does not rename codes.

## Diagnostic Model

Every diagnostic has:

- A stable `code` from this registry.
- A `severity`.
- A concise, specific `message` that states the established reason for the
  failure or warning.
- An optional primary source identity and zero-based, half-open `span` into
  that source.

It may also carry the following context. Empty fields are omitted rather than
rendered as placeholders.

| Field | Meaning | Human rendering |
| --- | --- | --- |
| `notes` | Short additional facts, suggestions, or qualifications. | `note:` lines |
| `related` | An ordered list of related messages, each with `message`, `source`, and `span`. | source-qualified notes with available source excerpts |
| `frames` | Evaluation, definition, rule, include, or script boundaries crossed while propagating the failure. Each frame has a kind, label, and optional source location. | numbered evaluation backtrace with available source excerpts |
| `target` | Current target when one target directly owns the failure. | headline subject |
| `targetStack` | Ordered root-to-current target dependency path. | `required by a → b → target` |
| `tips` | Concrete recovery actions. | `help:` lines |
| `cause` | Underlying process or host failure metadata, such as executable, status, signal, and capture truncation limits. | `caused by:` block |

The primary span identifies the expression, selector, rule segment, or recipe
that directly failed. A related span identifies a separate declaration or call
site that helps explain the failure. A frame is propagation context, not a
second primary failure.

A source location is `{source, span}`, where `source` is a non-empty source
identity and `span` has integer byte offsets satisfying
`0 <= start <= end <= source length`. A primary diagnostic has both fields or
neither. A related message and a source-qualified frame always carry both; an
unqualified frame has neither. A frame may name a source different from the
primary source. Source text is UTF-8. Columns count terminal display cells:
tabs advance to the next eight-column boundary, combining code points have zero
width, and wide code points have width two.

`cause` is an object with a `kind` and a concise outcome message. Process causes
may include `program`, `status`, `signal`, `stdoutTruncated`, `stderrTruncated`,
and their byte limits. Captured stdout and stderr are deliberately omitted from
diagnostic causes because arbitrary process output cannot be guaranteed free of
secrets. Live recipe streams and explicit collected operation values are
unchanged; this policy protects diagnostic retention, not application logs.
Host-specific
causes may add additive fields, but they must never include environment values,
credential-like arguments, authorization headers, or other secrets.

All diagnostic lists are deterministic. Primary diagnostics are emitted in
source order when locations are comparable, otherwise in discovery order.
Notes, related messages, tips, and frames retain propagation order and emit
equivalent items once. Target stacks retain their complete root-to-current
order, including a repeated target that explains a dependency cycle.

Lower layers return the most specific code and primary span they can establish.
An upper layer may append frames, related notes, target context, tips, and a
cause, but it must not replace the original code, severity, message, or primary
span. Equivalent frames must be deduplicated in propagation order.

Messages and all text retained in a diagnostic must outlive the parser,
evaluator, or process callback that created them. Formatting a returned
diagnostic after those stacks unwind must be safe.

### Authored provenance and configuration queries

Source extraction, include expansion, combined inputs, and temporary query
programs must preserve mappings from evaluated expressions to authored source
identities and byte offsets. Synthetic prefix offsets and predicate-local offsets
are distinct coordinate spaces; neither may be reported as an offset into an
authored file without an origin mapping. Resolve and retain diagnostic locations
before temporary source maps or programs are released.

When an authored origin is available, a configuration-condition failure points
at the operation or expression that directly failed, including failures reached
through lazy definitions and helper calls in other files. The selecting condition
is outer propagation context, not a replacement primary location. Definition
frames identify their declarations; call frames identify the authored call sites.
Retain available include-site provenance that explains how those sources were
selected. The same rules apply to native and embedding-host source composition.

A query caller that supplies no authored origin must still receive useful frame
kinds and labels, but synthetic locations must remain unqualified. Do not invent
locations or discard known authored locations merely because other frames lack
them. An outer boundary may supply a primary location only when no more specific
authored primary location is known.

This provenance does not change configuration purity, evaluation order, lazy
semantics, diagnostic codes, or capability policy. In particular, a denied
configuration operation must explain that runtime grants cannot authorize it.

## Severities

- `warning`: execution may continue and the result remains usable.
- `error`: the current parse, evaluation, target, or command cannot complete.
- `fatal`: the instance cannot safely continue, normally due to exhausted or
  corrupted host state.

Human output uses the words `error`, `warning`, and `fatal`. A symbol may
reinforce severity in rich terminal output, but symbols and colour never carry
meaning by themselves.

## Codes

| Code | Default severity | Meaning |
| --- | --- | --- |
| `PARSE_ERR` | error | Invalid syntax; recovery may lower it to warning. Malformed pattern groups instead leave the token plain |
| `REF_MISSING` | error | Unknown reference or missing record component |
| `SEL_NO_CONTEXT` | error | Selector used without a matching context |
| `SEL_INDEX_INVALID` | error | Invalid index or slice |
| `OP_UNKNOWN` | error | Unknown operation |
| `TOOL_UNKNOWN` | error | Tool reference used without a tool resolver |
| `TOOL_MISSING` | error | Referenced tool was not found or is not executable |
| `EXPR_INVALID` | error | Invalid expression value, type, or arity |
| `JSON_INVALID` | error | Malformed JSON input |
| `ARGS_INVALID` | error | Script argv violates a `parse-args` schema; identifies a zero-based argument index without disclosing argv values |
| `USER_ERROR` | error | Explicit user failure from `fail!` |
| `EVAL_DEPTH` | error | Recursive evaluation exceeds the 32-expression stack-depth limit |
| `DEF_INVALID` | error | Invalid definition or binding form |
| `DEF_ESCAPE` | error | Binding a callable into an ancestor would strand its captured scope; ordinary returned callables may retain their owners |
| `PAT_INVALID` | error | Invalid pattern combination or missing pattern capture reference |
| `PAT_LIMIT` | error | Pattern matching exceeded its work budget |
| `TPL_PARSE` | error | Malformed template directive, argument list, or verbatim literal |
| `TPL_BLOCK` | error | Unterminated, stray, or mismatched template block directive |
| `TPL_STYLE` | error | Missing or unknown template comment style |
| `TPL_CYCLE` | error | Recursive template inclusion |
| `CAP_DENIED` | error | Capability denied |
| `PHASE_INVALID` | error | Operation is not valid in the current phase |
| `TGT_NO_DEFAULT` | error | No target or value entry was requested and the required default is absent |
| `TGT_NO_RULE` | error | No rule or source for target |
| `TGT_AMBIG` | error | More than one rule instance matches |
| `TGT_ARGUMENT` | error | Invalid, duplicate, missing, unknown or shadowing target argument/parameter |
| `ENV_CONFLICT` | error | Active shared rule instance has a different recipe environment |
| `DEP_CYCLE` | error | Dependency cycle |
| `RECIPE_FAIL` | error | Recipe process exited unsuccessfully |
| `RECIPE_TIMEOUT` | error | Recipe process timed out |
| `CAPTURE_LIMIT` | error | Command substitution exceeded its capture byte limit |
| `CAPTURE_ENCODING` | error | Captured text is not valid UTF-8 |
| `SERVICE_READY_TIMEOUT` | error | Service did not become ready before its timeout |
| `SERVICE_START_FAILED` | error | Service exited before becoming ready |
| `SERVICE_EXITED` | error | Service exited unexpectedly after becoming ready |
| `SERVICE_UNHEALTHY` | error | Service failed its configured health checks |
| `SERVICE_RESTART_EXHAUSTED` | error | Service restart attempts were exhausted |
| `EXEC_CANCELLED` | error | Execution was cancelled |
| `FINALIZE_TIMEOUT` | error | A finalizer exhausted its cleanup budget or could not start before the shared unwind deadline |
| `HOST_FAIL` | error | Host request, spawn, pipe, wait, or signal failure |
| `FS_ERR` | error | Filesystem read, write, stat, or glob failure |
| `FS_EXISTS` | error | No-clobber publication found an existing destination entry; staging is preserved |
| `FS_UNSUPPORTED` | error | The resource, host, or filesystem cannot provide the required safe publication primitive |
| `FS_PUBLISHED` | error | Publication already committed but subsequent work failed; `cause.published` and `cause.stagedRemoved` preserve the outcome, not permission to retry or roll back |
| `RES_INVALID` | error | Invalid resource identity, URI or protocol use |
| `PLUGIN_CONFIG` | error | Invalid or conflicting plugin declarations |
| `PLUGIN_PROTOCOL` | error | Invalid plugin request/response shape, identity or value |
| `PLUGIN_LIMIT` | error | Plugin request or response exceeds its byte limit |
| `PLUGIN_FAIL` | error | Plugin host/process reports an operation failure |
| `PLUGIN_TIMEOUT` | error | Plugin operation exceeded its timeout |
| `OUTPUT_MISSING` | error | Successful recipe omitted a declared output |
| `CACHE_UNUSABLE` | warning | Cache record or manifest cannot be used |
| `CLEAN_STATE` | error/warning | Output ownership cannot be persisted or cleanup requires safety/recovery review; uncertain production is a warning when ordinary publication remains valid |
| `CLEAN_BUSY` | error | A cooperating producer, service or cleanup operation holds the project cleanup boundary |
| `FEATURE_UNSUP` | error | Valid but unsupported feature on this target |
| `NO_MEMORY` | fatal | Allocator or fixed heap budget exhausted |
| `HANDLE_INVALID` | error | Invalid, stale, or foreign ABI handle |
| `CMD_UNKNOWN` | error | Unknown CLI command |
| `OPT_UNKNOWN` | error | Unknown CLI option |
| `OPT_NO_VALUE` | error | Missing CLI option value |
| `OPT_CONFLICT` | error | Conflicting CLI options |
| `OPT_VALUE_INVALID` | error | Invalid CLI option value |
| `BUILD_NO_SOURCE` | error | No build source was found |
| `NO_ARTIFACT` | error | Requested target has no readable artifact |
| `VAL_NOT_JSON` | error | Human `do cat` definition value has no plain JSON representation |

Embedding-wrapper errors such as `DISPOSED` identify client lifecycle misuse,
not portable runtime diagnostics; they are documented with the embedding API.

## Rules

- A lower package returns the most specific code it can establish.
- Wrapping adds context but retains the original code, severity, message, and
  primary source span.
- Command exit uses `RECIPE_FAIL`; inability to spawn or reap uses `HOST_FAIL`.
- A missing requested source file uses `FS_ERR`; a file target with no rule and
  no existing file uses `TGT_NO_RULE`.
- A target-less invocation with no `default` target uses `TGT_NO_DEFAULT` and
  reports the available named targets as a note.
- A nonempty definitions-only value session without selected entries, later
  executable fragments, or a `default` definition also uses `TGT_NO_DEFAULT`,
  with available definition names rather than invented build targets.
  Expression-statement and empty-program runs do
  not require a default (`009-cli.md`). Removed `do expr` and `do kash` commands
  use `CMD_UNKNOWN` with the appropriate `do run` migration spelling.
- File and inline fragments in execution mode are not an `OPT_CONFLICT`.
  Conflicts discovered while compiling the combined scope retain their existing
  definition/rule codes and source spans; diagnostics identify the originating
  file or distinct inline fragment rather than a flattened synthetic source.
- Malformed cache data is a miss and `CACHE_UNUSABLE` warning, never a fatal error.
- An unavailable requested remote executor uses `FEATURE_UNSUP` before a
  process starts.
- Malformed pattern group syntax is `PARSE_ERR`; pattern misuse detected at
  evaluation, such as a missing capture reference or an invalid argument
  combination, uses `PAT_INVALID`.
- A malformed `if`, `and`, `or`, `match`, or comparison form — wrong operand
  count, an unorderable mixed-kind comparison, or a non-string `match` subject —
  uses `EXPR_INVALID`. A `match` clause whose pattern is not a valid matcher
  uses `PAT_INVALID`.
- Document template failures use `TPL_PARSE`, `TPL_BLOCK`, `TPL_STYLE`, and
  `TPL_CYCLE` as specified in `016-templates.md`; the same codes cover a
  malformed recipe directive.
- OOM formatting must not allocate.
- Operation argument diagnostics name the operation, the one-based operand
  index (excluding the application head), the expected kinds or constraint, and
  the established actual kind. The primary span identifies that operand.
  Arity errors state the expected count or range and the supplied count.
  Rejected values are not printed merely to explain their kinds.

### Definition and include conflicts

A duplicate value or function definition uses `DEF_INVALID` and names the
conflicting binding. Its primary location identifies the later declaration;
`related` identifies the original declaration when available. Declarations in
included files or distinct source fragments retain their authored locations,
not offsets into flattened or synthetic source text. Include frames identify
the source inclusion paths that establish the conflict, including distinct
include sites when the same source was loaded twice.

Recovery tips reflect the established declaration forms: use a module's `?=`
default for intended consumer configuration, `+=` for an intended list
contribution, or remove unintended repeated inclusion/declaration. Do not
recommend these changes indiscriminately or infer that a repeated include is
always invalid. Includes have no implicit include-once behavior; skipped defaults
and valid repeated contributions are not conflicts. Producer conflicts retain
their existing rule diagnostic codes rather than becoming definition errors.

Authored setting checks using `fail!` retain `USER_ERROR` and their authored
message and location. They should name the setting and constraint without
including the rejected value; diagnostics must not expand the setting to explain
the error. Such checks do not establish a new automatic validation phase.

### Finalization failures

[`finally!`](043-kame-finalization.md) preserves an established body diagnostic
when cleanup also fails. Retain the cleanup code/message in `notes`, or in a
source-qualified `related` message when its authored location is available;
include nested failures once in unwind order. Never replace the body's code,
message, primary span or cause with a generic cleanup failure. When only cleanup
fails, its diagnostic is primary. `FINALIZE_TIMEOUT` distinguishes budget
exhaustion from a process's own `RECIPE_TIMEOUT`; explain whether cleanup timed
out or was not started because the shared deadline had expired.

Diagnostic precedence does not make capability denial or cancellation
recoverable: those control outcomes remain protected even if an earlier body
failure is the primary displayed diagnostic. The same context and secrecy rules
apply to cleanup messages as to ordinary failures; no callback return values,
environment contents or captured process output are retained to explain them.

## Language-Boundary Recovery

Keep existing diagnostic codes while making parser ownership and recovery clear.
The rules below constrain the established reason, primary span and useful tips,
not exact English wording. Human, plain and JSON output expose the same guidance;
evaluated values and environment contents are not included to illustrate errors.

| Failure | Required explanation and recovery |
| --- | --- |
| Rendering a callable (`EXPR_INVALID`) | Identify the actual callable kind, not a record/bytes conversion failure. For `@{name}`, suggest `@(name)` when invocation is intended; never invoke it implicitly. |
| Applying a noncallable (`EXPR_INVALID`) | Identify the actual head kind and authored call. If the head is a reference being rendered with `@(name)`, suggest `@{name}` for lookup; never fall back to that interpretation. |
| Invalid reference expansion (`PARSE_ERR`) | State that `@{...}` requires a reference; point at the invalid body and show `@(function args...)` for a call when applicable. |
| Invalid mixed Kash word (`EXPR_INVALID`) | Name the destination (argument, command head, redirection or setup key), actual fragment kind, and scalar constraint. Explain that list splicing is only valid for standalone unquoted argv substitutions. |
| Missing/invalid selector context (`SEL_NO_CONTEXT`, `SEL_INDEX_INVALID`) | Identify the required frame or invalid bound. In template text, also explain `\@` for a literal at-sign when the authored spelling may be literal, such as `--mtime=@0`; do not guess from an external executable name. |
| Missing explicit file prefix (`PARSE_ERR`, or `TGT_NO_RULE` where applicable) | Distinguish a logical name from a file path and suggest `./` when a file path is plausible. Never silently reclassify a name or modify source. |
| Denied environment read (`CAP_DENIED`) | Identify environment-read authority and, when known, the name. Explain the scoped `--allow-env=NAME` grant; definition configuration and child environment settings do not grant that read. |
| Invalid rule settings placement (`PARSE_ERR`) | When the malformed metadata placement is recognizable, show `target : inputs ; [shell: kash]`, or `target : ; [shell: kash]` with no inputs. Do not imply valid metadata is unsupported. |
| Template syntax in direct Kash (`PARSE_ERR`) | Name the direct Kash boundary and show `$name` / `${name}` for a reference or `(function args...)` for a call, without adding an implicit template pass. |
| Unquoted literal at-sign inside a Kash word (`PARSE_ERR`) | When reserved at-sign syntax is the established cause, explain quoting the literal word, such as `"--mtime=@0"`. Do not apply template-recipe selector rules or treat shell quotes as protection from a template pass. |
| Removed `--define-value` option (`OPT_UNKNOWN`) | Show `--set NAME=LITERAL` for closed typed data; `NAME=TEXT` and `--define` remain strings. No compatibility alias or type inference is offered. |

Recognized unclosed expressions/references name their missing delimiter without
extra separator errors. Literal parentheses in raw Kash words can be quoted or
escaped; quoted literal parentheses must not be diagnosed as calls. A pipe is
not diagnosed as noncanonical merely because it has application-equivalent
semantics. Valid selector use and deliberately literal brace text are not errors.

Grant tips do not broaden authority automatically or suppress phase failures.
They must not imply `--env NAME=VALUE`, `NAME=VALUE`, `KAME_NAME` or `--set`
authorizes `(env NAME)`. Where a tip names an explicit grant, it must respect
`009-cli.md`'s replacement policy rather than suggesting that all default grants
will remain enabled.

## Human and Plain Rendering

Human diagnostics are written to stderr. They use this information hierarchy:

```text
{severity}[{code}]: [{subject}] {message}
  required by {root} → … → {subject}

  ╭─ {source}:{line}:{column}
 {line} │ {source text}
      │ {span marker and, when useful, a short diagnosis}

Trace (innermost first)
  ├─ #0  {kind} {label} · {frame-source}:{line}:{column}
  ╰─ #1  {outer-kind} {outer-label} · {outer-source}:{line}:{column}
     │ {source text, only if this source line was not already shown}

note: {additional context}
note: {related-source}:{line}:{column}: {related context}
help: {concrete recovery action}

caused by: {host or process outcome}
```

Use one diagnostic headline in both human and plain output. `subject` is the
failed target when known; otherwise omit the bracketed subject. Do not add a
separate target/code headline. Lifecycle outcome rows retain target, timing and
status only; they do not repeat the diagnostic code, message or cause metadata.
Every error must still show its specific message and all available context
needed to understand its reason.

Source excerpts are shown for the primary location, located frames, and located
related notes whenever the corresponding authored text is available. Primary and
related blocks have numbered source-line gutters; compact trace excerpts use an
unnumbered gutter because their frame rows already identify the line. Trace
excerpts are indented beneath the frame label, not its tree branch. Gutters
visually separate source text from diagnostic prose. Locations use contiguous `source:line:column` text
for editor and terminal recognition. Coordinates are one-based lines and display
columns. Tabs advance to the next eight-column boundary. The marker covers the
visible part of the primary span on its first line, is at least one column wide
for an empty span, and never extends past that line's visible text (except a
point marker at EOF). Human span markers use box-drawing brackets (`╰──╯`) or
a single-column tick (`╵`); plain output retains `^~~~`. Trace excerpts do not
repeat span markers. Include one preceding or following line only when it
makes the diagnosis clearer; do not add empty context merely to fill the
template. Keep excerpts on one visual line: clip long lines around the relevant
span, indicating omitted prefixes/suffixes with ellipses, rather than wrapping
short suffixes onto separate rows. Clipping respects display cells and UTF-8
boundaries and keeps the focus visible. Locations remain authored coordinates,
not viewport columns. The renderer accepts an explicit terminal width and uses
a stable 80-column fallback; clipping is deterministic for a given width. Plain
output uses a fixed 80-column viewport; JSON is never clipped.

Use the authored Kame location, not a generated shell-script location.
For a diagnostic without a source location, render the severity, code, and
message without inventing a `<command>:1:1` location.
Prefer retained authored text over rereading a file when it is available. Source
text is presentation input, not an additional field in retained diagnostics or
JSON. If text cannot be obtained, retain the known source identity and any
resolvable coordinates without an excerpt; do not drop the frame or fabricate
line and column values. Rendering must not evaluate expressions or expand values
to obtain context.

`notes` add facts or qualified suggestions; they do not repeat the primary
message. `tips` state a concrete action and use `help:`. A target stack appears
only for an actual nested target dependency.

### Evaluation backtrace

When frames are present, human and plain output use a compact, visibly labelled
`Trace` section stating `innermost first`. Number displayed frames
consecutively from `#0`, retaining propagation order from the immediate context
outward. Each entry uses one row for its kind, label, and available authored
location. Show one source excerpt below a row only if that authored source line
has not appeared in the primary block or an earlier frame; do not repeat source
blocks or markers for overlapping operation/definition/call/condition frames.
Unlocated frames still show their
kind and label. Do not sort frames by source location or conflate definition
forcing, function calls, include sites, and target dependencies: this is an
evaluation trace, not a native implementation stack.

Show all distinct frames by default; source-line sharing does not merge frames
or discard their individual kinds, labels, or coordinates. Do not silently truncate the trace or
require a verbose/debug option to see known locations and source lines. A frame
may be represented in the primary source block rather than repeated in the
numbered list only when that block explicitly identifies the same kind, label,
source, and span. Sharing a location alone does not make two frames equivalent.
This presentation-only omission must not remove frames from structured JSON.

Use indentation and compact rows, without blank lines between frames, so the
failure and its callers remain easy to scan without colour. Plain output uses the same information
hierarchy and source excerpts with ASCII gutters and markers; rich human output
uses Unicode rails and tree branches. With colour enabled, severity and primary
markers use severity colours, locations are cyan, operation/call labels yellow,
definition labels purple, and structural context muted. Notes and recovery tips
follow the backtrace; underlying cause metadata follows Kame's explanation.
Do not add evaluated arguments, local values, environment contents, or captured
process output to explain frames.

The `cause` block follows Kame's explanation. It distinguishes the
interpreted diagnosis from the process outcome:

```text
Outcome     build failed
Diagnosis   recipe uses an unavailable executable
Cause       sh exited with status 127
```

Process exit status and signal belong in structured cause fields, not duplicated
in the primary message or lifecycle rows. Render them once in the cause block.
When a process cause has no message, use `process exited` before its metadata;
other message-less causes use their kind. Never render an empty `caused by:`.

Captured stdout and stderr are never retained or rendered in a diagnostic cause,
even when they were not streamed. The cause may state that output was streamed
and includes available capture truncation flags and byte limits. Explicit live
streams and collected shell-operation values remain unchanged.

### Certainty and Writing

State facts only when Kame established them through parsing, evaluation,
or a documented host result. Use qualified language for an inference, for
example `this appears to be an incomplete if block`, rather than claiming that
the user made a particular mistake.

Messages lead with the relevant target, operation, identifier, or syntax. They
avoid blame, vague wording such as `invalid usage`, duplicated metadata, and
unbounded command or process output. Quote source identifiers and syntax with
backticks when helpful. Formatting is deterministic so diagnostics are useful
in tests, CI logs, bug reports, and editor integrations.

### Terminal Presentation

The default human form is rich only when stderr is a terminal. It may use bold
red for errors, yellow for warnings, cyan for locations, and muted styling for
context and process output. Plain form contains the same information and no
terminal control sequences.

The global presentation controls are:

```text
--color auto|always|never
--diagnostic-format human|plain
```

`auto` honours `NO_COLOR`, `CLICOLOR=0`, `CLICOLOR_FORCE`, and `TERM=dumb`.
An explicit command-line choice takes precedence over environment variables.
`plain` and redirected output never emit ANSI escapes. These controls affect
presentation only; they never change execution, diagnostic data, or JSON.

## JSON Representation

`--json` emits diagnostic data as JSON Lines on stdout and leaves stderr unused
after argument parsing. It is the machine-readable form of the same diagnostic,
not a reduced summary. Existing schema-1 fields remain stable:

```json
{
  "schema": 1,
  "type": "diagnostic",
  "diagnostic": {
    "code": "RECIPE_FAIL",
    "severity": "error",
    "message": "recipe exited unsuccessfully",
    "source": "Makefile.kmk",
    "span": { "start": 84, "end": 101 },
    "notes": ["output was not produced"],
    "related": [{ "message": "declared here", "source": "Makefile.kmk", "span": { "start": 42, "end": 47 } }],
    "frames": [{ "kind": "rule", "label": "build", "source": "Makefile.kmk", "span": { "start": 42, "end": 101 } }],
    "target": "build",
    "targetStack": ["all", "build"],
    "tips": ["check that the compiler is installed"],
    "cause": { "kind": "process", "message": "process exited", "program": "sh", "status": 127, "stderrTruncated": false, "stderrLimit": 65536 }
  }
}
```

`source` and `span` remain offsets so tools can resolve them against the source
they supplied or received. `notes` remains an array of strings; `related` is
the separate array for located messages. New optional context fields are
additive within schema 1, and consumers must ignore unknown fields. Captured
`cause.stdout` and `cause.stderr` are no longer emitted: omission is the secrecy
policy, rather than heuristic filtering of arbitrary process output. JSON
preserves deterministic capture truncation flags and limits. JSON must redact secrets,
contain no ANSI escapes, and use no terminal-width-dependent wrapping.

## Rendering Invariants

- Human, plain, and JSON representations preserve the same code, severity,
  message, primary source identity/span, and available context.
- Human diagnostics place Kame's explanation before an underlying process
  cause metadata.
- A renderer omits unavailable information; it never fabricates a source
  location, target path, command, or recovery action.
- A renderer must tolerate diagnostics with no optional fields and must be able
  to render `NO_MEMORY` using static emergency storage.
- ANSI styling and terminal width affect only rich human presentation, never
  meaning, plain output, or JSON.
- Warnings may report useful context but do not make an otherwise successful
  command fail. A fatal diagnostic stops additional work as safely as possible
  and still receives the emergency renderer.

## Acceptance Tests

- Every normative failure named in `docs/spec` references a registered code.
- Language-boundary failures retain their codes and authored spans while naming
  the actual kind/context and exposing the recovery above consistently in human,
  plain and JSON output. Coverage includes callable rendering, noncallable calls,
  reference bodies, scalar word destinations, literal selector collisions,
  explicit path prefixes, environment grants and misplaced rule metadata.
- Tips never invoke a value, reinterpret syntax, grant capabilities, modify
  source, infer command-specific template semantics or expose evaluated secrets.
- Duplicate-definition diagnostics name the binding, locate the conflicting and
  original declarations, and retain include-site provenance across repeated or
  nested includes. Valid repeated contributions and skipped defaults remain
  valid; recovery tips do not change composition semantics.
- Every code matches `^[A-Z]+(?:_[A-Z]+)*$` and appears once in this registry.
- Wrapping a diagnostic preserves code, severity, and original source span.
- Finalization preserves primary body diagnostics and appends secondary cleanup
  codes/messages and available locations without changing protected cancellation
  or capability outcomes; cleanup-only failure and budget exhaustion remain
  distinguishable in human, plain and JSON output.
- A source diagnostic reports one-based line and tab-aware display column,
  renders a non-empty span marker, supports combining and wide Unicode text,
  and does not mark a following line.
- A propagated diagnostic renders its primary location, related location or
  frame context, and target stack without duplicating equivalent frames.
- A source-selection condition calling a helper that forces lazy definitions
  and fails in an operation reports that operation's authored primary location,
  definition declarations, call sites, and outer condition context. Cover
  definitions from included files and earlier inputs, and both native and
  JavaScript/WASM composition paths. Purity failures retain their codes and
  explain why runtime grants do not authorize conditions.
- Extracted declaration and predicate origins resolve independently; no synthetic
  offset is presented as an authored location. Queries without origins retain
  unqualified frame labels rather than misleading coordinates.
- Human and plain snapshots show compact, explicitly ordered, numbered frame rows
  with inline locations, one source excerpt per distinct authored line, and a
  primary span marker. Missing or
  unreadable source does not suppress frames. Multiline spans mark only their
  first line; empty spans, tabs, combining and wide Unicode, and narrow terminals
  remain readable and deterministic. Long excerpts are clipped around the focus,
  not wrapped; ellipses and marker alignment respect display-cell widths.
- Human output uses Unicode structure and semantic colours for locations, labels,
  markers and headings when enabled. Disabling colour retains the same Unicode
  layout; explicit plain output has uncoloured ASCII structure. JSON retains complete frames
  and spans independently of clipping or shared excerpts.
- Distinct frame kinds or labels at the same source location remain visible.
  Representing the immediate frame in the primary block does not lose its
  identity or alter the complete structured JSON frame list.
- A command failure renders Kame's diagnosis before its process cause and
  does not repeat output that was already streamed.
- A failed target has one diagnostic headline, with its target inline. Lifecycle
  rows do not repeat diagnostic details. Command-substitution failures render
  status/signal metadata once in a nonempty cause block in human and plain output;
  JSON retains the structured cause fields.
- Human, plain, and JSON formatting represent the same code, message, source
  span, notes, frames, target context, tips, and bounded cause metadata.
- Plain and redirected output contain no ANSI escape sequences; explicit colour
  selection and the documented colour environment variables are deterministic.
- Given the same width, diagnostic excerpts clip identically; plain and JSON output are
  invariant across terminal widths.
- Schema-1 diagnostic additions are optional, additive, and preserve the
  string-only `notes` representation. Causes expose capture truncation metadata
  but omit captured stdout/stderr. Live stream events remain unchanged.
- Static `NO_MEMORY` formatting succeeds with a failing allocator.
