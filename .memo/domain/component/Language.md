---
identity: 918c16cb-e129-4b65-9fb8-493e56b74dfe
created: 2026-11-25T18:43:46.329784+00:00
updated: 2026-11-25T18:43:46.741867+00:00
---
# Language front end (`lang/*`)

The language front end parses, formats and inspects source in every
[domain/concept:Context] without evaluating anything. It produces immutable,
program-owned ASTs.

## Packages

- `lang/source`: source text, byte spans (zero-based, half-open) and display
  positions.
- `lang/expr`: the expression grammar, plus the Kash command and process
  grammar. The two are mutually recursive, which is why there is no
  `lang/kash` package. Also patterns, glob matching and pattern-overlap checks.
- `lang/definition`, `lang/rule`: definition headers, declaration headers and
  rule bodies.
- `lang/script`: statement composition for Kame files, Kash blocks, modules,
  includes, `generate` and interactive tokens (`ParseKameFile`, `ParseKash`,
  `ParseKashRuleBody`).
- `lang/template`: string, target and document (`.kmt`) templates, and
  comment styles.
- `lang/format`: canonical source formatting.
- `lang/ast`: schema-2 AST JSON for `kame do parse`.
- `lang/usage`: command usage models (KDL, man pages, fish completions) for
  Kash completion (spec 058). It uses only the Go standard library.
- `lang/fixture`: an empty placeholder. Shared fixture checks live in its
  `test/` package.

## Interactions

- ASTs feed [domain/component:Evaluator] and [domain/component:Program].
- `kame do parse` and `kame do fmt` reach the front end through
  [domain/component:Cli].

## Invariants

- Parsing never depends on bindings, grants, host availability or phase.
- Formatting is canonical and idempotent. It keeps comments, literal and path
  kinds, the distinction between pipes and nested calls, sections and spans.
  It uses one tab per block level and no trailing `:` on block headers.
- Old-syntax sources fail with `PARSE_ERR` ([domain/decision:unified_language]).
- `lang/ast` imports `lang/eval` to reuse process-topology lowering. This is
  a dependency the architecture spec does not list
  ([domain/architecture:kame_system]).

Sources: `docs/spec/055-unified-kame-kash-language.md` §15;
`docs/spec/004-language.md`; `docs/spec/018-source-style.md`;
`docs/spec/001-architecture.md` §Package Boundaries.
