# Kash

## Purpose

Kash is Kame's command and process language. It makes command-oriented programs
read like shell scripts while retaining Kame's typed values, lazy evaluation, and
explicit dependency model.

Kash does not introduce a second expression language. It adds statements,
commands, pipelines, redirections, and process-control forms around the existing
Kame expression language. A Kash implementation composes the Kame expression
parser and evaluator; it does not translate values into shell text and reparse
them.

This specification adds:

- Kash source files containing definitions and command statements.
- Shell-familiar value lookup and command substitution.
- An explicit syntax boundary for Kame expression evaluation.
- Block control statements for conditional and pattern dispatch.
- First-class process pipelines, redirections, asynchronous execution, and
  recovery.

## Relationship to Kame sources

Kame has three related source layers:

| Layer | Responsibility |
| --- | --- |
| Kame expressions | Values, references, functions, patterns, templates, and value transformations. |
| `.kmk` scripts and rules | Declarative definitions, targets, inputs, and recipes. |
| Kash | Command statements and process orchestration, using Kame expressions as its computation layer. |

Kash is a new outer syntax, so it is not a literal grammar superset of a Kame
script. Raw command lines are not Kame expressions. It is, however, an
expression extension in the user-facing sense: every Kash value computation
delegates to the existing Kame expression grammar and evaluator rather than a
second value language.

Existing `.kmk` recipe behavior is unchanged. Rule bodies currently render to
one shell script and are not parsed as Kash. A future rule may explicitly opt
into Kash for its body, but no existing recipe is reinterpreted by this
specification.

## Source and statements

A Kash source is a sequence of comments, blank lines, definitions, statements,
and block control statements. `;` separates statements on a line. Definitions
have the same shape and lazy semantics as Kame definitions:

```kash
name = "John Smith"
(banner WHO) = "Hello, " + $WHO
```

A non-definition statement is a Kash command expression, including a pipeline,
redirection, asynchronous command, or recovery form:

```kash
git status --short
build | tee ./build.log
watch-assets &
```

The Kame expression grammar named `EXPRESSION` in `004-language.md` remains the
sole grammar for values, applications, lists, records, lambdas, references,
patterns, and expression pipes. Kash delegates normal definition right-hand
sides and `@(EXPRESSION)` to that grammar unchanged. Kash's `$...` forms wrap a
Kame reference, and its `$(...)` form wraps a process expression; neither adds
an alternate value grammar. This makes parser ownership visible in source and
diagnostics.

## Expansion boundary

Kash has four distinct substitutions:

| Form | Parser | Meaning |
| --- | --- | --- |
| `$REFERENCE` | Kame reference parser | Resolve and insert a value from the current Kame scope. |
| `${REFERENCE}` | Kame reference parser | Braced spelling of `$REFERENCE`. |
| `@(EXPRESSION)` | Kame expression parser | Evaluate an arbitrary Kame expression and insert its value. |
| `$(COMMAND)` | Kash statement parser | Run a Kash command or pipeline and capture its standard output. |

### References

`$REFERENCE` is the convenient form for inserting an already-defined Kame value.
`REFERENCE` uses Kame's complete reference grammar, not a separate shell-variable
grammar. It therefore includes forms such as `$project.name`, `$files.0`, and
`$config.{host,port}` as well as `$name`.

`${REFERENCE}` has exactly the same evaluation semantics. It only establishes the
end of a reference when literal text follows; it is not a general expression form.

A `$` lookup never falls back to the operating-system environment. `$PATH` looks
up the Kame binding `PATH`; it is an unknown reference when no such binding
exists. Environment access remains explicit through `env.`, for example
`$env.PATH` or `@(env.PATH)`.

### Kame expressions

`@(EXPRESSION)` is Kash's explicit entry into Kame's expression and
meta-programming layer. The enclosed text is parsed and evaluated exactly as
Kame expression syntax. It is the form for calls, functions, lists, records,
patterns, templates, and transformations that are more than direct lookup:

```kash
emit @(replace source ".c" ".o")
report @(map sources inspect)
```

`@(...)` does not create a Kash value model or evaluator. It evaluates to an
ordinary Kame value and carries normal Kame dependencies, capabilities, failures,
and source spans.

`@NAME` and `@tmpl(...)` are reserved Kash meta-programming forms for generated
syntax and templates. They are not value substitutions and do not change the
meaning of `$REFERENCE`, `@(EXPRESSION)`, or `$(COMMAND)`. Their quoting and
hygiene rules require a separate macro/template specification.

### Command substitution

`$(COMMAND)` is command substitution. `COMMAND` is parsed with Kash command and
pipeline syntax, runs as a process graph, and its standard output becomes the
substitution value:

```kash
revision = $(git rev-parse --short HEAD)
echo revision=$revision
```

It never means Kame expression evaluation. A failed command substitution fails
its enclosing expression unless a Kash recovery form handles it.

## Command values and arguments

A raw Kash line is command syntax:

```kash
git status --short
```

It lowers conceptually to a Kame process operation:

```kame
(run "git" "status" "--short")
```

Kash command arguments are typed value positions, not shell word-expansion
positions. A string expansion supplies one argument even when it contains
whitespace. Expansions are not re-split, implicitly globbed, or reparsed as
shell syntax. A raw `*.go` is therefore literal; a filesystem wildcard is an
explicit Kame value, such as `@(wildcard ./src/**/*.go)`.

A command may use `run` setup keys for process configuration:

```kame
(run :cwd build-dir :timeout 10 :NODE_ENV "production" "npm" "test")
```

The keys configure the process; remaining expressions form its executable and
arguments.

## Pipelines and redirection

A raw command pipeline is a first-class Kash process expression:

```kash
generate | transform | publish
compile < sources.txt > output.log
compile >> output.log
```

It lowers conceptually to a Kame streaming graph:

```kame
(pipe (run "generate") (run "transform") (run "publish"))
```

Kash connects command stages through that graph rather than eagerly buffering
intermediate output. `<` supplies standard input, `>` replaces an output file,
and `>>` appends to it.

Kame's value pipe remains distinct and is used inside an expression boundary:

```kash
emit @(sources | map render)
```

The pipe between raw command stages is a process stream; the pipe inside
`@(...)` has the value-pipe semantics specified by `004-language.md`.

## Process lifecycle and recovery

Appending `&` starts a command or pipeline asynchronously and immediately yields
its `Process`/`Result` value:

```kash
watch-assets &
```

Commands normally fail on a non-zero exit status. `CMD?` accepts that non-zero
exit while retaining the command's diagnostic output; it does not silence stderr:

```kash
grep needle missing-file?
```

`CMD ? ELSE` evaluates `ELSE` when the command fails:

```kash
deploy ? echo "deployment failed; continuing"
```

`LEFT ?? RIGHT` is value-level recovery. It evaluates `RIGHT` when `LEFT` is
`nil` or fails; otherwise it returns `LEFT`:

```kash
config = read-config ?? default-config
revision = $(git rev-parse --short HEAD) ?? "unknown"
```

These forms are deliberately distinct. `CMD?` accepts a process exit;
`??` selects a fallback value or computation. Neither changes Kame's existing
`?` special form, which handles unknown-reference recovery within a Kame
expression.

## Control statements

Kash adds block control statements around its commands. A block is introduced by
a keyword line and holds one or more statements indented deeper than the keyword.
Blocks nest, and dedenting to an existing enclosing level ends the inner block.
Block keywords are `if`, `elif`, `else`, `match`, and `case`; a block statement
is not `;`-separated.

