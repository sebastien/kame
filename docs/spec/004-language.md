# Kame Language

## Purpose

Kame combines a Lisp-like expression language with Make-like definitions
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

`include PATH` is a top-level source-composition directive. `PATH` is a quoted
or unquoted path without whitespace, resolved relative to the including file.
Includes are expanded depth-first at their declaration location; included
definitions and rules share the caller's scope and source order. Includes are
only valid for file-backed sources. Active-ancestry cycles are an error;
repeated nonrecursive includes expand again and ordinary declaration registration
checks duplicates.

`include? PATH` follows the same grammar and expansion order, but ignores a
missing file. Existing unreadable files, directories, malformed sources, and
cycles still fail before execution. AST JSON emits `optional: true` for this
form; formatting retains `include?`. Native source tracking retains missing
optional path identities so watch can notice their later creation. The directive
is source configuration and follows ordinary include loading policy.

### Conditional source declarations

Kame value (`.km`) and rule (`.kmk`) sources select declarations with
`when PREDICATE`, an optional `otherwise`, and a matching `end`:

```kmk
mode ?= "debug"
when (eq mode "debug")
include debug.kmk
otherwise
include release.kmk
end
```

These are top-level source markers, with rule-source markers and declarations
flush left and ordinary indentation for recipes. Branches may nest. Each
physical source must balance its markers; an included file cannot close its
caller's branch. More than one `otherwise`, a missing predicate, or an unmatched
marker is `PARSE_ERR`. The parser checks inactive text for syntax errors, and
formatting and AST inspection retain both branches and their authored byte
spans (`when`, `otherwise`, and `end-when` AST kinds).

Selection happens before registration or execution. Predicates must return a
boolean and may use pure expressions and lazy definitions selected earlier in
source order, including earlier selected includes and earlier explicit `.km`
or `.kmk` inputs. They cannot depend on subsequent declarations or the effects
of earlier value statements. Literal `--define NAME=VALUE` and case-sensitive
`KAME_NAME` configuration follow ordinary override precedence; the final
registration still validates explicit override names. Unused definitions remain
lazy. A predicate inside an inactive branch is not evaluated.

Only selected declarations enter the shared scope. Inactive includes are not
read, tracked, or cycle-checked, even when required or missing. Selected includes
retain the ordinary required/optional and active-ancestry rules. Inline and stdin
sources support conditional declarations but a selected include still requires
a file-backed source. A nonboolean predicate reports `EXPR_INVALID`; unknown
references, cycles, capability use, and effectful operations fail with their
ordinary diagnostic codes before recipes or statements execute. Runtime grants
do not grant predicates host authority.

Embedding hosts compose source branches before compilation. Passing authored
conditional markers directly to evaluator registration fails `FEATURE_UNSUP`
rather than registering both branches. The portable predicate query and the
native/JavaScript source loaders share predicate evaluation semantics.

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

Runs of underscores and underscore-prefixed digit names such as `_`, `__`, and
`_0` remain ordinary names everywhere except inside placeholder sections, where
the grammar of `014-patterns.md` claims them.

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

```kame
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
((body _ _1))
```

List items and application arguments are separated by whitespace, not commas.
A list containing `NAME:` entries is a record; every record key must have one
following value. Mixed keyed and unkeyed list entries are rejected.

An application is a parenthesized non-empty sequence. A parenthesized form
whose first item is a parameter list is a lambda. An empty application is
invalid.

### Embedded Kash Processes

`$(COMMAND)` is an expression atom as specified in `017-kash.md`. The expression
parser owns the substitution node and delegates its contents to the Kash process
parser. It is valid wherever an expression is accepted, including application
arguments, list/record values, and lambda bodies. On evaluation, it captures
stdout as a string using the caller's execution context from `005-evaluation.md`.
Parsing is independent of capabilities and host availability; execution is not.
Kash reference wrappers and its application shorthand or infix recovery syntax
are not thereby added to the Kame expression grammar.

### Strings

