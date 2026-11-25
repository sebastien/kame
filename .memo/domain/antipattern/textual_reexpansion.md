---
identity: 18de5ccf-bfc4-44d3-a2c4-97ba87b30549
created: 2026-11-25T18:43:37.743148+00:00
updated: 2026-11-25T18:43:38.374582+00:00
---
# Textual re-expansion

The pattern of turning values into text and letting a later stage reparse it:
Make variables expanded into recipe text, shell word splitting and globbing
of expanded values, generated makefiles and `$(eval …)`, or a failed parse
that falls back to literal text or a shell.

## Failure modes

- **Meaning depends on content.** A path with a space becomes two arguments,
  and a value containing `*` or `$` changes the command.
- **Types and provenance are lost.** Diagnostics point into generated text
  instead of the authored source.
- **Dependencies are hidden.** Commands resolved by a shell, and environment
  reads made by it, are invisible to freshness.

## How Kame avoids it

The language is designed against this pattern (`055` §1, §14):

- Kash words produce typed argv. A scalar is exactly one argument, and a list
  becomes one argument per item, never by splitting text
  ([domain/concept:Word]).
- Rules are values registered by `generate`, never source text
  ([domain/decision:declarations_as_data]).
- Path, pattern, template and text kinds never convert implicitly
  ([domain/concept:Pattern]).
- A parse failure never falls back to literal text or an external shell
  ([domain/concept:Context]).
- `eval` parses a single expression only. `fmt` is the canonical bridge from
  value to source.

## Preferred alternative

Keep data as values up to the process boundary. Pass `$name`, `${EXPR}` and
`(EXPR)` words, compute rules as values, and serialize explicitly with `json`
or `fmt`. Foreign-shell recipes (`; {shell: "bash"}`) are the explicit opt-in
to shell text. There, `@{EXPR}` interpolates into one script, and the author
is responsible for quoting.

Related: [domain/decision:unified_language] [domain/decision:kash_typed_actions]

Sources: `docs/spec/055-unified-kame-kash-language.md` §1, §7.1, §10.2, §13,
§14.
