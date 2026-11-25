---
identity: d11c2c62-831f-4e0e-b5ad-3d8f9cbfb23c
created: 2026-11-25T18:43:59.227690+00:00
updated: 2026-11-25T18:43:59.658282+00:00
---
# Phase

A phase is a stage of program execution with fixed authority. It decides
whether a reached operation may perform its effect now. Each phase has its
own authority, and runtime grants cannot widen it.

| Phase (`055` §14, `005`) | May do |
| --- | --- |
| Declaration loading, tracked generation | Pure computation, tracked read-only discovery, tracked environment reads |
| Target selection | Pure computation |
| Dependency resolution (planning) | Pure computation, tracked reads |
| Action execution | Processes, writes, effects |
| Output publication | Publishing rule results |

The program runtime names its pipeline **compile → plan → schedule →
render → execute** (glossary). Planning only establishes inputs and outputs.
It neither renders recipe bodies nor runs commands.

## Rules

- **Phases are inherited.** Value context runs in whichever phase demands
  it, and a reached lazy [domain/concept:Definition] inherits that phase.
  Declaration context runs during loading and generation. Action context has
  effects only during execution.
- **Some operations are always invalid during planning.** Processes, writes,
  `out!`/`err!`, `sh!` and clocks (`now`, `monotonic`) fail with
  `PHASE_INVALID` there, and also inside `= EXPR` bodies and foreign-shell
  rendering. Clocks need no grant, but are barred "because those phases must
  not depend on the time at which they ran" (`007` §Clock).
- **Planning cannot start processes.** `$(…)` and templates reached during
  planning fail. Direct Kash recipes evaluate in the execution phase.
- **Dry-run is not a phase.** `-n` plans and renders while suppressing every
  effect.

## Boundaries

- A phase is not a [domain/concept:Context]. Context is syntactic and fixes
  the grammar. Phase is dynamic and gates effects.
- A phase is not a [domain/concept:Capability]. The failure codes differ: the
  wrong phase gives `PHASE_INVALID`, a missing grant gives `CAP_DENIED`, and
  authorized but unavailable work gives `HOST_FAIL`.

Sources: `docs/spec/005-evaluation.md` §Phases, §Evaluation Context;
`docs/spec/006-runtime.md` §When to execute;
`docs/spec/055-unified-kame-kash-language.md` §14; `docs/glossary.md`
(Phase).