Expression strings use double quotes. They support `\"`, `\\`, `\n`, `\r`,
and `\t`. A quoted string may contain expression interpolation:

```kame
"Bundle of {(count files)} files"
```

Within a quoted expression string, `{(` opens interpolation and the matching
`)}` closes it. Parentheses and quoted strings inside the expression are
balanced normally. `\{` produces a literal `{`. An unclosed interpolation is
`PARSE_ERR`; it does not use recipe recovery. `@(expression)` is also accepted
inside quoted strings and has the same value semantics.

Expression strings are parsed by the expression parser. `{(...)}` interpolation
and the `\{` escape are specific to expression strings. `@(expression)` is the
one expansion shared with string templates (see String Templates); contextual
selectors are not expression-string syntax.

Single quote is reserved and is not an alternative string delimiter.

Plain `$(...)` inside a quoted expression string is literal text. To execute a
Kash process there, use existing expression interpolation, for example
`"Revision: {(cat $(git rev-parse --short HEAD))}"`. This does not change the
ownership of opaque recipe text by its shell.

### References

A reference starts with a name and contains dot-separated components:

```kame
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

```kame
(value | transform a)
```

is equivalent to `(transform a value)`. One or more `_` placeholders on the
right are each replaced with the piped value:

```kame
(value | transform _ a)
```

is equivalent to `(transform value a)`. A pipe must have expressions on both
sides.

### Comparison Operators

The symbols `=`, `==`, `!=`, `<`, `>`, `<=`, and `>=` are expression atoms and
may appear wherever an expression may appear. Used as an application head they
name the comparison operations of `007-library.md`:

| Symbol | Operation |
| --- | --- |
| `=` | `eq` |
| `==` | `is` |
| `!=` | `ne` |
| `<` | `lt` |
| `>` | `gt` |
| `<=` | `lte` |
| `>=` | `gte` |

`=`/`eq` and `==`/`is` have identical strict, kind-aware semantics; `is` is the
readable spelling of `eq`. The symbol and the spelled operation are aliases, and
canonical formatting preserves whichever spelling the source used. Elsewhere, `=`
is the definition separator and `<`/`>` introduce selectors only after `@`, so
these atoms conflict with no existing syntax.

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
- `if` selects among branches by truth. Operand count determines the branches:
  `(if TEST THEN [TEST THEN]... [ELSE])` evaluates tests left to right and
  evaluates exactly one then-body or the trailing else. An even operand count
  has no else; an odd count ends in else. Zero or one operand is
  `EXPR_INVALID`.
- `and` and `or` are lazy conjunction and disjunction. They evaluate operands
  left to right, stop at the operand that decides the result, and return that
  operand (see `005-evaluation.md`).
- `match` dispatches a string-like subject against pattern clauses:
  `(match SUBJECT [PATTERN BODY...]... [:else BODY...])`. The first matching
  clause runs; its named captures bind as names in that clause's child scope
  (see `005-evaluation.md`).
- `with` evaluates a record in a child scope where each field key is bound as a
  name: `(with RECORD BODY...)` (see `016-templates.md`).

These heads are special forms: their operands are evaluated only when the form
selects them. `if`, `and`, `or`, and `match` must not be implemented as eager
operations.

### Placeholder Sections

A parenthesized application with exactly one item containing at least one
placeholder is a placeholder section equivalent to a lambda over unnamed
parameters. `((f _ __ ___))` is `([a b c] (f a b c))`. Placeholders are
positional argument references, not names. Grammar, semantics, and formatting
are specified in `014-patterns.md`.

### Pattern Literals

A bare path atom or a quoted string without interpolation whose text contains
pattern groups is a pattern value. Groups with glob content (`{*}`, `{**}`,
`{name:*}`, `{name:**}`) are matchers; `{name}` and `{_N}` are expansion
references. Pattern values, matching, and the pattern form of `replace` are
specified in `014-patterns.md`. Rule target templates are unchanged.

## String Templates

String templates are parsed by the template parser. They appear as definition
right-hand sides that begin with `"`, as recipe text, and as standalone template
sources. They contain literal segments and expansions:

