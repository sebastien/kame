---
created: 2026-09-29T04:44:29+00:00
updated: 2026-11-25T19:45:55.964653+00:00
uuid: e33c3aa0-ef5d-4faa-a208-2efbafdf5ac3
identity: e33c3aa0-ef5d-4faa-a208-2efbafdf5ac3
---
# Kash

Kash is Kame's action language, the command grammar of the unified Kame/Kash
language (revision 2). It shares Kame's typed values, bindings and evaluator,
and adds command statements, process pipelines and block control flow.

The goal has not changed since the first design: a command-oriented build or
automation script should read like a shell script, without inheriting the
accidental word splitting and implicit environment of traditional shells.
Kash never converts values into shell text and reparses them.

Kash runs in four places:

- rule recipes, where it is the default rule body;
- `.kash` programs;
- interactive sessions (`kash`);
- inside expressions, through `$(…)`, `$?(…)` and `$&(…)`.

## Scope and model

A Kash block is a sequence of statements. The first token decides what a
statement is:

- `let` or `set` starts a binding;
- `def` starts a definition;
- `(` starts an expression statement;
- anything else is a command, so `echo a = b` runs `echo`.

```kash
let name = "John Smith"
let greeting = "Hello, ${name}"
def banner who = "Hello, ${who}!"
echo (banner "Kame")
```

- **`let NAME = EXPR`** evaluates eagerly and binds immutably for the rest of
  the block. A later `let` shadows it.
- **`set NAME = EXPR`** reassigns an existing `let` that no closure captures.
  It never creates a binding.
- **`def NAME PARAM… = EXPR`** defines a function. With an indented block
  instead of `= EXPR`, the function's body is itself Kash. Without
  parameters, `def NAME = EXPR` is a lazy, memoized value.
- **Control flow** uses blocks whose headers have no trailing colon: `if`,
  `elif` and `else`, `for NAME in EXPR`, `while EXPR`, `match`, `return`,
  `break`, `continue`, `exit`, `defer` and `with`.

Values are never converted into shell words. `echo $name` invokes `echo` with
one argument, `John Smith`.

## Values in commands

| Form | Meaning |
| --- | --- |
| `$name`, `$name.field`, `$xs.0` | Insert a value by reference. An unbraced reference stops at `/`. |
| `${EXPR}` | Evaluate an expression fragment, also inside quoted or mixed words. |
| `(EXPR)` | Evaluate an expression fragment. Adjacent fragments compose one word. |
| `$...xs` | Spread a list into separate arguments. |
| `$(COMMAND)` | Run a command or pipeline and capture its stdout as text. |

How values become arguments:

- A scalar always becomes exactly one argument.
- A list standing alone becomes one argument per item, and `nil` adds none.
- A record, bytes value or callable standing alone is rejected rather than
  converted to text.
- A quoted or mixed word always forms one argument, using Kame's string
  conversion.
- Only wildcards and `~` written unquoted in the source expand, against the
  command's directory. Substituted and quoted text stays literal.

```kash
let flags = ["-O2", "-Wall"]
cc $...flags -c $source -o (object-path source)
git log --format=%s -- ./src/*.c
```

There is no macro layer. `@` is literal text in command words, and an
unquoted `@{` is a parse error. Computed declarations are rule values
registered with `generate`. `eval` parses a single expression. Documents use
templates through `render`.

## Environment and state

The environment is explicit and tracked:

```kash
let mode = env/BUILD_MODE ?? "debug"   # env/NAME is nil when unset
echo $HOME/projects                    # upper-case names fall back to the environment
set env/NODE_ENV = "production"        # seen by later statements and processes
cd ./build
```

- **Reading.** `env/NAME` reads the environment store and yields `nil` when
  the variable is unset. A bare upper-case name also falls back to the
  environment, but fails with `ENV_VAR_MISSING` when unset, so typos are
  caught. Lower-case names never fall back.
- **Unit state.** `set env/NAME` and `cd` change the state of the current
  process-like unit: a script, a rule body, a capture, or an interactive
  session. Later statements and processes in the unit see the change; the
  parent unit never does.
- **Authority.** Environment reads are tracked dependencies and need the
  `env` grant. Writing the store is not a host environment change.
  `set env/NAME = nil` unsets a variable, subject to the environment
  authority rules of spec 051.

## Commands

A command statement is a head followed by words:

```kash
git status --short
```

Conceptually, this lowers to a process operation:

```kame
run! "git" "status" "--short"
```

**Resolving the head.** Kash tries, in order:

1. a lexical function;
2. one of a closed set of builtins (`cd`, `wait`, `kill`, `out!`, `err!`, …);
3. an executable found through the environment's `PATH`.

