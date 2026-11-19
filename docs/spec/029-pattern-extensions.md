---
revision: 2
---

# Pattern extensions

## Purpose

The shared rule-header and path-pattern machinery supports anonymous
rule-output captures, positional rule-input references, `pattern TEXT`
construction from runtime strings, regular-expression groups, bounded matching,
regex capture processors and regex substitution. These operations have the same
behavior on native and WASM.

The extension preserves anchored, leftmost-shortest matching, non-empty
captures, and the `*`, `**`, `?`, character-class, and named-capture behavior of
`004-language.md` and `014-patterns.md`.

## Anonymous rule captures

Rule output atoms accept `{*}` and `{**}` as anonymous captures. They match
one or more non-`/` bytes and one or more bytes including `/`, respectively.
Each anonymous group occupies its source-order position and does not create a
named value in the rule scope.

The rule body binds `captures`, a list of every output capture's text in
source order, counting named and anonymous groups alike. Positional captures
are read as `captures.0`, `captures.1`, and so on: `$captures.0` in a Kash body
and `@{captures.0}` in a foreign-shell recipe. A named capture keeps its named
binding too. A named capture called `captures` shadows the list.

```kame
rule ./build/{**}/{*}.o : ./src/{_0}/{_1}.c
	mkdir -p ./build/$captures.0
	cc -c $inputs.0 -o $outputs.0
```

Named captures keep their equality rule: repeated occurrences of a name must
match the same text. Anonymous groups are independent, even when adjacent, and
use the same leftmost-shortest search as named groups. Formatting preserves the
anonymous spelling exactly.

## Positional input references

An input atom may refer to any output capture by zero-based index with `{_N}`.
The index counts every capture group in output source order, including named
and anonymous groups. A named output capture can therefore be used by either
its name or its positional index. Repeated occurrences of one named capture
each occupy a position while retaining their equality constraint.

An out-of-range positional reference in a rule input renders as an empty
string, matching the behavior for an unresolved named input reference. `_N`
must use canonical decimal notation: `{_0}` is positional; `{_00}`, signs and
non-digits are not positional forms. Such names remain available as ordinary
named captures when declared in an output atom. A positional reference in an
output atom is `PARSE_ERR`, because output atoms are matchers.

## Runtime construction

`pattern TEXT` accepts one string and returns a pattern value built from its
text, classified as in `014-patterns.md`:

- Text with glob syntax or matcher groups yields a `PathPattern`, in which
  `{name}` captures as `{name:*}`.
- Text with only reference groups yields a `PathTemplate`.
- Text with neither yields a `PathPattern` that matches only its literal text.

The text need not begin with a path root: `pattern "{name:*}.txt"` matches the
text of string subjects such as `"notes.txt"`. Malformed groups, malformed
classes or regexes, and a positional reference `{_N}` alongside glob syntax or
matcher groups are rejected with `PAT_INVALID`. Strings remain strings unless
this operation is called; no operation interprets ordinary values as patterns
implicitly. Operations requiring a matcher or a reference may reject a valid
pattern of the other kind for that use.

```kame
source-pattern = pattern (str "./" "{" "name:*" "}.c")
```

## Regular-expression capture groups

A matcher group may use `{name:~REGEX}` or `{~REGEX}`. The optional name follows
the capture-name grammar. A regex matcher occupies one positional capture slot;
named occurrences retain the equality constraint, and unnamed occurrences are
independent. Regex matching is against the complete capture, which is
non-empty. The surrounding pattern remains anchored and leftmost-shortest,
including when several regex captures can divide the same subject in multiple
ways.

Inside a path atom, a group extends to its closing `}`: `(`, `)`, `|`, `,` and
`[`…`]` within a regex group do not end the atom. A group cannot contain
unescaped whitespace, `{` or `}`.

