---
revision: 2
---

# Canonical Formatting and Source Conventions

## Purpose

Kame and Kash source should make its structure visible: what data is declared,
which transformations are available, and what work is performed. Readability
takes priority over minimizing line count. Short expressions remain compact;
larger expressions expose their logical structure rather than becoming a wall
of text.

This specification defines two distinct contracts:

- **Canonical formatting** is enforced by `kame do fmt` and changes whitespace
  and layout without changing meaning.
- **Source conventions** guide authors in naming, grouping, and organizing code.
  The formatter does not rename identifiers or rearrange declarations.

This specification refines the formatting contract of `055` §15 and
[004-language.md](004-language.md#10-formatting). It applies to every context of
the grammar: Kame programs (`.km`), Kash programs (`.kash`), and the expressions
embedded in templates (`.kmt`).

## Naming Conventions

- Definitions, functions, parameters, local bindings and record keys use
  `lower-kebab-case`: `page-names`, `build-dir`, `object-path`. Record keys use
  another spelling only when an external data format requires it.
- `UPPER_CASE` names are reserved for environment variables and build-wide
  settings that the build reads from or exports to the environment (`SHELL`,
  `CC`). A bare `ENV-NAME` reference falls back to the environment when no
  lexical binding exists (`055` §6), so naming ordinary values in lowercase
  keeps a misspelled value from silently becoming an environment read.
- Effectful functions end in `!`: `publish-page!`, `write-report!`. This includes
  functions that perform actions indirectly through other functions. Tracked
  observations and pure transformations do not use the suffix. Control effects
  such as `fail!` do. `038-direct-kash-and-values.md` defines the categories;
  naming does not replace runtime effect or capability tracking.
- Predicates end in `?`: `valid-page?`, `contains?`. Their result answers a
  yes-or-no question; a function that returns the matching item is not a predicate.
- Import aliases and namespace names are short lowercase names that identify the
  module: `import ./lib/docker as docker`.
- Names describe domain concepts. Prefer `page-outputs` to vague names such as
  `result` or `data` when a more precise name is available.

Literal symbol values such as `:debug` retain their domain spelling.

Suffixes communicate intent, not authority or evaluation policy. They do not
grant capabilities, introduce effects, or change lazy evaluation. Standard
library names remain valid even where they predate these conventions.

## File Structure

Organize a file into these sections, omitting sections that are not needed:

1. `import` and `from` statements, then `include` statements.
2. `export` statements declaring the module's public interface.
3. Named value definitions: configuration, discovery, and derived values.
4. Function definitions: transformations, predicates, and effectful helpers.
5. Rule, task and service declarations, or executable script statements.

Within each section:

- Group definitions that describe the same domain concept or processing stage.
- Prefer dependencies before their consumers where practical. Lazy definitions
  may refer to functions defined later; the section order is not execution order.
- Separate conceptual groups with a blank line. Do not separate every closely
  related definition merely to give each one more space.
- Use short `#` section comments when they help readers navigate the file.
  Explain intent or constraints, rather than repeating the following expression.
- Keep a function's implementation together. Prefer a named helper when it
  reveals a useful concept, not solely to avoid a multiline expression.
- Keep rules in a recognizable progression: entry points, production rules,
  then maintenance tasks where applicable.
- Keep executable statements in their intended execution order.

These are authoring conventions. The formatter must not sort statements, move
imports or includes, group values, invent section comments, or reorder
execution.

Consumer configuration is an intentional exception to includes-first guidance:
place settings before the `include` whose `?=` defaults they replace, then
append consumer contributions and extra rules afterward. Follow the
[consumer composition contract](004-language.md#89-consumer-composition), rather
than moving an include for visual consistency. Distinguish configurable settings
from derived values in names or comments; an ordinary `=` value definition does
not itself prohibit CLI overrides.

## General Formatting

- Use LF line endings and a terminal newline.
- Emit no trailing whitespace outside preserved literal content.
- Indent with **one tab per block level**: definition bodies, rule bodies,
  control blocks, `match` arms and expression continuation lines each add one
  tab to the indentation of the line that opens them. Spaces never indent a
  block or a continuation line. Blank lines carry no indentation.
- Use one space around `=`, `?=`, `+=`, `=>`, `|>`, `??`, `:` in rule headers,
  and every binary operator: `a + b`, `a == b`, `a and b`, `a / b`. Spaces are
  required around `-` and `/` (`055` §3.2) and canonical around the others.
- Separate inline list elements, record fields, import names and export names
  with `, `. Write record fields as `key: value`, with no space before `:`.
- Expanded lists use newlines between elements. Expanded records end each
  field with a comma. Process groups never take a trailing comma.
- Write no space inside brackets, braces or parentheses: `[a, b]`, `{a: 1}`,
  `(x => x + 1)`. Empty collections are `[]` and `{}`.
- Write zero-argument calls as `f ()`, with one space before `()`.
- Write field access, spreads, qualified names and placeholders without inner
  spaces: `page.name`, `...flags`, `std/map`, `_1`.
- Block headers (`def`, `if`, `elif`, `else`, `for`, `while`, `match`, rule
  headers) have no trailing `:`.
- Target an 80-column line width, including the enclosing indentation and any
  definition prefix. Count Unicode code points, not UTF-8 bytes; tabs advance to
  the next multiple-of-eight column.
- Keep an expression inline when its complete canonical inline representation
  fits and it contains no required physical line breaks.
- Otherwise use its expanded structural layout. A multiline child makes its
  containing construct multiline; do not pack other elements beside it.
- Do not retain authored expression line breaks merely because they were present.
  Inline and expanded layouts depend on structure and width, not input layout.
- Never split an indivisible token or rewrite literal content to satisfy width.
  Such content may exceed the target width.
- Preserve source-order comments and existing blank-line counts as required by
  004. Do not remove a comment to obtain an inline layout.

The formatter computes layout at the construct's actual starting column. The
layouts below are structural rules, not special cases for library functions
such as `map` and `filter`.

### Structure the formatter preserves

Formatting changes whitespace, line breaks, collection separators and
continuations. It never:

- inserts, removes or moves parentheses, because a parenthesized group with a
  top-level placeholder is a section (`055` §4.3);
- converts between a value pipe and nested applications;
- inserts, removes or moves `_` placeholders;
- converts between an `if` expression and an `if` statement, or between a
  lambda and a section;
- changes literal kinds: path atoms, quoted strings, raw strings, symbols and
  numbers keep their authored kind and spelling.

Formatting joins authored backslash continuations when possible and emits a
continuation when a list item or record field contains a wrapped application.

Because newlines end an expression except where `055` §4.1 allows continuation,
an expanded layout only breaks a line at a point where the expression continues:
inside an open delimiter, after a trailing `=`, `=>` or binary operator, or
before a more-indented line beginning with `|>` or a binary operator. Where no
such break point exists, the construct stays on one line and may exceed the
width target. The formatter joins authored backslash continuations and never
emits them; headers and `def` signatures stay on one line and may exceed the
width target.

## Expression Layouts

### Applications

Short applications remain inline:

```kame
map (page => page.name) pages
```

An application outside any open delimiter cannot place its arguments on
separate lines. When it does not fit, its delimited arguments (parenthesized
groups, lists, records, `$(…)`) expand in place, starting with the last one,
until the application fits or none remain:

```kame
navigation = map (page =>
	navigation-item page.name (page-url page.name) page.published-at
) published-pages
```

Inside an open delimiter, an expanded application keeps its head on the opening
line and puts each argument on its own line, one tab deeper. At list or record
depth, the formatter adds explicit backslash continuations when it wraps the
application:

```kame
items = [
	navigation-item \
		section.metadata.navigation-title \
		(page-url section.parent.name) \
		section.order
]
```

The head can itself be an expression and follows the same rules.

### Delimited groups

Lists, records and parenthesized groups share one expanded layout: the opening
delimiter ends its line, each element goes on its own line one tab deeper,
and the closing delimiter returns to the indentation of the line that opened
it. Expanded lists use newlines alone. Expanded records include a comma after
each field. An expanded `$(…)`, `$?(…)` or `$&(…)` puts its command list on the
following lines one tab deeper, one statement per line, and returns the closing
`)` to the opening line's indentation. Delimiters opened on the same line close
together on one line (`})`):

```kame
entry = {
	name: page.name,
	href: page-url page.name,
	sections: sections-of page.name,
}
```

Short collections remain inline: `[./src/main.c, ./src/util.c]`,
`{name: page.name, href: page-url page.name}`. Keep a record key and value
together when they fit. Otherwise put the value on the next line, one tab
deeper than the key. Shorthand fields (`{name, file}`) and spreads
(`{...base, mode: "debug"}`) keep their authored form. Preserve item and field
order.

### Lambdas and sections

```kame
(page => page.name)
(a b => a * b)
(() => now ())
(join __ _)
```

An expanded lambda keeps its parameters and `=>` on the opening line, puts its
body on the next line one tab deeper, and returns the closing `)` to the opening
line's indentation:

```kame
(page =>
	navigation-item page.title (page-url page.name)
)
```

Sections use the application and delimited-group layouts. Parameter order,
rest-parameter spelling (`rest...`) and placeholder positions never change.

### Operators and fallback

Binary operator chains stay inline when they fit. An expanded chain breaks
before each operator of the loosest level, putting the operator at the start of
a continuation line one tab deeper:

```kame
publishable = page.published
	and not page.draft
	and page.date <= build-date
```

`??` follows the same layout. Operators of a tighter level inside an operand
stay on that operand's line.

### Value pipes

Preserve the authored choice between a pipe chain and nested applications:

```kame
help |> map std-help-line |> join "\n"
join "\n" (map std-help-line help)
```

Both are canonical. Formatting changes only spacing and line breaks, never
converts between them, and never moves, inserts or removes `_` placeholders.
Parsing retains enough authored structure to distinguish a pipe from its
application-equivalent evaluation. Lowering for evaluation must not dictate
source formatting or change evaluation order, multiplicity or effects. An
authored parenthesized subject, `(tool "git") |> f`, keeps its parentheses.

A short chain is inline with one space on each side of `|>`. An expanded chain
keeps its subject on the first line and puts each stage on a continuation line
one tab deeper, beginning with `|>` and one space:

```kame
navigation = published-pages
	|> filter (item => item.visible)
	|> map navigation-item
	|> join "\n"
```

Stage operands use the ordinary layouts at their actual columns. Compact
embedded formatting inside strings, templates and Kash words keeps the chain on
one physical line without rewriting it into calls. Kash process pipelines (`|`)
are a separate streaming syntax and are never converted to or from value pipes.

### Conditional expressions

```kame
mode = if release? then "-O2" else "-O0"
```

An `if` expression expands only inside an open delimiter. The expanded form
keeps `if CONDITION then RESULT` on the opening line and starts each
`elif CONDITION then RESULT` and the `else RESULT` part on its own line, one tab
deeper:

```kame
output = (if valid-page? page then page-output page
	elif draft? page then nil
	else fail! "Invalid page: ${page.name}"
)
```

Each branch keeps its own nested layout. Where an `if` expression is not inside
a delimiter and does not fit, it stays on one line; authors who need a
multiline conditional parenthesize it or use an `if` statement in a block.

### Match

`match` arms are a block: each arm is on its own line, one tab deeper than the
line containing `match`, as `PATTERN => EXPR`. Within one `match`, the `=>` of
consecutive single-line arms is aligned by padding the patterns with spaces:

```kame
def object-path source = match source
	./src/{path:**}.c => ./build/${path}.o
	./gen/{name}.c    => ./build/gen/${name}.o
	else              => fail! "not a source: ${source}"
```

An arm whose result does not fit ends with `=>` and puts the result on the next
line, one tab deeper than the arm; that arm does not take part in alignment.
Each arm ends at a newline, even inside an open delimiter, so arms carry no
separating commas. An arm result that contains `=>` (a lambda) is
parenthesized by the author (`055` §4.5); the formatter never adds or removes
those parentheses. In a block, a statement-form `match` arm is followed by its
indented statement block. Preserve arm order; formatting does not change
matching precedence.

## Definitions and Statements

### Definitions

Short definitions remain inline:

```kame
page-names = map page-name pages
def page-url name = "/${name}/"
```

If a definition does not fit inline, end the line with `=` and put the value on
the next line, one tab deeper. Recompute the value's layout at that column;
moving a value below `=` does not by itself require expanding it internally:

```kame
def section-nav name =
	sections-of name |> map (section => navigation-item section)
```

After a parameterless `=`, the indented lines form a block only when the first
begins with `let`, `set`, `return`, `for`, `while`, `defer`, a statement `if`
(no `then` on that line) or a statement `match`; otherwise they continue the
expression (`055` §5.1). The formatter relies on this rule and never produces
an ambiguous layout: when a continued expression's first line would begin with
one of those keywords (a `match` expression, or an `if` expression whose `then`
would not share that line), it
keeps the value on the `=` line. An author who wants it below `=` parenthesizes
it, which removes the ambiguity.

Apply the same rule to `?=` defaults, `+=` contributions, `let` and `set`.
Preserve parameter order, rest parameters, operator spelling and authored
names. A `def` header with its parameters is never split, and the formatter
never emits a backslash continuation.

A block-bodied `def`, and a top-level `NAME =` with a block, put the block on
the following lines one tab deeper:

```kash
def compile source output
	mkdir -p (dirname output)
	cc -c $source -o $output
```

### Blocks

Block statements (`let`, `set`, `if`/`elif`/`else`, `for … in …`, `while`,
`match`, `return`, `yield`, `break`, `continue`, `exit`, `defer`, commands) each
start a line at their block's indentation. `elif` and `else` align with their
`if`. Conditions are expressions and use the expression layouts:

```kash
for source in files ./src/*.c
	let object = object-path source
	if $?(test -f $object).ok
		continue
	compile $source $object
```

A blank line between statements keeps its count; a blank line directly after a
block header is removed.

### Modules

`import`, `from`, `export` and `include` statements are single-line: one space
between their parts and `, ` between names, as in
`from ./lib/c import compile, object-path as obj`. Formatting never splits,
merges or sorts them; split a long list into several statements by hand.

## Rule Declarations

A rule header is one line:

```kame
rule ./build/{name}.o : ./src/{name}.c ./src/{name}.h, ./gen/config.h | ./build/dir
	cc -c $inputs.0 -o $outputs.0

task cached lint : ./src/*.c ; {env: {MODE: mode}}
	lint $...inputs
```

- One space after `rule`, `task`, `service`, `always` and `cached`, and between
  output items.
- One space on each side of the `:` and of the `|` that begins order-only
  prerequisites; whitespace between items of a parallel group; a comma attached
  to the last item of a sequenced group followed by one space.
- A rule, task or service with no inputs, including a file rule, has no `:`:
  `task clean`, `rule ./build/stamp`.
- Apart from an expanded metadata record, the header stays on one line even
  beyond the width target; the formatter joins authored backslash
  continuations and never emits them.
- Metadata follows ` ; ` as a record and uses the record layouts. When the
  metadata record expands, its fields go one tab deeper and its closing `}` ends
  the header.
- A rule body is a block one tab deeper than the header. A Kash body uses the
  block and command layouts. A foreign-shell recipe body is template text: the
  first tab is the block prefix, and any further indentation is recipe-owned
  content preserved verbatim.
- An expression body follows ` = ` on the header line, or, when it does not fit,
  the header ends with `=` and the expression continues on the next line one
  tab deeper.

Declaration-time `if`/`for` blocks and `rule` expressions in value position use
the same block and header layouts.

## Kash Commands and Process Graphs

A command statement keeps its words on one line, separated by one space; word
fragments (`prefix-(name)-suffix`, `${env/HOME}/x`) are never respaced. A value
fragment `(EXPR)` inside a word uses compact inline expression formatting.

A Kash statement stays on one line unless its process graph nests a group or a
`|(filter …)` branch. Expansion is structural, not width-driven: a graph with
neither form keeps the inline layout at any length.

A newline ends a Kash statement unless it falls inside an open delimiter, so an
expanded graph breaks lines only inside group parentheses. An expanded graph
keeps its subject and operators on the opening line; only nested groups and
`|(filter …)` branches open new lines one tab deeper; the closing `)` shares its
line with following operators:

```kash
printf "a\nb\n" |(frame :lines) |(balance) (|(each) cat > ./first.txt, |(each) cat > ./second.txt) |(merge :order round-robin) cat
```

becomes

```kash
printf "a\nb\n" |(frame :lines) |(balance) (
	|(each) cat > ./first.txt,
	|(each) cat > ./second.txt
) |(merge :order round-robin) cat
```

A group's opening `(` ends the operator line. Each member goes on its own line
one tab deeper, members are separated by `,` with no trailing comma, and the
closing `)` returns to the indentation of the line that opened the group. A
member keeps its own operators on its line, expanding further only for its own
nested groups and filter branches.

Destinations are not pipes. A fan-out operator's destinations (`merge`,
`broadcast`, `balance`) and an implicit-stage command (`each`) follow their
operator after a single space. The `|` an author may write before an
implicit-stage command carries no structural meaning, so formatting canonicalizes
it to the space form. The authored statement

```kash
printf "a\n" |(frame :lines) |(each) | cat
```

is formatted as

```kash
printf "a\n" |(frame :lines) |(each) cat
```

An operator's own spelling stays on its line: `|(tee PATH)` and
`|(tee :append PATH)` are written as authored, and a `2>&1` written after
`>&channel` follows that writer because the alias copies the channel
destination. Statement suffixes `?`, `? fallback`, `&`, `>&` and `>@` stay
attached to their own statement.

Compact and expanded layouts describe the same graph. Formatting an expanded
statement again leaves it unchanged, never moves a stage across an operator, and
reparsing it preserves the graph structure exactly.

## Language Boundaries

The expression layouts apply wherever the expression grammar applies. They do
not replace surrounding grammars:

- Kash commands and process pipelines retain their own syntax and ordering,
  expanding only as *Kash Commands and Process Graphs* describes.
- Template text, foreign-shell recipe text and raw strings remain literal
  content, not source to be reindented. Template directives (`@if EXPR`,
  `@for x in xs`) keep their operands on the directive line in compact
  expression formatting. `${EXPR}` interpolations use compact expression
  formatting and must not introduce physical line breaks into their enclosing
  text.
- Quoted string escaping and interpolation spelling follow `055` §3.3 and 004.

The width target is not permission to rewrite shell text, process semantics,
template output, or string values.

## What Good Looks Like

This illustrative Kame program shows imports, compact definitions, conceptual
groups, a larger definition whose structure is visible, and rules at the end:

```kame
import ./lib/site as site

# Content discovery
content = files ./src/pages/*.md
pages = map site/page-data content

# Navigation
page-names = pages |> map (page => page.name)
navigation = map navigation-item pages

# Page transformations
def page-url name = "/${name}/"
def publishable? page = not page.draft

def navigation-item page = {label: page.title, href: page-url page.name}

def section-nav name =
	sections-of name
		|> map (section => {
			label: section.metadata.navigation-title,
			href: page-url section.parent.name,
			order: section.order,
		})

# Rules
task default : ./www/index.html

rule ./www/{name}.html : ./src/pages/{name}.md
	site/render-page! $inputs.0 $outputs.0
```

Helper names in this example are illustrative, not additional library APIs.
An author should be able to scan the named values first, understand the
transformations next, and find rules or execution at the end of the file.

## Acceptance Criteria

- Formatting twice produces byte-for-byte identical output.
- Parsing formatted output produces an equivalent AST, excluding spans.
- Native and WASM hosts produce identical canonical text.
- Formatting never inserts or removes parentheses, placeholders or pipes, and
  never changes a literal's kind. Expanded lists use newlines, expanded records
  use commas after each field, and process groups have no trailing comma.
- Formatting joins authored backslash continuations when possible and emits
  them when wrapping applications directly inside lists or records.
- Indentation is one tab per block or continuation level. Inline separators are
  `, `. Binary operators have one space on each side. Block headers have no
  trailing `:`.
- Golden cases cover inline and expanded forms, nesting, long heads and
  definition headers, record fields, lambdas, `if` expressions inside and
  outside delimiters, `match` arm alignment, rule headers with groups, order-only
  prerequisites and metadata, expression bodies, and multiline literals.
- Width-boundary cases cover exactly 80 columns and 81 columns, including
  tab indentation, definition prefixes, and non-ASCII text.
- Constructs without a legal break point stay on one line beyond the width
  target and still reparse identically.
- Comments, blank-line counts, literal content, statement order, parameter
  order, and execution order remain preserved under their existing contracts.
- Formatting does not enforce naming conventions by changing identifiers.
- Formatting does not perform effects, resolve names or evaluate declarations
  to choose layout; changing a binding, an import or `PATH` never changes the
  formatted output.
- Inline/expanded value pipes and nested applications keep their authored form
  through repeated formatting. Coverage includes mixed nesting, explicit and
  repeated placeholders, multiline operands, comments, and compact expressions
  in strings, templates, rule headers and Kash words.
- Kash process graphs expand for nested groups and filter branches only, keep
  fan-out destinations and implicit-stage commands off the pipe syntax, and stay
  byte-identical across repeated formatting with an unchanged graph after
  reparsing.
