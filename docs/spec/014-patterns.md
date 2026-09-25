# Placeholder Sections and Patterns

## Purpose

This specification adds two expression-language features that remove common
boilerplate from build scripts:

- Placeholder sections: `((f _ __ ___))` is the equivalent of
  `([a b c] (f a b c))`, giving lambda shorthand without naming parameters.
- Pattern values: paths or strings containing match or reference groups become
  first-class values, and `replace` gains a match/expand form so
  `(replace ./{**}/{*}.c ./build/{_0}/{_1}.c)` rewrites paths positionally.

Patterns reuse the target-template grammar of `004-language.md` so one matching
model serves rules and expressions. Sections reuse lexical functions so no new
evaluation machinery is introduced.

## Placeholder Sections

### Placeholders

A placeholder is a name atom of one of these forms:

```text
PLACEHOLDER = "_" UNDERSCORES | "_" DIGITS
UNDERSCORES = "_"*
DIGITS      = digit digit*          ; canonical decimal, no leading zero
```

A run of k underscores has index `k - 1`: `_` is index 0, `__` is index 1,
`___` is index 2. An underscore followed by decimal digits without a leading
zero has that index: `_0` is index 0, `_1` is index 1, `_12` is index 12.
`_` and `_0` are the same placeholder.

A name that merely starts with an underscore is not a placeholder: `_0x`,
`_00`, `_file`, and `_` inside a longer identifier remain ordinary names.

### Section Form

A placeholder section is a parenthesized application whose items are exactly
one expression, where that expression contains at least one placeholder:

```littlemake
((f _))
((f _ __ ___))
((f _0 _1 _2))
((_))
((map _ files))
```

The first four are respectively equivalent to:

```littlemake
([a] (f a))
([a b c] (f a b c))
([a b c] (f a b c))
([a] a)
```

Recognition rules:

- A parenthesized form with zero or more than one item is never a section;
  `((f))` remains a nested call and `(f _)` remains a call passing the value of
  the name `_`.
- The section body is the single item. The scan for placeholders descends the
  whole body subtree but does not descend into a nested section form, so a
  nested section owns its own placeholders: `((map _ ((nth _ 0))))` is
  `([x] (map x ([i] (nth i 0))))`.
- A body with no placeholder is an ordinary nested call, not a section.

### Semantics

The section arity is one plus the highest placeholder index in its body:
`((f _))` has arity 1, `((f _1))` has arity 2 and ignores its first argument,
`((f __))` has arity 2. A repeated index reuses the same argument:
`((cons _ _))` is `([a] (cons a a))`.

A section lowers to an ordinary lexical function with that arity and the body
as its single body expression. Calling it uses the existing function-call
machinery, so too few or too many arguments are `EXPR_INVALID`.

A placeholder atom is a dedicated syntactic form, not a name reference.
Evaluating it reads the argument at its index from the nearest enclosing
section call. Name lookup never occurs, so a user definition named `_0` cannot
shadow a placeholder.

Sections are first-class callable values. They flow through `map`, `filter`,
`apply`, and definition bindings like any lexical function.

### Pipes

Pipe rewriting inspects only direct right-side items for `_` placeholders, so
a placeholder inside a section is never rewritten and the section travels as
one item:

```littlemake
(files | map ((replace ./{**}/{*}.c ./build/{_0}/{_1}.c)))
```

rewrites to `(map (replace ./{**}/{*}.c ./build/{_0}/{_1}.c) files)` where the
two-argument pattern `replace` returns a callable section applied per item.

### Formatting

Canonical formatting preserves the section form: a section never expands to an
explicit parameter list. Placeholder atoms render canonically as `_` plus
their decimal index, so `((f _))` formats as `((f _0))`. Parsing formatted
output produces an equivalent AST, and formatting is idempotent.

## Pattern Values

### Pattern Literals

A pattern literal is a bare path atom or a quoted string without interpolation
whose text contains one or more pattern groups. Interpolated strings are never
patterns. Runtime string values are never reclassified: a string computed by an
expression and containing braces is a plain string.

Group forms are:

| Form | Kind | Meaning |
| --- | --- | --- |
| `{*}` | matcher | anonymous capture of one or more non-`/` bytes |
| `{**}` | matcher | anonymous capture of one or more bytes including `/` |
| `{name:*}` | matcher | named capture of one or more non-`/` bytes |
| `{name:**}` | matcher | named capture of one or more bytes including `/` |
| `{name}` | reference | named expansion reference |
| `{_N}` | reference | positional expansion reference, N canonical decimal |

