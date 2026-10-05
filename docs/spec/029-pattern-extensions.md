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

## Acceptance

- `./{**}/{*}.c` parses, formats idempotently, and matches `./src/demo.c` with
  anonymous captures `src`, then `demo`.
- Anonymous captures are independent and leftmost-shortest; named repeated
  captures still require equal text.
- `./{**}/{*}.c : ./{_0}/{_1}.h` maps the selected target
  `./src/demo.c` to input `./src/demo.h` on native and WASM hosts.
- Positional references work when the corresponding output captures are named,
  and their indexes count repeated named groups as separate source positions.
- Existing named-only rule matching, input rendering, cache identity, planning,
  diagnostics and source formatting remain stable.
- `(pattern (cat "./" "{" "name:*" "}.c"))` returns a pattern and can be
  passed to existing matching/replacement operations; malformed runtime
  patterns produce `PAT_INVALID`.

## Remaining D11 clauses

- Regular-expression groups have an anchored, portable grammar with explicit
  named and positional captures; malformed expressions produce source-aware
  diagnostics on both hosts.
- Regex matching, replacement and capture-processing operations specify their
  accepted value kinds, return shapes, coercions, missing-capture behavior,
  bounds and allocation ownership.
- Native, WASM CLI and evaluator results agree for captures, replacements,
  formatting and diagnostics.