- `@(expression)` evaluates an expression, including a reference expression.
- `@<`, `@>`, and argument forms resolve contextual selectors.
- A backslash escapes `@` and `\`.

`@(expression)` is shared with expression strings (see Strings); contextual
selectors are template-only, while `{(...)}` interpolation is expression-string-only.

Malformed `@(` expansions remain literal and produce an `PARSE_ERR` warning. A
well-delimited but invalid contained expression is an error.

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
| `@<?` | unique normal file inputs newer than the oldest output, in file recipes |
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

```kame
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

Anonymous `{*}`/`{**}` output captures, positional rule input references
`{_N}`, regular-expression capture groups, and runtime pattern construction are
specified in `029-pattern-extensions.md`.

Expression-language pattern literals use a related grammar with `{name}` as an
expansion reference rather than a capture; see `014-patterns.md`.

## Definitions

A value definition is:

```kame
NAME = value
```

`NAME ?= value` registers a lazy default only when that name is not already
registered. A later default is skipped without evaluating its right-hand side;
a second ordinary `=` definition remains an error. The `?=` operator is
contiguous: `name? = value` still defines the name `name?`. Default function
definitions are invalid. This declaration form applies to Kame value/build
sources; Kash keeps its existing standalone `=` header grammar.

A function definition is:

```kame
(NAME argument... rest...) = value
```

A rest parameter ends in `...`, appears once, and must be last.

Definition right-hand sides are classified as follows:

1. A leading `(`, `[`, or `$(` is parsed as one expression value.
2. A leading `"` is parsed as one quoted string template.
3. Otherwise top-level whitespace separates one or more unquoted string
   templates; one item is a scalar and multiple items form a list.

Once classified as an expression, parse failure does not fall back to a string.
Definitions are lazy as specified in `005-evaluation.md`.

## Rules

Named task rules may declare standalone named arguments after their target;
required and optional forms, literal defaults, and their distinction from file
captures are defined in `022-target-arguments.md`.

A rule has a header and zero or more indented body lines:

```kame
./output : ./input
	command @< @>

default : ./output

task cached-name : dependency
	command

service server : ./output
	command --watch
```

```text
RULE = (PREFIXED-RULE | ALWAYS-RULE | UNPREFIXED-RULE) ENVIRONMENT?
ENVIRONMENT = WS* ";" WS* "env" (WS+ QUOTED-ASSIGNMENT)+
PREFIXED-RULE = ("task" | "service") WS+ NAME-TARGET WS* ":" (WS+ INPUTS)?
ALWAYS-RULE = "always" WS+ OUTPUTS WS* ":" (WS+ INPUTS)?
UNPREFIXED-RULE = OUTPUTS WS* ":" (WS+ INPUTS)?
OUTPUTS = OUTPUT-TARGET (WS+ OUTPUT-TARGET)*
INPUTS = INPUT (WS+ INPUT)*
OUTPUT-TARGET = NAME | PATH | TEMPLATE | QUOTED-PATH
NAME-TARGET = NAME | NAME-TEMPLATE
INPUT = NAME | PATH | WILDCARD-PATH | TEMPLATE | QUOTED-STRING | "@(" EXPRESSION ")" | INTERPOLATED-PATH
```

Whitespace inside quoted strings and balanced input expressions does not split
header items. Each whole `@(EXPRESSION)` contributes a value independently;
strings, resources, lists and nil are flattened in authored order. Quoted input
tokens and interpolated paths such as `@(ROOT)/suffix` render one path through
the shared template engine and retain dependencies of their expressions.
An unquoted explicit path containing `*`, `?`, or `[` is a wildcard input.
It lowers to the dependency-tracked `wildcard` operation, produces sorted files,
and contributes no input when unmatched. Recursive `**` follows the library
contract. Membership changes invalidate dependent output identity. Quoted paths
retain literal wildcard characters, and Kash argv keeps its existing semantics.
Formatting preserves the authored wildcard token; AST JSON marks it `wildcard`.
Each reached pattern is a lazy shared glob resource, including an empty match;
watch invalidation updates its membership and retained consumers. See
`023-wildcard-sources.md` for the source lifecycle contract.

A file rule may have multiple outputs. Phony tasks, cached tasks,
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

Service lifecycle metadata and process ownership are specified in
`024-managed-services.md`.

`always` followed by one or more explicit file outputs marks a file rule as
always stale, without changing its artifact kind, inputs, captures, or output
verification. It is invalid on named/cached tasks or services. A standalone
header `always :` remains an ordinary bare task named `always`. Formatting
preserves the prefix; AST and plan JSON emit `always: true` when set. Runtime
rerun and sharing semantics are specified in `006-runtime.md`.

A header may end with `; env "NAME=value" ...`. Assignments must be literal
quoted strings, with decoded escapes, no interpolation or NUL, and an ASCII
name matching `[A-Za-z_][A-Za-z0-9_]*`. Empty values are valid. A semicolon
inside a quoted input or balanced expression remains part of that input.
Alternatively, a header may end with a Kame metadata record:
`target : inputs ; [shell: kash env: [MODE: mode]]`. The supported keys are
`shell` and `env`; duplicate and unknown keys are errors. `env` contains valid
environment names with string values, including pure Kame expressions. Settings
are evaluated under planning policy before prerequisite recipes execute.
`shell` overrides the build-wide `SHELL` definition for this rule. It accepts the
Kash constructor, an executable string with `-c` implied, or a nonempty argv
list. The record and legacy `env` suffix are alternative header forms.

Metadata is separate from prerequisite items and preserves its authored order
in formatting and AST/plan JSON. Recipe inheritance, override precedence and
shared-context failures are defined in `006-runtime.md`.

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

A recipe body is a `plain`-style document template (`016-templates.md`). A
whole-line, keyword-gated directive (`@if`, `@elif`, `@else`, `@for`, `@with`,
`@let`, `@include`, `@raw`, `@end`) is removed together with its rule indent and
line ending and contributes no shell text; the selected body lines render as
shell. Inline `@(...)`, `@<`, `@>`, and the `@@`/`\@` escapes retain their
string-template meaning. Text that is not a directive line remains opaque shell.

## Scripts

A script contains comments, blank lines, definitions, rules, and top-level
expressions. Imports are not part of the initial script language.

The parser uses line context to distinguish a rule header from expression and
record punctuation. Indented text without a preceding rule is `PARSE_ERR`.

### Executable source layers

The source suffix names the independently usable outer language:

| Suffix | Source form |
| --- | --- |
| `.km` | Value program: comments, blank lines, includes, lazy definitions, and top-level expressions |
| `.kmk` | Rule program: the script grammar, including definitions and recipes |
| `.kash` | Process program: the Kash grammar in `017-kash.md` |
| `.ksh` | Alternate suffix for exactly the `.kash` grammar |

`.km` composes the existing definition and expression parsers; it does not
introduce another expression grammar or reinterpret expressions as shell text.
Rule headers and recipe bodies are invalid in a value program. Definitions
retain their documented RHS classification and lazy semantics; top-level
expression statements always use expression parsing, never RHS classification.
Thus a statement `42` is an integer and a statement `:nil` is nil. Balanced
expressions may span lines, and comments/blank lines do not become statements.

`include PATH` retains the source-composition behavior above. Included sources
retain their parser and spans: a `.km` source can supply definitions to a `.kmk`
program without being reparsed as recipe text. A value program cannot import
rule declarations through an include to circumvent its grammar restriction.
Kash inclusion is not added by these execution conventions.

File execution, named value entries, expression-statement result selection,
program arguments, and the unified `kame do run` command are defined in
`009-cli.md`. Source suffixes select a language, not a requirement to use the
other two layers. Parsing and formatting never execute a program. The `expr`
parser remains available for exactly one expression independently of the `km`
program parser.

The runner may compose several file and inline sources (`009-cli.md`). Parse
each with its selected outer grammar and preserve its source spans before
registering a shared program scope; do not concatenate heterogeneous text and
reparse it. A balanced expression, rule recipe, or Kash control block cannot
start in one fragment and finish in another. Top-level duplicate definitions
across fragments follow the same registration errors as duplicates in one
source. Forward visibility does not merge branch-local scopes or turn ordinary
definitions into mutable sequential assignments.

## Formatting

Canonical expression layouts and authoring conventions are specified in
[018-source-style.md](018-source-style.md). Naming and file organization are
authoring conventions; the formatter must not rename or reorder source items.

Formatting is AST-based and canonical:

- `LF` line endings.
- One space around definition `=` and rule `:`.
- One tab before recipe content; blank recipe lines carry no indentation.
- Two-space indentation inside multiline expressions.
- No trailing whitespace.
- Blank lines between script items, between recipe lines, and at the beginning
  and end of script input keep their count. A blank line between a rule header
  and its first recipe line is not part of the recipe and is removed.
- Scripts end with a newline; trailing blank lines are preserved, so input
  without a terminal newline gains exactly one.

Comments remain attached in source order. Whitespace is canonicalized as
described above; the original source text is not preserved verbatim. Formatting
must be idempotent, and parsing formatted output must produce an equivalent AST
excluding spans.

## Declaration continuations

Outside quoted strings, a backslash immediately followed by LF or CRLF acts as
whitespace in definitions, expression whitespace and rule headers. Continuations
may span indented physical lines. Parsers retain the original source bytes, so
spans and diagnostics still refer to authored lines and columns. Comments end at
their physical line ending; recipe backslashes remain shell-owned. Multi-line
expression definitions and verbatim string definitions remain supported.

## Acceptance Tests

- Continued declarations execute and format idempotently, retaining authored
  spans, CRLF behavior, comment boundaries and shell recipe backslashes.
- Each language package parses and formats its syntax without using the script
  parser.
- Blank lines keep their count between script items, between recipe lines, and
  at the script edges; whitespace-only blank lines are canonicalized.
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
- Command-substitution atoms delegate to Kash, retain embedded source spans,
  and parse/format without performing host effects. A leading `$(` definition
  RHS is an expression, while `$(...)` in plain quoted strings remains literal.
- A target template with no capture group parses as a literal target.
- Expression-string `{(...)}` interpolation and template `@(...)` expansions
  are parsed by their respective packages.
- `def` distinguishes value and function bindings by the number of operands
  after the bound name.
- `if`, `and`, `or`, and `match` are special forms: each operand is evaluated
  only when selected, so an unselected branch's dependencies and effects never
  run.
- `(if c a)` returns `a` or `:nil`, `(if c a b)` returns `a` or `b`, and an even
  operand count has no else.
- `and`/`or` return the deciding operand and stop evaluating after it.
- `match` selects the first matching clause, binds its named captures, and
  returns `:nil` when nothing matches without an `:else` clause.
- `=`/`eq`, `==`/`is`, `!=`/`ne`, `<`/`lt`, `>`/`gt`, `<=`/`lte`, and `>=`/`gte`
  are aliases; `=` and `==` share one strict, kind-aware equality.
- Symbolic operator atoms parse and format idempotently wherever an expression
  is allowed.
- A recipe directive line contributes no shell text, while a non-directive
  comment such as `@media` remains literal shell text.
- Placeholder sections parse, format idempotently, and evaluate as lambdas;
  `_` outside a section remains a name.
- Pattern literals classify per `014-patterns.md`; mixed matcher and reference
  groups are rejected.
- Malformed recipe interpolation remains literal and emits `PARSE_ERR`.
- Selectors retain their exact source spans.
- Recipe lines format with tabs and preserve additional shell indentation.
- Script formatting is idempotent and preserves comment order.
- Golden fixtures cover the compatible syntax from the legacy parser and
  formatter tests.
