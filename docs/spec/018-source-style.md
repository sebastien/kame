# Canonical Formatting and Source Conventions

## Purpose

Kame source should make its structure visible: what data is declared, which
transformations are available, and what work is performed. Readability takes
priority over minimizing line count. Short expressions remain compact; larger
expressions expose their logical structure rather than becoming a wall of text.

This specification defines two distinct contracts:

- **Canonical formatting** is enforced by the formatter and changes whitespace
  and layout without changing meaning.
- **Source conventions** guide authors in naming, grouping, and organizing code.
  The formatter does not rename identifiers or rearrange declarations.

This specification extends the formatting contract in
[004-language.md](004-language.md#formatting). It specifies the intended standard,
not a claim that all layouts are already implemented.

## Naming Conventions

- Named value declarations use `UPPER_CASE`: `PAGE_NAMES`, `BUILD_DIR`.
- Functions, parameters, and local bindings use `lower-kebab-case`.
- Effectful functions end in `!`: `publish-page!`, `write-report!`. This includes
  functions that perform effects indirectly through other functions. Pure
  transformations do not use the suffix.
- Predicates end in `?`: `valid-page?`, `contains?`. Their result answers a
  yes-or-no question; a function that returns the matching item is not a predicate.
- Record keys use `lower-kebab-case` unless an external data format requires
  another spelling.
- Names describe domain concepts. Prefer `PAGE_OUTPUTS` to vague names such as
  `RESULT` or `DATA` when a more precise name is available.

Here, "symbols" in source organization means named value declarations, not
literal symbol values such as `:else`. Literal symbols retain their domain
spelling; they are not automatically uppercased.

Suffixes communicate intent, not authority or evaluation policy. They do not
grant capabilities, introduce effects, or change lazy evaluation. Existing
standard-library names remain valid even where they predate these conventions.

## File Structure

Organize a file into these sections, omitting sections that are not needed:

1. Includes required by the file.
2. Named value declarations: configuration, discovery, and derived values.
3. Function definitions: transformations, predicates, and effectful helpers.
4. Build rules or executable script statements.

Within each section:

- Group declarations that describe the same domain concept or processing stage.
- Prefer dependencies before their consumers where practical. Lazy declarations
  may refer to functions defined later; the section order is not execution order.
- Separate conceptual groups with a blank line. Do not separate every closely
  related declaration merely to give each one more space.
- Use short section comments when they help readers navigate the file. Explain
  intent or constraints, rather than repeating the following expression.
- Keep a function's implementation together. Prefer a named helper when it
  reveals a useful concept, not solely to avoid a multiline expression.
- Keep rules in a recognizable progression: entry points, production rules,
  then maintenance tasks where applicable.
- Keep executable statements in their intended execution order.

These are authoring conventions. The formatter must not sort declarations,
move includes, group values, invent section comments, or reorder execution.

## General Formatting

- Use LF line endings and a terminal newline for scripts.
- Emit no trailing whitespace outside preserved literal content.
- Use two spaces per expression indentation level.
- Use one tab before recipe content by default, preserving additional
  recipe-owned indentation. Existing recipe indentation options remain valid;
  they do not change the canonical two-space expression layout.
- Use one space between inline elements and around definition `=` or `?=`.
- Target an 80-column line width, including the enclosing indentation and any
  definition prefix. Count Unicode code points, not UTF-8 bytes; tabs advance to
  the next multiple-of-eight column.
- Keep an expression inline when its complete canonical inline representation
  fits and it contains no required physical line breaks.
- Otherwise use its expanded structural layout. A multiline child makes its
  containing expression multiline; do not pack other arguments beside it.
- Do not retain authored expression line breaks merely because they were present.
  Inline and expanded layouts depend on structure and width, not input layout.
- Never split an indivisible token or rewrite literal content to satisfy width.
  Such content may exceed the target width.
- Attach closing delimiters to the final element; do not give them a separate
  line. Empty collections and empty applications remain inline.
- Preserve source-order comments and existing blank-line counts as required by
  004. Do not remove a comment to obtain an inline layout.

The formatter computes layout at the expression's actual starting column.
Special forms use the layouts below; ordinary applications use the application
layout. This is a structural rule, not a list of special cases for library
functions such as `map` and `filter`.

## Expression Layouts

### Let

Short bindings and bodies remain inline:

```kame
(let [name value] (transform name))
```

An expanded `let` puts its bindings and each body expression on separate lines:

```kame
(let
  [name value]
  (prepare name)
  (transform name))
```

An expanded binding list keeps each name and value as a logical pair. Put the
first pair after `[` and align subsequent pairs with it:

```kame
(let
  [page (page-data name)
   sections (sections-of name)
   navigation (section-nav name)]
  (page-payload page sections navigation))
```

Keep a pair inline when it fits. Otherwise put its value on the next line,
indented two spaces beyond the binding name. A multiline value must not cause
the following binding to appear on its closing line.

### Conditional

```kame
(if ready? value fallback)
```

In an expanded conditional, indent the condition and alternative by two spaces,
and the consequent by four spaces:

```kame
(if
  (valid-page? page)
    (page-output page)
  (fail "Invalid page"))
```

Each branch retains its own nested layout. An omitted alternative introduces
neither an empty line nor a placeholder.

### Lambda

```kame
([page] page.name)
```

Expanded lambdas retain parameters on the opening line when they fit and put
each body expression on its own indented line:

```kame
([page]
  (let
    [metadata (page-metadata page)]
    (navigation-item metadata)))
```

If the parameter list itself exceeds the width, expand it as a list while
preserving parameter order and rest-parameter spelling.

### Application

```kame
(map ([page] page.name) PAGES)
```

Expanded applications put the operator on the opening line and each argument
on a separate line, indented two spaces:

```kame
(map
  ([page] (navigation-item page.name (page-url page)))
  PAGES_WITH_PUBLISHED_NAVIGATION_METADATA)
```

The operator can itself be an expression and follows the same nested rules.

### Lists and Records

Short collections remain inline:

```kame
[./src/main.c ./src/util.c]
[name: page.name href: (page-url page.name)]
```

Expanded lists have one item per line; expanded records have one field per
line. Put the opening bracket on its own line and indent entries by two spaces:

```kame
[
  name: page.name
  href: (page-url page.name)
  sections: (sections-of page.name)]
```

Keep a record key and value together when they fit. Otherwise place the value
on the next line, indented two spaces beyond the key. Preserve item and field
order. Binding lists and match clauses use their specialized layouts instead.

### Match

Short matches remain inline. Expanded matches put the subject and each clause
on separate lines. Within an expanded clause, put its pattern after `[` and
its result on the next line, indented two spaces beyond the clause:

```kame
(match
  (basename file)
  ["{name:*}.md"
    [name: name file: file]]
  [:else
    :nil])
```

Keep a clause inline if it fits and its children remain inline. Preserve clause
order; formatting does not change matching precedence.

## Definitions

Short named values and function definitions remain inline:

```kame
PAGE_NAMES = (map page-name PAGES)
(page-url name) = (cat "/" name "/")
```

If a definition does not fit inline, put its value below the operator and
indent it by two spaces. Recompute the value's layout at that column; moving a
value below `=` does not by itself require expanding the value internally.

```kame
(section-nav name) =
  (map
    ([section] (navigation-item section))
    (sections-with-published-navigation-metadata name))
```

Apply the same rule to default definitions using `?=`. Preserve parameter
order, rest parameters, operator spelling, and authored names. If a function
header is itself too long, put parameters on separate two-space-indented lines
and attach `) =` or `) ?=` to the final parameter.

## Language Boundaries

Use these expression layouts in expression sources, value scripts, and build
definitions. They do not replace surrounding language grammars:

- Rule headers and recipes retain their own formatting contracts.
- Kash statements and process pipelines retain their own syntax and ordering.
- Template text and verbatim strings remain literal content, not source to be
  reindented. Inline interpolations use compact expression formatting; they
  must not introduce physical line breaks into their enclosing text.
- Quoted string escaping and interpolation spelling continue to follow 004.

The width target is not permission to rewrite shell text, process semantics,
template output, or string values.

## What Good Looks Like

This illustrative value script shows compact declarations, conceptual groups,
and a larger definition whose structure is visible:

```kame
# Content discovery
CONTENT = (sorted (wildcard ./src/pages/*.md))
PAGES = (map page-data CONTENT)

# Navigation
PAGE_NAMES = (map ([page] page.name) PAGES)
NAVIGATION = (map navigation-item PAGES)

# Page transformations
(page-url name) = (cat "/" name "/")
(publishable? page) = (not page.draft)

(navigation-item page) = [label: page.title href: (page-url page.name)]

(section-nav name) =
  (map
    ([section]
      [
        label: section.metadata.navigation-title
        href: (page-url section.parent.name)
        order: section.order])
    (sections-of name))
```

Helper names in this example are illustrative, not additional library APIs.
An author should be able to scan the named values first, understand
the transformations next, and find rules or execution at the end of the file.

## Acceptance Criteria

- Formatting twice produces byte-for-byte identical output.
- Parsing formatted output produces an equivalent AST, excluding spans.
- Native and WASM hosts produce identical canonical text.
- Golden cases cover inline and expanded forms, nesting, long operators and
  headers, binding pairs, record fields, match clauses, and multiline literals.
- Width-boundary cases cover exactly 80 columns and 81 columns, including
  enclosing indentation, definition prefixes, and non-ASCII text.
- Comments, blank-line counts, literal content, declaration order, parameter
  order, and execution order remain preserved under their existing contracts.
- Formatting does not enforce naming conventions by changing identifiers.
- Formatting does not perform effects or evaluate declarations to choose layout.
