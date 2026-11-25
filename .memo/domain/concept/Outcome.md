---
identity: 82969456-f89d-4e46-a1ea-f28fe37571d0
created: 2026-11-25T18:43:57.690602+00:00
updated: 2026-11-25T18:43:58.123724+00:00
---
# Outcome

An outcome is the record that describes how an invocation ended, for a
function call or a process graph alike. Functions and processes share one
invocation model: a call yields a value, and every invocation also has an
inspectable outcome (`055` §1, principle 8).

| Field | Meaning |
| --- | --- |
| `state` | `:completed`, `:failed` or `:cancelled` |
| `ok` | `true` only when completed with status 0 and no signal |
| `status`, `signal` | Exit status (derived for functions) and terminating signal |
| `value` | A function's return value, or the stdout captured by `$?(…)` |
| `error` | The failure with its source location, or `nil` |
| `stages` | Per-stage process outcomes (`049`) |

The record also has `stdoutCaptured`, `stderrCaptured`, `effectiveStatus`,
`effectiveSignal` and `graph`.

## Reading outcomes

- `$(CMD)` returns captured stdout and raises on failure.
- `$?(CMD)` returns the outcome and does not raise for process exits, launch
  failures, timeouts or function failures. It still raises for capability
  denial, phase or parse errors, and cancellation of the enclosing
  invocation.
- `$&(CMD)` and `CMD &` return an owned handle. `wait` yields its outcome.

## Boundaries

- An accepted exit is not success. `CMD ?` gives `state: :completed`,
  `ok: false` and `effectiveStatus` 0. To treat accepted exits as success,
  test `.state == :completed`.
- `??` does not catch process failures. It only covers `nil` and
  missing-lookup failures. Recover in the command list (`a || b`) or by
  inspecting the outcome.
- Functions have no integer status. When one is needed, it is derived:
  completed → 0; failed → the failing process's status, or 1; cancelled →
  the consumer stopped pulling, which is not a failure of the graph.
- A return value never becomes stdout. `$(f x)` captures what `f` writes, not
  what it returns.

## Open questions

`055` §7.3 and §8.1 put the stdout captured by `$?(…)` into `value`, and so
does the implementation (`captureOutcome` in
`lang/eval/process_expression.go`). Three other specs still say `$?(…)` keeps
no output: `049` §Invocation outcomes (`value` is `nil` for graphs), `042`
§8.3 and `017`. `055` governs.

Related: [domain/concept:ProcessGraph] [domain/concept:Kash]
[domain/architecture:kash_execution]

Sources: `docs/spec/055-unified-kame-kash-language.md` §7.3, §8;
`docs/spec/049-process-inspection.md`;
`docs/spec/047-kash-output-ports-and-command-lists.md`.
