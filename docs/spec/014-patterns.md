---
revision: 2
---

# Placeholder Sections and Patterns

## Purpose

This specification defines two expression-language features that remove common
boilerplate from build scripts:

- Placeholder sections: `(f _ __ ___)` is equivalent to `(a b c => f a b c)`,
  giving lambda shorthand without naming parameters.
- Typed path patterns: path atoms containing glob syntax or groups are
  first-class `PathPattern` and `PathTemplate` values. `capture`, `bind` and
  `subst` match and rebuild paths, so
  `sources |> subst ./{**}/{*}.c ./build/{_0}/{_1}.o` rewrites paths positionally.

Path patterns use the group grammar of rule headers (`004-language.md`), so one
matching model serves rules, `match` arms and expressions. Sections lower to
lexical functions, so no new evaluation machinery is introduced.
`029-pattern-extensions.md` adds regex groups, anonymous rule captures and
runtime construction; `041-pattern-predicates-and-substitution.md` defines
`match?` and the full `subst` surface.

## Placeholder Sections

### Placeholders

A placeholder is a dedicated atom of one of these forms:

```text
PLACEHOLDER = "_" UNDERSCORES | "_" DIGITS
UNDERSCORES = "_"*
DIGITS      = digit digit*          ; canonical decimal, no leading zero
```

A run of k underscores has index `k - 1`: `_` is index 0, `__` is index 1,
`___` is index 2. An underscore followed by decimal digits without a leading
zero has that index: `_0` is index 0, `_1` is index 1, `_12` is index 12.
`_` and `_0` are the same placeholder. Indexes run from `_0` through `_4095`;
a larger index is rejected before allocation.

A name that merely starts with an underscore is not a placeholder: `_0x`,
`_00`, `_file`, and `_` inside a longer identifier remain ordinary names.

A placeholder is valid only inside a section or at the top level of a `|>`
stage (see "Pipes"). Anywhere else it is `PARSE_ERR`.

### Section Form

A parenthesized group is a **placeholder section** when its top level contains
a placeholder:

```kame
(f _)
(f _ __ ___)
(f _0 _1 _2)
(_)
(map _ sources)
```

The first four are respectively equivalent to:

```kame
(a => f a)
(a b c => f a b c)
(a b c => f a b c)
(a => a)
```

Recognition rules:

- The top level of a group is the expression inside its parentheses, excluding
  nested parenthesized groups, lambdas, lists, records, `$(…)` and `$?(…)`.
  Interpolations in string and path atoms written directly in the group are
  part of its top level.
- A nested parenthesized group owns the placeholders at its own top level:
  `(map _ (nth 0 _))` is `(x => map x (i => nth 0 i))`.
- A placeholder inside a list, record, lambda, `$(…)` or `$?(…)` belongs to no
  enclosing section. It is `PARSE_ERR` unless a parenthesized group inside that
  construct owns it: `(f [_, 1])` is `PARSE_ERR`, `(f [(g _), 1])` is not.
- Placeholders at the top level of a `|>` stage belong to that stage, not to an
  enclosing group: `(xs |> f _ 1)` is a grouped pipeline, not a section.
- A group with no top-level placeholder is ordinary grouping: `(f a)` is the
  application `f a`, and `f ()` is a zero-argument call.

### Semantics

The section arity is one plus the highest placeholder index at its top level:
`(f _)` has arity 1, `(f _1)` has arity 2 and ignores its first argument,
`(f __)` has arity 2. Gaps in `_N` count toward the 4096-parameter limit. A
repeated index reuses the same argument: `(cons _ _)` is `(a => cons a a)`.

A section lowers to an ordinary lexical function with that arity and the group
expression as its body. Calling it uses the existing function-call machinery
(`005-evaluation.md`), so too many arguments are `EXPR_INVALID` and missing
arguments read as `nil`.

A placeholder atom is a dedicated syntactic form, not a name reference.
Evaluating it reads the argument at its index from the owning section call.
Name lookup never occurs, so a user definition named `_0` cannot shadow a
placeholder.

Sections are first-class callable values. They flow through `map`, `filter`,
`apply`, and definition bindings like any lexical function:

