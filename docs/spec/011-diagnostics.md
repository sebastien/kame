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
| `related` | An ordered list of related messages, each with `message`, `source`, and `span`. | source-qualified `note:` lines |
| `frames` | Evaluation, definition, rule, or script boundaries crossed while propagating the failure. Each frame has a kind, label, and optional span. | `while …` or `called from …` context |
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
| `DEF_INVALID` | error | Invalid definition or binding form |
| `DEF_ESCAPE` | error | function value escapes its scope; define it with (def name [params] body) instead |
| `PAT_INVALID` | error | Invalid pattern combination or missing pattern capture reference |
| `TPL_PARSE` | error | Malformed template directive, argument list, or verbatim literal |
| `TPL_BLOCK` | error | Unterminated, stray, or mismatched template block directive |
| `TPL_STYLE` | error | Missing or unknown template comment style |
| `TPL_CYCLE` | error | Recursive template inclusion |
| `CAP_DENIED` | error | Capability denied |
| `PHASE_INVALID` | error | Operation is not valid in the current phase |
| `TGT_NO_DEFAULT` | error | No target or value entry was requested and the required default is absent |
| `TGT_NO_RULE` | error | No rule or source for target |
| `TGT_AMBIG` | error | More than one rule instance matches |
| `ENV_CONFLICT` | error | Active shared rule instance has a different recipe environment |
| `DEP_CYCLE` | error | Dependency cycle |
| `RECIPE_FAIL` | error | Recipe process exited unsuccessfully |
| `RECIPE_TIMEOUT` | error | Recipe process timed out |
| `EXEC_CANCELLED` | error | Execution was cancelled |
| `HOST_FAIL` | error | Host request, spawn, pipe, wait, or signal failure |
| `FS_ERR` | error | Filesystem read, write, stat, or glob failure |
| `OUTPUT_MISSING` | error | Successful recipe omitted a declared output |
| `OUTPUT_CONFLICT` | error | Declarative yield conflicts with a shell command |
| `CACHE_UNUSABLE` | warning | Cache record or manifest cannot be used |
| `FEATURE_UNSUP` | error | Valid but unsupported feature on this target |
| `NO_MEMORY` | fatal | Allocator or fixed arena exhausted |
| `HANDLE_INVALID` | error | Invalid, stale, or foreign ABI handle |
| `CMD_UNKNOWN` | error | Unknown CLI command |
| `OPT_UNKNOWN` | error | Unknown CLI option |
| `OPT_NO_VALUE` | error | Missing CLI option value |
| `OPT_CONFLICT` | error | Conflicting CLI options |
| `OPT_VALUE_INVALID` | error | Invalid CLI option value |
| `BUILD_NO_SOURCE` | error | No build source was found |
| `NO_ARTIFACT` | error | Requested target has no readable artifact |

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
- Unsupported service execution uses `FEATURE_UNSUP`.
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

## Human and Plain Rendering

Human diagnostics are written to stderr. They use this information hierarchy:

```text
✗ {subject} failed · {code}
  required by {root} → … → {subject}

{source}:{line}:{column}: {severity} {code}: {message}
 {previous-line} │ {source text}
 {error-line}    │ {source text}
                 │ {caret marker and, when useful, a short diagnosis}
 {next-line}     │ {source text}

note: {additional context}
note: {related-source}:{line}:{column}: {related context}
help: {concrete recovery action}

caused by: {host or process outcome}
```

The headline is shown for a target or command failure. `subject` is the failed
target when known and `command` otherwise. A warning uses a warning headline;
a standalone parser or evaluator diagnostic may omit the headline and begin at
the source location. Every error must still show its specific message and all
available context needed to understand its reason.

The source block is shown only when the primary span refers to available
authored source. It uses one-based line and display-column coordinates. Tabs
advance to the next eight-column boundary. The marker covers the failing span
on its first line, is at least one column wide for an empty span, and never
extends past that line. Include one preceding or following line only when it
makes the diagnosis clearer; do not add empty context merely to fill the
template. The renderer accepts an explicit terminal width and uses a documented
stable fallback when no width is available; wrapping is deterministic for a
given width and never changes plain or JSON output.

Use the authored Kame location, not a generated shell-script location.
For a diagnostic without a source location, render the severity, code, and
message without inventing a `<command>:1:1` location.

`notes` add facts or qualified suggestions; they do not repeat the primary
message. `tips` state a concrete action and use `help:`. A target stack appears
only for an actual nested target dependency. Frames appear from the immediate
context outward, using wording such as `while evaluating definition "SOURCES"`
or `called from Makefile.kmk:3:1: rule "build"`.

The `cause` block follows Kame's explanation. It distinguishes the
interpreted diagnosis from the process outcome:

```text
Outcome     build failed
Diagnosis   recipe uses an unavailable executable
Cause       sh exited with status 127
```

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
    "cause": { "kind": "process", "message": "sh exited with status 127", "program": "sh", "status": 127, "stderrTruncated": false, "stderrLimit": 65536 }
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
- Every code matches `^[A-Z]+(?:_[A-Z]+)*$` and appears once in this registry.
- Wrapping a diagnostic preserves code, severity, and original source span.
- A source diagnostic reports one-based line and tab-aware display column,
  renders a non-empty span marker, supports combining and wide Unicode text,
  and does not mark a following line.
- A propagated diagnostic renders its primary location, related location or
  frame context, and target stack without duplicating equivalent frames.
- A command failure renders Kame's diagnosis before its process cause and
  does not repeat output that was already streamed.
- Human, plain, and JSON formatting represent the same code, message, source
  span, notes, frames, target context, tips, and bounded cause metadata.
- Plain and redirected output contain no ANSI escape sequences; explicit colour
  selection and the documented colour environment variables are deterministic.
- Given the same width, diagnostics wrap identically; plain and JSON output are
  invariant across terminal widths.
- Schema-1 diagnostic additions are optional, additive, and preserve the
  string-only `notes` representation. Causes expose capture truncation metadata
  but omit captured stdout/stderr. Live stream events remain unchanged.
- Static `NO_MEMORY` formatting succeeds with a failing allocator.
