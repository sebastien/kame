---
identity: df37e5c3-5729-4329-801a-b26e00e02c56
created: 2026-11-25T18:43:58.418900+00:00
updated: 2026-11-25T18:43:58.850539+00:00
---
# Pattern

A pattern is a typed path matcher with named captures. Path atoms come in
three kinds that never convert implicitly into one another (`055` §9.1):

| Kind | Example | Role |
| --- | --- | --- |
| `Path` | `./src/main.c`, `${dir}/x.o` | One location |
| `PathPattern` | `./src/**/*.c`, `./src/{path:**}.c` | Matches paths; may capture |
| `PathTemplate` | `./build/{path}.o` | Builds a path from captures |

- **Groups.** `{name:*}` captures one segment and `{name:**}` spans
  directories. `{*}` and `{**}` are anonymous. Positional (`{_0}`), regex
  (`{name:~REGEX}`) and predicate groups are defined in `029` and `041`.
- **Operations.** `capture PATTERN PATH` returns a capture record or `nil`.
  `bind TEMPLATE CAPTURES` returns a `Path`. `match` dispatches over arms.
  `subst` replaces matches and keeps nonmatches. `match?` tests. `files`
  discovers paths with a tracked membership dependency.
- **Rule headers assign roles.** Output atoms are matchers: `{name}`
  captures like `{name:*}`. Input atoms are templates over those captures.
  The selected captures form the [domain/concept:Rule] instance.

## Boundaries

- A quoted string is text, never a pattern. Text becomes a pattern only in a
  `match` arm or through `pattern TEXT`, which parses pattern syntax, not Kame
  source.
- Brace groups in a path atom are never record literals.
- `${…}` in a pattern is evaluated with the atom. `{name}` stays a deferred
  capture.
- A path renders as text only in string interpolation, template output and
  argv.

Related: [domain/model:values] [domain/decision:unified_language]

Sources: `docs/spec/055-unified-kame-kash-language.md` §3.4, §4.5, §9;
`docs/spec/014-patterns.md`; `docs/spec/029-pattern-extensions.md`;
`docs/spec/041-pattern-predicates-and-substitution.md`.
