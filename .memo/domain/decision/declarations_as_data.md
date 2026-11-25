---
identity: 231fa5b0-05f9-4d58-b4bb-ca483fb267e7
created: 2026-11-25T18:44:06.962871+00:00
updated: 2026-11-25T18:44:50.463730+00:00
status: accepted
date: "2026-10-05"
---
# Declarations are data: rule values and `generate`

Status: accepted.
Dates: bounded generated declarations 2026-10-05 (spec 025, commit
`8f5dcf04`); rule values and declaration-time control flow in revision 2,
2026-11-18 ([domain/decision:unified_language]).

## Context

Builds need computed declarations: one rule per variant, per discovered
source, or per module. Make produces them by expanding text (`$(eval …)`,
`$(foreach …)`, generated include files) and then parsing the result, which
loses types and provenance.

## Alternatives

- **Generate source text and reparse it.** Rejected: "Nothing is generated as
  source text and reparsed" (`055` §1, principle 6). See
  [domain/antipattern:textual_reexpansion].

## Decision

- In expression position, `rule`, `task` and `service` construct a `Rule`
  value without registering it.
- `generate EXPR` registers one `Rule` or a list of them in the current
  declaration transaction. It is valid only in declaration context.
- Top-level `if` and `for` run in declaration context. Each declared rule
  closes over its own iteration scope.

## Invariants

- Registration is transactional. Every batch is validated before any
  declaration becomes visible, and re-evaluation replaces a generator's
  previous set.
- Declaration-time work is limited to pure computation, tracked read-only
  discovery (`files`) and tracked environment reads. It cannot read file
  contents, run processes or write.
- Every declaration keeps its provenance (file, iterations, call chain,
  `generate` site). Collisions report `TGT_AMBIG` with that provenance.
- Discovery that overlaps a declared output is rejected
  (`DISCOVERY_OVERLAP`).

## Consequences

- Modules can export functions that build rules. Importers register them
  explicitly ([domain/concept:Module]).
- `fmt` and `eval` are the only bridges between text and values, and `eval`
  parses only a single expression.

Sources: `docs/spec/055-unified-kame-kash-language.md` §9.4, §10.3–10.5, §13;
`docs/spec/025-generated-declarations.md`.
