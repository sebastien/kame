# Kash

Kash is Kame's shell language. It extends Kame's core expression language with
shell-oriented statement syntax, command execution, and streaming pipelines,
while preserving Kame's typed values and evaluation model. It is intended to
make a command-oriented build or automation script read like a shell script
without inheriting the accidental word splitting and implicit environment of
traditional shells.

## Scope and model

A Kash source file is a sequence of statements. A statement may define a value,
evaluate a Kame expression, or start a command. The expression forms retain
their normal Kame meaning; Kash adds convenient surface syntax around them.
Semicolons separate statements on one physical line.

```kash
name = "John Smith"; greeting = "Hello, " + $name
(banner WHO) = "Hello, " + $WHO
echo @(banner "Kame")
```

Definitions use the same forms as Kame rules:

- `NAME = EXPR` defines a value.
- `(NAME ARG ...) = EXPR` defines a parameterized expression.
- `;` permits multiple statements on one line.

Values are not converted into shell words. Expanding a string produces one
argument, even when it contains whitespace. For example, `name = "John Smith"`
followed by `echo $name` invokes `echo` with one argument, `John Smith`.

## Expansion and metaprogramming

Kash makes the boundary between shell convenience and Kame computation explicit:

| Form | Meaning |
| --- | --- |
| `$REFERENCE` | Resolve and insert a Kame value or reference. |
| `${REFERENCE}` | Braced spelling of the same reference, for use beside literal text. |
| `@(EXPRESSION)` | Evaluate an arbitrary Kame expression and insert its value. |
| `$(COMMAND)` | Run a Kash command or pipeline and capture its standard output. |

`$REFERENCE` is the convenient form for an already-defined value. A reference
uses Kame's reference syntax, so `$project.name`, `$files.0`, and
`$config.{host,port}` are valid as well as `$name`. `${REFERENCE}` only delimits
a reference; it is not a second general expression syntax.

`@(EXPRESSION)` is the explicit entry into Kame's expression and
meta-programming layer. It is used for calls, value transformations, patterns,
lists, records, templates, and any other computation that is more than a direct
reference. It retains the expression language's normal grammar and semantics;
Kash does not define a competing set of values or functions.

`$(COMMAND)` is command substitution. Its contents use Kash command and
pipeline syntax, run as a process, and yield captured standard output. It never
means Kame expression expansion.

Each expansion supplies a value or command argument; it is never subject to a
second, implicit split.

```kash
branch = $(git branch --show-current)
echo "building " $branch
emit @(replace source ".c" ".o")
```

`@NAME`, `@(...)`, and `@tmpl(...)` are Kash's meta-programming forms. They
operate during program construction rather than ordinary value evaluation, so
generated syntax, definitions, and templates can be composed before execution.
Their detailed quoting and hygiene rules belong to the macro/template design;
they must not introduce implicit shell-style splitting.

The process environment is explicit through `env.`. `$PATH` is a Kame binding
lookup, not an implicit environment read; use `$env.PATH` or `@(env.PATH)` when
an environment value is required. This keeps environment access visible in the
program rather than falling back from every unresolved name.

```kash
home = env.HOME
env.NODE_ENV = "production"
```

## Commands

A raw line is a command followed by arguments:

```kash
git status --short
```

Conceptually, this is equivalent to:

```kame
(run "git" "status" "--short")
```

The raw form is syntax for constructing a `run` expression, not an invitation
to reinterpret its arguments with shell quoting, globbing, or word splitting.
Explicit Kame expressions can be supplied wherever an argument is accepted.

`run` accepts keyword setup options before its command expression. In addition
to command arguments, it supports process configuration such as working
directory, timeout, and environment variables:

```kame
(run :cwd build-dir :timeout 10 :NODE_ENV "production" "npm" "test")
```

The keyword options configure the process; remaining expressions form its
command and arguments.

## Pipelines and redirection

Pipelines are first-class expressions:

```kash
generate | transform | publish
```

This is conceptually:

```kame
(pipe (run "generate") (run "transform") (run "publish"))
```

Kash builds a Kame engine streaming graph for the pipeline rather than eagerly
buffering each stage. A pipe is value-sensitive: when its left side is a Kame
value, it follows Kame's ordinary `|` semantics; when its stages are commands,
it connects their process streams. This permits shell-like pipelines and Kame
data transformations to compose without erasing their types.

Input and output redirections are available on command pipelines:

```kash
compile < sources.txt > output.log
compile >> output.log
```

`<` supplies standard input, `>` replaces the destination, and `>>` appends to
it. The redirections belong to the pipeline execution graph and do not change
the values passed as command arguments.

## Asynchrony, failure, and recovery

Appending `&` starts a command or pipeline asynchronously in the background:

```kash
watch-assets &
```

The expression immediately yields its process handle/result rather than waiting
for completion.

Commands normally fail when their process exits non-zero. `?` accepts a
non-zero command exit while retaining its normal diagnostic output; it does not
silence stderr or otherwise hide failure details:

```kash
grep needle missing-file?
```

The handled form provides an explicit recovery expression:

```kash
deploy ? echo "deployment failed; continuing"
```

`??` is expression-level fallback. `LEFT ?? RIGHT` evaluates `RIGHT` when
`LEFT` evaluates to `nil` or fails; otherwise it produces `LEFT`'s value. This
is distinct from `CMD?`: the latter accepts a non-zero command exit, whereas
`??` selects an alternate value or recovery computation.

```kash
config = read-config ?? default-config
revision = $(git rev-parse --short HEAD) ?? "unknown"
```

## Results

Running a command, including a pipeline, produces a `Process`/`Result` value.
That value represents process completion, exit status, standard streams, and
other execution details instead of reducing command execution to an untyped
string. Command substitution, `$(CMD)`, is the deliberate conversion point: it
captures the command's standard output and returns it as a value.

This separation lets a Kash program either inspect process results, stream a
process into another operation, or explicitly capture output when text is the
desired result.
