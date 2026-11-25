---
identity: e545c45d-fdbf-4aab-bd00-49e18ef54840
created: 2026-11-25T18:43:54.673096+00:00
updated: 2026-11-25T18:43:55.106971+00:00
---
# Kame

Kame (*kah-meh*, verlan for *make*) is a general-purpose build and
orchestration tool, used for builds, tests, provisioning and deployment. Kame
is also the name of its source language. It keeps Make's targets,
prerequisites and recipes, but runs them on one incremental, demand-driven
engine with typed values.

## Senses of the name

- **The Kame language.** One unified language
  ([domain/decision:unified_language]) shared by values, declarations, Kash
  actions and templates ([domain/concept:Context]).
- **A Kame program** (`.km`, alias `.kame`). It holds lazy
  [domain/concept:Definition]s, imports and includes ([domain/concept:Module]),
  and build declarations ([domain/concept:Rule]). It has no top-level
  commands.
- **The `kame` CLI.** It discovers `Kamefile`, `Makefile.km`, `make.km` or
  `src/km/main.km`, builds targets (`default` when none is named), runs
  explicit `.km` and `.kash` sources, and offers `kame do …` inspection and
  tooling ([domain/component:Cli]).

## Design goals (README)

- **Familiar, not Make-compatible.** File artifacts, phony tasks, cached
  tasks and services are distinct kinds.
- **Clear language boundaries.** Expression bodies compute artifact content,
  Kash recipes pass typed arguments to processes, and foreign-shell recipes
  are explicit.
- **Demand-driven computation.** Lazy definitions, dynamic dependencies,
  streaming and watch invalidation share one engine, informed by *Build
  Systems à la Carte*.
- **Inspectable reuse.** Content signatures and tracked dependencies decide
  freshness, and plans and dry runs can be inspected before execution.

## Boundaries

- **Not Make.** There is no textual macro expansion, no implicit shell
  recipe, and no timestamp freshness
  ([domain/antipattern:textual_reexpansion],
  [domain/decision:content_freshness]).
- **Not a shell.** Process orchestration belongs to [domain/concept:Kash].
  Expressions reach processes only through `$(…)`, `$?(…)` and `$&(…)`, in
  phases that allow them.

Related: [domain/architecture:kame_system]

Sources: `README.md`; `docs/spec/055-unified-kame-kash-language.md` §1–2;
`docs/spec/009-cli.md`; `docs/skills/kame/ref/language.md`.
