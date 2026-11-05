# Typed process capture and optional executable lookup

## Scope

This adopts the SDK process-inspection proposal for single typed-argv commands.
It adds no shell evaluation, implicit acceptance, graph capture, asynchronous
capture, retries, terminal handoff, or additional authority. Spec 048 governs
selected ordinary exit acceptance and invocation input ownership.

## Typed capture

```kame
(capture! [stdout: :true stderr: :true stdoutLimit: 65536 stderrLimit: 4096]
  :accept-exit [1] :cwd ./workspace "checker" "--report")
```

`capture! POLICY SETUP... EXECUTABLE ARGUMENTS...` is a process special form.
POLICY is one record with boolean `stdout` and `stderr` selections (omitted means
false) and optional positive integer `stdoutLimit` and `stderrLimit` byte bounds
up to 2147483647. At least one stream must be selected. Each omitted bound uses
the invocation capture limit independently. Unknown fields, invalid kinds,
conflicting stream destinations, and `:async` are rejected before launch.
The remaining setup and typed argv use the same rules as `run!`, including cwd,
environment, timeout and `:accept-exit`. `pipe!` accepts only `run!` stages.

The completed Result retains raw status, signal and stage metadata, and adds
`stdout` and `stderr`: exact bytes for selected streams, nil otherwise. Selected
empty output is empty bytes, not nil. `stdoutCaptured` and `stderrCaptured` agree
with those fields. Selected streams are never also published; unselected streams
keep ordinary streaming behavior. Decode explicitly with `text`; invalid UTF-8,
NUL and trailing newlines are not modified during capture.

Default input is closed, including while evaluating reached capture operands.
There is one launch, concurrent stream draining, and no result until process and
stream completion. Each bound is independent; overflow reports `CAPTURE_LIMIT`,
cancels and reaps owned work, and never returns successful truncated output.
`effectiveStatus`/`effectiveSignal` retain failure provenance independently of the
raw aggregate. Effective failure follows spec 048: unaccepted exits propagate, accepted ordinary
exits retain raw status, and signals, launch errors, deadlines and cancellation
remain failures. Captured bytes do not enter ordinary diagnostics or logs.
Suspension must not relaunch, replay effects or duplicate bytes. Dry-run performs
no capture and fabricates no output or successful Result.

## Optional lookup

`(tool NAME | ?)` is the value-pipe spelling of `(? (tool NAME))`; no separate
optional lookup operation is introduced. It uses the ordinary executable value,
startup PATH, overrides and tracked resources of `(tool NAME)`. NAME must be a
nonempty string without NUL; invalid names report `EXPR_INVALID` even under `?`.
The `?` special form handles `TOOL_MISSING` in addition to `REF_MISSING`, returning
the first operand without either failure, or nil when all operands are missing.
Successful nil and false values are returned unchanged, not skipped. Protected
failures, authority denial and operational errors remain failures. Bare
`(tool NAME)` still fails when unavailable. Lookup never launches a probe. Stage
child PATH does not change this resolver. A later launch can still fail after
resolution.

Absence is a tracked nil value. Revalidation refreshes the resolver observation;
nil-to-executable, executable-to-nil and changed paths invalidate dependents.
An unchanged nil does not itself publish a changed value. Resolved executable
content/metadata tracking and existing internal-read authority stay intact.

## Host contract and checks

Native and Node/WASM implement the same supported behavior. Configured single
process requests carry a normalized `capture` record alongside stage setup.
Native completion transports bytes directly. Node uses the additive
`kame_wasm_complete_capture` ABI to supply JSON metadata and two raw byte buffers;
payload bytes never expand into JSON value arrays or pass through text decoding.
Bounds apply to retained payload, not all transport or runtime allocations.

Conformance covers binary/empty output, simultaneous streams, independent bounds,
stream isolation, selected/unaccepted exits, signals, deadlines, cancellation,
closed input, once-only evaluation, invalid policy before effects, and optional
lookup absence, overrides and revalidation. Unsupported hosts must reject before
affected execution, not substitute shell source, temporary files or lossy text.
