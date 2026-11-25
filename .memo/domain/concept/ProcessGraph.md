---
identity: 50b46d78-1f2e-41ea-8cfa-b8bd3ce0362a
created: 2026-11-25T18:43:59.967087+00:00
updated: 2026-11-25T18:44:00.401065+00:00
---
# Process graph

A process graph is a set of stages that run concurrently, connected by bounded
channels. A stage is an executable, a function call or a native stream
operator. A command statement lowers to one graph. The whole graph is built
and validated (tools, grants, arguments, redirections) before any stage starts
or any file is truncated, and connected stages start together.

## Parts

- **Channels.** A channel is a bounded, closeable byte or framed connection
  with exactly one writer and one reader.
  - Fan-in and fan-out happen only through `|(merge)`, `|(broadcast)` and
    `|(balance)`.
  - Other operators are `|(filter P | B)`, `|(frame :lines)`, `|(each) CMD`
    and `|(tee PATH)`.
- **Framing.** Records keep their exact LF or CRLF bytes and are at most
  64 KiB. Errors replace truncation.
- **Named channels.** `stream NAME` declares a lexical endpoint, used with
  `>&NAME` and `<&NAME`. The names `in`, `out` and `err` are reserved.
- **Presentation sinks.** `>&out` and `>&err` route stdout to the current
  output or error boundary. They are not channels, and each stdout port has
  exactly one destination.

## Limits

- Host reads are at most 16 KiB, with at most 256 KiB queued per process.
- Operator buffers are 16 KiB or 64 KiB. Captures default to 1 MiB.
- Backpressure blocks producers. Data is never dropped unless a framed
  broadcast opts in.

## Failure and ownership

- **Fail fast.** A nonzero exit or signal (including SIGPIPE), a launch
  failure, a timeout or cancellation closes and reaps everything.
- **Status.**
  - A linear pipeline reports its rightmost failing stage.
  - A graph reports its last failing prepared node.
  - A signal reports 128+n.
- **Ownership.** Async graphs (`&`, `$&(…)`), workers and relays belong to
  the invocation. They are joined on success, or cancelled and reaped on
  failure: "No implicit daemons remain."
- **Process groups.** Each stage gets its own OS process group. Cancellation
  sends TERM, waits a grace period, sends KILL, then reaps. Descendants that
  start a new session escape, which is outside the POSIX contract.

## Boundaries

- A channel is not an engine dependency edge. Build dependencies discovered at
  run time remain engine edges ([domain/concept:Node]).
- `|` carries bytes. `|>` transforms values.
- A function stage's stdout bytes and its yielded values are separate
  channels.
- The group syntax `(A, B)` is neither a Kame collection nor an OS process
  group.

Related: [domain/concept:Outcome] [domain/architecture:kash_execution]
[domain/concept:HostRequest]

Sources: `docs/spec/042-kash-streaming.md`, `docs/spec/017-kash.md`
§Pipelines and redirection, `docs/spec/047-kash-output-ports-and-command-lists.md`,
`docs/spec/003-posix-process.md`, `docs/glossary.md` (Process graph,
channel, and framing).
