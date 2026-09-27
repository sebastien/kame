# POSIX Process Host

## Purpose

The POSIX host runs complete recipe scripts while streaming output and managing
the entire process group. Solod's standard library does not provide child
process creation, waiting, signals, or process groups, so this package uses a
small C interop layer.

The first implementation targets hosted POSIX systems. Windows is deferred.

## Process Request

A process request contains:

- Shell executable and arguments.
- Complete script bytes.
- Working directory.
- Complete explicit environment.
- Optional timeout.
- Maximum retained bytes for each output stream.

Strings and arrays in a submitted request are owned by the request until the
host reports terminal completion.

The child receives exactly the request environment. The host does not implicitly
inherit its own environment. The CLI may copy its ambient environment into the
request; cached tasks then fingerprint that complete environment.

## Spawn Contract

The host must:

- Create separate stdout and stderr pipes.
- Put the child in a new process group before user commands run.
- Execute the configured shell with the script as one shell session.
- Close every unused pipe endpoint in parent and child.
- Report spawn failure without creating a running process record.
- Never invoke a shell through `system()`.

`posix_spawn` is preferred if it can establish the required process group and
file actions portably on supported systems. A `fork`/`exec` implementation must
restrict the child path to async-signal-safe operations.

## Events

The host emits ordered events containing the process request ID:

- Started with process ID and process-group ID.
- Stdout bytes.
- Stderr bytes.
- Terminal with outcome `exited`, `timed-out`, `cancelled`, or `failed`, plus
  optional normal status, terminating signal, and host diagnostic.

Bytes within each stream retain read order. Ordering between stdout and stderr
is whichever event the polling loop observes; no stronger ordering is claimed.

Output events contain owned bytes valid until the receiver frees or transfers
them. Empty reads are not emitted. Exactly one terminal event is emitted after
both pipes reach EOF and the direct child is reaped. Spawn failure emits only a
`failed` terminal event because no process or pipe exists.

If pipe cleanup or `waitpid` itself fails permanently, the host performs all
remaining best-effort closes and signals, then emits one `failed` terminal with
`HOST_FAIL`. This is the only case where successful reaping cannot be confirmed.

## Polling and Backpressure

The native event loop uses nonblocking descriptors and `poll` or an equivalent
POSIX primitive. It continues draining both pipes until EOF even after the child
has exited. It calls `waitpid` until the child is reaped.

The process host must not permanently block a graph worker while waiting for
pipe output. A dedicated process loop or thread may multiplex all active child
descriptors and send small completion messages to the engine.

Reads are emitted in chunks of at most 16 KiB. Each process has at most 256 KiB
of queued output events. When full, the loop temporarily omits that process's
read descriptors from polling, allowing pipe backpressure to block that child
without blocking other children. If the runtime event consumer disappears, the
runtime cancels the process. Retained logs are separately capped by bytes, not
Unicode characters; a live consumer still receives output beyond that cap.

## Cancellation and Timeout

Cancellation and timeout apply to the process group:

1. Send `SIGTERM` to the process group.
2. Continue draining output and polling `waitpid` during a fixed grace period.
3. Send `SIGKILL` to the process group if it remains alive.
4. Drain pipe EOF and reap the direct child before reporting one terminal event.

Cancellation is idempotent. A process that exits during cancellation reports
cancelled or timed out according to the initiating request, plus its observed
status when available.

CLI shutdown waits for this sequence. Kame guarantees termination of
descendants that remain in the process group it created. A descendant that
deliberately creates a new session escapes this guarantee; stronger containment
is outside the POSIX process-group contract.

## Retries

Retries belong to the runtime, not the low-level spawn primitive. Each attempt
creates a new process group and event sequence. Cancellation stops the active
attempt and prevents future attempts. A timeout may be retryable according to
the runtime request.

## Recipe Semantics

All command lines from one rule are joined with newlines and executed in one
shell. Consequently shell variables, `cd`, traps, and multiline control flow
persist across recipe lines.

The host does not parse shell syntax. Source-line mapping is runtime metadata
derived before execution.

## Signals

The CLI maps `SIGINT` and `SIGTERM` into cancellation of active root requests.
It does not forward arbitrary signals directly to child PIDs. Process-group
termination follows the cancellation contract.

Signal handlers perform only signal-safe notification. Cleanup happens in the
normal event loop.

## Acceptance Tests

- A script writes distinguishable data to stdout and stderr and both streams are
  delivered without corruption.
- Multiple recipe lines share shell variables and working directory changes.
- A non-zero exit reports the exact status and all output written before exit.
- A missing shell reports a spawn diagnostic and leaves no process record.
- Cancellation terminates a shell and a background grandchild in its process
  group.
- Timeout sends TERM, escalates to KILL when ignored, drains pipes, and reaps the
  child.
- Cancellation called twice is harmless.
- A child that closes one output stream early does not prevent delivery from the
  other.
- Output queue saturation pauses only the affected child and resumes without
  dropping bytes.
- Cancellation emits exactly one terminal event after explicit `waitpid`
  confirms the direct child is no longer waitable.
- A forced `waitpid` error produces one `failed` terminal with `HOST_FAIL` after
  best-effort descriptor cleanup.
- Output larger than the retained limit remains fully streamed while retained
  logs contain the byte prefix and a truncation flag.
- Repeated spawn and cancellation under sanitizer reports no descriptor, child,
  or memory leak.
