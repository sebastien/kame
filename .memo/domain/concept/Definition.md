---
identity: d3af9a4b-7e59-47e6-9a42-c8d3706a6d3b
created: 2026-11-25T18:43:51.595696+00:00
updated: 2026-11-25T18:43:52.018445+00:00
---
# Definition

A definition is a top-level named value or function: in a Kame program, or as
a `def` in a `.kash` module. `NAME = EXPR` (short for `def NAME = EXPR`) is
lazy, memoized and order-independent. Its first use evaluates it as an engine
[domain/concept:Node], and concurrent consumers share that work.

Forms (`055` §5.1):

- `NAME = EXPR`: lazy value. `NAME =` followed by a block computes it from
  statements.
- `def NAME PARAM… = EXPR`: function with an expression body. Zero-argument
  functions are declared `def NAME ()` and called `NAME ()`.
- `def NAME PARAM…` followed by a block: function with an action-context body.
- `NAME ?= EXPR`: configurable default. `--define NAME=TEXT` or
  `--set NAME=LITERAL` replaces it.
- `NAME += EXPR`: contributes list items to a configurable list, in source
  order.

## Block bindings, by contrast

- `let NAME = EXPR` is evaluated eagerly when reached, immutable and
  block-scoped. A later `let` shadows it.
- `set NAME = EXPR` assigns an existing, uncaptured `let` in the same
  invocation. It never creates a binding.
- `set env/NAME = EXPR` writes the [domain/concept:ProcessUnit] environment,
  not a lexical binding.

## Boundaries

- A definition is not a target. It has its own resource kind (`definition`),
  so it may share a spelling with a named rule. Target selection prefers
  `target`, `task` or `service` before falling back to a definition.
- `NAME = EXPR` is invalid inside blocks. Program files contain no top-level
  commands.
- A reached lazy definition inherits the [domain/concept:Phase] of the
  computation that demands it.

Related: [domain/model:values] [domain/concept:Rule]

Sources: `docs/spec/055-unified-kame-kash-language.md` §5;
`docs/spec/002-engine.md` §Resources; `docs/spec/006-runtime.md` §When to
execute; `docs/glossary.md` (Definition).
