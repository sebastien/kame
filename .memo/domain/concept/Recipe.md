---
identity: 597a3e62-3007-4d05-8b05-ccb8459b71f0
created: 2026-11-25T18:44:01.435210+00:00
updated: 2026-11-25T18:44:02.001828+00:00
---
# Recipe

A recipe is the body of a [domain/concept:Rule]: the work that produces its
outputs. The body's form selects the producer, "never by inspecting body
text" (`044`).

| Body | Producer | Evaluation |
| --- | --- | --- |
| Indented block, no external `shell` | Kash action block (the default) | Action context, execution phase |
| `= EXPR` | Expression body: its String or Bytes value is published to exactly one file output | Value context, read-only |
| Indented block with `; {shell: "bash"}` or a build-wide `SHELL` | Foreign-shell template, rendered to one script and run as one process | Template context, rendered read-only |

## Rules

- **Kash bodies are parsed directly** and never template-rendered
  ([domain/decision:kash_typed_actions]). A final non-`nil` `return VALUE`
  publishes atomically to the single output.
- **Expression bodies and foreign-shell rendering are read-only.** `out!`,
  processes and clocks fail with `PHASE_INVALID`, and grants cannot override
  this ([domain/concept:Phase]).
- **Publication is atomic.** A failed computation leaves the existing artifact
  untouched, and dry-run never publishes. Kame creates the output's parent
  directories before running the recipe.
- **Bindings.** The body binds `inputs`, `outputs`, `changed-inputs`,
  `captures`, the named captures and target arguments, and closes over its
  lexical scope.
- **Lifetime.** Async work started by a body is joined before outputs are
  verified or published, and the body's `defer` cleanup runs when it ends.
- **Execution dependencies.** The interpreter's identity is one. A body that
  launches a process also consumes the whole effective child environment,
  recorded as a digest.
- **Ordering.** One node's recipe stays one ordered unit. Parallelism happens
  between graph nodes.

## Boundaries

- A recipe's effects are not proof of ownership. Only successfully published
  declared outputs and tracked writes are recorded for automatic clean
  (`040`).
- In foreign-shell text, `$…` belongs to the shell, and ordinary command names
  are not tracked. Use `@{x/NAME}` to track one
  ([domain/concept:ToolReference]).

Related: [domain/concept:Template] [domain/concept:Kash]

Sources: `docs/spec/044-value-producing-rule-bodies.md`;
`docs/spec/055-unified-kame-kash-language.md` §10.1–10.2;
`docs/spec/017-kash.md` §Rule bodies; `docs/spec/006-runtime.md`
§Rendering; `docs/spec/002-engine.md` §Parallelism.
