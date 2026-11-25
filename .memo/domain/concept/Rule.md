---
identity: 2a20ef74-b52c-404a-805a-3115bc68755e
created: 2026-11-25T18:44:02.323041+00:00
updated: 2026-11-25T18:44:02.764064+00:00
---
# Rule

A rule is a build declaration: outputs, inputs, optional metadata and an
optional body ([domain/concept:Recipe]). The leading keyword fixes its kind,
its engine resource key and its reuse policy.

| Kind | Syntax | Key | Reuse |
| --- | --- | --- | --- |
| File rule | `rule OUTPUTS : INPUTS` | `file` | Reused while its [domain/concept:Freshness] proof holds; artifacts persist |
| Always-stale file rule | `rule always …` | `file` | No freshness check or cache lookup; reruns once per requested-root epoch |
| Phony task | `task NAME` | `target` | Always stale; runs whenever reached from a root; produces no artifact |
| Cached task | `task cached NAME` | `task` | Reused on a matching [domain/concept:Cache] record, which replays its logs |
| Service | `service NAME` | `service` | Long-running process: started on interest, published when ready, stopped after the last release (`024`) |

## Structure

- **Outputs** are matchers: `{name}` captures one segment, like `{name:*}`.
  File outputs may be computed with `${EXPR}` or `(EXPR)`, resolved purely at
  registration.
- **Inputs** are templates over the output captures ([domain/concept:Pattern]).
  - Whitespace separates items in a parallel group.
  - `,` starts the next sequenced group. It is a demand barrier, not an effect
    sequence.
  - `|` begins order-only prerequisites. These must finish, but they are not
    content observations and are excluded from `inputs` and `changed-inputs`.
  - A bare name is a symbolic resource (a task or a definition). An explicit
    path with no producing rule must already exist.
- **Metadata** `; {…}`: `shell`, `env`, `authority`, `tty`, `foreground`,
  `executor`, and the service keys (`ready`, `health`, `restart`, `stop`,
  `log-bytes`).
- **Target arguments.** Tasks and services may take named arguments
  `{name}` or `{name=default}` (`022`). They are part of instance and cache
  identity.

## Selection and instances

- A literal rule beats templates. Dominated templates are dropped (`045`).
  Remaining ambiguity is `TGT_AMBIG`, and no match is `TGT_NO_RULE`.
  Registration order and source existence never break ties.
- A **rule instance** is one declaration plus its complete capture map. All
  of its rendered outputs map to one node, so sibling outputs share one
  execution.

## Boundaries

- A rule is not a target (the requested result), and not a
  [domain/concept:Definition], which is a separate resource kind. For bare
  names, rule lookup wins.
- Rules are values. In expression position, `rule`, `task` and `service`
  construct an unregistered `Rule`, which `generate` registers
  ([domain/decision:declarations_as_data]).
- A service is not a task. It cannot publish files or use cached identity.

Related: [domain/model:build_graph] [domain/component:Program]

Sources: `docs/spec/055-unified-kame-kash-language.md` §10;
`docs/spec/006-runtime.md` §Rule Selection, §File Rules, §Tasks, §Rule
Instances and Multiple Outputs; `docs/spec/045-rule-precedence.md`;
`docs/spec/022-target-arguments.md`; `docs/spec/024-managed-services.md`.
