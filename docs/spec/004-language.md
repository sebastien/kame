---
revision: 2
---

# Kame Language

## Purpose

Define the detailed grammar of the unified Kame/Kash language: lexical
structure, expressions, statements and definitions, rule declarations, includes
and modules, and source files. `055-unified-kame-kash-language.md` is the
normative overview; this specification refines it and must stay consistent with
it. Evaluation is specified in `005-evaluation.md`, command words and process
semantics in `017-kash.md`, patterns in `014-patterns.md`, and templates in
`016-templates.md`.

Expression, command, declaration and template syntax must each be independently
parseable and formattable. Source parsing composes those parsers; it never
creates a second representation or reparses generated text. The grammar and
classification rules below are independent of any parser implementation.

## 1. Source model

### 1.1 Encoding, spans and line endings

Source is UTF-8. Line endings `LF` and `CRLF` are accepted; canonical formatting
emits `LF`.

Parsers retain zero-based, half-open byte spans into a named source file.
Diagnostic rendering computes one-based lines and display columns; tabs advance
to the next eight-column boundary (`011-diagnostics.md`). Spans of embedded
grammars (a value fragment inside a command word, a command inside `$(…)`, an
expression inside `@{…}`) are authored spans in the enclosing file, never offsets
into synthetic text.

### 1.2 Comments

A comment starts with `#` at the start of a line or at a token position and runs
to the end of its physical line. In command words, `#` begins a comment only at
the start of a word, so `a#b` is one word. `#` inside strings, path atoms and
foreign-shell recipe text is literal. `//` is not a comment.

### 1.3 Line structure and continuation

A newline ends an expression, statement or declaration header unless:

- a delimiter (`(`, `[`, `{`, `$(`, `$?(`, `@{`) is still open;
- the line ends with a binary operator, `=>`, `=`, `?=` or `+=`;
- the next line is more indented and begins with `|>` or a binary operator.

Outside quoted strings, a backslash immediately followed by `LF` or `CRLF` acts as
whitespace in expressions, definitions and declaration headers, so a long rule
header may span several physical lines. Continued lines may be indented.
Parsers retain the original bytes, so spans and diagnostics still refer to the
authored lines and columns. Comments end at their physical line ending.
Backslashes in foreign-shell recipe bodies belong to the shell.

### 1.4 Parser contract

Parsers return an AST and zero or more diagnostics. Invalid syntax is never
silently discarded and never falls back to literal text, textual definition
values or an external shell: it is `PARSE_ERR` with the authored span. Parsing
never depends on grants, host availability, bindings, `PATH` or execution phase,
and never reads the filesystem or the environment.

## 2. Lexical structure

### 2.1 Names

```text
NAME       = (letter | "_") (letter | digit | "_" | "-")* ("?" | "!")?
ENV-NAME   = [A-Z] [A-Z0-9_]*
QUALIFIED  = NAMESPACE "/" (NAME | QUOTED-STRING)
NAMESPACE  = NAME
```

Letters and digits are ASCII. Examples of names are `build`, `_value`,
`sha256-file` and `exists?`. `42x`, `-name` and `name?more` are not names. A
digit-leading token is a number only when the whole token is a valid number;
otherwise it is `PARSE_ERR`.

- `-` is part of a name: `sha256-file` is one name. Subtraction needs whitespace
  on both sides (`a - b`).
- `?` or `!` may end a name once. `name?` is a name; `name?more` is not.
- Placeholder tokens are not names (§2.6). A positional placeholder `_N` uses
  canonical decimal without leading zeros. `_00` and `_value` are ordinary
  names.
- An `ENV-NAME` is a name of uppercase letters, digits and `_` starting with a
  letter. It takes part in the environment fallback of §3.13.

### 2.2 Qualified names and namespaces

`/` without surrounding whitespace, between a name and a name or quoted segment,
forms a qualified name. Division needs whitespace on both sides (`a / b`). An
unspaced `a/b` that is neither a qualified name nor a path atom is `PARSE_ERR`.
Namespaces have one level: `a/b/c` is `PARSE_ERR`. A quoted segment allows
names that are not identifiers: `x/"python3.12"`.

Namespaces live in their own table, separate from value bindings, so a variable
named `x` does not conflict with the `x/` namespace. The slash never makes a
reference a path.

| Namespace | Meaning |
| --- | --- |
| `x/NAME` | The external executable `NAME`, resolved by the tool resolver. Bypasses lexical bindings and builtins. |
| `env/NAME` | The environment store of the current process-like unit. Reading is tracked and capability-checked and yields `nil` when unset. |
| `std/NAME` | The standard library operation `NAME` (`007-library.md`). |
| `module/dir`, `module/file` | The authoring module's directory and source file, as absolute `Path` values. |

Import aliases (§7.2) add further namespaces. Reserved namespace names cannot be
used as aliases.

### 2.3 Keywords

Reserved words: `def`, `let`, `set`, `rule`, `task`, `service`, `always`,
`cached`, `generate`, `if`, `then`, `elif`, `else`, `match`, `for`, `in`,
`while`, `break`, `continue`, `return`, `yield`, `exit`, `defer`, `import`,
`from`, `as`, `export`, `include`, `include?`, `and`, `or`, `true`, `false`,
`nil`.

A reserved word cannot be a binding, parameter or target name. In command
position a reserved word is a statement keyword; to run an executable with a
reserved name, write `x/NAME`. `sep` is a keyword only inside the `@for`
template directive.

### 2.4 Numbers

```text
DIGITS     = digit ("_"? digit)*
BIN-DIGITS = bin-digit ("_"? bin-digit)*
OCT-DIGITS = oct-digit ("_"? oct-digit)*
HEX-DIGITS = hex-digit ("_"? hex-digit)*
DECIMAL    = "0" | nonzero-digit ("_"? digit)*
INTEGER    = "-"? (DECIMAL | "0b" BIN-DIGITS | "0o" OCT-DIGITS | "0x" HEX-DIGITS)
FLOAT      = "-"? DIGITS "." DIGITS ([eE] [+-]? DIGITS)?
           | "-"? DIGITS [eE] [+-]? DIGITS
```

Base-prefixed digits follow the same single-underscore rule. A leading or
trailing underscore, consecutive underscores, `1.`, `.5`, hexadecimal floats and
a sign after a base prefix are invalid. Integer literals must fit `int64`;
overflow is `PARSE_ERR`.

A leading `-` immediately followed by a digit is part of the numeric literal, so
every negative number has a literal form (`-1`, `-0.5`, `-1e3`). `neg VALUE`
negates a computed value. A `-` attached to a name (`-x`) is `PARSE_ERR` in
expression position; in a command word it is ordinary text (`mkdir -p`).

### 2.5 Booleans, nil and symbols

`true` and `false` are the booleans and `nil` is the nil value. They are
keywords, not symbols.

A symbol is `:` immediately followed by a name: `:name`, `:completed`. Signed
symbols `:+name` and `:-name` are also valid. There is no whitespace after `:`.
Symbols are values of their own kind, and `fmt` preserves that kind.

### 2.6 Placeholders

`_`, `__` and `___` are the first, second and third parameters of a placeholder
section; `_N` is the zero-based Nth parameter, `_0` through `_4095`. Larger
positions are `PARSE_ERR` and are rejected before any allocation. A placeholder
outside a section or a value-pipeline stage is `PARSE_ERR`. Section semantics
are in §3.6.

### 2.7 Strings

`"…"` strings decode the escapes `\"`, `\\`, `\n`, `\r` and `\t`, and
interpolate `@{EXPR}` (§3.9). `@@` is a literal `@`. A string may span lines;
the line endings inside it are part of its value. Any other backslash sequence
is `PARSE_ERR`.

`'…'` strings are raw: no escapes and no interpolation. A raw string cannot
contain `'`.

