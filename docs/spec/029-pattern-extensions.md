# Pattern extensions

## Purpose and status

D11 extends the shared target and expression pattern machinery. The initial
implementation adds anonymous captures to rule outputs and positional capture
references to rule inputs. `(pattern TEXT)` explicitly constructs a validated
pattern value from a runtime string. Regular-expression groups and operations,
and capture processors remain explicit D11 work; this document will gain their
grammar and acceptance cases before those portions are implemented.

The extension preserves anchored, leftmost-shortest matching, non-empty
captures, and the existing `*`, `**`, `?`, character-class, and named-capture
behavior in `004-language.md` and `014-patterns.md`.

## Anonymous rule captures

Rule output templates accept `{*}` and `{**}` as anonymous captures. They match
one or more non-`/` bytes and one or more bytes including `/`, respectively.
Each anonymous group occupies its source-order position and does not create a
named value in the rule scope.
Every output capture is also bound in the rule scope as `_0`, `_1`, and so on,
in source order. A named capture keeps its named binding too. If a named
capture uses a name that collides with a positional binding, the named binding
takes precedence.

Named captures keep their existing equality rule: repeated occurrences of a
name must match the same text. Anonymous groups are independent, even when
adjacent, and use the same leftmost-shortest search as named groups. Formatting
preserves the anonymous spelling exactly.

## Positional input references

An input template may refer to any output capture by zero-based index with
`{_N}`. The index counts every capture group in output source order, including
named and anonymous groups. A named output capture can therefore be used by
either its name or its positional index. Repeated occurrences of one named
capture each occupy a position while retaining their equality constraint.

An out-of-range positional reference renders as an empty string, matching the
existing behavior for an unresolved named input reference. `_N` must use
canonical decimal notation: `_0` is valid; `_00`, signs and non-digits are not
positional forms. Such names remain available as ordinary named captures when
declared in an output template.

## Runtime construction

`(pattern TEXT)` accepts one string and returns a pattern value containing its
text. The string is parsed and rejected with `PAT_INVALID` if it contains
malformed groups or mixes matcher and expansion references. Strings remain
strings unless this operation is called; construction does not interpret
ordinary values implicitly. A valid string without groups may be constructed
as a pattern, though operations requiring a matcher or expansion group may
reject it for that use.

## Regular-expression capture groups

A matcher group may use `{name:~REGEX}` or `{~REGEX}`. The optional name follows
the existing pattern-name grammar. A regex matcher occupies one positional
capture slot; named occurrences retain the existing equality constraint, and
unnamed occurrences are independent. Regex matching is against the complete
capture, which is non-empty. The surrounding pattern remains anchored and
leftmost-shortest, including when several regex captures can divide the same
subject in multiple ways.

The portable regex grammar is byte-oriented ASCII syntax: literal bytes;
`.`, matching any byte except `/`; bracket classes `[abc]`, ranges `[a-z]`,
and negation `[!abc]`; grouping with `(...)`; alternation with `|`; and the
quantifiers `?`, `*`, and `+`. A backslash quotes the next byte. Empty
alternatives, empty groups, stacked quantifiers, lookaround, backreferences,
Unicode properties, and counted repetition are invalid. The regex group itself
must be non-empty even when its expression could match the empty string.
Evaluation has a fixed portable step budget derived from expression and input
length; exceeding it fails with `PAT_LIMIT` rather than consuming unbounded
time. Parser diagnostics point to the authored regex byte that is invalid.

## Regex operations and processors

`(regex-match PATTERN SUBJECT)` requires a pattern value containing at least one
regex matcher and a string subject. It returns `:nil` on no match, otherwise a
record with `text` (the complete matched subject), `captures` (a list of every
capture in source order), and `named` (a record mapping each distinct named
capture to its text). Captures are strings; empty named values are not possible
because captures are non-empty. Repeated named matchers contribute each source
slot to `captures`, while `named` contains their single equality-checked value.

`(capture INDEX MATCH)` selects a positional entry from the `captures` list,
where `INDEX` must be an integer. A negative or out-of-range index returns
`:nil`; a non-integer index or non-match input is invalid. `(capture NAME
MATCH)` selects from the `named` record; an absent name returns `:nil`, and a
non-string name or non-match input is invalid. These processors do not coerce
their index, name, or match result.

`(regex-replace MATCH-PATTERN EXPANSION SUBJECT)` uses the same expansion
pattern rules as `replace`: a string expansion is constant, while a pattern
value must contain references and no matchers. The subject must be a string or
a list of strings. A non-matching scalar or list item produces `:nil`, missing
references fail with `PAT_INVALID`, and invalid kinds fail with `EXPR_INVALID`.
All results and retained captures are invocation-owned and released on success,
diagnostic, cancellation and list-item failure. The regex parser and matcher
are shared portable code; diagnostics, capture ordering, formatting and
replacement results must agree across native, WASM CLI and evaluator tests.

## Acceptance

- `./{**}/{*}.c` parses, formats idempotently, and matches `./src/demo.c` with
  anonymous captures `src`, then `demo`.
- Anonymous captures are independent and leftmost-shortest; named repeated
  captures still require equal text.
- `./{**}/{*}.c : ./{_0}/{_1}.h` maps the selected target
  `./src/demo.c` to input `./src/demo.h` on native and WASM hosts.
- Positional references work when the corresponding output captures are named,
  and their indexes count repeated named groups as separate source positions.
- Recipe and input expressions can refer to anonymous captures through `_N`.
- Existing named-only rule matching, input rendering, cache identity, planning,
  diagnostics and source formatting remain stable.
- `(pattern (cat "./" "{" "name:*" "}.c"))` returns a pattern and can be
  passed to existing matching/replacement operations; malformed runtime
  patterns produce `PAT_INVALID`.

## Remaining D11 clauses

- Implement the regex group grammar, bounded portable matcher and source-aware
  diagnostics above.
- Implement `regex-match`, `capture` and `regex-replace` with the specified
  kinds, return shapes, missing-capture behavior, bounds and ownership.
- Verify native, WASM CLI and evaluator parity for captures, replacements,
  formatting and diagnostics.
