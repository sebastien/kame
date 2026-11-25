---
identity: 4eb9a35d-f25a-48a9-845f-48b21d65010c
created: 2026-11-25T18:43:50.803723+00:00
updated: 2026-11-25T18:43:51.254580+00:00
---
# Context

A context is a syntactic position that decides which grammar parses the
source and what kind of work it may do. It is chosen by syntax alone. Bindings,
imports, `PATH`, grants, host availability and phase never change the parse.

| Context | Grammar | Permitted work |
| --- | --- | --- |
| Value | Expression | Compute typed values; effects only where the phase allows them |
| Declaration | Expression and declaration statements | Build and register declarations; pure computation and tracked read-only discovery |
| Action | Command and block statements | Invoke functions and executables, run process graphs and control flow, perform granted effects |
| Template | Literal text with `@` directives | Render text; embedded expressions run in value context |

Entering a context (`055` §2):

- **Action:** rule, task and service bodies, block `def` bodies, the `.kash`
  top level, and `$(…)`, `$?(…)`, `$&(…)`.
- **Value:** `(…)` and `${…}` in command words, the right-hand side of
  `let`/`set`/`return`, conditions, `${…}` in strings, paths and rule headers,
  and `@{…}` in templates and foreign-shell recipes.
- **Declaration:** top-level `if` and `for` in a Kame program.

## Boundaries

- A context is not a [domain/concept:Phase]. The context fixes the grammar and
  the kind of work. The phase decides whether an effect may run now.
- Name resolution depends on position (expression or command head), not on
  context. "Context restricts effects, not names" (`055` §1, §6).
- A parse failure never falls back to literal text or an external shell. A
  context-denied operation fails before its effect.

Related: [domain/decision:unified_language] [domain/concept:Kash]
[domain/concept:Template]

Sources: `docs/spec/055-unified-kame-kash-language.md` §1, §2, §6.
