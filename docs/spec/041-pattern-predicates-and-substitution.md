# Pattern predicates and substitution

## Purpose

`match?` tests whether a complete string matches a Kame pattern. `sub` rewrites
matching strings and preserves nonmatches. Testing and rewriting are separate
operations: substitution must not serve as an implicit filtering predicate.

This specification refines [the standard library](007-library.md),
[expression patterns](014-patterns.md), and
[pattern extensions](029-pattern-extensions.md). Its substitution contract
supersedes the earlier requirement that pattern `replace` return `:nil` on a
nonmatch. It does not change pattern grammar, rule matching, `regex-match`,
`capture`, or `regex-replace`.

## Operation surface

| Form | Result |
| --- | --- |
| `(match? MATCH-PATTERN TEXT)` | `:true` or `:false` |
| `(match? MATCH-PATTERN)` | Unary matching predicate |
| `(sub TEXT FROM TO)` | Literal substitution with three string arguments |
| `(sub MATCH-PATTERN EXPANSION SUBJECT)` | Pattern substitution |
| `(sub MATCH-PATTERN EXPANSION)` | Unary substitution callable |

`replace` is a deprecated alias for `sub`, with the same signatures, evaluation
rules, results, diagnostics, and callable behavior. It does not preserve the
old nonmatch-to-nil behavior. Deprecation is documented; calling the alias does
not emit runtime warnings or add output. Formatting preserves the authored
operation name rather than silently migrating it.

## Matching predicate

`MATCH-PATTERN` must be a pattern value containing at least one matcher group
and no expansion references. Pattern literals and `(pattern TEXT)` construction
are supported; ordinary strings are not implicitly converted to patterns.
Malformed or inappropriate patterns report `PAT_INVALID`. A non-pattern first
argument reports `EXPR_INVALID`.

Matching is anchored to the entire subject and uses the shared pattern engine,
including anonymous and named captures, recursive captures, repeated-name
equality, leftmost-shortest matching, non-empty captures, and regex groups.
`match?` returns an actual boolean, not a capture record, replacement string,
or nil. Captures do not introduce bindings into the caller's scope.

The subject must be a string. Nil, lists, patterns, and other kinds report
`EXPR_INVALID`; there is no implicit coercion or list lifting. Use `map` to
obtain a list of booleans, or `filter`/`filter-out` to select subjects.

The one-argument form validates and retains its pattern when the predicate is
constructed and returns a callable of arity one. Applying it has the same
behavior as the two-argument form. Both collection callback argument orders
remain supported.

```kame
(match? ./{*}.{*} "./foo.txt") ; :true
(match? ./{*}.{*} "./foo")     ; :false
(match? ./{*}.{*} "./.txt")    ; :false: captures cannot be empty
(filter-out (match? ./{*}.{*}) ["./foo.txt" "./foo"])
; ["./foo"]
```

Pattern matching itself is pure, requires no filesystem capability, and creates
no dependencies. Evaluating the operands may independently require capabilities
or register dependencies. Exceeding the shared regex matching budget reports
`PAT_LIMIT`, never `:false`.

## Substitution

### Literal form

`(sub TEXT FROM TO)` with three strings replaces all literal occurrences of
`FROM` in `TEXT`, using the existing literal `replace` contract. All arguments
are evaluated eagerly. This form does not perform pattern matching or expose
captures, and a text with no occurrences is returned unchanged.

### Pattern form

A match pattern as the first argument selects the existing anchored pattern
replacement mechanism. The expansion is evaluated only after a successful
match, with the pattern's captures bound in an invocation-local scope. Existing
expansion references, scalar text conversion, lexical scope, effect handling,
and diagnostics from `014-patterns.md` remain applicable.

For a string subject:

- A match produces the expansion's string result.
- A nonmatch returns the original string exactly, without normalization,
  coercion, or evaluation of the expansion.
- An invalid expansion or failed selected expansion reports its diagnostic;
  it is not treated as a nonmatch.

For a list subject, every member must be a string. Substitution preserves input
order and cardinality, rewrites each matching member, and retains every
nonmatching member unchanged. An empty list produces an empty list. A nil
subject retains the existing nil passthrough contract; other subject kinds and
non-string list members report `EXPR_INVALID`.

The two-argument form returns a callable of arity one and defers expansion until
each matching invocation. Captures must not leak between invocations or shadow
caller bindings after the call. The `sub` name and deprecated `replace` alias
must both support capture-scoped deferred expressions, not merely constant
expansion values.

```kame
(sub ./journal/{name:*} ./{name} "./journal/foo") ; "./foo"
(sub ./journal/{name:*} ./{name} "./other/foo")   ; "./other/foo"
(sub ./journal/{name:*} ./{name} ["./journal/foo" "./other/foo"])
; ["./foo" "./other/foo"]
(sub ./journal/{name:*} missing "./other/foo")   ; "./other/foo"
```

Substitution must propagate matcher budget failures as `PAT_LIMIT`; it must not
return the original subject after an unsuccessful match caused by an error.
Callable state and temporary captures are invocation-owned and released on
success, rejection, diagnostic, or cancellation. Native and WASM behavior must
agree, including suspension and replay of selected expansion effects.

## Composition and migration

```kame
PRODUCT_BY_NAME =
  (filter-out (match? ./{*}.{*})
    (map (sub ./journal/{name:*} ./{name}) SOURCES_JOURNAL))
```

For `SOURCES_JOURNAL = ["./journal/foo" "./journal/bar.txt"]`, this produces
`["./foo"]`. Substitution leaves unrelated paths unchanged; it does not select
only journal paths. Callers requiring that restriction must explicitly filter
with `(match? ./journal/{*})` before substitution.

New documentation and examples use `sub`. Existing `replace` calls continue to
resolve but must account for the changed nonmatch result. In particular, filters
using a pattern replacement as a predicate must migrate to `match?`: both a
replacement string and an unchanged string are truthy. Removing nil entries
after substitution no longer removes nonmatches. `regex-replace` retains its
separate nil-on-nonmatch contract.

The semantic change must invalidate cached operation results computed under
the old `replace` contract. Renaming alone is not sufficient because callers
may continue to use the deprecated alias.

## Acceptance

- Direct and unary `match?` calls return identical booleans for matching and
  nonmatching subjects, including regex, recursive, and repeated-name patterns.
- Anchoring and non-empty captures distinguish `./foo.txt`, `./foo`, and
  `./.txt` as shown above; captures do not leak into caller scope.
- Predicate callables work with `map`, `filter`, and `filter-out` in either
  supported callback order; invalid arity and subject kinds are rejected.
- Direct and callable `sub` preserve nonmatching strings byte-for-byte, and
  list substitution preserves order and length without inserting nil entries.
- Literal substitution, empty lists, and nil subject passthrough retain their
  specified behavior.
- Unselected expansions are not evaluated; selected capture-scoped expressions
  retain lexical bindings and execute effects once across suspension and replay.
- `replace` and `sub` agree for every supported form, including deferred
  expansion; alias calls add no warning output.
- Invalid patterns, missing selected references, invalid list members, and
  matcher budget failures remain diagnostics rather than nonmatch results.
- The product example produces `["./foo"]` from the stated journal sources.
- Native and WASM agree, formatting is idempotent, old operation-cache entries
  are not reused, and the separate `regex-replace` contract is unchanged.
