# LittleMake Language

## Purpose

LittleMake combines a Lisp-like expression language with Make-like definitions
and rules. Template, expression, and rule syntax must each be independently
parseable and formattable. Script parsing composes those parsers rather than
creating a second representation.

This specification preserves the tested legacy language while resolving its
documented contradictions.

## Source Model

Source is UTF-8. Parsers retain zero-based, half-open byte spans into a named
source file. Diagnostic rendering computes one-based line and display columns;
tabs advance to the next eight-column boundary.

Line endings `LF` and `CRLF` are accepted. Canonical formatting emits `LF`.

A line whose first non-whitespace text begins with `#` or `//` is a comment.
Comment markers inside strings, paths, or recipe text are literal.

Parsers return an AST and zero or more diagnostics. Invalid syntax must never be
silently discarded. Recovery may preserve malformed recipe interpolation as
literal text with an `PARSE_ERR` warning.

## Common Atoms

### Names

A name begins with an ASCII letter or `_` and continues with ASCII letters,
digits, `_`, or `-`. A single trailing `?` or `!` is allowed.

```text
NAME = (letter | "_") (letter | digit | "_" | "-")* ("?" | "!")?
```

Examples are `build`, `_value`, `sha256-file`, and `exists?`. `42`, `42x`,
`-name`, and `name?more` are not names. A digit-leading token is parsed as a
number only when the complete token is a valid number; otherwise it is invalid.

### Paths

A path is explicit when it begins with `/`, `./`, or `../`. No other string is
classified as a filesystem path in the initial language.

Protocol paths are deferred. In particular, `name:value` is not initially a
resource URI.

### Symbols

Symbols begin with `:`. The symbols `:true`, `:false`, and `:nil` evaluate to
boolean and nil values. Other symbols evaluate to their name without `:`.

### Numbers

Numeric grammar is:

```text
DIGITS = digit ("_"? digit)*
BIN-DIGITS = bin-digit ("_"? bin-digit)*
OCT-DIGITS = oct-digit ("_"? oct-digit)*
HEX-DIGITS = hex-digit ("_"? hex-digit)*
DECIMAL = "0" | nonzero-digit ("_"? digit)*
INTEGER = "-"? (DECIMAL | "0b" BIN-DIGITS | "0o" OCT-DIGITS | "0x" HEX-DIGITS)
FLOAT = "-"? DIGITS "." DIGITS ([eE] [+-]? DIGITS)?
      | "-"? DIGITS [eE] [+-]? DIGITS
```

Base-prefixed digits follow the same optional-single-underscore rule. A leading
or trailing underscore, consecutive underscores, `1.`, `.5`, hexadecimal
floats, and a sign after a base prefix are invalid.

Integer literals must fit `int64`. Numeric overflow is `PARSE_ERR`.

## Expression Language

Expressions use these values:

```littlemake
:true
:false
:nil
42
-3.5
"hello"
name
project.name
[1 2 3]
[name: "app" path: ./src]
(operation argument...)
([argument...] body...)
```

List items and application arguments are separated by whitespace, not commas.
A list containing `NAME:` entries is a record; every record key must have one
following value. Mixed keyed and unkeyed list entries are rejected.

An application is a parenthesized non-empty sequence. A parenthesized form
whose first item is a parameter list is a lambda. An empty application is
invalid.

### Strings

Expression strings use double quotes. They support `\"`, `\\`, `\n`, `\r`,
and `\t`. A quoted string may contain expression interpolation:

```littlemake
"Bundle of {(count files)} files"
```

Within a quoted expression string, `{(` opens interpolation and the matching
`)}` closes it. Parentheses and quoted strings inside the expression are
balanced normally. `\{` produces a literal `{`. An unclosed interpolation is
`PARSE_ERR`; it does not use recipe recovery. `@(expression)` is also accepted
inside quoted strings and has the same value semantics.

Expression strings are parsed by the expression parser. `{(...)}` interpolation
and the `\{` escape are specific to expression strings. `@(expression)` is the
one expansion shared with string templates (see String Templates);
`@{reference}` and contextual selectors are not expression-string syntax.

Single quote is reserved and is not an alternative string delimiter.

### References

A reference starts with a name and contains dot-separated components:

```littlemake
project.name
files.0
files.-1
files.1..4
files...3
config.{host,port}
```

