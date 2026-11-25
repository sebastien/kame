---
identity: 185fe9d2-d446-432f-af6c-779da5201c22
created: 2026-11-25T18:44:09.278862+00:00
updated: 2026-11-25T18:44:49.214761+00:00
status: accepted
date: "2026-10-09"
---
# Kash: typed command actions instead of shell text

Status: accepted.
Dates: proposed 2026-09-28 (spec 017, commit `1806184d`); direct recipes
adopted 2026-10-09 (spec 038, commit `d9e8012b`); merged into the language on
2026-11-18 ([domain/decision:unified_language]).

## Context

Make recipes are shell text produced by textual expansion. Values become
words that the shell splits, globs and requotes, and the shell resolves
command names, so tools are not tracked as dependencies and errors point into
generated text. Kame's early recipes were also shell text rendered from
templates.

## Alternatives

- **Shell-text recipes rendered from document templates.** Kept only as
  opt-in foreign-shell recipes (`; {shell: "bash"}`), see
  [domain/concept:Recipe].
- **Template-expand Kash recipes before parsing them.** This was the first
  Kash implementation, with `@(…)` expression boundaries. It was removed on
  2026-10-09.

## Decision

Recipes default to [domain/concept:Kash], parsed directly from the authored
source:

- [domain/concept:Word]s become typed argv entries. Scalars are never split
  or globbed.
- Command heads resolve as tracked executables
  ([domain/concept:ToolReference]).
- Processes compose into [domain/concept:ProcessGraph]s, and every
  invocation has an [domain/concept:Outcome].

## Rationale

From the adoption commit, expressions "remain typed, diagnostics retain
authored source locations, and command heads resolve as tracked executable
dependencies" (`d9e8012b`). The original design note aimed for scripts that
read like shell "without inheriting the accidental word splitting and implicit
environment of traditional shells" ([world/entry:Design.Kash]).

## Consequences

- `str` no longer serializes collections implicitly. Use `json` instead.
- Foreign-shell recipes remain available, but their ordinary command names
  are resolved by the shell and are not tracked.
- Kash was extended to standalone scripts and an interactive shell
  ([domain/concept:Session]).

Sources: `docs/spec/017-kash.md`, `docs/spec/038-direct-kash-and-values.md`,
Git history.
