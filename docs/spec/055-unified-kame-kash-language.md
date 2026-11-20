---
revision: 2
---

# Unified Kame/Kash Language

## 1. Purpose

Define one source language for Kame values, Kash actions, build declarations and
document templates. Keep Make's useful features: parameterized and pattern
rules, conditional and computed declarations, and includes. Drop implicit
textual expansion and reparsing.

This is a **clean break**. The previous surface syntax is removed with no
compatibility mode, legacy parser or migration tool. Sources written in the old
syntax are rejected with ordinary parse diagnostics.

This specification is the normative overview of the language at major
revision 2. The other specifications use this syntax and refine its parts:

- `004-language`: grammar detail.
- `005-evaluation`: evaluation.
- `014-patterns`, `029-pattern-extensions`, `041-pattern-predicates-and-substitution`:
  patterns.
- `016-templates`, `030-template-extensions`: templates.
- `017-kash`, `038-direct-kash-and-values`, `050-kash-libraries`: Kash and
  modules.
- `025-generated-declarations`: generation.
- `007-library`: operations, data-last (§4.6).

They also keep their contracts for capabilities, ownership, process lifetime,
streams, freshness, caching, output ownership, services, diagnostics and WASM
parity. Where another specification conflicts with this one, this one
governs.

### Principles

1. **Context selects the grammar.** Value, declaration and template contexts
   use expression grammar. Action context uses command grammar. Which grammar
   applies is decided syntactically and never depends on name resolution.
2. **One value model.** Every context shares typed values and lexical bindings.
3. **Separate value and process pipelines.** `|>` transforms values; `|`
   connects process streams. Neither coerces into the other.
4. **Context restricts effects, not names.** A name resolves the same way
   everywhere. The context and phase decide whether the resolved operation may
   run.
5. **Paths and patterns are typed.** There is no implicit conversion between
   path, pattern, template and text.
6. **Declarations are data.** Rules are values. They can be written directly
   or computed. Nothing is generated as source text and reparsed.
7. **Imports do not build.** Importing a namespace and registering
   declarations are separate operations.
8. **Functions and processes share an invocation model.** A call yields a value.
   Every invocation also has an inspectable outcome.

## 2. Contexts and source files

| Context | Grammar | Permitted work |
| --- | --- | --- |
| **Value** | Expression | Computes typed values. Effects only where the evaluation phase authorizes them. |
| **Declaration** | Expression and declaration statements | Constructs and registers declarations. Pure computation and tracked read-only discovery only. No processes or writes. |
| **Action** | Command and block statements | Invokes functions and executables, coordinates processes, runs control flow, performs granted effects. |
| **Template** | Literal text with `@` directives and interpolation | Renders text. Expressions are evaluated in value context. |

| Suffix | Top level |
| --- | --- |
| `.km` (alias `.kame`) | Kame program: top-level definitions, imports, includes and declarations |
| `.kash` (alias `.ksh`) | Kash program: the top level is an action block |
| `.kmt` | Document template |

There is no separate build-program suffix: a Kame program may hold value
definitions, declarations, or both. The CLI selects declared targets or
definitions by name (`009`).

These syntactic positions switch context:

| Position | Enters |
| --- | --- |
| A rule, task or service body; a block `def` body; the `.kash` top level | Action |
| `$(…)`, `$?(…)` and `$&(…)` | Action (one process graph or command list) |
| `(…)` or `${…}` inside a command word; the right-hand side of `let`, `set` and `return`; conditions | Value |
| `${…}` in Kame strings, path atoms and rule headers | Value |
| `@{…}` in document templates and foreign-shell recipes | Value |
| Top-level `if`/`for` in a Kame program | Declaration |

A parse failure never falls back to literal text or an external shell. A
context-denied operation fails before its effect occurs. Parsing never depends on
grants, host availability, bindings or execution phase.

## 3. Lexical structure

### 3.1 Source

Source is UTF-8, and `LF` and `CRLF` are accepted. Canonical formatting emits
`LF`. Spans are zero-based, half-open byte offsets, as specified in `011`.
A comment starts with `#` at the start of a line or of a token position and
runs to the end of the line. In command words, `#` begins a comment only at the
start of a word. `//` is not a comment.

### 3.2 Names and namespaces

```text
NAME       = (letter | "_") (letter | digit | "_" | "-")* ("?" | "!")?
ENV-NAME   = [A-Z] [A-Z0-9_]*
QUALIFIED  = NAMESPACE "/" (NAME | QUOTED-STRING)
NAMESPACE  = NAME
```

- `-` is part of a name: `sha256-file` is one name. Subtraction needs
  whitespace on both sides (`a - b`).
- `/` without surrounding whitespace, between a name and a name or quoted
  segment, forms a qualified name. Division needs whitespace on both sides
  (`a / b`). An unspaced `a/b` that is neither a qualified name nor a path atom
  is `PARSE_ERR`.
- Namespaces have one level: `a/b/c` is `PARSE_ERR`. A quoted segment allows
  names that are not valid identifiers: `x/"python3.12"`.
- Namespaces live in their own table, separate from value bindings, so a
  variable named `x` does not conflict with the `x/` namespace.

Reserved namespaces:

| Namespace | Meaning |
| --- | --- |
| `x/NAME` | The external executable `NAME`, resolved by the established tool resolver. Bypasses all lexical bindings and builtins. |
| `env/NAME` | The environment store of the current process-like unit (§5.3). Reading is tracked and capability-checked and yields `nil` when unset. `set env/NAME = …` writes it. |
| `std/NAME` | The standard library operation `NAME` (`007`). |
| `module/dir`, `module/file` | The authoring module's directory and source file, as absolute `Path` values. |

Import aliases (§11) add further namespaces. Reserved namespace names cannot be
used as aliases.