In expression position `$` has no meaning inside a string: `"$HOME"` is the five
characters `$HOME`. To embed a process result, interpolate a capture:
`"Revision: @{$(git rev-parse --short HEAD)}"`. Inside a command word, `"…"`
strings interpolate `$` references instead (§6.1, `017-kash.md`).

A quoted string is text in expression position. It is never reclassified as a
path, pattern, template or resource.

### 2.8 Path atoms

A path atom begins with `./`, `../`, `/` or `~/`, or with a leading
interpolation directly followed by `/` (`@{root}/suffix`). No other token is a
filesystem path; strings are never reclassified as paths. A path atom continues
until whitespace or an unbalanced delimiter: `,`, `)`, `]` or `}`. Inside a path
atom:

- `@{EXPR}` interpolates a value (§3.9).
- `{name}`, `{name:*}`, `{name:**}`, `{*}`, `{**}` and the other group forms of
  `014-patterns.md`, `029-pattern-extensions.md` and
  `041-pattern-predicates-and-substitution.md` are pattern groups.
- `*`, `**`, `?` and `[…]` bracket classes are glob syntax. Bracket classes
  belong to the path atom, so their delimiters are not mistaken for an enclosing
  list's.
- `\` escapes the following byte, making glob or group syntax literal.

Brace groups in a path atom are never record literals, and an interpolation
inside a group is `PARSE_ERR`. Glob and group syntax is recognized only in
authored static fragments: a value inserted with `@{…}` is literal path text and
cannot inject pattern syntax. Interpolated values must render as NUL-free text.

The kind of a path atom follows from its syntax (§3.10): plain atoms are `Path`
values, atoms with glob syntax or matcher groups are `PathPattern` values, and
atoms with only `{name}` reference groups are `PathTemplate` values. Rule
headers assign roles instead (§8.2).

Protocol resources are not path atoms. They are explicit values constructed with
`resource "URI"`; their syntax and canonical identity are specified in
`031-resource-protocols.md`.

## 3. Expressions

### 3.1 Grammar

```text
EXPR          = IF-EXPR | LAMBDA | FALLBACK-PIPE
IF-EXPR       = "if" EXPR "then" EXPR ("elif" EXPR "then" EXPR)* "else" EXPR
LAMBDA        = PARAMS "=>" EXPR
PARAMS        = "()" | NAME+ (NAME "...")? | NAME "..."
FALLBACK-PIPE = OR ("??" OR)* ("|>" STAGE)*
STAGE         = APP
OR            = AND ("or" AND)*
AND           = CMP ("and" CMP)*
CMP           = SUM (("==" | "!=" | "<" | ">" | "<=" | ">=") SUM)?
SUM           = PRODUCT (("+" | "-") PRODUCT)*
PRODUCT       = APP (("*" | "/" | "%") APP)*
APP           = POSTFIX (POSTFIX | "()" | "..." POSTFIX)*
POSTFIX       = ATOM ("." (NAME | INTEGER | QUOTED-STRING))*
ATOM          = NAME | QUALIFIED | ENV-NAME | LITERAL | PATH-ATOM
              | LIST | RECORD | "(" EXPR ")" | "$(" ACTION ")" | "$?(" ACTION ")"
              | "$&(" ACTION ")"
              | MATCH-EXPR | PLACEHOLDER
