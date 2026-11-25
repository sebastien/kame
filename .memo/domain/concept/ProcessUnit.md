---
identity: 2da1090f-0024-47f9-8de4-cd84a25c4e31
created: 2026-11-25T18:44:00.697539+00:00
updated: 2026-11-25T18:44:01.136011+00:00
---
# Process-like unit

A process-like unit is the scope that owns Kash's mutable execution state and
that `exit` ends. Units are:

- a Kash script,
- a rule body,
- a `$(…)` or `$?(…)` capture,
- in an interactive [domain/concept:Session], the session for state and the
  current entry for `exit`,
- the top-level fragments of one `kame do run` invocation, which together form
  a single unit.

## State

Each unit inherits two pieces of state from its parent when it starts:

- **Environment store.** The child-process environment. It is read through
  `env/NAME`, bare upper-case `ENV-NAME` references, or `env NAME-EXPR`, and
  written with `set env/NAME = EXPR`. Values are scalars, and `nil` unsets.
  Executable lookup uses the store's `PATH`.
- **Working directory.** Changed with `cd`.

## Rules

- A write is visible to every later statement, lookup and process in the same
  unit, even after the function that made it returns.
- Writes never reach the parent unit, so a rule body or capture cannot change
  its caller's environment or directory.
- Per-command `env:` and `cwd:` settings and `with cwd: … env: …` blocks apply
  an overlay without writing the store.
- Writing the store is not a host environment mutation and needs no grant.
  Unsetting or overriding a protected name follows the environment authority
  of `051`.
- `exit N` ends the innermost unit with status 0–255 and runs the pending
  cleanup of every invocation it unwinds. `return` ends a `def`; `exit` never
  targets one.

## Boundaries

A unit is not an OS process. It is a language scope whose commands may start
many processes. Environment reads through the store remain tracked and
capability-checked ([domain/concept:Capability]).

Related: [domain/concept:Kash] [domain/concept:Outcome]

Sources: `docs/spec/055-unified-kame-kash-language.md` §5.3, §8.4;
`docs/spec/051-kash-session-authority.md`;
`docs/spec/048-kash-practical-scripting.md`.
