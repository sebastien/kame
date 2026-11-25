---
identity: 8ff3cb33-6432-46cc-9cbc-67ee359db4e0
created: 2026-11-25T18:43:47.809293+00:00
updated: 2026-11-25T18:43:48.396995+00:00
---
# Program runtime (`program`)

The program runtime compiles sources into a program and materializes targets.
It covers declaration registration, rule selection, planning, rendering and
execution, publication, freshness proofs, cached-task records, services,
output ownership and sessions. It is the main consumer of the
[domain/component:Engine].

Package: `src/go/kame/program` ("compiles scripts and materializes
targets").

## Areas (files in `src/go/kame/program`)

- **Compile and declarations:** `compile.go` (`CompileMany`),
  `declaration_*.go` (blocks, discovery, include, import),
  `generated_rule_values.go`, `computed_outputs.go`.
- **Selection and planning:** `selection.go`, `precedence.go` (spec 045),
  `planning.go` (`Plan`, `ExpandPlan`), `input_groups.go`, `inspection*.go`.
- **Execution:** `producer.go`, where `produce` drives one rule instance
  through inputs, render, cache, effects and submission. Also `render.go`,
  `recipe_settings.go`, `materialize.go`, `host_events.go` (request pump and
  process events), `finalization.go`.
- **Freshness:** `file_context.go`, whose header states that "the host only
  transports resource facts and opaque record bytes, never freshness
  decisions". Also `preflight.go` and `reasons.go`.
- **Cache:** `cache.go` (records, SHA-256 fingerprints, native and forwarded
  lookup) and `cache_encode.go` (budgeted manifest).
- **Services:** `service_config.go`, `service_restart.go`, plus readiness and
  probes in `host_events.go`.
- **Ownership:** `ownership.go` (output and target locks, ledger hooks,
  automatic clean).
- **Sessions and events:** `session.go` (`CompileSession`), `interactive.go`
  (`AppendKash`), `event_json.go` (JSON Lines events and diagnostics).

## Interactions

- Registers producers on the [domain/component:Engine].
- Evaluates bodies and definitions with the [domain/component:Evaluator].
- Sends work to a [domain/component:Host] through `host.ProgramHost`.
- Driven by [domain/component:Cli] and by the WASM runtime
  ([domain/component:Embedding]).

## Invariants

- Freshness and caching decisions stay here and never move into a host.
- All outputs of one rule instance share one node and one execution.
- Declaration registration is transactional.

Related: [domain/model:build_graph] [domain/concept:Phase]

Sources: `docs/spec/006-runtime.md`, `docs/spec/008-cache.md`,
`docs/spec/024-managed-services.md`, `docs/spec/039-output-ownership.md`,
`docs/spec/040-automatic-clean.md`.