LITERAL       = NUMBER | STRING | SYMBOL | "true" | "false" | "nil"
LIST          = "[" (ITEM ("," ITEM)* ","?)? "]"
RECORD        = "{" (FIELD ("," FIELD)* ","?)? "}"
ITEM          = EXPR | "..." EXPR
FIELD         = (NAME | QUOTED-STRING) ":" EXPR | NAME | "..." EXPR
MATCH-EXPR    = "match" EXPR NEWLINE INDENT (ARM NEWLINE)+ DEDENT
ARM           = (PATTERN | "else") "=>" EXPR
```

Binding strength, from tightest to loosest:

| Level | Forms | Associativity |
| --- | --- | --- |
| 1 | atoms, `.` field access | left |
| 2 | application (juxtaposition) | left |
| 3 | `*` `/` `%` | left |
| 4 | `+` `-` | left |
| 5 | `==` `!=` `<` `>` `<=` `>=` | none |
| 6 | `and` | left |
| 7 | `or` | left |
| 8 | `??` | right |
| 9 | `\|>` | left |
| 10 | `=>`, `if … then … else` | right; extend as far as possible |

`add 1 2 * 3` is `(add 1 2) * 3`. Lambdas and `if` expressions are usually
parenthesized. Comparisons do not chain: `a < b < c` is `PARSE_ERR`. `=` is never
an operator. Binary operators require whitespace on both sides.

### 3.2 Application

`f a b` applies `f` to `a` and `b`, evaluating arguments left to right.
Parentheses only group: `f (g a) b`. `...xs` spreads a list into arguments; its
value must be a list, otherwise `EXPR_INVALID` when demanded.

- A bare name is always a reference to its value: `map object-path xs` passes
  the function. Reading a callable never invokes it.
- `f ()` calls `f` with zero arguments. `()` is valid only as the sole argument
  of an application or as a lambda's parameter list; elsewhere it is
  `PARSE_ERR`.
- Applying a non-callable value fails with `EXPR_INVALID`.
- Arity, rest-parameter and duplicate-parameter rules follow `005-evaluation.md`.

### 3.3 Lists, records and spreads

Lists are `[a, b, c]`; records are `{name: value, "key with space": v}`. Commas
are mandatory between elements, and a trailing comma is allowed. `[]` is the
empty list and `{}` the empty record. A field written as a bare `name` is
shorthand for `name: name`. Duplicate literal keys in one record are
`PARSE_ERR`.

```kame
argv = [...prefix, "printf", "%s\n"]
nums = [0, ...[1, 2], ...(map upper ["a", "b"]), 3]
config = {...defaults, port: 8080, host}
```

The `...` prefix must be adjacent to one complete expression: a reference,
literal, application, `$(…)` capture or parenthesized group. In a list, the
operand must evaluate to a list, whose elements are inserted in order, one level
deep; an empty list inserts nothing. In a record, the operand must evaluate to a
record, whose fields are inserted in order; later fields replace earlier fields
of the same key. A list spread inside a record, a record spread inside a list, or
a missing operand is `PARSE_ERR` when syntactically evident and `EXPR_INVALID`
when demanded otherwise. Ordinary items keep their nesting, so `[prefix]` and
`[...prefix]` differ.

Formatting, cloning and AST inspection preserve the authored spread marker and
its operand span; formatting never replaces a spread with a `concat` call.

### 3.4 Field access

`r.name`, `xs.0`, `xs.-1` and `r."key"` select a record field or a list index.
Negative indexes count from the end. Field access applies to any atom, including
a parenthesized expression: `(outcome).status`, `$?(make).ok`. Slicing uses the
library (`slice`), and computed keys use `get KEY RECORD`. A missing field is a
missing-lookup failure (§3.8).

### 3.5 Lambdas

`(x => x + 1)`, `(a b => a * b)`, `(() => now ())`, `(first rest... => …)`. A
rest parameter ends in `...`, appears once and comes last. A lambda closes over
its lexical scope. Its body is one expression; multi-statement functions are
written with block `def` (§5.1).

### 3.6 Placeholder sections

A parenthesized group is a **placeholder section** when its top level contains a
placeholder. The top level excludes nested parentheses, lambdas, lists, records,
`$(…)` and `$?(…)`. The section is equivalent to a lambda over the placeholders
it uses:

```kame
xs |> filter (ends? ".c" _)     # filter (s => ends? ".c" s) xs
pairs |> map (join __ _)            # map (a b => join b a) pairs
```

`(f _ __ ___)` is `(a b c => f a b c)`. A repeated placeholder refers to the same
parameter. Gaps in `_N` count toward the 4096-parameter limit. Grammar details,
semantics and formatting are specified in `014-patterns.md`.

### 3.7 Value pipeline `|>`

`VALUE |> STAGE` evaluates `VALUE` once. `STAGE` is a name or an application.

- If the stage's top level contains `_`, each `_` is replaced by the value. Only
  `_` is valid in a stage; `__`, `___` and `_N` there are `PARSE_ERR`.
- Otherwise the value is appended as the last argument: `xs |> map f` is
  `map f xs`, and `x |> f` is `f x`.

Parenthesized groups inside a stage are sections of their own:
`xs |> filter (ends? ".c" _)` filters `xs`. Pipe lowering does not evaluate
the subject early, and special forms keep their evaluation control:
`lookup ?? fallback |> f` is `f (lookup ?? fallback)`, and a stage is never
followed by `??`.

Parsing retains the authored chain and placeholder positions for formatting and
AST inspection. Formatting preserves pipes as pipes and nested calls as nested
calls; it never infers one from the other or changes evaluation order or
multiplicity. Layout follows `018-source-style.md`. `|` is never a value
operator; process pipelines are a separate command construct (§6.1).

### 3.8 Operators, conditionals and fallback

Arithmetic and comparison operators name library operations (`007-library.md`):

| Operator | Operation |
| --- | --- |
| `+`, `-`, `*`, `/`, `%` | `add`, `sub`, `mul`, `div`, `mod` |
| `==` | `eq?` |
| `!=` | `ne?` |
| `<` | `lt?` |
| `>` | `gt?` |
| `<=` | `lte?` |
| `>=` | `gte?` |

`==` is strict, kind-aware equality. The operator and the spelled operation are
the same function; formatting preserves whichever spelling the source used.
`not` is a library function: `not (ready? s)`.

`if C then A else B` evaluates only the selected arm; `elif` adds branches. The
expression form requires `else`. Only `false` and `nil` are false; zero, empty
strings and empty collections are true.

`and` and `or` are lazy: they evaluate operands left to right, stop at the
operand that decides the result, and return that operand.

`A ?? B` yields `A` unless `A` is `nil` or fails with a missing-lookup failure,
in which case it evaluates `B`. Missing-lookup failures are `REF_MISSING`,
`TOOL_MISSING`, `ENV_VAR_MISSING` and a missing field. Protected and other
failures propagate. `x/git ?? x/hg` picks the first available tool.

`if`, `and`, `or`, `??` and `match` are special forms: an operand is evaluated
only when selected, so an unselected operand's dependencies and effects never
occur. They must not be implemented as eager operations.

### 3.9 Interpolation

`@{EXPR}` interpolates in `"…"` strings, path atoms, rule headers, `.kmt`
documents and foreign-shell recipes. `EXPR` is any expression of §3.1:

```kame
greeting = "Hello @{project.name}"
summary = "Bundle of @{count files} files"
stamp = "Built @{now ()}"
```

`@{name}` reads the binding `name` and never invokes it; `@{f ()}` calls `f`
with zero arguments. There is no type-dependent invocation. Interpolation
renders strings, numbers, booleans, symbols and paths as text and `nil` as the
empty string. Lists, records and bytes are `EXPR_INVALID` unless an explicit
operation (`join`, `json`, `fmt`) renders them. `@@` is a literal `@`.
Interpolation never applies HTML escaping, shell quoting or source quoting
implicitly.

Delimiters, nested strings and embedded processes inside `@{…}` are balanced by
their own parsers. An unclosed `@{` is `PARSE_ERR` in every context. `@` not
followed by `{` or `@` is literal text in strings and templates. In command
words `@` is ordinary text, except that an unquoted `@{` is `PARSE_ERR` (`055`
§7.1).

Raw strings, `@raw` template bodies, `@@` escapes and command-word data are
inert. Formatting preserves these distinctions and escapes literal `@` where
needed so that reparsing cannot activate interpolation.

### 3.10 Paths, patterns and discovery

| Kind | Written as | Meaning |
| --- | --- | --- |
| `Path` | `./src/main.c`, `@{dir}/x.o` | One location |
| `PathPattern` | `./src/**/*.c`, `./src/{path:**}.c` | Matches paths; may capture |
| `PathTemplate` | `./build/{path}.o` | Builds a path from captures |

Groups with glob content (`{*}`, `{**}`, `{name:*}`, `{name:**}`) are matchers.
In a `PathPattern`, `{name}` is shorthand for `{name:*}` and `{_N}` is
`PARSE_ERR`. In a `PathTemplate`, `{name}` and `{_N}` are expansion references.
Matching is anchored to the entire path, including any literal suffix. Kinds
never convert implicitly. A path renders as text only in interpolation,
template output and argv.

Interpolation fixes literal parts when the atom is evaluated and leaves groups
deferred: `@{root}/{name:*}.@{ext}` constructs a matcher, and
`@{out}/{name}` constructs a template. To use a lexical binding in a path, write
`@{name}`; `{name}` is never resolved lexically. Deliberately dynamic pattern
syntax uses `pattern TEXT`.

```kame
captures = ./src/lib/math/vector.c |> capture ./src/{path:**}/{name}.c
output = captures |> bind ./build/{path}/{name}.o
```

`capture PATTERN PATH` returns a capture record or `nil`; `bind TEMPLATE
CAPTURES` returns a `Path`. Pattern `subst` and `resub` evaluate their
replacement only after matching, with named and positional captures available as
string bindings. See `014-patterns.md` and `038-direct-kash-and-values.md`.

A `PathPattern` is a value; it does not enumerate the filesystem. `files
PATTERN` returns a sorted, duplicate-free `List[Path]` and records a tracked
membership dependency. Zero matches produce `[]`, and one match is a one-item
list. Parsing and formatting never enumerate files. Evaluation keeps the read
authority, phase restrictions, dependency tracking and watch lifecycle of
`023-wildcard-sources.md`. In declaration context the result is a planning-time
snapshot, and a pattern overlapping a declared output is rejected with
`DISCOVERY_OVERLAP`. As a rule input, a wildcard path atom keeps dynamic
membership semantics (§8.4).

### 3.11 `match`

`match` dispatches a subject against ordered arms:

```kame
def object-path source = match source
	./src/{path:**}.c => ./build/@{path}.o
	./gen/{name}.c    => ./build/gen/@{name}.o
	else              => fail "not a source: @{source}"
```

An arm pattern is a path pattern, a string pattern or a literal. In arm-pattern
position a quoted string containing groups is a string pattern (`014-patterns.md`);
a group-free quoted string is a literal. The first matching arm runs; its named
captures bind as names in that arm's scope. Without an `else` arm, a nonmatch
yields `nil`. `else` must be last. The statement form (§5.4) takes indented
blocks instead of expressions after `=>`.

### 3.12 Commands inside expressions

| Form | Result |
| --- | --- |
| `f a b` | The function's return value. Raises on failure. |
| `$(CMD)` | The process graph's stdout, as text under `017-kash.md` capture rules. Raises on failure. |
| `$?(CMD)` | The invocation outcome record. Never raises for process or function failure. |
| `$&(CMD)` | Starts `CMD` asynchronously and returns its handle (`017-kash.md`). |

`$(…)`, `$?(…)` and `$&(…)` are expression atoms, valid wherever an expression is:
arguments, list and record items, lambda bodies, conditions and definition
right-hand sides. The expression parser owns the node and delegates its content
to the command parser: one process graph or command list in action context,
without blocks. Parsing is independent of capabilities and host availability;
execution uses the caller's execution context (`005-evaluation.md`). A typed
return value is never converted to stdout: `$(f x)` captures what `f` writes.

```kame
revisions = sources |> map (f => $(git log -1 --format=%h -- $f))
```

### 3.13 Name resolution in expressions

In expression position (value, declaration and template contexts, and value
operands in action context):

1. Lexical bindings: block bindings, parameters, module definitions and imported
   names.
2. The standard library (`std/NAME` is always available explicitly).
3. For an `ENV-NAME` only: the environment store, as a tracked,
   capability-checked read. Lowercase names never fall back to the environment.
4. Otherwise `REF_MISSING`.

An unset environment variable reached through step 3 fails with
`ENV_VAR_MISSING`; use `NAME ?? default`. `env/NAME` yields `nil` when unset and
an empty string when set but empty. Environment reads require the
environment-read grant and record a named dependency; parsing, formatting,
unused definitions and unselected branches never read the environment.
Command-position resolution is specified in §6.2.

## 4. Target templates

A target template contains literal text and zero or more capture groups. A
template with no group matches only its literal text, and rule headers classify
it as a literal path or name.

```kame
rule ./out.o : ./out.c
rule ./build/{stem:*}.o : ./src/{stem}.c
rule ./build/{path:**}/{name:*}.o : ./src/{path}/{name}.c
task live-{name} : ./sites/{name}
```

```text
GROUP     = "{" CAPTURE (":" PATTERN)? "}"
CAPTURE   = (letter | "_") (letter | digit | "_" | "-")*
PATTERN   = GLOB-ITEM+
GLOB-ITEM = "*" | "**" | "?" | CLASS | ESCAPED | LITERAL
```

`PATTERN` cannot contain an unescaped `{`, `}` or `:`. `ESCAPED` is `\` followed
by one byte. A character class is `[`, an optional leading `!`, one or more
literal bytes or ascending `a-z` ranges, and `]`; an unclosed or empty class is
`PARSE_ERR`.

In an output, `{name}` is equivalent to `{name:*}`. `*` matches one or more
non-`/` bytes, `?` exactly one non-`/` byte, and `**` one or more bytes including
`/`. Matching is anchored to the whole target, and captures are never empty.
When adjacent variable-width groups can match, captures use leftmost-shortest
matching. Reusing a capture name requires every occurrence to match the same
text.

Anonymous `{*}`/`{**}` output captures, positional input references `{_N}`,
regular-expression groups and runtime pattern construction are specified in
`029-pattern-extensions.md`; predicates and substitution in
`041-pattern-predicates-and-substitution.md`.

## 5. Statements and definitions

### 5.1 Top-level definitions

At the top level of a Kame program, and in the module scope of a `.kash` source
(§7.2):

```kame
version = "1.0"
sources = files ./src/**/*.c

def greeting name = "Hello @{name}!"

def compile source output
	mkdir -p (dirname output)
	cc -c $source -o $output
```

```text
VALUE-DEF    = NAME "=" (EXPR | NEWLINE BLOCK)
DEFAULT-DEF  = NAME "?=" EXPR
CONTRIB-DEF  = NAME "+=" EXPR
FUNCTION-DEF = "def" NAME PARAMS? ("=" EXPR | NEWLINE BLOCK)
```

- `NAME = EXPR` declares a **lazy, memoized**, order-independent value. The
  right-hand side is exactly one expression.
- `NAME =` followed by an indented block computes a lazy value from a block of
  `let`, `set`, control statements and `return`. The block takes the
  definition's context, so commands in it are context errors except when it is
  evaluated in an action phase.
- `def NAME = EXPR` (no parameters) is the same lazy value as `NAME = EXPR`.
  It is the only value-definition form at the `.kash` top level.
- `def NAME PARAM… = EXPR` declares an expression-bodied function. A
  zero-parameter function is `def NAME () = EXPR`, called as `NAME ()`.
- `def NAME PARAM…` or `def NAME ()` followed by an indented block declares a
  block-bodied function whose body is in action context.

Parameters use the lambda parameter grammar of §3.5, including one final rest
parameter. Definitions are lazy as specified in `005-evaluation.md`: aliases
(`a = b`) keep the referenced value's kind and dependencies, forward references
are valid, and cycles fail with `DEP_CYCLE` when demanded. Unused definitions are
never evaluated. A second `=` or `def` of the same name in one scope is
`DEF_INVALID`.

Top-level `NAME = EXPR` is not valid inside blocks; blocks use `let`. In a
`.kash` file the top level is an action block, so module definitions there are
written with `def`.

Definition values are typed: `n = 42` is a number, `name = "build"` is a string,
`flags = ["-Wall", "-O2"]` is a list, and `name = build` references the binding
`build`. There is no textual fallback.

### 5.2 Defaults and list contributions

`NAME ?= EXPR` registers a lazy default only when `NAME` is not already
registered. A later default is skipped without evaluating its right-hand side;
a second ordinary `=` definition is still `DEF_INVALID`. The operator is
contiguous: `name? = value` defines the name `name?`. Default function
definitions are invalid. Defaults are valid only at the top level of Kame
programs.

`NAME += EXPR` contributes a list to a value binding. The operator is
contiguous. It is not sequential assignment: registration collects every
selected contribution before evaluation, and ordinary bindings stay immutable.

```kame
checks ?= []
checks += [./tests/unit, ./tests/integration]
checks += extra-checks
extra-checks = [./tests/smoke]
```

- An earlier `=` or selected `?=` provides the base; without one the base is
  `[]`. A contribution registers the name, so later `?=` defaults are skipped,
  and later `=` declarations fail `DEF_INVALID`.
- The base and every contribution must evaluate to lists. Concatenation is
  shallow, keeps duplicates and item kinds, and follows source order, with
  includes expanded depth-first at their position. Non-list operands fail
  `EXPR_INVALID` at the offending operand; there is no coercion or arithmetic
  accumulation.
- The aggregate is evaluated only when demanded. Forward references, dependency
  tracking, suspension, invalidation and capability policy are those of ordinary
  definitions. Declaration-time conditions see only contributions selected
  before them.
- A contribution refers to the final binding, never to an earlier accumulator,
  so `checks += checks` and indirect cycles fail `DEP_CYCLE` when demanded.
- Contributions to functions, function contributions and `+=` in `.kash`
  sources are invalid. AST JSON marks each contribution with
  `contribution: true`; formatting preserves `+=`.

### 5.3 Configuration overrides

Value definitions (`=`), selected defaults (`?=`) and contribution aggregates
accept the explicit configuration channels of `009-cli.md`: `--define
NAME=VALUE`, operand `NAME=VALUE` and case-sensitive `KAME_NAME` supply literal
strings; `--set NAME=LITERAL` supplies closed typed data. An override replaces
the whole binding (for an aggregate, without evaluating its operands). Overrides
never evaluate configuration as code. Function definitions cannot be configured.

### 5.4 Blocks

A block is the indented lines after its header. Its indentation is the leading
whitespace of its first nonblank line. Each later nonblank line must begin with
exactly that prefix or end the block; a line that is still indented but has a
different prefix is `PARSE_ERR`. Tabs and spaces may not be mixed within one
block prefix. Each block has its own lexical scope, and so does each loop
iteration. Headers never end with `:`.

| Statement | Meaning |
| --- | --- |
| `let NAME = EXPR` | Evaluate eagerly when reached and bind immutably for the rest of the block. A later `let` of the same name shadows it. |
| `set NAME = EXPR` | Assign a `let` binding of an enclosing block in the same invocation. Cannot create a binding. Targeting a binding captured by a lambda or nested `def` is `PARSE_ERR`. |
| `set env/NAME = EXPR` | Write the unit's environment store; `nil` unsets. Action context only. |
| `def …` | Nested function, closing over the block scope. |
| `if EXPR` / `elif EXPR` / `else` | Conditional blocks. |
| `for NAME in EXPR`, `while EXPR` | Loops. |
| `match EXPR` | Indented `PATTERN =>` arms, each followed by a block or an expression on the same line. |
| `return EXPR?` | End the enclosing `def` invocation with a value (default `nil`). |
| `yield EXPR` | Emit one stream element; makes the `def` a generator. |
| `break`, `continue` | Loop control. |
| `exit EXPR?` | End the innermost process-like unit (status 0–255, default 0). |
| `defer STATEMENT` | Cleanup, as in `017-kash.md`. |
| Command statement | Anything else (§6.1). |

A statement is a binding only when it starts with `let` or `set`: `echo a = b`
is a command. Conditions of `if`, `elif` and `while` are expressions; to test a
command, use its outcome:

```kash
if $?(test -f ./ready).ok
	echo ready
for source in files ./src/*.c
	compile $source (object-path source)
while not (ready? server)
	sleep 1
```

Falling off the end of a `def` returns `nil`; there is no implicit
last-statement value. Inside a generator a bare `return` ends the stream and
`return EXPR` is `PARSE_ERR`. Semantics of outcomes, derived function statuses,
generators and `exit` are those of `055` §8 and `017-kash.md`.

## 6. Commands

### 6.1 Command statements and words

A command statement is a head followed by words. Words keep the typed-argv
semantics of `017-kash.md`:

| Word form | Meaning |
| --- | --- |
| bare text | A literal string |
| `'…'` | A literal string |
| `"…"` | A string with `$` interpolation |
| `$name`, `$name.field`, `$xs.0` | Reference substitution |
| `${REFERENCE}` | Braced reference, including `${ns/name}` |
| `$...name`, `$...{REFERENCE}` | List spread into separate argv entries |
| `(EXPR)` | Value fragment |
| `$(…)` | Captured stdout |

Adjacent fragments compose one word: `--out=(object-path f)`,
`prefix-$name.txt`. Scalars produce exactly one argv entry and are never split,
globbed or reparsed; lists must be spread explicitly; records and other
non-scalars are `EXPR_INVALID`. `Path` values render as their authored path
text. Bare command words are literal text, including `*` and `?`: they are not
expression atoms and never enumerate files.

An unbraced `$` reference stops at `/`: `$dir/file.txt` is `$dir` followed by
`/file.txt`. Qualified references need braces: `${env/HOME}/x`. Because of the
environment fallback, `$HOME/x` works unbraced. `@` has no meaning in command
words. A zero-argument call inside a word is written `(f ())`.

Pipelines (`|`), sequencing (`;`, `&&`, `||`), redirection, `>&out`/`>&err`,
`CMD ?` acceptance, recovery and async `&` follow `017-kash.md`,
`042`, `047` and `049`.

### 6.2 Command-position resolution

The head of a command statement resolves:

1. Lexical callable bindings, including imported names. A lexical binding to a
   non-callable is `EXPR_INVALID`; it never falls through to an executable.
2. The closed set of Kash builtins: `cd`, `wait`, `kill` everywhere; `jobs`,
   `fg`, `bg`, `alias`, `unalias`, `history`, `option` and `explain` in
   interactive sessions only. An interactive-only builtin in a script or rule
   body is `PARSE_ERR`.
3. Executables, through the tool resolver, using the environment store's
   `PATH`.

The standard library is not consulted in command position: `basename $f .c`
runs the executable, and `(basename f)` calls the library function. `std/NAME`,
`x/NAME`, `$REFERENCE` and path heads (`./tool`) are explicit heads. A head that
resolves to a function calls it with its words as arguments (bare words as
strings, `$x` and `(expr)` as typed values) and discards the return value at
statement level. In value and declaration contexts step 3 is never attempted,
so an unknown command fails before any effect.

## 7. Includes and modules

### 7.1 Includes

```kame
include ./rules/common.km
include? ./local.km
```

`include` and `include?` are source-composition statements, not runtime
operations. Each takes exactly one literal operand: a path atom beginning with
`./`, `../`, `/` or `~/`, without interpolation. Quoted strings, empty paths,
NUL, glob or pattern syntax, `@{…}` interpolation, references and computed
operands are `PARSE_ERR`.

- **Placement.** Includes are valid at the top level of a Kame program, inside
  declaration-time `if`/`for` blocks (§8.7), and unconditionally at the top
  level of a `.kash` source (`050-kash-libraries.md`). In expressions,
  definition bodies, lambdas, containers, other action blocks and recipe bodies
  an include is `EXPR_INVALID`, even in unused definitions and inactive
  branches. Parsing checks every authored branch.
- **Resolution.** The operand resolves relative to the directory of the
  including file, never the evaluation directory or root. Path identity is
  normalized lexically; this is not a physical realpath or symlink-cycle check.
  A selected include requires a file-backed source; a relative include or
  module path in an inline, stdin or `eval` source is `RES_INVALID`, so such
  sources may contain relative includes only in branches that are not selected.
- **Expansion.** An include splices the file's declarations into the caller's
  scope and declaration transaction at that position, depth-first, sharing
  scope and source order. The included file is a Kame program; Kash library
  inclusion is specified in `050-kash-libraries.md`. Included sources keep their
  own authored spans.
- **Cycles and repetition.** An include whose target is in the active include
  ancestry is `DEP_CYCLE`, naming the chain. Repeated nonrecursive includes expand again and register
  again under ordinary collision rules: defaults may be skipped, contributions
  may repeat, and conflicting definitions or producers fail. Includes are not an
  include-once mechanism.
- **Optional includes.** `include?` skips only a missing file. An existing
  unreadable file, a directory, a malformed source or a cycle still fails before
  execution. Source tracking retains selected missing optional paths so watch
  notices their creation. AST JSON marks the statement `optional: true`.

Diagnostics for conflicting declarations retain include provenance
(`011-diagnostics.md`).

### 7.2 Imports and exports

```kame
import ./lib/c                      # namespace c/…
import ./lib/docker as docker       # namespace docker/…
from ./lib/c import compile, object-path
from ./lib/c import compile as cc-compile
export compile, object-path
```

```text
IMPORT = "import" MODULE-PATH ("as" NAME)?
         | "from" MODULE-PATH "import" IMPORT-NAME ("," IMPORT-NAME)*
IMPORT-NAME = NAME ("as" NAME)?
EXPORT = "export" NAME ("," NAME)*
```

- `import PATH` loads a module once by normalized source identity and binds its
  exports as a namespace named after the file stem, or after the `as` alias.
  `from PATH import …` binds selected exports directly, with optional aliases.
  Module paths are literal and resolve relative to the importing file, like
  include operands. A path that does not end in `.km`, `.kame`, `.kash` or
  `.ksh` tries those suffixes; several existing candidates are `RES_INVALID`,
  none is `FS_ERR`, and importing a `.kmt` is `RES_INVALID`.
- `export` is top-level only and declares the public interface. Without an
  `export` statement a module exports nothing. Exporting an undefined or
  imported name is `DEF_INVALID`; `export` cannot re-export imported names, so
  a facade defines `def f = lib/f`. Exporting a name twice is allowed.
- Importing evaluates nothing eagerly, registers none of the module's
  declarations and runs no commands. A module may export functions returning
  `Rule` values; the importer registers them with `generate`.
- Import cycles are `DEP_CYCLE`, naming the chain. Two imports binding the same
  namespace, a reserved or invalid stem without a valid `as`, a reserved alias,
  and a `from` name colliding with a module definition or another `from` name
  are `DEF_INVALID`. Importing a name the module does not export is
  `REF_MISSING`.
- Imported names resolve before builtins and executables in command position
  without changing effect authority. Kash module specifics are in
  `050-kash-libraries.md`.

## 8. Rules and declarations

### 8.1 Forms

```kame
rule ./build/{name}.o : ./src/{name}.c
	cc -c $inputs.0 -o $outputs.0

rule ./build/{name}.js ./build/{name}.map : ./src/{name}.ts
	tsc $inputs.0 --outDir ./build

rule always ./build/stamp : ./src/*.c | ./build/dir
	date > $outputs.0

rule ./build/config.json : ./config.km = json config

task test : ./build/app
	./build/app --test

task cached lint : ./src/*.c ; {env: {MODE: mode}}
	lint $...inputs

task default : test

task clean
	rm -rf ./build

service dev
	npm run dev
```

```text
DECL        = FILE-RULE | TASK | SERVICE
FILE-RULE   = "rule" ("always")? OUTPUTS (":" INPUTS?)? META? BODY?
TASK        = "task" ("cached")? TARGET-NAME TARGET-ARGS? (":" INPUTS?)? META? BODY?
SERVICE     = "service" TARGET-NAME TARGET-ARGS? (":" INPUTS?)? META? BODY?
OUTPUTS     = OUTPUT (WS+ OUTPUT)*
OUTPUT      = PATH-ATOM | QUOTED-STRING | "@{" EXPR "}"
TARGET-NAME = NAME | NAME-TEMPLATE | QUOTED-STRING | "@{" EXPR "}"
INPUTS      = GROUP (WS* "," WS+ GROUP)* (WS* "|" WS+ GROUP)?
GROUP       = INPUT (WS+ INPUT)*
INPUT       = PATH-ATOM | NAME | QUOTED-STRING | "@{" EXPR "}"
META        = ";" RECORD
BODY        = "=" EXPR | NEWLINE INDENTED-BLOCK
```

The colon is optional when there are no inputs (`task clean`). Separators
inside quoted strings, path atoms and balanced `@{…}` or record delimiters are
not header punctuation. Whitespace inside quoted strings and balanced
interpolations does not split header items. `022-target-arguments.md` specifies the named
arguments (`TARGET-ARGS`) that can follow a task or service name.

### 8.2 Classification

The leading keyword classifies the declaration:

- **File rule** (`rule`): one or more outputs, each an explicit path, path
  template or computed path. A file rule may have several outputs.
- **Phony task** (`task NAME`): exactly one name or name template.
- **Cached task** (`task cached NAME`): exactly one name or name template.
- **Service** (`service NAME`): exactly one name or name template; lifecycle
  metadata and process ownership are specified in `024-managed-services.md`.

A `NAME-TEMPLATE` contains a target capture and no explicit path prefix
(`live-{name}`). A path in a task or service target, or a name in a file-rule
output, is `PARSE_ERR`. An output token such as `out.o` that is neither a path
atom nor valid in its position is `PARSE_ERR`, and the diagnostic suggests
`./out.o`. A literal quoted output is valid in a file rule only when its decoded
value begins with an explicit path prefix.

`rule always` marks a file rule as always stale without changing its artifact
kind, inputs, captures or output verification. `always` and `cached` are
keywords and are not valid in other positions of a header. AST and plan JSON
emit `always: true` when set; rerun and sharing semantics are in
`006-runtime.md`.

Output atoms are matchers (`{name}` captures as `{name:*}`). Input atoms are
templates over those captures, so `./src/{name}.c` in the inputs reuses the
output's `name` capture.

### 8.3 Computed outputs

An output written as a standalone `@{EXPR}` contributes values; an output atom
containing `@{…}` renders one target string.

```kame
build-dir = ./build
rule @{build-dir}/{name}.o : ./src/{name}.c
	cc -c $inputs.0 -o $outputs.0

extra-outputs = [./build/one, [./build/two, nil], []]
rule @{extra-outputs} : ./seed
	generate-all $outputs.0 $outputs.1

chosen = "chosen"
task @{chosen}
	echo $chosen
```

- Output expressions resolve in pure registration context before target
  selection (`005-evaluation.md`). All outputs resolve and validate before any
  rule becomes available or any recipe executes. Configured definitions and
  overrides apply. Resolution does not depend on the requested target, the
  rule's captures, target arguments, or `inputs`/`outputs`. Host requests,
  reads, process execution and effects are forbidden, including through lazy
  definitions and helpers.
- A standalone interpolation contributes strings, paths or pattern values;
  nested lists flatten recursively in authored order, and `nil` and empty lists
  contribute nothing. Other types, empty strings and an empty complete output
  set are `EXPR_INVALID`. An interpolated atom or quoted string renders exactly
  one target string, without list splicing.
- Expressions are expanded first; each resolved value is then parsed as a target
  template, classified and validated with the complete output set before
  registration. Capture syntax supplied by expression results is active. No
  second interpolation pass is performed on computed text.
- Explicit prefixes remain mandatory: with `build-dir = "build"`,
  `@{build-dir}/{name}.o` is not a file pattern. Invalid resolved path or name
  shapes and malformed target templates are `PARSE_ERR` at the authored output
  span. Classification and cardinality restrictions apply to the resolved
  output set.
- Duplicate resolved outputs within one rule are `PARSE_ERR`. Duplicate literal
  targets across authored and generated rules are `TGT_AMBIG`; pattern
  precedence and ambiguity rules are unchanged.
- Diagnostics retain authored spans. Formatting and parse ASTs retain the
  authored expressions; plans and graph inspection expose resolved outputs.
  Watch recompilation resolves a fresh output set under the same pure policy.

### 8.4 Inputs and prerequisite groups

Input items are path atoms, names, quoted strings and `@{…}` interpolations.

- **Groups.** Whitespace-separated items form one parallel group. A comma ends
  the group and starts the next: every prerequisite in a group finishes before
  the runtime evaluates or requests the next group. A comma follows an item and
  is followed by whitespace and the next group. The comma adds a scheduling
  barrier only; it does not change input identity.
- **Order-only.** `|` begins the order-only prerequisites (`006-runtime.md`).
  Order-only items may themselves be grouped with commas. Sequencing is
  independent of normal versus order-only purpose.
- **Computed inputs.** A standalone `@{EXPR}` contributes values; strings,
  paths, resources and lists flatten in authored order into its group, and `nil`
  and empty lists contribute nothing while keeping the barriers around them. An
  interpolated path atom such as `@{root}/js/{path}.js` evaluates as a path
  expression with dependencies retained, and its `{path}` groups resolve
  against the rule's captures.
- **Quoted inputs.** Quoted inputs render as strings with `@{…}` interpolation;
  bare `{path}` in a quoted input stays literal. Quoted inputs without
  interpolation are static literal inputs, including literal wildcard bytes.
  Computed bytes are never reparsed for captures or interpolation, and decoding
  never performs a second interpolation or backslash pass before capture
  matching. Capture and diagnostic spans keep authored coordinates.
- **Wildcards.** An unquoted path atom with `*`, `?` or `[` outside a group is a
  wildcard input. It lowers to the tracked `files` discovery, produces sorted
  files and contributes nothing when unmatched; membership changes invalidate
  dependent output identity. Each reached pattern is a lazy shared glob
  resource whose membership watch updates (`023-wildcard-sources.md`).
  Formatting preserves the authored token, and AST JSON marks it `wildcard`.
- **Names.** A name input is a task or service prerequisite.

Input discovery is read-only: sequencing never permits host reads or processes
during registration, and read-only inspection never executes a prerequisite to
cross a barrier. Schema-2 build inspection exposes each prerequisite's
one-based sequence group and the barriers and stages of
`037-build-inspection.md`. AST JSON uses `pattern` for output and input capture
patterns and `template` for decoded string or interpolation syntax.

### 8.5 Metadata

A header may end with `; RECORD`:

```kame
task cached lint : ./src/*.c ; {shell: kash, env: {MODE: mode}}
rule ./build/report.txt : ./data.csv ; {shell: "bash", env: {LC_ALL: "C"}}
```

- Supported keys are `shell` and `env` for every rule kind; `authority`, `tty`
  and `foreground` (`051-kash-session-authority.md`); the service keys of
  `024-managed-services.md`; and file-rule `executor`
  (`032-remote-execution.md`). Duplicate keys and keys unsupported for the rule
  kind are errors.
- `env` is a record whose keys are environment names matching
  `[A-Za-z_][A-Za-z0-9_]*` and whose values are strings, possibly computed by
  pure expressions. Empty values are valid; NUL is not. It is a child assignment
  record, not an environment-read or unset grant.
- `shell` overrides the program-wide `SHELL` definition for this rule. It
  accepts `kash` (the default Kash body), an executable string (`"bash"`, with
  `-c` implied), an explicit path (`/bin/sh`) or a nonempty argv list
  (`["/bin/bash", "-eu"]`).
- Metadata values are evaluated under planning policy before prerequisite
  recipes execute. A `;` inside a quoted input or balanced interpolation is part
  of that input.

Metadata is separate from prerequisite items and keeps its authored field order
in formatting and AST/plan JSON. Inheritance, override precedence and
shared-context failures are in `006-runtime.md`.

### 8.6 Bodies

A body is either `= EXPR` on the header line (continuing per §1.3), or the
indented block after the header.

**Indentation.** A body line belongs to the rule when it begins with at least
one tab or space. The exact leading whitespace of the first nonblank body line
is the rule indent; every nonblank body line must begin with that prefix. The
parser removes the prefix and preserves further indentation. A line with a
shorter or different prefix ends the rule, or is `PARSE_ERR` when still
indented. Indented text without a preceding header is `PARSE_ERR`. Canonical
formatting uses one tab per body level.

**Body bindings.** The body binds `inputs` (all inputs in order), `outputs`
(all outputs), `changed-inputs` (unique normal file inputs whose content digest
differs from the recorded digest, in file rules), the named captures and target
arguments, and closes over its lexical scope. Index them as ordinary lists:
`$inputs.0`, `outputs.-1`, `$...inputs`. These are ordinary lexical names;
out-of-range indexes use the ordinary index diagnostics of `005-evaluation.md`.

**Kash body.** By default a body is a Kash action block parsed in full before
execution, with no template pass (`038-direct-kash-and-values.md`). `$` words
and `(EXPR)` fragments supply typed values; `@` has no meaning. If the block
ends with `return VALUE` and `VALUE` is not `nil`, the value is published to the
single output under `044-value-producing-rule-bodies.md`. `exit N` ends the
rule body with status `N`.

**Expression body.** `= EXPR` evaluates one expression and atomically publishes
its text or bytes to the single file output under `044`. No process or template
expansion is involved. Content computation is read-only, including reached
lazy definitions.

**Foreign-shell body.** When `shell` (or `SHELL`) selects an external shell, the
body is a `plain`-style document template (`016-templates.md`) rendered to one
shell script, which Kame does not parse:

- A whole-line, keyword-gated directive (`@if`, `@elif`, `@else`, `@for`,
  `@with`, `@match`, `@case`, `@let`, `@include`, `@raw`, `@end`) is removed
  together with its rule indent and line ending and contributes no shell text.
- Inline `@{EXPR}` interpolates and `@@` is a literal `@`. Inputs and outputs
  are `@{inputs.0}`, `@{outputs.0}` and `@{join " " inputs}`.
- `$`, quotes and backslashes belong to the shell and pass through. Other `@`
  text, such as `@media` or `user@host`, is literal shell text.
- A malformed `@{` is `PARSE_ERR`; there is no literal recovery.
- Template expansion is read-only, including reached lazy definitions.

```kame
SHELL = "bash"

rule ./build/{name}.css : ./src/{name}.scss
	@if minify
	sass --style=compressed @{inputs.0} > @{outputs.0}
	@else
	sass @{inputs.0} > @{outputs.0}
	@end
```

### 8.7 Declaration-time control flow

Top-level `if`, `elif`, `else` and `for` in a Kame program run in declaration
context:

```kame
variants = ["debug", "release"]

for variant in variants
	let flags = if variant == "debug" then ["-g"] else ["-O2"]

	rule ./build/@{variant}/{name}.o : ./src/{name}.c
		cc $...flags -c $inputs.0 -o $outputs.0

if mode == "debug"
	include ./debug.km
elif mode == "local"
	include? ./local.km
else
	include ./release.km
```

- Declaration blocks contain `let`, nested control, declarations, `include`
  and `generate`. Commands are `PARSE_ERR` there. `if` without `else` selects
  nothing when no condition holds.
- `@{variant}` is fixed when the declaration is evaluated; `{name}` stays a
  capture. Each declared rule closes over its own iteration scope.
- Registration order follows source and iteration order.
- Inactive branches register nothing, and their conditions and includes are not
  evaluated, read, tracked or cycle-checked.

Conditions and iterated expressions run before final registration or execution.
They may use pure expressions, tracked read-only discovery, and definitions
registered earlier in source order, including those from earlier selected
includes and earlier explicit sources. They cannot depend on later declarations
or on effects. Explicit literal configuration and `KAME_NAME` follow ordinary
override precedence and environment authority; they do not authorize host
operations in conditions. These restrictions propagate through lazy definitions
and helpers, and runtime grants do not widen them. Path operations in
conditions use the root directory (including `-C`): `abspath` resolves against
it without filesystem access. Unknown references, cycles, capability use and
effectful operations fail with their ordinary codes before any recipe executes.

Failures identify the directly failing authored expression and retain helper
calls, lazy-definition evaluation, enclosing iterations and the selecting
condition as propagation context, rather than reporting the outer `if` or `for`
header. Native and embedding-host composition follow the provenance and
backtrace requirements of `011-diagnostics.md`; queries without authored origins
never report synthetic offsets as file locations.

### 8.8 Rules as values and generation

In expression position `rule`, `task` and `service` construct a `Rule` value
without registering it. The header and body follow the same grammar and close
over the lexical scope:

```kame
def object-rule source =
	rule (object-path source) : source
		cc -c $inputs.0 -o $outputs.0

generate (sources |> map object-rule)
```

In expression position a parenthesized expression is valid as an output or
input item. `generate EXPR` registers a `Rule` or a list of `Rule` values in the
current declaration transaction and is valid only in declaration context. All
declarations, authored or generated, share validation, collision detection,
target identity, freshness, ownership and scheduling. The transaction, allowed
work, limits and provenance are specified in `055` §10.5 and
`025-generated-declarations.md`.

### 8.9 Consumer composition

A reusable rule module exposes consumer settings with `?=` defaults and
documents its contribution bindings and rule entry points. Consumers declare
settings before including the module and contribute afterwards:

```kame
build-dir = ./build/custom
include ./sdk/rules.km
checks += ["project-check"]

task project-check
	./scripts/check
```

The module supplies `build-dir ?= ./build` and a list-valued `checks` binding;
`"project-check"` names a task prerequisite when it reaches a rule header
through `@{checks}`. No declaration reordering, mutable hooks or process
execution is implied.

## 9. Source files

### 9.1 Suffixes and top levels

| Suffix | Top level |
| --- | --- |
| `.km` (alias `.kame`) | Kame program: definitions, defaults, contributions, imports, exports, includes, declarations, declaration-time control flow and `generate` |
| `.kash` (alias `.ksh`) | Kash program: an action block (`017-kash.md`) |
| `.kmt` | Document template (`016-templates.md`) |

A Kame program may hold value definitions, declarations or both; there is no
separate build-program suffix. Comments and blank lines never become
statements. Commands at the top level of a Kame program are `PARSE_ERR`. The
parser uses keywords to distinguish declarations from definitions, so rule
headers and record or expression punctuation never conflict.

Default entry files are `Kamefile`, `Makefile.km`, `make.km` and
`src/km/main.km`. File execution, named value and target entries, program
arguments and `kame do run` are specified in `009-cli.md`. Parsing and
formatting never execute a program. The `expr` parser parses exactly one
expression independently of the program parser; `eval TEXT` uses it and never
parses statements, declarations or commands.

### 9.2 Roots

`include` and `import` paths resolve relative to the directory of the current
source file. All other relative paths, including rule inputs and outputs,
resolve against the root directory: the entry file's directory for Kame
programs and the invocation's working directory for Kash programs. `-C`
overrides both. Relative path values keep their authored form; host operations
and rule identity resolve them against the root. A module reaches its own files
through `module/dir`: `@{module/dir}/templates/page.kmt`.

### 9.3 Multiple sources

The runner may compose several file and inline sources (`009-cli.md`). Each is
parsed with the grammar its suffix selects, keeping its own spans, before a
shared program scope is registered; heterogeneous text is never concatenated and
reparsed. A balanced expression, rule body or control block cannot start in one
source and finish in another. Duplicate top-level definitions across sources
fail like duplicates within one source. Forward visibility does not merge
block scopes or turn definitions into sequential assignments.

## 10. Formatting

`kame do parse` and `kame do fmt` handle every context of this grammar without
evaluating anything. Canonical layouts and authoring conventions are specified
in `018-source-style.md`. The formatter never renames or reorders source items.

Formatting is AST-based and canonical:

- `LF` line endings and no trailing whitespace.
- One tab per block level, including rule bodies; blank body lines carry no
  indentation.
- `, ` between list and record elements; spaces around binary operators, `=`,
  `?=`, `+=`, `=>` and the rule `:`.
- No trailing `:` on block headers.
- Blank lines between top-level items, between body lines, and at the start and
  end of the source keep their count. A blank line between a rule header and
  its first body line is removed.
- Sources end with a newline; input without a terminal newline gains exactly
  one.

Formatting preserves comments in source order, path and literal kinds, quoted
versus raw strings, pipes versus nested calls, sections, spreads, authored
wildcard and interpolation tokens, and metadata order. It is idempotent, and
parsing formatted output produces an AST equivalent to the original excluding
spans.

## 11. Acceptance tests

- Each grammar (expression, command, declaration, template) parses and formats
  independently; program parsing composes them without reparsing text.
- The same line parses as a command in action context and as an expression in
  value context. Changing a binding, import or `PATH` never changes the parse
  tree or formatted output.
- Names: `sha256-file` is one name, `a - b` subtracts, `a/b` is a qualified name
  or `PARSE_ERR`, `a/b/c` is `PARSE_ERR`, `-1` is a literal and `-x` is
  `PARSE_ERR` in expressions.
- Numbers reject misplaced underscores, `1.`, `.5`, hex floats and `int64`
  overflow.
- Strings decode only the specified escapes; raw strings are inert; `$` is
  literal in expression strings; `@@` yields `@`; an unclosed `@{` is
  `PARSE_ERR` everywhere.
- Lists and records require commas, accept trailing commas and shorthand fields,
  and reject duplicate literal keys. Spreads keep their markers and spans through
  parse, clone, format and AST output; spreading the wrong kind fails.
- Brace groups in path atoms never parse as records; bracket classes adjacent to
  list delimiters stay in the path atom.
- Application: whitespace application, `f ()`, grouping and the precedence table
  hold; comparisons do not chain; applying a non-callable is `EXPR_INVALID`.
- Field access works on names and parenthesized expressions, including negative
  indexes and quoted keys; a missing field is recoverable with `??`.
- `if`, `and`, `or`, `??` and `match` evaluate an operand only when selected.
  `and`/`or` return the deciding operand; `??` recovers only missing lookups.
- `match` selects the first matching arm, binds named captures and returns
  `nil` without an `else` arm.
- Operators and their spelled operations are the same functions; formatting
  preserves the authored spelling.
- Placeholder sections parse, format idempotently and evaluate as lambdas at
  their stated boundaries; a placeholder outside a section or stage is
  `PARSE_ERR`; positions above `_4095` are rejected before allocation.
- `|>` inserts last or at `_`; pipes, nested calls and placeholder positions
  survive parse/format/parse without changing evaluation order, multiplicity,
  dependencies or effects. `|>` and `|` produce distinct trees.
- `@{name}` reads and `@{f ()}` calls in strings, path atoms, rule headers,
  templates and foreign-shell bodies alike; lists and records require explicit
  rendering.
- Path atoms classify as `Path`, `PathPattern` or `PathTemplate`; mixed matcher
  and reference groups are rejected; inserted values never inject pattern
  syntax; `{name}` is never resolved lexically.
- `files PATTERN` returns sorted lists, tracks membership and never runs during
  parsing or formatting; declaration-time discovery overlapping a declared
  output is `DISCOVERY_OVERLAP`.
- `$(…)` and `$?(…)` delegate to the command parser, retain embedded spans and
  parse and format without host effects.
- Expression-position names resolve lexically, then `std`, then uppercase
  environment; an unset bare `ENV-NAME` is `ENV_VAR_MISSING`, `env/NAME` is
  `nil`. Command position resolves lexically, then builtins, then executables,
  and never consults the standard library.
- Command words: `$name`, `${ns/name}`, `$...list` and `(expr)` fragments
  compose; `$dir/file` stops at `/`; scalars are never split or globbed.
- Target templates match `./archive.o` against `./{stem:*}.o` with
  `stem = "archive"`; `*` does not cross `/` and `**` does; empty captures and
  malformed classes are rejected; a group-free template is a literal target.
- Definitions: `=` is lazy and typed, `def` with `=` or a block declares
  functions, duplicate definitions are `DEF_INVALID`, cycles are `DEP_CYCLE`.
- `?=` defaults skip without evaluating; `+=` aggregates in source order, rejects
  non-list operands and self-reference, and is replaced whole by configuration
  overrides.
- Blocks enforce a consistent indentation prefix; `let` is eager; `set` assigns
  only existing, uncaptured bindings; headers have no trailing `:`.
- Includes: literal path-atom operands only, quoted strings are `PARSE_ERR`;
  source-relative resolution, `RES_INVALID` for relative paths in sources that
  are not file-backed; depth-first expansion into the caller's scope;
  `include?` skips only a missing file and tracks it for watch; active cycles
  are `DEP_CYCLE`; repeated includes register again; includes in expression or
  action positions other than the `.kash` top level are `EXPR_INVALID` even
  when unused or inactive.
- Imports expose only exported names, register no declarations and reject
  cycles with `DEP_CYCLE`; duplicate namespaces, reserved or invalid aliases,
  colliding `from` names and exports of unknown or imported names are
  `DEF_INVALID`; importing a non-exported name is `REF_MISSING`; repeated
  exports are accepted.
- Rules: the keyword classifies the declaration; `rule out.o : in.c` is
  `PARSE_ERR` with a `./out.o` suggestion; tasks and services take exactly one
  name; `rule always` sets `always: true`.
- Computed outputs cover paths and names, explicit-prefix validation, quoted
  interpolation, expression-supplied captures, nested lists, `nil` and empty
  outputs, cardinality and kind errors, duplicate targets (`PARSE_ERR` within a
  rule, `TGT_AMBIG` across rules), configured overrides, indirect forbidden
  operations, unavailable rule-local bindings, authored diagnostics, formatting
  idempotence, watch recompilation and native/WASM parity.
- Prerequisite groups: commas add barriers without changing identity, `nil` and
  empty computed inputs keep barriers, `|` introduces order-only inputs, and
  inspection reports one-based sequence groups.
- Wildcard inputs track membership, contribute nothing when unmatched, and
  quoted wildcard bytes stay literal.
- Metadata records accept only supported keys for the rule kind, reject
  duplicates, and keep their order through formatting and JSON.
- Rule bodies: consistent indentation prefixes, Kash bodies parsed before
  execution, `return VALUE` and `= EXPR` publish under `044`, foreign-shell
  bodies remove directive lines, interpolate `@{…}` and pass `$` and `@media`
  through.
- Declaration-time `for`/`if` register deterministic rule sets with
  per-iteration closures; inactive branches neither evaluate conditions nor read
  includes; conditions see only earlier selected definitions and authorized
  configuration, never host effects; failures report the authored failing
  expression with condition context.
- `generate` registers `Rule` values transactionally with provenance.
- Continued lines (open delimiters, trailing operators, backslash-newline)
  parse and format idempotently, keeping authored spans, CRLF handling and
  comment boundaries; foreign-shell backslashes stay shell-owned.
- Formatting is idempotent, preserves comment order and blank-line counts, uses
  tabs for bodies with further indentation preserved, and round-trips to an
  equivalent AST.
- Golden fixtures cover the expression, command, declaration, template and
  program grammars and their canonical formatting.