### 3.3 Keywords and literals

Reserved words: `def`, `let`, `set`, `rule`, `task`, `service`, `always`,
`cached`, `generate`, `if`, `then`, `elif`, `else`, `match`, `for`, `in`, `while`,
`break`, `continue`, `return`, `yield`, `exit`, `defer`, `with`, `stream`,
`import`, `from`, `as`, `export`, `include`, `include?`, `and`, `or`, `true`,
`false`, `nil`. `sep` is a keyword only inside a template `@for`.

Names are lower-kebab-case by convention (`object-path`, `changed-inputs`),
matching the library (`018`). Upper-case names are reserved by convention for
environment variables and build-wide settings, because unbound upper-case names
fall back to the environment (§6).

In command position a reserved word is a statement keyword. To run an
executable with a reserved name, write `x/NAME`.

Literals:

- Numbers follow `007`. A leading `-` immediately followed by a digit is part
  of the numeric literal, so every negative number has a literal form (`-1`,
  `-0.5`, `-1e3`). `neg VALUE` negates a computed value. A `-` attached to a
  name (`-x`) is `PARSE_ERR` in expression position; in a command word it is
  ordinary text (`mkdir -p`).
- Booleans `true` and `false`; the nil value `nil`.
- Symbols: `:name`, `:+name`, `:-name`. A symbol has no whitespace after `:`.
- `"…"` strings decode escapes and interpolate `${EXPR}`. `\${` is literal.
  A string may span lines.
- `'…'` strings are raw: no escapes and no interpolation.
- `"""…"""` verbatim block strings are raw and multi-line (`016`). A longer run of
  quotes delimits content that itself contains `"""`.
- Placeholders `_`, `__`, `___` (first, second and third parameter) and `_N`
  (the zero-based Nth parameter, `_0` through `_4095`). Outside a section a
  placeholder is `PARSE_ERR`.

### 3.4 Path atoms

A path atom begins with `./`, `../`, `/` or `~/`, or with a leading
interpolation directly followed by `/` (`${root}/suffix`). It continues until
whitespace or an unbalanced delimiter: `,`, `)`, `]` or `}`. Inside a path atom:

- `${EXPR}` interpolates a value.
- `{name}`, `{name:*}`, `{name:**}`, `{*}`, `{**}` and the other group forms of
  `014`/`029`/`041` are pattern groups.
- `*`, `**`, `?` and `[…]` are glob syntax.

Brace groups in a path atom are never record literals.

## 4. Expressions

### 4.1 Grammar

```text
EXPR        = IF-EXPR | LAMBDA | FALLBACK-PIPE
IF-EXPR     = "if" EXPR "then" EXPR ("elif" EXPR "then" EXPR)* "else" EXPR
LAMBDA      = PARAMS "=>" EXPR
PARAMS      = "()" | NAME+ (NAME "...")? | NAME "..."
FALLBACK-PIPE = OR ("??" OR)* ("|>" STAGE)*
OR          = AND ("or" AND)*
AND         = CMP ("and" CMP)*
CMP         = SUM (("==" | "!=" | "<" | ">" | "<=" | ">=") SUM)?
SUM         = PRODUCT (("+" | "-") PRODUCT)*
PRODUCT     = APP (("*" | "/" | "%") APP)*
APP         = POSTFIX (POSTFIX | "()" | "..." POSTFIX)*
POSTFIX     = ATOM ("." (NAME | INTEGER | QUOTED-STRING))*
ATOM        = NAME | QUALIFIED | ENV-NAME | LITERAL | PATH-ATOM
            | LIST | RECORD | "(" EXPR ")" | "$(" ACTION ")" | "$?(" ACTION ")"
            | "$&(" ACTION ")"
            | MATCH-EXPR | PLACEHOLDER
LIST        = "[" (ITEM ("," ITEM)* ","?)? "]"
RECORD      = "{" (FIELD ("," FIELD)* ","?)? "}"
ITEM        = EXPR | "..." EXPR
FIELD       = (NAME | QUOTED-STRING) ":" EXPR | NAME | "..." EXPR
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
parenthesized. Comparisons do not chain: `a < b < c` is `PARSE_ERR`. `=` is
never an operator. `not` is a library function: `not (ready? s)`.

An expression continues onto following lines inside an open delimiter, subject
to the list and record item boundaries below. It also continues when a line
ends with a binary operator, `=>` or `=`, or when the next line is more indented
and begins with `|>` or a binary operator. A backslash immediately before the
newline also continues an expression or a declaration header. Otherwise a
newline ends the expression.

### 4.2 Application

`f a b` applies `f` to `a` and `b`, evaluating arguments left to right.
Parentheses only group: `f (g a) b`. `...xs` spreads a list into arguments.

- A bare name is always a reference to its value: `map object-path` passes the
  function.
- `f ()` calls `f` with zero arguments. `()` is valid only as the sole
  argument of an application or as a lambda's parameter list.
- Applying a non-callable value fails with `EXPR_INVALID`.
- Arity, rest-parameter and duplicate-parameter rules follow `005`.

Lists are `[a, b, c]`; records are `{name: value, "key with space": v}`. A field
written as a bare `name` is shorthand for `name: name`. `...` spreads a list
into a list or a record into a record. Separate elements and fields with a
comma or a newline. A trailing comma is allowed. At collection depth a newline
ends a complete item. An operator, a required `if` branch, a nested delimiter,
or an explicit backslash continuation can keep the expression open. A record
value may begin on the line after its colon. Field access is `r.name`, `xs.0`
or `r."key"`.

### 4.3 Lambdas and sections

`(x => x + 1)`, `(a b => a * b)`, `(() => now ())`, `(first rest... => …)`.
A lambda closes over its lexical scope.

A parenthesized group is a **placeholder section** when its top level contains
a placeholder. The top level excludes nested parentheses, lambdas, lists,
records, `$(…)`, `$?(…)` and `$&(…)`. The section is equivalent to a lambda over the
placeholders it uses:

```kame
xs |> filter (ends? ".c" _)         # filter (s => ends? ".c" s) xs
pairs |> map (join __ _)            # map (a b => join b a) pairs
```

A repeated placeholder refers to the same parameter. Gaps in `_N` count toward
the 4096-parameter limit.

### 4.4 Value pipeline `|>`

`VALUE |> STAGE` evaluates `VALUE` once. `STAGE` is a name or an application.

- If the stage's top level contains `_`, each `_` is replaced by the value.
  Only `_` is valid in a stage.
- Otherwise the value is appended as the last argument: `xs |> map f` is
  `map f xs`.

Parenthesized groups inside a stage are sections of their own:
`xs |> filter (ends? ".c" _)` filters `xs`. The parser retains pipes and
placeholder positions for formatting and AST inspection. Lowering does not
change evaluation order or multiplicity, and special forms keep their own
evaluation control. `|` is never a value operator.

### 4.5 Conditionals, fallback and match

`if C then A else B` evaluates only the selected arm. Only `false` and `nil`
are false. `and` and `or` are lazy and return the deciding operand.

`A ?? B` yields `A` unless `A` is `nil` or fails with a missing-lookup failure.
Missing-lookup failures are `REF_MISSING`, `TOOL_MISSING`, `ENV_VAR_MISSING`
and a missing field. In those cases it evaluates `B`. Other failures
propagate.

`match` dispatches a subject against ordered arms:

```kame
def object-path source = match source
    ./src/{path:**}.c => ./build/${path}.o
    ./gen/{name}.c    => ./build/gen/${name}.o
    else              => fail! "not a source: ${source}"