```kame
xs |> filter (ends? ".c" _)     # filter (s => ends? ".c" s) xs
pairs |> map (join __ _)            # map (a b => join b a) pairs
```

### Pipes

`VALUE |> STAGE` evaluates `VALUE` once. If the stage's top level contains `_`,
each `_` is replaced by the value; otherwise the value is appended as the last
argument. Only `_` is valid at a stage's top level; `__`, `___` and `_N` there
are `PARSE_ERR`. Parenthesized groups inside a stage are sections of their own:
`xs |> filter (ends? ".c" _)` filters `xs`.

Pattern substitution accepts a list subject, so no extra section is needed:

```kame
sources |> subst ./{**}/{*}.c ./build/{_0}/{_1}.o
```

is `subst ./{**}/{*}.c ./build/{_0}/{_1}.o sources`, preserving nonmatching
entries under `041-pattern-predicates-and-substitution.md`. `{_0}` and `{_1}`
are path-template references, not placeholders.

### Formatting

Canonical formatting preserves the section form: a section never expands to an
explicit parameter list, and a pipe never rewrites into a nested call.
Placeholder atoms keep their authored spelling (`_`, `__`, `___` or `_N`).
Parsing formatted output produces an equivalent AST, and formatting is
idempotent.

## Path Kinds

### Kinds

A path atom (055 §3.4) evaluates to one of three kinds:

| Kind | Example | Meaning |
| --- | --- | --- |
| `Path` | `./src/main.c`, `@{dir}/x.o` | One location |
| `PathPattern` | `./src/**/*.c`, `./src/{path:**}.c` | Matches paths and strings; may capture |
| `PathTemplate` | `./build/{path}.o` | Builds a path from captures |

Classification is syntactic and uses only the authored static text of the atom:

- Glob syntax outside a group (`*`, `**`, `?`, a `[…]` character class), or one
  or more matcher groups: a `PathPattern`.
- One or more reference groups (`{name}`, `{_N}`) and no glob syntax or matcher
  group: a `PathTemplate`.
- Neither: a `Path`.

Rule headers and `match` arms assign roles instead of using this
classification: rule output atoms and arm patterns are matchers, and rule input
atoms are templates over the output captures (`004-language.md`, 055 §10.1).

Kinds never convert implicitly. A path, pattern or template is never a string,
and a string is never reclassified as a path or pattern: quoted strings are
always text, including strings containing braces. `pattern TEXT`
(`029-pattern-extensions.md`) is the explicit boundary from text to a pattern
or template.

### Group Grammar

```text
GROUP     = "{" ( CAPTURE ( ":" MATCHER )? | ANON | "_" DIGITS ) "}"
ANON      = "*" | "**" | "~" REGEX
CAPTURE   = (letter | "_") (letter | digit | "_" | "-")*
MATCHER   = GLOB-ITEM+ | "~" REGEX
GLOB-ITEM = "*" | "**" | "?" | CLASS | ESCAPED | LITERAL
```

