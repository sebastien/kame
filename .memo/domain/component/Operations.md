---
identity: c64aa71b-26e1-46ee-a64d-dc13863eee2b
created: 2026-11-25T18:43:47.035788+00:00
updated: 2026-11-25T18:43:47.454540+00:00
---
# Operations (`operations`)

The operations package registers the standard library (`std/NAME`) into the
evaluator's registry. It covers general, comparison, collection, text, path,
pattern, filesystem and discovery, environment, process, template, tool,
clock, and usage/completion operations. Its single export is `Register`.

## Operation contract

Every operation declares:

- name, call function and arity;
- unconditional and argument-dependent capabilities;
- a category: computation, tracked observation, output or publication,
  process action, control effect, or volatile observation;
- allowed phases;
- a stable version.

Operation versions enter cache fingerprints, so changing a version
invalidates cached tasks ([domain/concept:Cache]).

## Conventions

- **Data-last.** `f PARAMS VALUE` is equivalent to `VALUE |> f PARAMS`
  ([domain/pattern:data_last_pipeline]). Comparisons keep `gt? LEFT RIGHT`.
- **Suffixes.** `?` marks predicates (`match?`, `empty?`); `not`, `bool` and
  the operators are exceptions. `!` marks effects (`write!`, `out!`, `err!`,
  `sh!`, `finally!`). "A suffix is not evidence of a category or authority":
  the contract decides.
- **Purity.** Pure operations never read clocks, the environment, global
  state, files or processes. Phase metadata is an upper bound, not
  permission.
- **Control forms are syntax.** `if`, `match`, `and`, `or`, `??`, `|>`, lambdas,
  `$(…)`, `eval` and block statements control their operands' evaluation, so
  they are not registry operations.

## Interactions

- Called by [domain/component:Evaluator] through the registry, after its
  capability and phase checks.
- Host-backed operations (`operations/host.go`) submit
  [domain/concept:HostRequest]s and declare file or glob dependencies before
  submitting.
- Completion operations use `lang/usage` ([domain/component:Language]).
- Plugins (`plugins`, spec 033) add explicitly loaded, trusted operations
  backed by a native process or a JavaScript callback. They never add syntax,
  definitions, rules or capabilities.

Sources: `docs/spec/007-library.md`; `docs/spec/005-evaluation.md` §Scope;
`docs/spec/033-plugins.md`; `docs/skills/kame/ref/library.md`.
