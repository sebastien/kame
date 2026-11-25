---
identity: a85b9396-0b33-4b38-ab4b-19ce875d1746
created: 2026-11-25T18:44:03.789310+00:00
updated: 2026-11-25T18:44:04.326393+00:00
---
# Template

A document template is literal text containing Kame directives and
`@{EXPR}` interpolation. It renders to text and never parses or runs its
output. Templates are `.kmt` files, template values, and foreign-shell recipe
bodies.

- **Directives** are keyword-gated and usually take a whole line: `@if`,
  `@elif`, `@else`, `@for … [sep …]`, `@with`, `@match`/`@case`, `@let`,
  `@include`, `@raw` and `@end [label]`. Other `@` text, such as `@media` or
  `user@example.com`, stays literal, and `@@` escapes. Comment styles (for
  example `<!-- @for … -->`) are inferred from the file extension; `.kmt`
  uses `plain`.
- **Lowering:** directives lower at parse time to ordinary expressions (`if`,
  `match`, `map` plus `join`, lambdas, `render`). There is no second evaluator,
  so templates share laziness, capabilities and dynamic dependencies, and a
  malformed template fails before producing any text.
- **Interpolation:** `@{}` renders scalars and paths. Lists, records, bytes and
  callables are `EXPR_INVALID`. Nothing is escaped implicitly; quote for the
  target format yourself.
- **Rendering:** `render TEMPLATE PAYLOAD?` returns text, reads files under the
  `read` grant and records them as dependencies. `kame do render` denies read
  by default.

## Boundaries

- Templates are not Kash. In a template, `$REF`, `${…}` and `$(…)` are literal
  text that a foreign shell may interpret later. Kash bodies are parsed
  directly and never pass through templates
  ([domain/decision:kash_typed_actions]).
- `@{…}` belongs to templates and foreign-shell recipes. `${…}` belongs to
  Kame strings, paths, rule headers and Kash words.
- A template is not a target template. Rule headers use path
  [domain/concept:Pattern]s.

Related: [domain/concept:Recipe] [domain/concept:Context]

Sources: `docs/spec/016-templates.md`;
`docs/spec/030-template-extensions.md`;
`docs/spec/055-unified-kame-kash-language.md` §12.