`MATCHER` cannot contain an unescaped `{`, `}`, or `:`. `ESCAPED` is `\`
followed by one byte. A character class is an optional leading `!` followed by
one or more literal bytes or ascending `a-z` ranges and a closing `]`. Regex
groups (`~REGEX`) are specified in `029-pattern-extensions.md`.

| Form | Role | Meaning |
| --- | --- | --- |
| `{*}` | matcher | anonymous capture of one or more non-`/` bytes |
| `{**}` | matcher | anonymous capture of one or more bytes including `/` |
| `{name:*}` | matcher | named capture of one or more non-`/` bytes |
| `{name:**}` | matcher | named capture of one or more bytes including `/` |
| `{name:GLOB}` | matcher | named capture of the glob match, such as `{name:?[a-z]*}` |
| `{name}` | matcher in a `PathPattern`, reference in a `PathTemplate` | `{name:*}` when matching; the capture named `name` when building |
| `{_N}` | reference | positional reference to capture N, canonical decimal |

Within a `PathPattern`, `{name}` captures exactly as `{name:*}` does, matching
rule-output behavior. A positional reference `{_N}` in a `PathPattern` is
`PARSE_ERR`: a pattern cannot both match and refer to captures.

Glob syntax outside groups matches but does not capture: `*` matches one or
more non-`/` bytes, `?` exactly one non-`/` byte, `**` one or more bytes
including `/`, and a class one byte from the class. Only groups occupy capture
positions.

`\` escapes the following byte inside a path atom; `\{`, `\}`, `\*`, `\?` and
`\[` produce literal bytes. Brace groups inside a path atom are always pattern
groups, never record literals. An unclosed, empty or otherwise malformed group
or class is `PARSE_ERR`; it never falls back to literal text.

### Interpolation

Path atoms may interpolate literal prefixes and suffixes around fully authored
groups: `@{root}/{name:*}.@{ext}` is a `PathPattern` and `@{out}/{name}` is a
`PathTemplate`. `@{…}` is evaluated when the atom is evaluated; `{name}` stays
a deferred capture or reference. Inserted bytes are literal pattern data:
braces, backslashes and glob or regex punctuation in an inserted value cannot
inject groups, globs or escapes. Group names and matcher/regex syntax must
occupy one authored static segment: interpolation inside `{…}` is `PARSE_ERR`.
Deliberately constructed syntax uses the explicit `pattern TEXT` boundary.
Computed paths retain the explicit root, text-only, no-NUL and suspension rules
of `038-direct-kash-and-values.md`.

Evaluating a `PathPattern` never enumerates the filesystem. Discovery is the
explicit `files PATTERN` operation (`005-evaluation.md`).

### Match Semantics

Matching uses rule-target semantics from `004-language.md`:

- Matching is anchored to the complete subject text.
- Captures are indexed left to right in source order, counting named and
  anonymous groups alike; glob syntax outside groups occupies no index.
- Adjacent variable-width groups and wildcards use leftmost-shortest matching.
- Reusing one capture name requires every occurrence to match the same text.
- Captures are never empty.

A pattern matches a string subject's text or a `Path` subject's authored path
text. Other subject kinds are `EXPR_INVALID`.

### Expansion Semantics

A `PathTemplate` renders its literal text and resolves each reference group:

- `{_N}` resolves to the capture at index N.
- `{name}` resolves to the capture named `name`.
- A reference to a missing capture is `PAT_INVALID` at invocation.
- Escaped bytes render literally.
- Captured values are inserted as literal text; they never introduce groups,
  globs or escapes.

### Value Kind

`PathPattern` and `PathTemplate` values carry their canonical text and parsed
groups and are immutable. `fmt` and value display write them as their path atom
(or `pattern "TEXT"` for a rootless pattern), so they re-evaluate with the same
kind. In string interpolation, template output, argv and rule-input rendering
they render as their canonical text. Scalar `str` rejects them, and operations
that expect strings reject them with `EXPR_INVALID`. Cache fingerprinting
encodes their kind and text. They are not JSON-compatible strings.

## Capture and Bind

```kame
captures = ./src/lib/math/vector.c |> capture ./src/{path:**}/{name}.c
# {path: "lib/math", name: "vector"}
output = captures |> bind ./build/{path}/{name}.o
# ./build/lib/math/vector.o
```

`capture PATTERN SUBJECT` matches a `Path` or string subject against a
`PathPattern`. On a match it returns a record mapping each distinct named
capture to its matched string, in first-occurrence source order; anonymous
captures match but contribute no field. On no match it returns `nil`. Repeated
named captures must agree for the match to succeed. A `PathTemplate` or `Path`
pattern operand is `PAT_INVALID` and `EXPR_INVALID` respectively; a subject of
another kind is `EXPR_INVALID`.

`bind TEMPLATE CAPTURES` expands a `PathTemplate` against a record of captures
and returns a `Path`; a rootless template built with `pattern` returns a
string. `{name}` resolves the field `name`. Field values must be strings,
numbers or paths and render as their text without NUL bytes; other kinds are
`EXPR_INVALID`. An absent field, or a positional reference `{_N}`, which a
capture record cannot satisfy, is `PAT_INVALID`. A non-template operand is
`EXPR_INVALID`. Extra fields are ignored.

Both operations are pure: they perform no filesystem access, create no
dependencies, and keep path kinds.

## Substitution

`subst` is the substitution operation; the complete operation surface,
including `match?` and nonmatch preservation, is in
`041-pattern-predicates-and-substitution.md`. It has two modes:

- `subst FROM TO TEXT` with plain strings replaces every literal `FROM`
  occurrence in `TEXT` with `TO`. All three operands evaluate normally in
  caller scope, left to right. Wrong literal operand types or a missing subject
  report `EXPR_INVALID`; only pattern mode supports a two-argument callable.
- `subst PATTERN REPLACEMENT [SUBJECT]` performs anchored pattern matching and
  evaluates `REPLACEMENT` with the successful match's named captures in scope.
  It is not an eager evaluation of every operand.

Pattern mode first evaluates and validates `PATTERN` in caller scope; it must be
a `PathPattern`. With three operands, it next evaluates `SUBJECT` in caller
scope, without any new capture bindings. The subject contract is:

- A string or `Path` subject produces the replacement result on a match and the
  original subject, unchanged, on no match.
- A list subject maps over its items in order and produces a list of the same
  length; nonmatching items remain unchanged.
- A `nil` subject produces `nil`.

### Capture-scoped replacement expressions

For each successful subject match, create a child of the enclosing lexical
scope and bind each distinct named capture to its matched string. Captures
shadow enclosing bindings only while evaluating `REPLACEMENT`; they do not
leak into the caller, another list item, or a later invocation. Repeated names
retain their equality constraint. Anonymous captures remain accessible through
positional references such as `{_0}` in a template replacement and do not
introduce lexical names.

Only then evaluate `REPLACEMENT`, including its string and path-atom
interpolations, in that scope. Ordinary names and composed expressions can use
captures, and unrelated names follow normal lexical lookup:

```kame
subst ./journal/{name:*} ./{name} "./journal/demo"
subst ./journal/{name:*} ./@{name} "./journal/demo"
subst ./journal/{name:*} "@{name}" "./journal/demo"
subst ./journal/{name:*} name "./journal/demo"
subst ./journal/{name:*} (uppercase name) "./journal/demo"
```

These return the path `./demo`, the path `./demo`, `"demo"`, `"demo"`, and
`"DEMO"`, respectively.

The selected replacement value determines the result:

| Replacement value | Result |
| --- | --- |
| string | Its text, including interpolations already evaluated in capture scope |
| `Path` | The path, including interpolations already evaluated in capture scope |
| `PathTemplate` | Its expansion against this match's named and positional captures, as a `Path` (a string for a rootless template) |
| integer, float, boolean | Its ordinary scalar `str` text |
| any other kind, or a `PathPattern` | `PAT_INVALID` |

No match returns the original subject without evaluating the replacement or
acquiring its dependencies/effects. Nil subjects and empty subject lists
likewise do not evaluate it. Syntax is still parsed in every operand. Unknown
ordinary names in a selected expression report `REF_MISSING`; missing capture
references in a template remain `PAT_INVALID`.

Replacement results are data, not new expression, template or glob source. In
particular, captured metacharacters in `./{name}` or `./@{name}` do not start
filesystem enumeration. Reached replacement reads and effects retain the
caller's capabilities, phase restrictions, suspension and publication policy.

### Deferred replacement callables

With two pattern-mode operands, `subst` returns a callable of arity one that
retains the pattern, the unevaluated replacement expression, and its enclosing
lexical scope. It must not look up capture names, evaluate replacement helpers,
or acquire replacement dependencies merely to construct the callable. Each
invocation applies the same capture-scoped behavior to its subject. The
callable is invocation-owned and follows ordinary closure lifetime/publication
restrictions in `005-evaluation.md`.

Both of these rewrite each source path:

```kame
map (subst ./{**}/{*}.c ./build/{_0}/{_1}.o) sources
sources |> subst ./{**}/{*}.c ./build/{_0}/{_1}.o
```

Named references use capture names:

```kame
subst ./src/{name:*}.c ./build/{name}.o ./src/demo.c
```

produces `./build/demo.o`. Capture names are equally available in a deferred
expression replacement:

```kame
journal = files ./journal/*
product-by-name = journal |> map (subst ./journal/{name:*} "@{name}")
product-paths = journal |> subst ./journal/{name:*} ./{name}
```

An explicit lambda must pass its subject when applying a replacement:
`journal |> map (j => subst ./journal/{name:*} name j)`.

Strings used as the pattern operand never become patterns; matching requires a
`PathPattern` literal or a value from `pattern TEXT`. A plain string first
operand selects literal mode.

## Diagnostics

- A positional reference `{_N}` inside a `PathPattern`, interpolation inside a
  group, and unclosed, empty or malformed groups and classes are `PARSE_ERR`.
- A placeholder outside a section or pipe stage, or `__`/`_N` at a pipe stage's
  top level, is `PARSE_ERR`.
- Invalid argument combinations, a template where a pattern is required, and
  expansion references to missing captures are `PAT_INVALID`.
- Patterns and templates passed to string-expecting operations, and subjects of
  unsupported kinds, are `EXPR_INVALID`.
- No match is not an error: `capture` returns `nil` and `subst` preserves the
  original subject.

Regular-expression groups, regex operations, anonymous rule captures and
runtime pattern construction are specified in `029-pattern-extensions.md`.

## Acceptance Tests

- `(f _)`, `(f _0)`, and `(a => f a)` evaluate identically.
- `(f _1)` has arity 2 and ignores its first argument.
- `(cons _ _)` receives one argument twice.
- `f _` outside a section or stage is `PARSE_ERR`; `_0x` is an ordinary name.
- Nested groups bind their own placeholders; placeholders inside lists, records
  and lambdas belong to no enclosing section.
- `xs |> f _ 1` places `xs` at `_`; `xs |> f 1` appends it; `__` at a stage's
  top level is `PARSE_ERR`.
- Wrong section arity with too many arguments is `EXPR_INVALID`; a missing
  argument reads as `nil`.
- Sections pass through `map`, `apply`, and definitions.
- `./src/main.c` is a `Path`, `./src/**/*.c` and `./{**}/{*}.c` are
  `PathPattern`s (the latter with two positional captures), and
  `./build/{_0}/{_1}.c` is a `PathTemplate`. Kinds never convert implicitly and
  quoted strings with braces stay strings.
- `{name}` in a `PathPattern` captures as `{name:*}`; `{_N}` in a
  `PathPattern` is `PARSE_ERR`.
- Interpolated values in path atoms cannot inject groups or globs.
- `capture ./src/{path:**}/{name}.c ./src/lib/math/vector.c` returns
  `{path: "lib/math", name: "vector"}`; a nonmatch returns `nil`; repeated
  named captures must agree.
- `bind ./build/{path}/{name}.o {path: "lib/math", name: "vector"}` returns
  the `Path` `./build/lib/math/vector.o`; absent captures are `PAT_INVALID`.
- `subst ./{**}/{*}.c ./build/{_0}/{_1}.c ./a/b/x.c` is `./build/a/b/x.c`; a
  nonmatching subject is unchanged.
- A list subject maps in order and retains nonmatching entries.
- Two-argument pattern `subst` returns a callable usable directly and through a
  pipe.
- `subst ./src/{name:*}.c ./build/{name}.o ./src/demo.c` is `./build/demo.o`.
- Named captures resolve in bare-name, interpolated string/path and composed
  replacement expressions, for both direct calls and deferred callables.
- Captures shadow enclosing names only in the replacement, not the subject,
  and never leak across list items or repeated invocations; nested replacements
  restore the outer capture bindings.
- A nonmatch, nil subject and empty subject list skip a replacement containing
  a missing name, failing read or effect; callable construction also skips it.
- Each successful list item evaluates its own replacement once; suspension and
  cancellation retain/release scopes and do not replay accepted effects.
- Replacement results containing braces or glob metacharacters remain data;
  `./{name}` expansion and `"@{name}"` interpolation both work without
  implicit filesystem access.
- String, path, template and numeric/boolean replacements render as specified;
  invalid selected values are `PAT_INVALID`, while missing ordinary expression
  names retain `REF_MISSING`.
- Literal string `subst` replaces every occurrence.
- A pattern renders as its text in interpolation, argv and rule inputs; `str`
  rejects it; `fmt` preserves its kind.
- A string operation given a pattern is `EXPR_INVALID`.
- Formatting sections, pipes and patterns is idempotent and preserves section
  form, pipe form and placeholder spelling.
- Pattern and section tests under `mem.Tracker` leak no values.
