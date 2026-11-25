---
identity: 159fc5cd-d381-4efe-9eed-690f0d5efc46
created: 2026-11-25T18:44:10.771850+00:00
updated: 2026-11-25T18:44:48.785699+00:00
status: accepted
date: "2026-11-18"
---
# Revision 2: one unified Kame/Kash language

Status: accepted.
Date: defined 2026-11-18 (commit `0f079db0`, spec 055); implemented
2026-11-19 to 2026-11-20 (commits `c0c112b3`, `52a9ff3c`, `4afb4a9d`,
`fc7467b2`).

## Context

Before revision 2, Kame values, rule files, Kash actions and templates were
specified and parsed separately, and values, bindings and name resolution
were defined in several places. Build files (`.kmk`) used Make-like headers
(`./out : ./in ; [shell: kame]`), parenthesized expressions and Make-style
selectors such as `@<`. The CLI had separate `rule` and `script` language
modes (README at release 0.6.0, commit `e3df7def`).

## Alternatives

- **Keep separate grammars** and evolve each one.
- **Compatibility modes, legacy parsers or migration tools.** Rejected: "a
  clean break … no compatibility mode, legacy parser or migration tool"
  (`055` §1).

## Decision

One source language, with grammar selected by [domain/concept:Context].
The principles from `055` §1:

1. Context selects the grammar syntactically, never through name resolution.
2. One value model ([domain/model:values]).
3. Value pipes (`|>`) and process pipes (`|`) stay separate.
4. Context restricts effects, not names.
5. Paths and patterns are typed ([domain/concept:Pattern]).
6. Declarations are data ([domain/decision:declarations_as_data]).
7. Imports do not build ([domain/concept:Module]).
8. Functions and processes share an invocation model
   ([domain/concept:Outcome]).

## Rationale

Keep Make's useful features (parameterized and pattern rules, computed
declarations, includes) but "drop implicit textual expansion and reparsing"
(`055` §1). The spec also requires that no implicit conversion between text
and code, path and string, stream and value, or return value and stdout may
change meaning silently (`055` §14). See
[domain/antipattern:textual_reexpansion].

## Consequences

- Old-syntax sources fail with `PARSE_ERR`. `.kmk` files became `.km`.
- `${EXPR}` interpolates in strings, paths, rule headers and Kash words.
  `@{EXPR}` is reserved for document templates and foreign-shell recipes.
- Every spec declares `revision: 2`, and `055` governs conflicts
  (`docs/spec/README.md`).
- There are no migration notes. User-facing language guidance is in
  `docs/skills/kame/ref/language.md`.

Sources: `docs/spec/055-unified-kame-kash-language.md`,
`docs/spec/README.md`, Git history.