The portable regex grammar is byte-oriented ASCII syntax: literal bytes;
`.`, matching any byte except `/`; bracket classes `[abc]`, ranges `[a-z]`,
and negation `[!abc]`; grouping with `(…)`; alternation with `|`; and the
quantifiers `?`, `*`, and `+`. A backslash quotes the next byte. Empty
alternatives, empty groups, stacked quantifiers, lookaround, counted
repetition, and Unicode-property escapes such as `\p{Letter}` are invalid.
Backslash quotes exactly the next byte, so `\1` matches a literal `1`; it does
not introduce a backreference. The regex group itself must be non-empty even
when its expression could match the empty string. Invalid regex syntax in a path
atom is `PARSE_ERR`, and in `pattern TEXT` it is `PAT_INVALID`; both point to
the authored regex byte that is invalid.

Evaluation has a portable budget of 1,000,000 NFA state steps across one full
pattern match, including candidate capture boundaries. Exceeding it fails with
`PAT_LIMIT` rather than consuming unbounded time.

## Regex operations and processors

`regex-match PATTERN SUBJECT` requires a `PathPattern` containing at least one
regex matcher and a string or `Path` subject. It returns `nil` on no match,
otherwise a record with `text` (the complete matched subject text), `captures`
(a list of every capture in source order), and `named` (a record mapping each
distinct named capture to its text). Captures are strings; empty named values
are not possible because captures are non-empty. Repeated named matchers
contribute each source slot to `captures`, while `named` contains their single
equality-checked value.

`regex-group INDEX MATCH` selects a positional entry from the `captures` list,
where `INDEX` must be an integer. A negative or out-of-range index returns
`nil`; a non-integer index or non-match input is `EXPR_INVALID`.
`regex-group NAME MATCH` selects from the `named` record; an absent name returns
`nil`, and a non-string name or non-match input is `EXPR_INVALID`. These
processors do not coerce their index, name, or match result. Both take the
match record last, so they compose with `|>`:

```kame
version = "v1.24.3" |> regex-match (pattern "v{major:~[0-9]+}.{~[0-9]+}.{~[0-9]+}")
minor = version |> regex-group 1
```

`resub PATTERN EXPANSION SUBJECT` uses the expansion rules of `subst`: a string
expansion is constant, while a template expansion must be a `PathTemplate` with
references and no matchers. The subject must be a string, a `Path`, or a list of
those. A nonmatching scalar or list item produces `nil`, missing references
fail with `PAT_INVALID`, and invalid kinds fail with `EXPR_INVALID`. This
nil-on-nonmatch contract is separate from `subst`, which preserves nonmatches
(`041-pattern-predicates-and-substitution.md`).

All results and retained captures are invocation-owned and released on success,
diagnostic, cancellation and list-item failure. The regex parser and matcher
are shared portable code; diagnostics, capture ordering, formatting and
substitution results must agree across native, WASM CLI and evaluator tests.

## Acceptance

- `./{**}/{*}.c` parses, formats idempotently, and matches `./src/demo.c` with
  anonymous captures `src`, then `demo`.
- Anonymous captures are independent and leftmost-shortest; named repeated
  captures still require equal text.
- `rule ./{**}/{*}.c : ./{_0}/{_1}.h` maps the selected target `./src/demo.c`
  to input `./src/demo.h` on native and WASM hosts.
- Positional references work when the corresponding output captures are named,
  and their indexes count repeated named groups as separate source positions.
- Rule bodies read anonymous captures through `captures.N` in Kash words,
  expressions and foreign-shell recipes.
- Named-only rule matching, input rendering, cache identity, planning,
  diagnostics and source formatting are unaffected by these extensions.
- `pattern (str "./" "{" "name:*" "}.c")` returns a `PathPattern` that can be
  passed to `capture`, `match?` and `subst`; reference-only text returns a
  `PathTemplate`; malformed runtime patterns produce `PAT_INVALID`.
- `{name:~[a-z]+}` and `{~(ab|cd)+}` parse in path atoms and rule outputs,
  preserve their spelling when formatted, and expose each non-empty capture by
  name and source-order index.
- `regex-match PATTERN SUBJECT`, `regex-group INDEX-OR-NAME MATCH`, and
  `resub PATTERN EXPANSION SUBJECT` have identical capture, substitution,
  invalid-input and budget behavior on native and WASM.
