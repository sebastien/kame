---
identity: bcbda0ab-acee-48a0-b3bf-857af3b41c5f
created: 2026-11-25T18:43:44.920836+00:00
updated: 2026-11-25T18:43:45.332097+00:00
---
# Evaluator (`lang/eval`)

The evaluator runs ASTs over engine values in every context. It evaluates
expressions and Kash statements, and it applies the capability and phase
checks that gate effects.

Package: `src/go/kame/lang/eval` ("evaluates ASTs over core values").

## Areas

- **Scopes and contracts:** `Program`, `Context`, `Scope`. `context.go` holds
  phase and grant checks, `contract.go` the operation categories and phase
  masks, and `registry.go` the `Capability`, `Grant` and `Operation` types.
- **Calls and references:** `application.go` (calls, arity, `CAP_DENIED`),
  `expression.go` (references, including `x/NAME`), `tool.go` (tracked tool
  nodes).
- **Kash:** `kash_script.go` (`RunKash`), `kash_command*.go` (heads, builtins,
  `wait`), `kash_list.go`, `kash_glob.go`, `kash_exit.go`, `kash_source.go`,
  `interactive.go`.
- **Processes:** `process.go` (captures and requests),
  `process_expression.go` (pipe lowering and outcomes), `process_graph.go`,
  `process_channel.go`, `stream_graph.go`, `process_async.go` (handles,
  `wait`, joins).
- **Lifetime:** `defer.go` (`DeferScope`) and `finalization*.go`.
- **Other areas:** `module_import.go`, `declaration_include.go`,
  `foreign_shell.go`, `render.go` (templates), `rule_value.go` (constructed
  `Rule` values).

## Interactions

- Consumes ASTs from [domain/component:Language].
- Dispatches operations registered by [domain/component:Operations].
- Builds [domain/concept:HostRequest] payloads, importing the `host` package,
  and submits them through the engine context.
- Acts as the producer for definitions and reactive expressions on the
  [domain/component:Engine]. [domain/component:Program] calls it to evaluate
  rule bodies.

## Invariants

- Authorization ([domain/concept:Capability]) is checked before a request is
  submitted, so a denial has no side effect.
- [domain/concept:Phase] restrictions apply even when a grant exists.
- Callables, scopes and expression-local closures are run-owned. They are
  never published or cached as engine values.
- The evaluation context is passed explicitly: node, scope, rule frame,
  grants, phase, host queue, cwd, environment store and cancellation. There
  is no thread-local or async-local evaluation state.
- Control forms (`if`, `match`, `and`, `or`, `??`, `|>`, lambdas, `$(…)`,
  `eval`) are syntax evaluated here, not registry operations.

Related: [domain/architecture:kash_execution] [domain/model:values]

Sources: `docs/spec/005-evaluation.md`, `docs/spec/017-kash.md`,
`docs/spec/001-architecture.md` §Ownership.
