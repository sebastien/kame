---
identity: 432a5d8c-1c7f-4d57-bad3-fd1257e7e55d
created: 2026-11-25T18:43:39.074878+00:00
updated: 2026-11-25T18:43:39.934830+00:00
---
# Kame system architecture

Kame is a build and orchestration tool organized as layers around a portable
reactive engine. Source text goes through the language front end into a
program. The program registers producers on the engine, and the engine asks a
host to do OS work through requests.

This is the entry point to the `domain` knowledge model; the links below lead
to every concept, component and decision.

## Layers

| Layer | Components | Responsibility |
| --- | --- | --- |
| Interface | [domain/component:Cli], [domain/component:Embedding] | Invocation, presentation, interactive sessions; WASM, JavaScript, Python, launchers |
| Runtime | [domain/component:Program] | Declarations, selection, planning, freshness, cache, execution, services, ownership hooks |
| Language | [domain/component:Language], [domain/component:Evaluator], [domain/component:Operations] | Parsing and formatting, evaluation, standard library |
| Core | [domain/component:Engine], `diagnostic` | Reactive graph, values, signatures; structured [domain/concept:Diagnostic]s |
| Host | [domain/component:Host], `ownership` | Portable requests with native POSIX and WASI implementations; output ledger |

Other packages:

- `plugins` (spec 033) adds explicitly loaded operations.
- `ownership` (spec 040) runs native automatic clean.
- `internal/{mem,jsonstream,c,testing}` are lifetime, JSON and test helpers.

## Dependency direction (spec 001)

```text
cmd -> cli + program          host/wasm -> cli + program + plugins
program -> language + operations + core + host contract
language -> core              core -> diagnostic + internal
```

"The host never owns graph policy. The engine never invokes POSIX directly."
([domain/decision:portable_host_boundary])

## Main flows

- **Build** (`kame [TARGET…]`):
  1. discover the source;
  2. parse it;
  3. register declarations, including `generate` and declaration-time
     `if`/`for`;
  4. select a rule instance and plan it;
  5. schedule nodes on the engine;
  6. check the [domain/concept:Freshness] or [domain/concept:Cache] proof;
  7. render, then execute through [domain/concept:HostRequest]s;
  8. verify and publish outputs, revalidate inputs, and record the proof
     and ownership.

  See [domain/model:build_graph].
- **Script** (`kame FILE.kash`, `kame do run`): parse all fragments, then run
  them in order in one scope and engine, as one
  [domain/concept:ProcessUnit], joining async work at the end. See
  [domain/architecture:kash_execution].
- **Interactive** (`kash`): a [domain/concept:Session] appends each entry to
  a live program and engine.
- **Watch** (`--watch`): roots are retained. Host notifications lead to a
  fingerprint check, then invalidation of resource keys, and the engine reruns
  only the affected branches. Source edits recompile before new effects.
- **Embedded** (Node/WASM): a JavaScript call goes to a wasm export, which
  emits host request events. JavaScript completes them, and the engine
  validates each completion.

## Cross-cutting rules

- **Lifetimes.** Four nested lifetimes: instance (operations, configuration),
  program (sources, ASTs, definitions), engine (nodes, keys, retained values,
  subscriptions), and run or message (scopes, plans, requests, events).
  Crossing a lifetime requires a clone or a transfer, even under Go's GC.
- **Determinism.** The same program, resource state and requests give the
  same selection, dependency order, per-node revisions, fingerprints and
  formatting.
- **Authority.** Effects need both a [domain/concept:Capability] grant and a
  [domain/concept:Phase] that allows them.
- **Parity.** Native and WASM produce the same results. A missing host
  capability is `FEATURE_UNSUP`.

## Divergences from spec 001

Observed on 2026-11-26 with `go list`; the spec has not been updated:

- **Unlisted packages:** `internal/*`, `lang/usage`, `cmd/kame-launcher`,
  `cmd/kame-wasm`.
- **Unlisted import edges:**
  - `cli` → `program` and `lang/eval`;
  - `lang/eval` → `host`;
  - `lang/ast` → `lang/eval`;
  - `host` → `lang/expr`;
  - `host/posix` → `core` and `host`;
  - `cmd/kame` → `host/posix`, `lang/*` and `operations`.
- **`host/wasm`:** `handle.go` says the package has no dependency on
  `program`, but eight of its files import it. The spec's arrow allows this.
- **Layout:** `lang/fixture` is empty, and `examples/` has no root module;
  the only one is `examples/engine`.

## Decisions

- [domain/decision:implementation_language+001], superseded by
  [domain/decision:implementation_language+002]
- [domain/decision:portable_host_boundary]
- [domain/decision:content_freshness]
- [domain/decision:kash_typed_actions]
- [domain/decision:declarations_as_data]
- [domain/decision:unified_language]

## Vocabulary

[domain/concept:Kame] [domain/concept:Kash] [domain/concept:Context]
[domain/model:values] [domain/concept:Rule] [domain/concept:Node]
[domain/pattern:data_last_pipeline] [domain/pattern:consumer_composition]
[domain/antipattern:textual_reexpansion]

Canonical sources: `docs/spec/README.md` (the specs are normative; `055`
governs conflicts), `docs/glossary.md`, `docs/ref-contributing.md`.
