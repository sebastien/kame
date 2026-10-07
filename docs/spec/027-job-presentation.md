# Job and CLI presentation contract

A job is a requested target; a
process is one host execution performed while a job is active. Presentation is
observational and must not alter scheduling, cancellation, or process policy.

## Human progress

Human mode reports target start, completion, failure, and cancellation on
stderr. Cancellation has its own wording and count, never appears as failure,
and keeps the invocation's nonzero cancellation status. Summary counts separate
completed, failed, and cancelled targets. Concurrent jobs retain target names
so interleaved lines remain attributable.

For each process, human mode displays its target, executable, bounded argv, and
elapsed milliseconds. It omits environment values and process input. At most
eight arguments and 160 UTF-8 bytes are shown; truncation is explicit. Pipelines
show at most four stages with the same per-stage bounds. Process streams remain
on their existing output streams.

## Change explanations

Human mode reports reasons for rejecting or bypassing reuse on stderr by default.
Every line names the target and distinguishes reevaluation from execution, for
example:

```text
[./page.html] reevaluate: input content changed: ./data.json
[./site.js] execute: output missing: ./site.js
[./output] reevaluate: process environment changed
[./page.html] reevaluate: source/context guard changed
```

The runtime supplies the decision and evidence under `006-runtime.md`; the
presenter does not infer changes from target visits, process starts, or elapsed
time. An execution decision is not proof that a process started or succeeded.

The stable reason codes are:

| Code | Meaning |
| --- | --- |
| `record-missing` | No saved reuse proof is available. |
| `record-invalid` | A saved proof is corrupt or incompatible. |
| `proof-unverifiable` | Existing observations cannot establish reuse. |
| `input-changed` | A consumed input signature differs. |
| `membership-changed` | The consumed dependency selection differs. |
| `output-missing` | A declared output is absent. |
| `output-changed` | A declared output signature differs. |
| `implementation-changed` | Recipe implementation identity differs. |
| `guard-changed` | A source/context guard differs; reevaluation is required. |
| `settings-changed` | Resolved execution settings differ. |
| `environment-changed` | A recorded named environment observation differs. |
| `process-environment-changed` | The consumed whole-child-environment digest differs. |
| `forced` | Invocation policy explicitly bypasses reuse. |
| `always` | Rule policy requires execution. |
| `uncached-task` | The task has no reuse policy. |

Messages may identify resource paths and observed variable names, but must not
include environment values, source contents, captured stdout/stderr, process
input, or old/new fingerprints. A whole-environment digest only supports an
aggregate explanation, not a list of guessed variable changes. Existing explicit
process streams remain unaffected.

The same decision/reason/resource is presented once per target generation;
resumed attempts must not repeat it. Distinct established reasons can be reported,
but reporting must not force exhaustive validation or add host work. Successful
unchanged reuse produces no reason line. Shared jobs remain attributable to their
own target, and reasons do not increment lifecycle or error counts.

## JSON events

JSON mode writes only schema-versioned event objects to stdout and leaves stderr
empty. It carries the same target and process lifecycle, including cancellation
and process runtime, without human progress lines or ANSI styling. Existing
event field names and meanings remain stable; new process display fields are
optional and bounded. Environment values are never included.

## Color and lessons

`--color auto|always|never` controls human diagnostics and progress consistently
with 011, including `NO_COLOR`, `CLICOLOR_FORCE`, and non-terminal output. JSON
and plain diagnostic output contain no ANSI escapes. Help and documentation
examples that advertise executable commands are exercised by acceptance tests
from a clean temporary project.

## Acceptance

- Missing/invalid proofs, consumed changes, output changes, and explicit policy
  bypasses produce attributable human and JSON reasons on native and WASM.
- Guard misses followed by unchanged results are described as reevaluation, not
  falsely as rebuilds. A no-op invocation emits no change reasons.
- Suspended retries deduplicate reasons; subsequent generations report new
  decisions independently.
- Human and JSON explanations omit environment values, source contents,
  captured process data, and old/new fingerprints, including for aggregate
  environment changes.

Tests cover concurrent target identity, separate cancellation reporting and
summary counts, bounded direct-argv and pipeline display, elapsed runtime, JSON
stream separation and schema stability, color environment behavior, and the
documented lessons. Native and WASM CLI output is compared where both expose the
same host event data; host-specific timing values are checked for type and
non-negativity rather than exact equality.