`if` selects a branch. Its condition is either a raw command — evaluated for its
exit status, where zero is true and non-zero is false — or an explicit Kame
expression in `@(...)`, evaluated with Kame's truth rules. A command condition
does not emit a failure diagnostic for a non-zero status; that is an implicit
`CMD?` scoped to the condition. `$(...)` is not a valid condition, because every
non-nil string is true under Kame truth, so it would always be true.

```kash
if test -f ./build/app
	gcc -o build/app src/app.c
elif @(options.release)
	gcc -O2 -o build/app src/app.c
else
	report "nothing to build"
```

`match` selects a branch by pattern, reusing the pattern clauses of the Kame
`match` form in `004-language.md`. Its subject is a value — `@(...)` or
`$(...)` — and each `case` names a match pattern. The first matching case runs
and its named captures bind as `$name` references within that arm; `else` is the
optional fallback.

```kash
match @(target)
	case ./src/{name:*}.c
		cc -c $name.c
	else
		report unknown
```

Only the selected branch runs, so an unselected branch's commands, dynamic
dependencies, and effects never occur. `if`/`elif`/`else` and `match`/`case`
delegate their tests and subjects to the Kame expression evaluator and their
bodies to Kash statements, so they carry normal Kame values, dependencies, and
diagnostics.

## Results and lowering

A command or pipeline returns a `Process`/`Result` value rather than an untyped
string. It carries process completion, status, streams, and related execution
information. `$(COMMAND)` is the explicit conversion point that captures
standard output as a value.

The Kash parser may own statement, pipeline, redirection, async, and recovery
AST nodes. Every embedded expression and reference must retain the normal Kame
AST node and source span. Lowering Kash nodes to `run`, `pipe`, and associated
process operations must preserve Kame's laziness, capability checks, dynamic
dependencies, and diagnostics. Block control statements lower to the Kame `if`
and `match` special forms: a command condition lowers to a process-status
predicate, an `@(...)` condition to the Kame expression, a `match` subject to a
value, and each branch body to a nested Kash statement sequence.

## Formatting and diagnostics

Formatting must preserve the parser boundary:

- `$REFERENCE`, `${REFERENCE}`, `@(EXPRESSION)`, and `$(COMMAND)` retain their
  distinct forms.
- Block keywords `if`, `elif`, `else`, `match`, and `case` remain on their own
  line, and each nested block is one tab deeper than its keyword.
- A Kame expression inside `@(...)` is formatted by the Kame expression
  formatter.
- A nested command substitution is formatted by the Kash formatter.
- Formatter output is idempotent and reparses to an equivalent Kash AST.

Malformed references are Kame reference errors, malformed `@(...)` contents are
Kame expression errors, and malformed `$(...)` contents are Kash command errors.
Diagnostics must identify the enclosing Kash source and the precise embedded
span.

## Acceptance tests

- `$name`, `$project.name`, and `${name}` resolve the same Kame bindings.
- `$PATH` does not read the environment, while `$env.PATH` does.
- `@(EXPRESSION)` accepts the complete Kame expression grammar and preserves
  values without string splitting.
- `$(COMMAND)` captures stdout and does not parse its contents as Kame syntax.
- Command arguments containing whitespace remain one argument.
- A raw wildcard is literal; `@(wildcard PATH)` is evaluated as a Kame wildcard.
- Raw command pipelines stream process data; expression pipes inside `@(...)`
  use Kame value-pipe behavior.
- `CMD?` retains diagnostics for accepted non-zero exits.
- `LEFT ?? RIGHT` evaluates the fallback only for a nil or failed left side.
- An `if` command condition is true on exit zero and false on a non-zero exit
  without a failure diagnostic; an `@(...)` condition uses Kame truth.
- `match`/`case` selects the first matching pattern, binds named captures as
  `$name` in that arm, and falls to `else` when nothing matches.
- Only the selected control-statement branch runs; an unselected branch's
  commands and effects never occur.
- Nested control blocks parse and format idempotently.
- Existing `.kmk` recipes retain their shell-text behavior unless an explicit
  future Kash recipe mode is selected.
