---
identity: dd12ec5d-f421-4889-a44f-75db9966e835
created: 2026-11-25T18:44:12.304267+00:00
updated: 2026-11-25T18:44:12.751332+00:00
---
# Value model

Kame programs, Kash actions and templates share one value model (principle 2
of [domain/decision:unified_language]). Values are immutable tagged data, and
containers own their elements.

## Kinds

The implementation's `core.Kind` (`src/go/kame/core/value.go`) has these
kinds:

| Kind | Notes |
| --- | --- |
| `nil`, `bool` | Only `nil` and `false` are false. |
| `int` (int64), `float` (float64) | `+`, `-` and `*` keep integers. `/` returns a float. `%` takes integers. |
| `string`, `bytes` | Captured output is exact. `line` drops one trailing LF. |
| `symbol` | `:name`; keeps its own kind. |
| `list`, `record` | `[a, b]` and `{key: value}`, with `...` spreads. Record keys are strings. |
| `path`, `path-pattern`, `path-template` | Typed path kinds ([domain/concept:Pattern]). |
| `pattern` | A pattern compiled from text with `pattern TEXT`. |
| `resource` | Canonical `file:///…` or `mem://…` identity (`031`). |
| `callable` | Operations, lexical functions, lambdas, placeholder sections. |
| `rule` | A constructed declaration, registered with `generate`. |
| `stream` | A lazy generator result (`yield`). |
| `process` | An owned async handle from `$&(…)` or `CMD &`. |
| `kash-module` | A loaded module namespace ([domain/concept:Module]). |

`002` §Values still lists only the original eleven kinds. `055` adds the
path kinds, rules and streams, and governs any conflict. An invocation
[domain/concept:Outcome] is an ordinary record.

## Invariants

- **Immutable once published.** A value retained past its lifetime must be
  cloned or transferred.
- **Runtime-only kinds.** Callables, processes, streams and rules cannot be
  serialized, cached or `fmt`-ed (`EXPR_INVALID`). Run-owned closures cannot
  cross the run boundary.
- **No implicit conversions** between text and code, path and string,
  stream and value, or return value and stdout (`055` §14).
- **Three renderings for three purposes:**
  - `fmt` gives canonical source that round-trips through `eval`.
  - `str` and `${…}` give text; lists are space-joined.
  - `json` gives explicit JSON.
- **Equality.** `==` and `eq?` are strict, recursive and kind-aware, and
  never serialize. Ordering accepts only two numbers or two strings.
- **Truth is not emptiness.** `"" or "fallback"` yields `""`. Test with
  `empty?`, `nil?` or `count`.

Related: [domain/concept:Definition] [domain/pattern:data_last_pipeline]

Sources: `docs/spec/002-engine.md` §Values;
`docs/spec/055-unified-kame-kash-language.md` §3–4, §9, §13–14;
`docs/spec/005-evaluation.md` §Value Display; `docs/spec/007-library.md`;
`docs/idioms-and-gotchas.md`.
