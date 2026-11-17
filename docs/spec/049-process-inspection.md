---
revision: 2
---

# Typed process capture, invocation outcomes and optional executable lookup

## Scope

This specification defines typed capture for single typed-argv commands, the
invocation outcome record read with `$?(…)`, and optional executable lookup.
It adds no shell evaluation, implicit acceptance, graph capture, asynchronous
capture, retries, terminal handoff, or additional authority. Spec 048 governs
selected ordinary exit acceptance and invocation input ownership. The language
surface follows `055-unified-kame-kash-language.md`.

## Typed capture

```kame
report = (capture! {stdout: true, stderr: true, stdoutLimit: 65536, stderrLimit: 4096}
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
`stdout` and `stderr`: exact bytes for selected streams, `nil` otherwise. Selected
empty output is empty bytes, not `nil`. `stdoutCaptured` and `stderrCaptured`
agree with those fields. Selected streams are never also published; unselected
streams keep ordinary streaming behavior. Decode explicitly with `text`; invalid
UTF-8, NUL and trailing newlines are not modified during capture.

Default input is closed, including while evaluating reached capture operands.
There is one launch, concurrent stream draining, and no result until process and
stream completion. Each bound is independent; overflow reports `CAPTURE_LIMIT`,
cancels and reaps owned work, and never returns successful truncated output.
`effectiveStatus`/`effectiveSignal` retain failure provenance independently of the
raw aggregate. Effective failure follows spec 048: unaccepted exits propagate,
accepted ordinary exits retain raw status, and signals, launch errors, deadlines
and cancellation remain failures. Captured bytes do not enter ordinary
diagnostics or logs. Suspension must not relaunch, replay effects or duplicate
bytes. Dry-run performs no capture and fabricates no output or successful Result.

## Invocation outcomes

Every invocation, of a function or of a process graph, has an outcome. The
outcome record extends the process `Result` of `017` with the invocation fields
of `055` §8.1:

| Field | Meaning |
| --- | --- |
| `state` | `:completed`, `:failed`, `:cancelled` or `:suspended` |
| `ok` | `true` exactly when `state` is `:completed`, the status is 0 and `signal` is 0 |
| `status` | Aggregate exit status; derived for functions |
| `signal` | Terminating signal or 0 |
| `value` | Function return value, or `nil` for a process graph |
| `error` | The failure, with its authored source location, or `nil` |
| `stages` | Per-stage outcomes in prepared stage/node order |

The remaining Result fields keep their meaning: `stdoutCaptured`,
`stderrCaptured`, `effectiveStatus`, `effectiveSignal`, the `stdout`/`stderr`
fields of typed capture, and the terminal `graph` metadata of `042` §9.2.
Each element of `stages` exposes `status`, `signal` and `outcome`, and a stage
that is a function invocation also exposes `state`, `value` and `error`.

```kash
let probe = $?(git diff --quiet)
if not probe.ok
	printf "tree is dirty (status %s)\n" $probe.status >&err

let check = $?(verify-config ./config.json)
if check.state == :failed
	printf "config check failed with status %s\n" $check.status >&err
```

- **Reading.** `$?(…)` evaluates its action-context content (one process graph
  or command list) and returns the outcome record. It never raises for a process
  failure or a function failure: an unaccepted nonzero exit, a signal, a launch
  failure, a timeout or a failure raised inside a called function is reported
  with `state: :failed` and a non-`nil` `error`. Capability denial, phase
  violations, parse errors and cancellation of the enclosing invocation are not
  invocation failures; they propagate unchanged. `wait` returns the same record
  for an async handle.
- **Function status.** Functions return values, not statuses. A function's
  status is derived: `:completed` is 0; `:failed` is the failing process's status
  when a process failure caused it, otherwise 1. A function invocation's
  `stages` list is empty unless the invocation is itself a process graph whose
  stages include it.
- **Cancellation.** A stage stopped because its consumer closed early, including
  a generator whose consumer stopped pulling, has `state: :cancelled`. A
  cancelled function stage is treated as a broken pipe and does not fail its
  graph; it runs its `defer` cleanup. Process stages keep the signal and failure
  policy of `042` §8.2.
- **Acceptance.** An exit accepted by terminal `?` or `:accept-exit` (spec 048)
  has `state: :completed` and keeps its raw `status`, so `ok` is `false`;
  `effectiveStatus` is 0. Acceptance never changes `state` for signals, launch
  failures, deadlines or cancellation.
- **Suspension.** `:suspended` is observable only on a generator's own
  invocation while it waits for its consumer; a completed `$?(…)` never yields
  it.
- **Retention.** The outcome retains no stream payload beyond explicit typed
  capture. Inspecting an outcome is a pure field read and never relaunches work.

## Optional lookup

`tool NAME ?? nil` is the fallback spelling of optional executable lookup; no
separate optional lookup operation is introduced. It uses the ordinary
executable value, startup PATH, overrides and tracked resources of `tool NAME`.
NAME must be a nonempty string without NUL; invalid names report `EXPR_INVALID`
even under `??`. The `??` operator handles `TOOL_MISSING` in addition to
`REF_MISSING`, `ENV_VAR_MISSING` and missing fields, and evaluates its right
operand only for those failures or a `nil` left operand. Successful `false`
values are returned unchanged. Protected failures, authority denial and
operational errors remain failures. Bare `tool NAME` still fails when
unavailable. Lookup never launches a probe. Stage child PATH does not change
this resolver. A later launch can still fail after resolution.

Absence is a tracked `nil` value. Revalidation refreshes the resolver observation;
nil-to-executable, executable-to-nil and changed paths invalidate dependents.
An unchanged `nil` does not itself publish a changed value. Resolved executable
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
lookup absence, overrides and revalidation. Outcome checks cover identical
fields for function and process invocations, derived function statuses inside
pipelines, `$?(…)` not raising for process or function failure while capability
denial and cancellation still propagate, accepted exits with `ok` false and
`effectiveStatus` 0, and cancelled generator stages that run cleanup without
failing their graph. Unsupported hosts must reject before affected execution,
not substitute shell source, temporary files or lossy text.