```

Each arm ends at a newline, even inside an open delimiter. An arm result that
itself contains `=>` (a lambda) must be parenthesized.

An arm pattern is a path pattern, a quoted string pattern or a literal. Arm
patterns are always matchers: `{name}` captures like `{name:*}`. Named captures
bind as names in that arm's scope. Without an `else` arm, a nonmatch yields
`nil`.

### 4.6 Data-last library

Standard operations take their primary data argument **last**: `map f xs`,
`filter pred xs`, `capture pattern path`, `bind template captures`. This
composes with `|>`. `007` signatures are retained except for renames listed in
this document. The `match` keyword replaces the former `match` special form.
The typed capture function is `capture`; the regex group accessor is
`regex-group` (`029`). The record field replacement operation is
`put KEY VALUE RECORD`, since `set` is a keyword.

## 5. Statements, bindings and blocks

### 5.1 Top-level definitions

At the top level of Kame programs, and in the module scope of
`.kash` sources (§11):

```kame
version = "1.0"
sources = files ./src/**/*.c

def greeting name = "Hello ${name}!"

def compile source output
    mkdir -p (dirname output)
    cc -c $source -o $output
```

- `NAME = EXPR` declares a **lazy, memoized**, order-independent value. It is
  shorthand for `def NAME = EXPR`.
- `def NAME PARAM… = EXPR` declares an expression-bodied function. A zero-argument
  function is declared with `def NAME () = EXPR` and called with `NAME ()`.
- `def NAME PARAM…` (or `def NAME ()`) followed by an indented block declares a
  block-bodied function. The body is in action context.
- `NAME =` (or `def NAME =`) followed by an indented block computes a lazy value
  from a block of `let`, `set`, control statements and `return`. The block takes
  the definition's context, so commands in it are context errors except when it
  is evaluated in an action phase. After a trailing `=` with no parameters, the
  indented lines form a block only when the first one begins with `let`, `set`,
  `return`, `for`, `while`, `defer`, a statement `if` (no `then` on that line)
  or a statement `match`. Otherwise they continue the expression (§4.1).
- `NAME ?= EXPR` declares a configurable default: configuration and
  command-line overrides replace it (`004`, `009`).
- `NAME += EXPR` contributes list items to a configurable list definition
  (`004`).

Kame program files contain no top-level expression or command statements. Inline
`-c` fragments may also hold expression statements, which run in sequence
against the loaded program (`009`). `NAME = EXPR` is not valid inside blocks. The `.kash` top level is a
block (§5.2), so a `.kash` module declares its lazy values and functions with
`def`.

### 5.2 Blocks

A block is the indented lines after its header. Its indentation is the leading
whitespace of its first nonblank line. Each later nonblank line must begin with
exactly that prefix or end the block. Tabs and spaces may not be mixed within
one block prefix. Each block has its own lexical scope, and so does each loop
iteration.

Block statements:

| Statement | Meaning |
| --- | --- |
| `let NAME = EXPR` | Evaluate `EXPR` **eagerly** when reached and bind it immutably for the rest of the block. A later `let` of the same name shadows it. |
| `set NAME = EXPR` | Assign a `let` binding of an enclosing block in the same invocation. `set` cannot create a lexical binding. Targeting a binding captured by a lambda or nested `def` is `PARSE_ERR`. |
| `set env/NAME = EXPR` | Write the environment store (§5.3). `nil` unsets. |
| `def …` | Nested function, closing over the block scope. |
| `if` / `elif` / `else`, `for NAME in EXPR`, `while EXPR` | Control blocks. Headers have no trailing `:`. |
| `match EXPR` | Statement form with `PATTERN =>` arms followed by blocks. |
| `return EXPR?` | End the enclosing `def` invocation, value-definition block or rule body with a value (default `nil`). `PARSE_ERR` elsewhere, including the `.kash` top level, which uses `exit`. |
| `yield EXPR` | Emit one stream element (§8.3). |
| `break`, `continue` | Loop control. |
| `exit EXPR?` | End the enclosing process-like unit (§8.4). |
| `defer STATEMENT` | Cleanup, as in `017`. |
| `with cwd: VALUE env: VALUE` | A block whose processes run with the given directory and environment overlay, without writing the unit state (`048`). |
| `for NAME from SOURCE FRAME` | Incremental record iteration over stdin or a file (`048`). |
| `stream NAME` | Declares a lexical channel endpoint for process graphs (`042`). |
| `(EXPR) …` | Expression statement: a statement that begins with `(` evaluates the expression for its effects and discards the value. |
| Command statement | Anything else (§7). |

A statement is a binding only when it starts with `let` or `set`.
`echo a = b` is a command.

`if`, `elif` and `while` conditions are expressions. To test a command, use its
outcome:

```kash
if $?(test -f ./ready).ok
    echo ready
for source in files ./src/*.c
    compile $source (object-path source)
while not (ready? server)
    sleep 1
```

Falling off the end of a `def` returns `nil`. There is no implicit
last-statement value.

### 5.3 Process-like unit state

Each process-like unit (§8.4) owns two pieces of mutable state, inherited from
its parent unit when it starts:

- **Environment store.** The child-process environment, read through `env/NAME`
  and bare `ENV-NAME` references (§6) and written with `set env/NAME = EXPR`.
  The value must be a scalar (string, number or path). `nil` removes the
  variable. A write is visible to all later statements, later lookups and
  processes started afterwards in the same unit, including after the function
  that performed it returns. Executable lookup (§6) uses the store's `PATH`.
- **Working directory.** Changed with `cd`, with the same visibility.

Writes never reach the parent unit, so a rule body or `$(…)` capture cannot
change its caller's environment or directory. Per-command overrides (`env:`,
`cwd:`) apply to one command only and leave the store unchanged. In an
interactive session (`052`) the session is the unit, so `set env/NAME` and `cd`
persist across entries. The top-level fragments of one `kame do run`
invocation also form a single unit. `set env/…` is an action-context statement.
Writing the store is not a host environment mutation. Setting a variable needs
no grant. Unsetting one, or overriding a protected name, follows the
environment authority of `051`.

## 6. Name resolution

**Expression position** (value, declaration and template contexts, and value
operands in action context):

1. Lexical bindings: block bindings, parameters, module definitions and
   imported names.
2. The standard library (`std/NAME` is always available explicitly).
3. For an `ENV-NAME` only: the environment, as a tracked, capability-checked
   read. Lowercase names never fall back to the environment.
4. Otherwise `REF_MISSING`.

An unset environment variable reached through step 3 fails with
`ENV_VAR_MISSING`, so typos are caught; use `NAME ?? default`. `env/NAME`
yields `nil` when unset.

**Command position** (the head of a command statement):

1. Lexical callable bindings, including imported names. A lexical binding to a
   non-callable is `EXPR_INVALID`; it never falls through to an executable.
2. The closed set of Kash command builtins below. Adding a builtin is a
   breaking change.
3. Executables, through the tool resolver, using the environment store's
   `PATH` (§5.3).

| Builtin | Available | Meaning |
| --- | --- | --- |
| `cd [DIR]` | everywhere | Change the unit's working directory (§5.3). No operand means `$HOME`, `-` means the previous directory, and `~`/`~/…` expand to `$HOME`, as in `052`. |
| `wait [JOB…]` | everywhere | Wait for owned async work and return its outcome. |
| `kill [-SIGNAL] JOB…` | everywhere | Signal owned async work. |
| `out! ARG…`, `err! ARG…` | everywhere | Write the arguments' exact text or bytes to the stage's stdout or stderr, so they work with pipes and redirection (`out! $content > $outputs.0`). These are the library's `out!` and `err!` (`007`). |
| `jobs` | interactive | List session jobs. |
| `fg [JOB]`, `bg [JOB]` | interactive | Resume a job in the foreground or background. |
| `alias`, `unalias` | interactive | Define, list or remove whole-line command aliases. |
| `history` | interactive | Select the history scope or clear history (`053`). |
| `option` | interactive | Inspect or change shell options (`054`). |
| `explain` | interactive | Show the full report of the latest failure. |

An interactive-only builtin used in a script or rule body is `PARSE_ERR`.
`052`'s `export` and `unset` session commands are replaced by
`set env/NAME = VALUE` and `set env/NAME = nil`.

The standard library is **not** consulted in command position. `basename $f .c`
runs the executable; `(basename f)` calls the library function. `std/NAME`,
`x/NAME`, `$REFERENCE` and path heads (`./tool`) are explicit heads. In value
and declaration contexts, step 3 is never attempted, so an unknown command
fails before any effect.

## 7. Command context

### 7.1 Commands and words

```kash
git status --short
cc $...flags -c $source -o $output
emit prefix-(name)-suffix
scp ./dist/app.tgz deploy@host:
```

A command statement is a head followed by words. Words keep the typed-argv
semantics of `017`:

| Word form | Meaning |
| --- | --- |
| bare text | A literal string |
| `'…'` | A literal string |
| `"…"` | A string with `$` interpolation |
| `$name`, `$name.field`, `$xs.0` | Reference substitution |
| `${EXPR}` | Evaluate a value fragment, including `${ns/name}` or `${(dirs REPOS)}` |
| `$...name`, `$...{REFERENCE}` | List spread into separate argv entries |
| `(EXPR)` | Value fragment |
| `$(…)` | Captured stdout |

Adjacent fragments compose one word. In quoted or mixed words `${EXPR}`
uses Kame string conversion, including space-separated lists and the
standard display format for records. Scalars produce one argv entry. A
standalone unquoted substitution can supply one argument per scalar list
item. Argument text stays intact through execution.

An unbraced `$` reference stops at `/`: `$dir/file.txt` is `$dir` followed by
`/file.txt`. Qualified references need braces: `${env/HOME}/x`. Because of
the environment fallback, `$HOME/x` works unbraced.

`@` is literal text in command words (`--mtime=@0`). Unquoted `@{` is
`PARSE_ERR`. Use `${EXPR}` to substitute a value or quote `@{` for literal text.

A stage setting `KEY: VALUE` takes one scalar word (`017`), except `env:`, which
takes a typed record. Each reserved setting reads its word according to its own
contract. `timeout: 10` is a number of seconds. `tty: true` and
`foreground: false` are booleans, read from the literal words `true` and
`false`. `cwd: ./build/(variant)` is a directory. A typed value can always be
supplied as a fragment or reference: `timeout: $limit`. Any other key sets an
environment variable to the word's text.

Pipelines (`|`), sequencing (`;`, `&&`, `||`), redirection, `>&out`/`>&err`,
`CMD ?` acceptance, recovery and async `&` keep their `017`, `042`, `047` and
`049` semantics.

### 7.2 Calling functions as commands

A command whose head resolves to a function calls it with its words as
arguments: bare words become strings, and `$x` and `(expr)` pass typed values.
At statement level the return value is discarded. Use expression position,
`let v = compile source out`, to keep it. The function's stdout and stderr take
part in pipelines and redirection like a process's.

### 7.3 Commands inside expressions

| Form | Result |
| --- | --- |
| `f a b` | The function's return value. Raises on failure. |
| `$(CMD)` | The process graph's stdout, as text under `017` capture rules. Raises on failure. |
| `$?(CMD)` | The invocation outcome (§8.1), with stdout captured into `value`. Doesn't raise for process exits, launch failures, timeouts or function failures. |
| `$&(CMD)` | Starts `CMD` asynchronously and returns its handle once the graph has started. The statement form is `CMD &`. |

```kame
revisions = sources |> map (f => $(git log -1 --format=%h -- $f))
```

```kash
let job = $&(watch-assets --port 8080)
run-tests
let done = $?(wait $job)
```

The content of these three groups is action context: one process graph or
command list, without blocks. Async `&` is not allowed inside them, because
`$&(…)` is the expression form of `&`. The handle's work is owned by the
enclosing invocation (`017`), exactly as for `CMD &`. A typed return value is never
converted to stdout. `$(f x)` captures what `f` writes, not what it returns.

`$?(…)` still raises for failures that are not about the invocation itself:
capability denial, phase errors, parse errors and cancellation of the
enclosing invocation. A fallback for a failed command is written in the
command list, `$(git rev-parse --short HEAD || printf unknown)`, or by
inspecting the outcome; `??` does not catch process failures.


## 8. Invocation outcomes and streams

### 8.1 Outcomes

Every invocation, whether of a function or a process, has an outcome record:

| Field | Meaning |
| --- | --- |
| `state` | `:completed`, `:failed` or `:cancelled` |
| `ok` | `true` exactly when `state` is `:completed` and, for processes, the status is 0 and there is no signal |
| `status` | Exit status, derived for functions (§8.2) |
| `signal` | Terminating signal or 0 |
| `value` | A function call's return value. For a `$?(…)` process graph, its captured stdout. Otherwise `nil`. |
| `error` | The failure, with its source location, or `nil` |
| `stages` | Per-stage process outcomes, as in `049` (empty for a function's own outcome) |

The record also carries the other fields of `049`'s Result:
`stdoutCaptured`, `stderrCaptured`, `effectiveStatus`, `effectiveSignal` and
`graph`. An accepted nonzero exit (`CMD ?`) has `state: :completed`,
`ok: false` and `effectiveStatus` 0. A condition that should treat accepted
exits as success tests `.state == :completed`.

The outcome is read with `$?(…)`, or from an async handle with `wait`. Async
statements, handles, ownership and cleanup follow `017`/`049` and apply equally
to function invocations.

### 8.2 Function status

Functions do not return integer statuses. When a function's status is needed,
for example as a pipeline stage, a recipe or a script entry point, it is
derived:

| State | Status |
| --- | --- |
| `:completed` | 0 |
| `:failed` | The failing process's status if a process failure caused it, otherwise 1 |
| `:cancelled` | The consumer stopped pulling, so the stage ended early. Not a failure of the graph. A process stage killed by `SIGPIPE` follows its own `042` rules instead. |

### 8.3 Generators

A `def` whose body contains `yield` returns a lazy `Stream` when called. The
body runs as the consumer pulls elements, and its internal state while waiting
is `:suspended`. Inside a generator a bare `return` ends the stream, and
`return EXPR` is `PARSE_ERR`. When a consumer stops early, the generator becomes
`:cancelled` and its `defer` cleanup runs. Streams are consumed by `for`, by
`|>` stages that accept streams, and by the stream operations of `012`/`042`.

A function's stdout bytes (consumed by `|`) and its yielded values (consumed
as a value) are separate channels and never convert into each other.

### 8.4 `exit`

`exit N` ends the innermost process-like unit with status `N` (0–255, default
0). Process-like units are the script, a rule body, a `$(…)`/`$?(…)` capture and,
interactively, the current entry; `exit` in an entry ends the session (`052`).
It runs the pending cleanup of every invocation it unwinds. `exit` does not
target a `def`; use `return`.

## 9. Paths and patterns

### 9.1 Kinds

| Kind | Written as | Meaning |
| --- | --- | --- |
| `Path` | `./src/main.c`, `${dir}/x.o` | One location |
| `PathPattern` | `./src/**/*.c`, `./src/{path:**}.c` | Matches paths; may capture |
| `PathTemplate` | `./build/{path}.o` | Builds a path from captures |

A path atom with glob syntax or matcher groups is a `PathPattern`. One with
only `{name}` placeholders is a `PathTemplate`. Rule headers assign roles
instead (§10.1). Kinds never convert implicitly. A path renders as text only
in string interpolation, template output and argv.

### 9.2 Resolution

- `include` and `import` paths resolve relative to the **directory of the
  current source file**.
- All other relative paths, including rule inputs and outputs, resolve against
  the **root directory**. For Kame programs this is the directory of the entry file. For Kash programs
  it is the invocation's working directory. `-C` overrides both.
- Relative path values keep their authored relative form. Host operations and
  rule identity resolve them against the root. Argv renders them as authored,
  and the process cwd stays the root unless `cd` or `cwd:` changes it.
- A module reaches its own files through `module/dir`:
  `${module/dir}/templates/page.kmt`.

### 9.3 Capture and bind

```kame
captures = ./src/lib/math/vector.c |> capture ./src/{path:**}/{name}.c
# {path: "lib/math", name: "vector"}
output = captures |> bind ./build/{path}/{name}.o
# ./build/lib/math/vector.o
```

`capture PATTERN PATH` returns a capture record, or `nil` when there is no
match. Repeated named captures must agree. `bind TEMPLATE CAPTURES` returns a
`Path`. It fails on absent or incompatible captures. `${…}` in a pattern or
template is evaluated when the atom is evaluated. `{name}` stays a deferred
capture. Anonymous, positional, predicate and regex groups follow
`014`/`029`/`041`.

There is no string-pattern literal: a quoted string is text. Pattern text
outside a path atom becomes a pattern only through `match` arms or the explicit
`pattern TEXT` operation. That operation parses pattern syntax, not Kame
source.

### 9.4 Discovery

`files PATTERN` returns a sorted `List[Path]` and records a tracked membership
dependency. In declaration context the list is a snapshot taken at planning
time. A change in membership invalidates the declarations that consumed it.
A declaration-time discovery pattern that overlaps any declared output is
rejected with `DISCOVERY_OVERLAP`, naming both the pattern and the overlapping
output declaration. As a rule input, a
wildcard path keeps the dynamic membership semantics of `023`.

## 10. Rules and declarations

### 10.1 Rule declarations

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

service dev
    npm run dev
```

```text
DECL      = FILE-RULE | TASK | SERVICE
FILE-RULE = "rule" ("always")? OUTPUTS (":" INPUTS?)? META? BODY?
OUTPUTS   = OUTPUT (WS+ OUTPUT)*
OUTPUT    = PATH-ATOM | QUOTED-STRING | "${" EXPR "}" | "(" EXPR ")"
TASK      = "task" ("cached")? TARGET-NAME TARGET-ARGS? (":" INPUTS?)? META? BODY?
SERVICE   = "service" TARGET-NAME TARGET-ARGS? (":" INPUTS?)? META? BODY?
TARGET-ARGS = ("{" NAME ("=" DEFAULT)? "}")+     # see 022
INPUTS    = GROUP ("," GROUP)* ("|" GROUP)?
META      = ";" RECORD
BODY      = "=" EXPR | INDENTED-BLOCK
```

- **Roles.** Output atoms are matchers: `{name}` captures as `{name:*}` does.
  Input atoms are templates over those captures. File-rule outputs must be
  path atoms or computed targets written as `${EXPR}` or `(EXPR)`. Both computed
  forms resolve to paths during declaration registration. Task and service
  targets are names or name templates.
- **Inputs.** Whitespace-separated items form a parallel group, a comma starts
  the next sequenced group, and `|` begins order-only prerequisites (`004`/`006`
  semantics). Input items are path atoms, names, quoted strings and `${…}`
  interpolations. Computed lists flatten.
- **Metadata.** The record supports `shell`, `env`, `authority`, `tty`,
  `foreground`, `executor` and the service keys of `024`.
- **Body bindings.** The body binds:
  - `inputs` and `outputs`.
  - `changed-inputs`: unique normal file inputs whose content changed since
    the last successful run (`006`).
  - `captures`: every output capture's text in source order (`029`).
  - The named captures, and target arguments (`022`).

  It closes over its lexical scope. Inputs may refer to positional captures as
  `{_0}`, `{_1}` (`029`).
- **Kash body.** The default body is an action block. If it ends with
  `return VALUE` and `VALUE` is not `nil`, the value is published to the single
  output under `044`.
- **Expression body.** `= EXPR` is an expression body whose value is published
  under `044`. It replaces `shell: kame`.
- **Kinds.** `task` is a phony task; `task cached` is a cached task.
  `rule always` marks a file rule as always stale.

### 10.2 Foreign-shell recipes

With `shell` set to an external shell (for example `{shell: "bash"}`, or a
build-wide `SHELL = "bash"`), the body is a document template rendered to one
shell script. `@{EXPR}` interpolates, `@` directives apply (§12) and `@@`
escapes. `$` belongs to the shell and passes through. Rule context is
interpolated like any value: `@{inputs.0}`, `@{outputs.0}`.

### 10.3 Declaration-time control flow

Top-level `if`, `elif`, `else` and `for` in a Kame program run in declaration
context:

```kame
variants = ["debug", "release"]

for variant in variants
    let flags = if variant == "debug" then ["-g"] else ["-O2"]

    rule ./build/${variant}/{name}.o : ./src/{name}.c
        cc $...flags -c $inputs.0 -o $outputs.0

if env/CI != nil
    include ./ci.km
```

- `${variant}` is fixed when the declaration is evaluated; `{name}` stays a
  capture. Each declared rule closes over its own iteration scope.
- Inactive branches register nothing, and their includes are not read,
  tracked or cycle-checked.
- Registration order follows source order and iteration order.
- Declaration blocks can contain `let`, nested control, declarations,
  `include` and `generate`. No commands.

### 10.4 Rules as values

In expression position, `rule`, `task` and `service` construct a `Rule` value
without registering it. The body follows the same grammar and closes over the
lexical scope:

```kame
def object-rule source =
    rule (object-path source) : source
        cc -c $inputs.0 -o $outputs.0

generate (sources |> map object-rule)
```

`generate EXPR` registers a `Rule` or a list of `Rule` values in the current
declaration transaction. It is valid in declaration context only.

### 10.5 Generation contract

All declarations, authored or generated, go through the same validation,
collision detection (`TGT_AMBIG`), target identity, freshness, ownership and
scheduling.

- **Registration is transactional.** All generated batches are validated
  before any declaration becomes visible. An invalid batch registers nothing,
  and re-evaluation replaces a generator's previous declaration set.
- **Allowed work.** Generation, and all declaration-time evaluation, may use:
  - pure computation;
  - tracked read-only discovery (§9.4);
  - tracked environment reads.

  It may not read file contents, run processes, write, perform untracked
  reads, query a partially registered graph, or inject declarations once a
  selected rule is executing.
- **Limits.** The limits of `025` apply per `generate` statement.
- **Provenance.** Every declaration retains its origin: the source file,
  enclosing loop iterations and bindings, the constructing call chain and the
  `generate` site.

## 11. Modules

```kame
import ./lib/c                      # namespace c/…
import ./lib/docker as docker       # namespace docker/…
from ./lib/c import compile, object-path
from ./lib/c import compile as cc-compile
export compile, object-path
include ./rules/common.km
include? ./local.km
```

- **`import`.** `import PATH` loads a module once by normalized source identity
  and binds its exports as a namespace. A path that does not end in `.km`,
  `.kame`, `.kash` or `.ksh` tries those suffixes (`050`). More than one
  existing candidate is `RES_INVALID`, and none is `FS_ERR`. The default name is the file stem; `as`
  overrides it, and is required when the stem is not a valid name. `from PATH import NAME, …` binds selected exports
  directly, with optional aliases.
- **`export`.** `export NAME, …` (top level only) declares the public
  interface. Without an `export` statement, a module exports nothing.
- **No registration on import.** Importing a module evaluates nothing eagerly,
  registers none of its declarations and runs no commands. Modules may export
  functions that return `Rule` values, which the importer registers with
  `generate`. Module paths follow §9.2, so a rule built by an imported function
  writes its outputs relative to the importer's root.
- **Cycles.** Import cycles are rejected.
- **`include`.** `include PATH` splices the file's declarations into the
  caller's scope and declaration transaction at that point. `include?` skips a
  missing file only. Include and import operands are literal path atoms
  (`./`, `../`, `/` or `~/`) without interpolation; quoted strings are not
  accepted. Conditional includes use declaration-time `if` in Kame programs.
  Kash sources have no conditional include, and they may include only Kash
  sources; including any other kind is `RES_INVALID`. Active include and import cycles are `DEP_CYCLE`, naming the chain.
  Repeated includes register again under ordinary collision rules.
- **Kash modules.** A `.kash` source always parses as an action block. When it
  is loaded as a module, through `import` or as the target of an `include` from
  a module, the loader checks that its top level contains only comments, `def`,
  `import`, `from`, `export` and unconditional `include`/`include?`. Anything
  else is `DEF_INVALID` at the offending statement's span.
- **Re-exports.** `export` names only the module's own definitions; it cannot
  re-export imported names. A facade module defines an alias such as
  `def f = lib/f`.
- **Imported names and commands.** Imported names resolve before executables in
  command position (§6) without changing effect authority.

## 12. Templates

### 12.1 Interpolation

`@{EXPR}` interpolates in `.kmt` documents and foreign-shell recipes.
Kame strings, path atoms, rule headers, and Kash command words use `${EXPR}`.
Both forms accept the expression grammar of §4. In Kame strings and quoted
or mixed Kash words, substitution renders atoms as text, joins list items
with spaces, and uses the standard display format for other values.
Document templates retain the rendering rules in `016`, including `@@`
for a literal `@`. Rendering modes may opt in to escaping explicitly.

### 12.2 Directives

```html
<h1>@{title}</h1>
@if users
<ul>
@for user in users
  <li>@{upper user.name}</li>
@end
</ul>
@else
<p>No users found.</p>
@end
```

A directive is `@KEYWORD` followed by whitespace-separated operands in the
expression grammar of §4, with no parentheses around the operands:

| Directive | Meaning |
| --- | --- |
| `@if EXPR`, `@elif EXPR`, `@else` | Conditional block |
| `@for NAME… in EXPR` | Repetition block; `@for NAME… in EXPR sep EXPR` puts the separator between iterations |
| `@with EXPR` | Binds the fields of a record as names for the block |
| `@match EXPR`, `@case PATTERN`, `@else` | Pattern dispatch; captures bind in the case body |
| `@let NAME = EXPR` | Binds a name for the rest of the enclosing block (no `@end`) |
| `@include PATH [EXPR]` | Renders a template with the current scope or the given payload. `PATH` is exactly one operand (a path atom, string, name with field access or parenthesized expression); everything after it is the payload. A relative path resolves against the including template's directory. |
| `@raw` | Verbatim block |
| `@end [if\|for\|with\|match\|raw]` | Closes the innermost `@if`, `@for`, `@with`, `@match` or `@raw`. The optional label must match the block it closes (`030`). |

`sep` is a keyword only within `@for`. Recognition is keyword-gated: the keyword
ends at whitespace, at `-`, or at the end of the directive. Other `@` text, such
as `@media` or `@example.com`, stays literal.

- **`plain` style.** Directives are recognized on whole lines only, and the
  only inline form is `@{EXPR}`.
- **Comment styles.** A directive is wrapped in a configured host-comment style
  (`016`, for example `<!-- @for post in posts -->`). A complete directive
  comment may also appear inline, because the comment delimiters mark where the
  directive ends (`030`).

A directive line contributes no text, indentation or line ending. Whitespace
trim markers are a `-` directly after `@` and a standalone `-` before the end of
the directive (`@-for …`, `@else -`; see `030`). `@raw` blocks bypass
interpolation and directives.

`render TEMPLATE PAYLOAD?` renders a `.kmt` path or template value with the
lexical scope plus optional payload fields, and returns text. Rendering never
parses or runs its output. Expressions evaluated during planning cannot run
processes.

## 13. `fmt` and `eval`

- `fmt VALUE` returns canonical, parseable expression source for a value:
  strings quoted and escaped, and lists, records, numbers, booleans, symbols,
  paths and patterns keeping their kinds. Runtime-only values (callables,
  processes, handles, streams, rules) are `EXPR_INVALID`.
- `eval TEXT` parses one expression and evaluates it in value context in the
  current lexical scope and phase. It does not parse statements, declarations
  or commands.

No other operation parses generated text as Kame or Kash source.

## 14. Phases and safety

Phases are declaration loading, tracked generation, target selection,
dependency resolution, action execution and output publication. Each phase has
its own authority, and runtime grants cannot widen it.

- Pure computations may be memoized by their arguments.
- Tracked reads record dependencies.
- Process execution and writes are possible only in action execution.
- Template rendering, interpolation and `$(…)` during planning cannot launch
  processes.

Diagnostics keep authored source spans across every boundary between contexts
and grammars, including a value fragment inside a command word inside a rule
inside a declaration loop, along with call and iteration context. No implicit
conversion between text and code, path and string, stream and value, or return
value and stdout may change meaning silently.

## 15. Tooling

`kame do parse` and `kame do fmt` handle every context of this grammar without
evaluating anything. They preserve comments, path and literal kinds, pipes
versus nested calls, sections, and spans. Formatting is idempotent and
canonical:

- one tab per block level
- `, ` between list and record elements
- spaces around binary operators
- no trailing `:` on block headers

Old-syntax sources fail with `PARSE_ERR`.

## 16. Conformance

A conforming implementation shall demonstrate:

1. **Context-selected grammar.** The same line parses as a command in action
   context and as an expression in value context. Changing a binding, an import
   or `PATH` never changes the parse tree or the formatted output.
2. **Application and precedence.** Whitespace application, `f ()`,
   parenthesized grouping and the precedence table hold. Comparisons don't
   chain. `a/b` versus `a / b` and `a-b` versus `a - b` behave as specified, and
   `-1` is a literal.
3. **Literals.** Comma-separated lists and records, shorthand fields, spreads
   and trailing commas parse. Brace groups inside path atoms never parse as
   records.
4. **Sections and pipes.** Placeholder sections work at their stated
   boundaries, and `|>` inserts last or at `_`. Value `|>` and process `|`
   produce distinct trees and never coerce into each other.
5. **Name resolution.**
   - Expression position: lexical, then `std`, then uppercase environment.
   - Command position: lexical, then builtins, then executables. The standard
     library is never consulted.
   - A lexical non-callable at a command head is an error.
   - Value and declaration contexts never launch an unknown command.
   - `x/`, `env/`, `std/`, `module/` and alias namespaces resolve as specified.
6. **Bindings.** Top-level `=` is lazy. `let` is eager. `set` can assign only
   existing, uncaptured bindings in the same invocation.
   - `set env/NAME` and `cd` are visible to later statements, lookups and
     processes in the same unit, including after the setting function returns.
   - Neither reaches the parent unit.
   - `set env/NAME = nil` unsets.
   - Executable lookup follows the updated `PATH`.
   - An unset bare `ENV-NAME` fails with `ENV_VAR_MISSING`.
   - Interactive-only builtins are rejected outside a session.
7. **Words.**
   - `$name`, `${ns/name}` and `$...list` behave as specified.
   - `(expr)` fragments compose with adjacent text.
   - `$dir/file` stops the reference at `/`.
   - Scalars are never split or globbed.
8. **Outcomes and status.**
   - `$(…)` raises on failure; `$?(…)` returns an outcome and doesn't raise; `$&(…)` returns an owned handle.
   - Functions and processes produce the same outcome fields.
   - Derived function statuses apply in pipelines.
   - A cancelled generator runs its `defer` cleanup and does not fail its
     pipeline.
   - `exit` ends the innermost process-like unit.
9. **Paths.** `Path`, `PathPattern` and `PathTemplate` stay distinct.
   `capture` and `bind` keep path kinds. Include and import paths resolve
   against the current file; other paths against the root or `-C`;
   `module/dir` against the module.
10. **Discovery.** A membership change invalidates the declarations that
    consumed it. Discovery that overlaps a declared output is rejected.
11. **Declaration-time control flow.** Top-level `for`/`if` register
    deterministic rule sets with per-iteration closures. Inactive branches
    neither evaluate values nor read includes.
12. **Generation.** `generate` is transactional, reports collisions with
    provenance, and replaces a generator's set on re-evaluation.
13. **Modules.** Imports expose only exported names, register no declarations
    and reject cycles. `include` registers declarations in the caller's
    context.
14. **Rule bodies.** Expression bodies and Kash `return VALUE` bodies publish
    under `044`. Foreign-shell recipes interpolate `@{…}` and pass `$` through.
15. **Templates, `fmt` and `eval`.** `@` directives and `@` escapes behave as
    specified. `fmt` output round-trips through `eval` with value kinds
    preserved. `eval` rejects statements and unauthorized effects.
16. **Backend agreement.** Native, WASM, formatter, parser, watch/reload and
    diagnostics agree on phase boundaries, authored locations and failure
    handling.
