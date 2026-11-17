---
revision: 2
---

# Pattern predicates and substitution

## Purpose

`match?` tests whether a complete string or path matches a pattern. `sub`
rewrites matching subjects and preserves nonmatches. Testing and rewriting are
separate operations: substitution must not serve as an implicit filtering
predicate.

This specification refines [the standard library](007-library.md),
[placeholder sections and patterns](014-patterns.md), and
[pattern extensions](029-pattern-extensions.md). It does not change pattern
grammar, rule matching, `capture`, `bind`, `regex-match`, `regex-group`, or
`resub`.

## Operation surface

| Form | Result |
| --- | --- |
| `match? PATTERN SUBJECT` | `true` or `false` |
| `match? PATTERN` | Unary matching predicate |
| `sub FROM TO TEXT` | Literal substitution with three string arguments |
| `sub PATTERN EXPANSION SUBJECT` | Pattern substitution |
| `sub PATTERN EXPANSION` | Unary substitution callable |

Every form takes its subject last, so it composes with `|>`:
`path |> match? ./src/{**}.c` and `sources |> sub ./src/{**}.c ./build/{_0}.o`.

## Matching predicate

`PATTERN` must be a `PathPattern`, written as a path atom or constructed with
`pattern TEXT`; strings are not implicitly converted to patterns. A
`PathTemplate` or a malformed pattern reports `PAT_INVALID`. Any other
first-argument kind reports `EXPR_INVALID`.

Matching is anchored to the entire subject text and uses the shared pattern
engine, including glob syntax, anonymous and named captures, recursive
captures, repeated-name equality, leftmost-shortest matching, non-empty
captures, and regex groups. `match?` returns an actual boolean, not a capture
record, replacement value, or `nil`. Captures do not introduce bindings into the
caller's scope; use `capture` (`014-patterns.md`) to obtain them.

The subject must be a string or a `Path`. `nil`, lists, patterns, and other
kinds report `EXPR_INVALID`; there is no implicit coercion or list lifting. Use
`map` to obtain a list of booleans, or `filter`/`filter-out` to select
subjects.

The one-argument form validates and retains its pattern when the predicate is
constructed and returns a callable of arity one. Applying it has the same
behavior as the two-argument form. Collection operations take the predicate
first and the collection last: `filter PREDICATE LIST`.

```kame
./foo.txt |> match? ./{*}.{*}        # true
./foo |> match? ./{*}.{*}            # false
./.txt |> match? ./{*}.{*}           # false: captures cannot be empty
[./foo.txt, ./foo] |> filter-out (match? ./{*}.{*})   # [./foo]
```

Pattern matching itself is pure, requires no filesystem capability, and creates
no dependencies. Evaluating the operands may independently require capabilities
or register dependencies. Exceeding the shared regex matching budget reports
`PAT_LIMIT`, never `false`.

## Substitution

### Literal form

`sub FROM TO TEXT` with three strings replaces all literal occurrences of
`FROM` in `TEXT`. All arguments are evaluated eagerly. This form does not
perform pattern matching or expose captures, and a text with no occurrences is
returned unchanged. Wrong literal operand types or a missing literal subject
report `EXPR_INVALID`.

### Pattern form

A `PathPattern` as the first argument selects anchored pattern substitution.
The expansion is evaluated only after a successful match, with the pattern's
captures bound in an invocation-local scope. Expansion references, result
kinds, scalar text conversion, lexical scope, effect handling, and diagnostics
from `014-patterns.md` apply.

For a string or `Path` subject:

- A match produces the expansion's result.
- A nonmatch returns the original subject exactly, without normalization,
  coercion, change of kind, or evaluation of the expansion.
- An invalid expansion or failed selected expansion reports its diagnostic;
  it is not treated as a nonmatch.

For a list subject, every member must be a string or a `Path`. Substitution
preserves input order and cardinality, rewrites each matching member, and
retains every nonmatching member unchanged. An empty list produces an empty
list. A `nil` subject produces `nil`; other subject kinds and other list member
kinds report `EXPR_INVALID`.

The two-argument form returns a callable of arity one and defers expansion until
each matching invocation. Captures must not leak between invocations or shadow
caller bindings after the call. Deferred callables support capture-scoped
expansion expressions, not merely constant expansion values.

```kame
sub ./journal/{name:*} ./{name} ./journal/foo
# ./foo
sub ./journal/{name:*} ./{name} ./other/foo
# ./other/foo
sub ./journal/{name:*} ./{name} [./journal/foo, ./other/foo]
# [./foo, ./other/foo]
sub ./journal/{name:*} missing ./other/foo
# ./other/foo
```

Substitution must propagate matcher budget failures as `PAT_LIMIT`; it must not
return the original subject after an unsuccessful match caused by an error.
Callable state and temporary captures are invocation-owned and released on
success, rejection, diagnostic, or cancellation. Native and WASM behavior must
agree, including suspension and replay of selected expansion effects.

## Composition

```kame
product-by-name = journal
	|> sub ./journal/{name:*} ./{name}
	|> filter-out (match? ./{*}.{*})
```

For `journal = [./journal/foo, ./journal/bar.txt]`, this produces `[./foo]`.
Substitution leaves unrelated paths unchanged; it does not select only journal
paths. Callers requiring that restriction filter explicitly with
`filter (match? ./journal/{*})` before substitution.

A substitution result is never a filtering predicate: both a rewritten value
and an unchanged subject are truthy, so selecting matches uses `match?`.
Removing `nil` entries after substitution does not remove nonmatches. `resub`
keeps its separate nil-on-nonmatch contract (`029-pattern-extensions.md`).

The operation version of `sub` participates in cached-task identity, so cached
results computed under a different substitution contract are not reused.

## Acceptance

- Direct and unary `match?` calls return identical booleans for matching and
  nonmatching subjects, including glob, regex, recursive, and repeated-name
  patterns, for both string and `Path` subjects.
- Anchoring and non-empty captures distinguish `./foo.txt`, `./foo`, and
  `./.txt` as shown above; captures do not leak into caller scope.
- Predicate callables work with `map`, `filter`, and `filter-out` in
  `OP PREDICATE LIST` order and with `|>`; invalid arity and subject kinds are
  rejected; a `PathTemplate` predicate pattern is `PAT_INVALID`.
- Direct and callable `sub` preserve nonmatching subjects byte-for-byte and
  kind-for-kind, and list substitution preserves order and length without
  inserting `nil` entries.
- Literal substitution, empty lists, and `nil` subject passthrough retain their
  specified behavior.
- Unselected expansions are not evaluated; selected capture-scoped expressions
  retain lexical bindings and execute effects once across suspension and replay.
- Invalid patterns, missing selected references, invalid list members, and
  matcher budget failures remain diagnostics rather than nonmatch results.
- The composition example produces `[./foo]` from the stated journal sources.
- Native and WASM agree, formatting is idempotent, cached results from a
  different operation version are not reused, and the separate `resub` contract
  is unchanged.