The standard library is never consulted in command position: `basename $f`
runs the executable, while `(basename f)` calls the library function. `x/NAME`
names an executable explicitly. Every executable reached is recorded as a
tracked dependency.

**Stage setup.** Setup keys before the executable configure one stage
without changing the unit:

```kash
cwd: ./build timeout: 10 NODE_ENV: production npm test
```

This corresponds to
`run! :cwd ./build :timeout 10 :NODE_ENV "production" "npm" "test"`.
`env:` takes a record, and `with cwd: … env: …` applies an overlay to a whole
block.

**Functions as commands.** A function used as a command receives its words as
arguments. Its stdout takes part in pipelines and redirection, like a
process's.

## Pipelines and redirection

```kash
git log --format=%s | sort | uniq -c > ./build/subjects.txt
```

- **Process streams only.** `|` connects process streams in one graph of
  concurrently running stages, with bounded buffers and backpressure. It is
  never a value operator: values flow through `|>`, a separate pipeline.
- **Explicit fan-in and fan-out.** Use `|(merge)`, `|(broadcast)` and
  `|(balance)`. Named channels are declared with `stream NAME`.
- **Redirection.** `<` supplies stdin, `>` replaces and `>>` appends. Stderr
  routing is limited to `2>`, `2>>` and `2>&1`, and `>&out` and `>&err` send
  stdout to the current output or error sink.
- **Checks come first.** All of them run before any stage starts or any file
  is truncated.

## Asynchrony, failure, and recovery

```kash
let job = $&(watch-assets --port 8080)
run-tests
let done = $?(wait $job)
```

`CMD &` and `$&(…)` start work asynchronously, and the enclosing invocation
owns it:

- success joins it;
- failure cancels and reaps it;
- nothing is ever detached.

`defer COMMAND` registers cleanup that runs once, in reverse order, when the
invocation ends.

A nonzero exit fails the statement. Kash has several explicit and distinct
ways to handle a failure:

```kash
grep needle missing-file ?                      # accept the exit; diagnostics still show
deploy ? echo "deployment failed; continuing"   # recovery command
make test && make install || echo "not installed"
let revision = $(git rev-parse --short HEAD || printf unknown)
```

- **`CMD ?`** accepts a nonzero exit without hiding stderr or diagnostics.
- **`CMD ? ELSE`** runs a recovery command after a failure.
- **`&&` and `||`** sequence completed commands. `||` runs only after a
  process exit failure.
- **`??`** is the expression-level fallback for `nil` and for missing
  references, tools, environment variables and fields. It does not catch
  process failures.
- **Capability denial and cancellation** can never be recovered.

## Results

Every invocation, of a function or a process, has an outcome record:

| Field | Meaning |
| --- | --- |
| `state` | `:completed`, `:failed` or `:cancelled` |
| `ok`, `status`, `signal` | Success flag, exit status, terminating signal |
| `value` | Return value, or the stdout captured by `$?(…)` |
| `error` | The failure, or `nil` |
| `stages` | Per-stage records |

Read it with `$?(…)`, or from `wait`:

```kash
if $?(test -f ./ready).ok
	echo ready
```

`$(…)` is the deliberate conversion point to text. It captures stdout
exactly, without stripping the trailing newline (use `line` for that), and
raises on failure. A function's return value is never its stdout: `$(f x)`
captures what `f` writes.

## History

- **2026-09-28.** First design note, written alongside the proposed Kash
  spec (commit `1806184d`). The original text is preserved in git there.
- **2026-10-09.** Recipes became parsed directly as Kash (spec 038). The
  `@(…)` expression boundary and template expansion of recipes were removed;
  values are written as `(EXPR)` fragments.
- **2026-11-18.** Revision 2 (spec 055). Differences from the first note:
  - Blocks bind with `let`, `set` and `def` instead of `NAME = EXPR` and
    `(NAME ARG) = EXPR`.
  - `${…}` evaluates a full expression, not only a reference.
  - The `@NAME` and `@tmpl(…)` meta-programming forms do not exist.
    Computed declarations use rule values and `generate`.
  - The environment moved from `env.NAME` to `env/NAME` and
    `set env/NAME`, and upper-case names now fall back to the environment.
  - Commands take stage setup as `KEY: VALUE` words. The expression form is
    `run!` with symbol keys.
  - `|` is never a value operator; value pipelines use `|>`.
  - `??` no longer catches command failures. Use `||`, `? ELSE` or `$?(…)`.
  - Process results became the shared invocation outcome record.
- **2026-11-22.** Authored, unquoted wildcards and `~` expand in command
  arguments.

See [domain/concept:Kash], [domain/architecture:kash_execution] and
[domain/decision:kash_typed_actions]. The normative specs are
`docs/spec/017-kash.md` and `docs/spec/055-unified-kame-kash-language.md`.
