---
identity: 682335f0-b96c-4977-b124-2e75ea1fae74
created: 2026-11-25T18:44:13.888923+00:00
updated: 2026-11-25T18:44:14.355067+00:00
---
# Data-last pipelines

Write value transformations as a left-to-right pipeline of data-last library
calls.

```kame
sources = files ./src/**/*.c
objects = sources |> subst ./src/{path:**}.c ./build/{path}.o
summary = sources |> map basename |> join ", "
large = 7 |> gt? _ 3
```

## How it works

- Standard operations take their primary data argument last: `map f xs`,
  `filter pred xs`, `capture pattern path`, `bind template captures`,
  `put KEY VALUE RECORD`.
- `VALUE |> STAGE` evaluates `VALUE` once. It appends the value as the last
  argument, or substitutes it for each top-level `_` in the stage.
- Placeholder sections such as `(ends? ".c" _)` and `(join __ _)` build small
  callables inline.

## Context

Transforming collections of paths, records and strings in definitions, rule
inputs and Kash arguments.

## Trade-offs

- Pipelines read in evaluation order and compose without temporaries. The
  formatter and AST keep the pipe form, and lowering does not change
  evaluation order.
- `|>` is never `|`. Process streams use `|`, and the two never coerce into
  each other ([domain/decision:unified_language]).
- To place the value anywhere but last, use `_` or a section. A placeholder
  outside a section or stage is `PARSE_ERR`.

Related: [domain/model:values] [domain/component:Operations]

Sources: `docs/spec/055-unified-kame-kash-language.md` §4.3, §4.4, §4.6;
`docs/ref-kame-expr.md` (Calls, arithmetic, and pipelines).