`\` escapes the following byte inside a pattern literal; `\{` and `\}` produce
literal braces. A path or string containing no brace is never pattern-parsed
and keeps its existing meaning. A pattern mixing matcher groups and reference
groups is a `PARSE_ERR`. Character classes require quoted string patterns:
`[` and `]` delimit list expressions, so a bare path atom ends before them.

Classification:

- One or more matcher groups and no reference groups: a match pattern.
- One or more reference groups and no matcher groups: an expansion pattern.
- No groups: a plain path or string, exactly as before.

`{name}` is a reference in expression patterns; this differs from rule target
templates, where `{name}` is equivalent to `{name:*}`. Rule target templates
are unchanged.

### Match Semantics

Matching reuses target-template semantics from `004-language.md`:

- Matching is anchored to the complete subject.
- Captures are indexed left to right in source order, counting named and
  anonymous captures alike.
- Adjacent variable-width groups use leftmost-shortest matching.
- Reusing one capture name requires every occurrence to match the same text.
- Captures are never empty.

### Expansion Semantics

An expansion pattern renders literal text and resolves each reference group:

- `{_N}` resolves to the capture at index N.
- `{name}` resolves to the capture named `name`.
- A reference to a missing capture is `PAT_INVALID` at invocation.
- Escaped bytes render literally.

### Value Kind

A pattern is a new value kind carrying its canonical text and parsed groups.
Patterns are immutable. `str` renders a pattern as its text, and recipe and
rule-input rendering render a pattern as its text. Patterns serialize as their
text and are cache-safe. Operations that expect strings reject patterns with
`EXPR_INVALID`; converting with `str` is explicit.

## Replace

`replace` accepts two or three arguments:

| Match argument | Expansion argument | Behavior |
| --- | --- | --- |
| string | string | literal replace of all occurrences, unchanged |
| match pattern | string | anchored match; expansion is the constant replacement |
| match pattern | expansion pattern | anchored match; references resolve to captures |
| any other combination | | `PAT_INVALID` |

With three arguments the third is the subject:

- A string subject produces a string on match and `:nil` on no match.
- A list subject maps over its items in order and produces a list of the same
  length; non-matching items produce `:nil` entries.
- A nil subject produces nil.

With two pattern arguments `replace` returns a callable section of arity one
that applies the match/expand behavior to its argument, so both of these
rewrite each source path:

```littlemake
(map (replace ./{**}/{*}.c ./build/{_0}/{_1}.c) sources)
(sources | replace ./{**}/{*}.c ./build/{_0}/{_1}.c)
```

Named references use capture names:

```littlemake
(replace ./src/{name:*}.c ./build/{name}.o "./src/demo.c")
```

produces `./build/demo.o`.

Runtime strings used as the match argument never become patterns; matching
requires a pattern literal or a pattern value.

## Diagnostics

- A pattern that parses cleanly but mixes matcher and reference groups is
  `PARSE_ERR`.
- Structurally invalid group syntax, such as unclosed or empty groups,
  leaves the token a plain path or string with its braces literal. This
  preserves the legacy meaning of braces in paths and strings.
- Invalid argument combinations and expansion references to missing captures
  are `PAT_INVALID`; patterns passed to string-expecting operations are
  `EXPR_INVALID`.
- No match is not an error; the result is `:nil`.

## Deferred

- Anonymous `{*}`, `{**}`, and positional `{_N}` groups in rule target
  templates. One shared pattern grammar makes this a later, small change.
- Pattern construction from runtime text through an operation.
- Regular-expression groups.

## Acceptance Tests

- `((f _))`, `((f _0))`, and `([a] (f a))` evaluate identically.
- `((f _1))` has arity 2 and ignores its first argument.
- `((cons _ _))` receives one argument twice.
- `(f _)` still evaluates the name `_`; `(_0x)` is an ordinary reference.
- Nested sections bind their own placeholders.
- Wrong section arity is `EXPR_INVALID`.
- Sections pass through `map`, `apply`, and definitions.
- `./{**}/{*}.c` classifies as a match pattern with two positional captures;
  `./build/{_0}/{_1}.c` classifies as an expansion pattern.
- `(replace ./{**}/{*}.c ./build/{_0}/{_1}.c "./a/b/x.c")` is
  `./build/a/b/x.c`; a non-matching subject is nil.
- A list subject maps in order and retains nil entries.
- Two-argument pattern `replace` returns a callable section usable directly
  and through a pipe.
- `(replace ./src/{name:*}.c ./build/{name}.o "./src/demo.c")` is `./build/demo.o`.
- Literal string `replace` behaves exactly as before.
- Mixing matcher and reference groups is `PARSE_ERR`; structurally invalid
  groups leave a plain path or string; both-match and
  match-string/expansion-pattern combinations are `PAT_INVALID`.
- A pattern renders as its text in recipes, rule inputs, and `str`.
- A string operation given a pattern is `EXPR_INVALID`.
- Formatting sections and patterns is idempotent and preserves section form.
- Pattern and section tests under `mem.Tracker` leak no values.
