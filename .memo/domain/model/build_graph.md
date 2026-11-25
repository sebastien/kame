---
identity: 0eb9f5d4-c24b-4af7-86de-f3db7274a50d
created: 2026-11-25T18:44:11.526159+00:00
updated: 2026-11-25T18:44:12.003027+00:00
---
# Build graph model

This model describes how authored declarations become shared runtime work.

## Entities

- **Declaration.** An authored or generated [domain/concept:Rule] registered
  in the program. It keeps its provenance (file, iterations, call chain,
  `generate` site).
- **Target.** A requested result: a path such as `./build/app`, or a name
  such as `test`, optionally with arguments (`deploy environment=prod`).
- **Rule instance.** One selected declaration with its complete capture map
  and target arguments. It owns all of its rendered outputs.
- **Resource key.** A kind (`file`, `target`, `task`, `service`,
  `definition`, plus glob, environment and tool resources) and a canonical
  name. It identifies one engine [domain/concept:Node].
- **Edge.**
  - *Static* edges come from declared inputs: parallel groups, sequenced
    groups and order-only prerequisites.
  - *Dynamic* edges are discovered while evaluating or rendering, through
    `read`, `files`, `env/…` and tool references. They belong to one
    generation.
- **Plan.** Target, key, rule, captures, rendered outputs, declared inputs,
  span, and a freshness of `fresh`, `stale` or `unknown`. A plan is an
  inspection artifact, not the live graph.
- **Proof or record.** The accepted evidence for reuse:
  [domain/concept:Freshness] for file rules, a [domain/concept:Cache] record
  for cached tasks.
- **Ledger entry.** Recorded ownership of a produced artifact, which is the
  only basis for automatic clean (`040`).

## Relationships

```text
target --selection--> rule instance --owns all outputs--> one node
node --static and dynamic edges--> input nodes (files, tasks, definitions, globs, env, tools)
definition --first reference--> lazy node shared by every consumer
service --kept alive by interest--> node published when ready
```

## Invariants

- **Sharing.** One node per resource key, and one execution per rule
  instance, shared by sibling outputs and by concurrent roots.
- **Deterministic selection.** A literal rule beats a non-dominated template;
  anything else is `TGT_AMBIG`. Registration order and source existence never
  decide.
- **Order of construction.** Registration is transactional and finishes
  before selection. Computed outputs resolve purely before selection.
- **Cycles.** A diamond is not a cycle; real cycles are `DEP_CYCLE`.
- **Order-only edges** schedule work and propagate failure, but never count
  as content.
- **Inspection is read-only.** `do plan`, `do inputs` and `do outputs` never
  claim that every reachable producer will execute. Inspection stages are not
  runtime phases or barriers.

Related: [domain/component:Program] [domain/component:Engine]
[domain/decision:declarations_as_data]

Sources: `docs/spec/006-runtime.md`, `docs/spec/002-engine.md`,
`docs/spec/025-generated-declarations.md`,
`docs/spec/037-build-inspection.md`, `docs/spec/045-rule-precedence.md`,
`docs/spec/022-target-arguments.md`, `docs/spec/040-automatic-clean.md`.
