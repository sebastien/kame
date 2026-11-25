---
identity: c2c95b9f-e3b2-4963-964d-30ee152e1002
created: 2026-11-25T18:43:40.509766+00:00
updated: 2026-11-25T18:43:41.442853+00:00
---
# Kash execution

This entry traces how a [domain/concept:Kash] statement becomes processes and
an [domain/concept:Outcome], across the components involved.

## Flow

1. **Parse** ([domain/component:Language]). `lang/script` splits the block
   into statements, and `lang/expr` parses command words, operators, stage
   setup and graph topology. The whole body or fragment set is parsed before
   anything runs.
2. **Evaluate words** ([domain/component:Evaluator]). Fragments are
   evaluated left to right as typed values in the scope of the
   [domain/concept:ProcessUnit], then turned into argv following
   [domain/concept:Word] rules (spreads, authored wildcards, `~`).
3. **Resolve heads.** Keyword, then lexical callable, then builtin, then an
   executable found through the unit's `PATH`. Each reached executable is
   recorded as a tracked tool ([domain/concept:ToolReference]).
4. **Prepare the graph** ([domain/concept:ProcessGraph]). Topology and cycles
   are checked from syntax. Tools, grants ([domain/concept:Capability]),
   arguments and redirections are validated before any launch or truncation.
5. **Submit.** A process [domain/concept:HostRequest] carries stages,
   redirections and topology (`host/payload.go`).
   - `host/posix` spawns each executable stage in its own process group.
     Under WASM, JavaScript uses `node:child_process`.
   - Function stages run in the evaluator.
   - Native stream operators belong to the host topology
     (`host/operators.go`).
6. **Stream.** Channels are bounded and apply backpressure. Stderr is
   forwarded live. Stdout goes to the next stage, a file, a port or a capture.
7. **Complete.** All stages are joined, or cancelled and reaped, producing one
   outcome with per-stage records.
8. **Decide.**
   - At statement level, the outcome drives failure propagation (`?`,
     `? ELSE`, `&&`, `||`).
   - `$(…)` returns stdout text.
   - `$?(…)` returns the outcome record.
   - `$&(…)` and `&` return an owned handle.
   - `defer` cleanup runs when the invocation ends.

## Where it runs

| | Rule body | Direct script | Interactive session | `$(…)`, `$?(…)` |
| --- | --- | --- | --- | --- |
| Unit that `exit` ends | One attempt | The whole session | The session | The capture |
| Extra builtins | none | `source` | `source`, `jobs`, `fg`, `bg`, `alias`, `unalias`, `history`, `option`, `explain`, `complete` | none |
| cwd and environment persist | Per attempt | Across fragments | Across entries | Until the capture ends |
| Terminal | Only with `tty`/`foreground` metadata | Inherits fds when output is unobserved | Lent to foreground graphs | Never |
| Authority | Build grants plus `authority` metadata | Kash-first default policy | Default grants follow `cd` | Inherited, never broadened |

## Invariants

- Nothing is converted to shell text and reparsed
  ([domain/antipattern:textual_reexpansion]).
- All async work is owned, then joined or reaped. No implicit daemons remain.
- Buffering is bounded everywhere. Captures default to 1 MiB.
- Exit fidelity: an unhandled status survives nested runners, and signals
  report 128+n.
- Capability denial and cancellation can never be recovered or suppressed.

## Code

- `lang/expr/kash*.go`, `lang/script/kash*.go`
- `lang/eval/kash_*.go`, `lang/eval/process*.go`, `lang/eval/stream_graph.go`,
  `lang/eval/defer.go`
- `host/process*.go`, `host/payload.go`
- `host/posix/posix.go`, `host/posix/topology.go`
- `program/recipe_settings.go`, `program/host_events.go`

Related: [domain/decision:kash_typed_actions] [domain/architecture:kame_system]

Sources: `docs/spec/017-kash.md`, `docs/spec/042-kash-streaming.md`,
`docs/spec/047-kash-output-ports-and-command-lists.md`,
`docs/spec/048-kash-practical-scripting.md`,
`docs/spec/049-process-inspection.md`,
`docs/spec/051-kash-session-authority.md`,
`docs/spec/052-kash-interactive.md`, `docs/spec/003-posix-process.md`.