Components are names, integer indexes, half-open slices, or key selections.
Slice endpoints may be omitted or negative. Selection keys are comma-separated
names.

### Pipes

`|` rewrites applications before evaluation:

```littlemake
(value | transform a)
```

is equivalent to `(transform a value)`. One or more `_` placeholders on the
right are each replaced with the piped value:

```littlemake
(value | transform _ a)
```

is equivalent to `(transform value a)`. A pipe must have expressions on both
sides.

### Special Forms

The evaluator recognizes these application heads specially:

- `?` returns the first operand that does not fail with unknown-reference.
- `let` evaluates sequential name/value bindings in a child lexical scope.
- `def` binds a value or function in the current lexical scope:
  - `(def NAME value)` binds a value.
  - `(def NAME [parameter...] body...)` binds a function. The parameter list
    uses lambda parameter syntax and may end in one rest parameter.

  A `def` with one operand after `NAME` is a value binding; with two or more,
  the first operand is the parameter list and the rest is the function body.
- `eval` evaluates expression text or an explicitly loaded script as specified
  in `005-evaluation.md`.

There is no language-level `if` in the initial implementation.

## String Templates

String templates are parsed by the template parser. They appear as definition
right-hand sides that begin with `"`, as recipe text, and as standalone template
sources. They contain literal segments and expansions:

- `@(expression)` evaluates an expression.
- `@{reference}` resolves a reference.
- `@<`, `@>`, and argument forms resolve contextual selectors.
- A backslash escapes `@` and `\`.

`@(expression)` is shared with expression strings (see Strings); `@{reference}`
and contextual selectors are template-only, while `{(...)}` interpolation is
expression-string-only.

Malformed `@(` or `@{` expansions remain literal and produce an `PARSE_ERR`
warning. A well-delimited but invalid contained expression is an error.

Rendered list values are joined with one ASCII space. Nil renders as an empty
string. Records and bytes cannot be interpolated into command text without an
explicit conversion operation.

## Selectors

Rule selectors are:

| Form | Meaning |
| --- | --- |
| `@<` | first input |
| `@<*` | all inputs |
| `@<#` | input count |
| `@<N` | input index |
| `@<A..B` | input slice |
| `@>` | first output |
| `@>*` | all outputs |
| `@>#` | output count |
| `@>N` | output index |

Function selectors are:

| Form | Meaning |
| --- | --- |
| `@_` | first argument |
| `@*` | all arguments |
| `@#` | argument count |
| `@N` | argument index |

Indexes are zero-based. Negative indexes count from the end. Missing context is
`SEL_NO_CONTEXT`; invalid bounds are `SEL_INDEX_INVALID`.

## Target Templates

A target template contains literal text and zero or more capture groups. A
template with no capture group matches only its literal text; rule headers
classify such a target as a path or name rather than a template:

```littlemake
./out.o
./{stem:*}.o
./{path:**}/{name:*}.c
live-{name}
```

```text
GROUP = "{" CAPTURE (":" PATTERN)? "}"
CAPTURE = (letter | "_") (letter | digit | "_" | "-")*
PATTERN = GLOB-ITEM+
GLOB-ITEM = "*" | "**" | "?" | CLASS | ESCAPED | LITERAL
```

`PATTERN` cannot contain an unescaped `{`, `}`, or `:`. `ESCAPED` is `\`
followed by one byte. A character class is an optional leading `!` followed by
one or more literal bytes or ascending `a-z` ranges and a closing `]`.

Initial groups are `{name}`, `{name:*}`, `{name:**}`, and groups containing glob
`?` or character classes. `{name}` is equivalent to `{name:*}`. `*` matches one
or more non-`/` bytes, `?` matches exactly one non-`/` byte, and `**` matches one
or more bytes including `/`. Character classes use `[abc]`, `[a-z]`, and
`[!abc]`; an unclosed or empty class is `PARSE_ERR`. `\` escapes the following
template byte. Matching is anchored to the complete target, and captures are
never empty.

When adjacent variable-width groups can match, captures use leftmost-shortest
matching. Reusing one capture name requires every occurrence to match the same
text.

Regular-expression groups and capture processors are deferred.

## Definitions

A value definition is:

```littlemake
NAME = value
```

A function definition is:

```littlemake
(NAME argument... rest...) = value
```

A rest parameter ends in `...`, appears once, and must be last.

Definition right-hand sides are classified as follows:

1. A leading `(` or `[` is parsed as one expression value.
2. A leading `"` is parsed as one quoted string template.
3. Otherwise top-level whitespace separates one or more unquoted string
   templates; one item is a scalar and multiple items form a list.

Once classified as an expression, parse failure does not fall back to a string.
Definitions are lazy as specified in `005-evaluation.md`.

## Rules

A rule has a header and zero or more indented body lines:

```littlemake
./output : ./input
	command @< @>

default : ./output

task cached-name : dependency
	command

service server : ./output
	command --watch
```

```text
RULE = PREFIXED-RULE | UNPREFIXED-RULE
PREFIXED-RULE = ("task" | "service") WS+ NAME-TARGET WS* ":" (WS+ INPUTS)?
UNPREFIXED-RULE = OUTPUTS WS* ":" (WS+ INPUTS)?
OUTPUTS = OUTPUT-TARGET (WS+ OUTPUT-TARGET)*
INPUTS = INPUT (WS+ INPUT)*
OUTPUT-TARGET = NAME | PATH | TEMPLATE | QUOTED-PATH
NAME-TARGET = NAME | NAME-TEMPLATE
INPUT = NAME | PATH | TEMPLATE | QUOTED-STRING | "@(" EXPRESSION ")"
```

Whitespace inside quoted strings and balanced input expressions does not split
header items. A file rule may have multiple outputs. Phony tasks, cached tasks,
and services have exactly one output target. `task` and `service` are reserved at
the start of a header and the prefixed form is tested before the unprefixed form.
A quoted output is valid only when its decoded value begins with an explicit
path prefix. `NAME-TEMPLATE` contains a target capture and no explicit path
prefix.

Headers are classified as:

- File rule: every output is an explicit path or explicit path template.
- Task: exactly one bare name or name template without a prefix.
- Cached task: `task` followed by exactly one name or name template.
- Service: `service` followed by exactly one name or name template.

Mixing path and name outputs is invalid. A target token such as `out.o` that is
neither a valid name nor an explicit path is a parse error; the diagnostic
should suggest `./out.o` when a file rule was likely intended.

Inputs may be names, explicit paths, templates, string values, or `@(expression)`
expansions. Input expression lists are flattened recursively at evaluation.

A body line belongs to the rule when it begins with at least one tab or space.
The exact leading whitespace prefix of the first nonblank body line is the rule
indent. Every nonblank body line must begin with that prefix. The parser removes
that prefix and preserves further indentation; a shorter or different prefix
ends the rule or reports `PARSE_ERR` when still indented. Canonical formatting
uses one tab for each recipe line.

All rendered body lines become one shell script. The parser does not parse shell
syntax.

## Scripts

A script contains comments, blank lines, definitions, rules, and top-level
expressions. Imports are not part of the initial script language.

The parser uses line context to distinguish a rule header from expression and
record punctuation. Indented text without a preceding rule is `PARSE_ERR`.

## Formatting

Formatting is AST-based and canonical:

- `LF` line endings.
- One space around definition `=` and rule `:`.
- One tab before recipe content.
- Two-space indentation inside multiline expressions.
- No trailing whitespace.
- One terminal newline for scripts.

Comments remain attached in source order. Exact original whitespace is not
preserved. Formatting must be idempotent, and parsing formatted output must
produce an equivalent AST excluding spans.

## Acceptance Tests

- Each language package parses and formats its syntax without using the script
  parser.
- Expression parse/format/parse covers literals, lists, records, lambdas,
  references, selectors, and pipes.
- Target templates match `./archive.o` against `./{stem:*}.o` with
  `stem=archive`.
- `*` does not cross `/`, while `**` does.
- Empty captures and malformed character classes are rejected.
- File rules require explicit path outputs; `output.o : input.c` is not treated
  as a file rule.
- Scalar, list, and function definitions follow deterministic RHS
  classification.
- A target template with no capture group parses as a literal target.
- Expression-string `{(...)}` interpolation and template `@(...)`/`@{...}`
  expansions are parsed by their respective packages.
- `def` distinguishes value and function bindings by the number of operands
  after the bound name.
- Malformed recipe interpolation remains literal and emits `PARSE_ERR`.
- Selectors retain their exact source spans.
- Recipe lines format with tabs and preserve additional shell indentation.
- Script formatting is idempotent and preserves comment order.
- Golden fixtures cover the compatible syntax from the legacy parser and
  formatter tests.
