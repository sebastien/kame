# Kash reference

Kash is Kame's process language: shell-like command scripts that keep Kame's
typed values, lazy definitions and dependency tracking. It is **not** a POSIX
shell or KornShell. Values are never turned into shell text and reparsed: argv is
built directly, without implicit splitting or globbing.

Kash is used in three ways:

- **Scripts**: `.kash` files (`.ksh` is equivalent) run by `kash` or `kame do run`.
- **Recipes**: `.kmk` rules with `SHELL = kash`, or rule metadata `[shell: kash]`.
- **Interactive shell**: a REPL over one live session (`kash` on a terminal).

Key terms:

| Term | Meaning |
| --- | --- |
| Statement | One command graph, definition, or control block; ends at newline or `;`. |
| Command graph | Stages joined by pipes and operators, launched together. |
| Stage | One executable invocation, with its setup (`:cwd`, `:timeout`, env) and redirections. |
| Expression | A parenthesized Kame expression `(…)`, evaluated by the Kame evaluator. |
| Capture | `$(COMMAND)`: run a graph and return its stdout as a string. |
| Result | Record describing a completed process: `status`, `signal`, `stages`, … |
| Execution scope | Script, function call, or `with` block: owns locals, async work and defers. |
| Session | One engine and policy shared by every source fragment or interactive entry. |

## Running

```sh
kash ./publish.kash preview          # file, then program arguments
kash -c 'echo $args' one two         # inline text
kash                                  # REPL on a terminal; piped stdin is read as source
kash -i ./lib.kash                    # load sources, then go interactive
kame do run ./publish.kash -- preview
kame do run --lang kash -c 'echo hello'
kame do parse --lang kash ./publish.kash
kame do fmt --lang kash -n ./publish.kash
```

- `kash` is the `kame` binary invoked under that name; it implies `--lang kash`
  and treats arguments after the source as program arguments. A
  `#!/usr/bin/env kash` shebang works since `#` starts a comment.
- All fragments are parsed before execution and share one engine and policy.
- Kash-first sessions get build-like default grants; Kash appended to a value
  session inherits its stricter policy. Capability, timeout and `-C` options
  apply as for any session.

## Language

### Statements and definitions

```kash
name = "Ada Lovelace"                 # lazy, evaluated on first use
let workspace = (line $(mktemp -d))   # eager, evaluated when reached
(greet WHO) = (str "Hello, " WHO)     # expression-bodied function

def probe dir
	if test -f "${dir}/ready"
		return 0
	return 75 ?

echo (greet name)
```

- `NAME = RHS` is lazy and memoized. The RHS is one Kame expression, a `$ref`,
  a `$(…)` capture, or a `??` chain of those.
- `let NAME = RHS` runs when execution reaches it. Reading it earlier raises
  `BINDING_UNINITIALIZED`; it cannot be redeclared or overridden by `--set`.
- `def NAME PARAM… [rest...]` defines a native function with an indented body
  (no braces, no `end`). Each call gets fresh locals and its own async/cleanup
  lifetime. Call functions through expressions, `(probe ./ws)`, not as commands.
- `return STATUS` (0–255) leaves the function; `return N ?` returns an accepted
  nonzero Result. Test it with `(success? …)`, since a Result record is truthy.
- `#` at word start begins a comment; `//` is literal text.
- A statement starting with `(EXPRESSION)` is evaluated for its effects.

### Words and argv

| Form | Meaning |
| --- | --- |
| `$name`, `$a.b`, `${name}` | Kame reference (`${name}.c` appends `.c`, `$name.c` reads field `c`). |
| `$...cmd` | Spread a flat list as argv, including the executable at command position. |
| `(EXPRESSION)` | Kame expression, standalone or as a word fragment: `--out=(name).json`. |
| `$(COMMAND)` | Capture stdout of a graph or `&&`/`||` list. |
| `$env.NAME` | Environment lookup (needs a read grant). `$PATH` is a Kame binding, not the env. |
| `"…"` | Groups one word; expands `$…` and `$(…)`, keeps `(…)` literal. |

- A scalar is always exactly one argument, whitespace included.
- A standalone list (`$files`, `(wildcard ./src/*.c)`) splices its items; an
  empty list adds nothing. Mixed or quoted words require scalars.
- Nil behaves like an unset shell variable: a standalone nil adds no argument,
  and inside a quoted or mixed word it reads as empty text.
- Raw `*.c` is literal: use `(wildcard …)` to glob.
- Single quotes and legacy `@(…)` are rejected.
- Captures keep output exactly, trailing newline included: use `(line …)` to
  drop one final LF, or `(strip …)` when the contract allows it.

### Processes

```kash
cat < ./input.txt | sort > ./output.txt
build >> ./build.log 2>&1
:cwd ./build :timeout 30 :NODE_ENV production npm test
:env $exports :accept-exit [1] checker --report
```

- `|` streams stdout to stdin with backpressure. `<` applies to the first stage,
  `>`/`>>` to the last. `2>`, `2>>` and `2>&1` (order-sensitive) handle stderr.
  `>&out` and `>&err` route to the current output or error sink.
- Stage setup precedes the executable: `:cwd`, `:timeout` (seconds), `:env RECORD`,
  `:KEY VALUE` env overrides, `:accept-exit [codes]`.
- Every reached command head is resolved and tracked as a tool dependency, using
  the startup `PATH` and `--tool NAME=PATH`. Missing tools report `TOOL_MISSING`.
- `(capture! POLICY … CMD ARGS…)` returns a Result with exact `stdout`/`stderr`
  bytes; `(run! …)` and `(pipe! …)` build graphs from expressions.
- A trailing `&` runs a graph asynchronously; the owning scope joins it. Bind a
  handle with `(run! :async :true …)` and `(await job)`.
- Advanced POSIX topology (groups, `|(merge)`, `|(broadcast)`, `|(tee PATH)`,
  `|(frame :lines)`, `|(filter …)`, `|(each)`, `stream` channels, `<(graph)`
  operands) is documented in the [Kash skill reference](skills/kame/ref/kash.md).

### Failure and recovery

| Form | Behavior |
| --- | --- |
| `A && B`, `A \|\| B` | Sequential list; `\|\|` runs only after a process exit or signal. |
| `CMD ?` | Accept a nonzero or signal exit; stderr stays visible. |
| `CMD ? FALLBACK` | Run a fallback command on recoverable failure. |
| `x = A ?? B` | Use `B` when `A` is nil or failed (definition RHS only). |
| `:accept-exit [1 7]` | Treat only those exit codes as success for one stage. |

An unhandled failure stops the scope. Pipelines fail if any stage fails. Recovery
never hides capability denial, cancellation or `exit`.

### Control flow

```kash
if test -f ./out.txt
	echo exists
elif (nop options.release)
	echo release
else
	echo missing

match $target
	case ./src/{name:*}.c
		echo $name

for f in files
	convert $f
while probe --pending
	worker --one
for line from ./events.jsonl :lines
	handle $line
```

- Command conditions test exit success; expression conditions use Kame truth.
- `for NAME in LIST` iterates a materialized list. `for NAME from SOURCE :lines|:nul`
  streams records (max 64 KiB each) from a file or `:stdin`.
- `break` and `continue` target the nearest loop in the same function.
- Bodies are indented; keywords sit on their own lines. Each branch and
  iteration has fresh locals.

### Scopes, cleanup and exit

```kash
with :cwd ./project :env [MODE: "release"]
	defer rm -f ./scratch
	configure && compile
```

- `with` runs a body with a scoped cwd and environment, and owns its async work
  and defers. Host cwd and environment are never mutated.
- `defer COMMAND` snapshots argv now and runs it at scope end; `defer (EXPR)`
  evaluates later. Defers run in LIFO order, even after failure, within a
  five-second budget.
- `exit STATUS` ends the whole execution boundary (script, recipe attempt or
  interactive session), joining or cancelling owned work and running defers.

### Environment overlay (recipes)

`export NAME = VALUE` and `unset NAME…` edit the child environment of the current
scope; nested scopes revert on exit. Reads and removals need recipe metadata
`[authority: [read: […] unset: […]]`; `[tty: :true foreground: :true]` hands the
terminal to foreground commands. Direct `do run` sessions have no such metadata.

### Libraries, recipes and context

```kash
(include ./lib/checks.kash)
config = (joinpath (source-dir) "defaults.json")
```

- Included files hold definitions only (comments, `=` definitions, `def`,
  nested includes). Includes are literal, top-level and relative to the
  including file. `.km`/`.kmk` programs can include `.kash` libraries too.
- `(kash ./mod.kash)` loads a module value (`mod.fn`); `(kash TEXT)` builds a
  script callable from text.
- `(source-dir)` is the directory of the authored file.
- In recipes and scripts: `inputs`, `outputs`, `inputs/changed` and `args` are
  typed lists (`args.0` for the first argument).

## Interactive shell

```text
kash                                  # REPL when stdin is a terminal
kash -i [FILE.kash…] [-- ARG…]        # preload sources, then interactive
kame do run -i [OPTIONS] [FILE.kash…] [-- ARG…]
```

On a terminal, entries are edited behind a `kash DIR> ` prompt; Enter inserts
a new line while the entry is incomplete and submits it once complete. With
piped input, lines are read without prompts.

```text
kash ~/src> let n = 3
kash ~/src> (str "n=" n)
"n=3"
kash ~/src> def hi who
	echo hi $who

kash ~/src> (hi "you")
hi you
```

### Entries

- An entry with an unclosed parenthesis or quote continues on the next line.
- An entry opening a block (`def`, `if`, `for`, `match`, …) continues until a
  blank line.
- A final line holding only `&` ends the entry and runs it in the background
  (on a terminal, Ctrl-X Enter adds that line after a one-line entry).
- Entries are named `<kash:N>` in diagnostics.
- A parse or registration error rejects the whole entry; nothing runs.
- A failing statement ends its entry only; the session continues.
- A leading expression prints its value, except nil and command Results.

### Session semantics

All entries share one engine, top-level scope, capability policy and cleanup scope.

| Entry | Behavior |
| --- | --- |
| `let NAME = …` | Evaluated now; a later `let` rebinds it. |
| `def NAME …` | A later `def` replaces it. |
| `NAME = …` | Lazy, defined once: redefinition is `DEF_INVALID` (use `let`). |
| `NAME += …` | `DEF_INVALID`. |
| Rules, includes | `FEATURE_UNSUP` in entries; preload them as files instead. |

### Session commands

These three single-line entries are shell commands, not Kash statements:

| Entry | Effect |
| --- | --- |
| `cd [DIR]` | Change the session directory. No operand is `$HOME`; `-` is the previous directory; `~` expands. |
| `export NAME=VALUE…` | Set variables in the child environment of later commands. |
| `unset NAME…` | Remove variables from that environment. |

Operands split on blanks. Single quotes are literal; `$NAME`/`${NAME}` expand
from the session environment as it was before the entry (missing names expand
to empty). With default grants, read/write grants follow `cd`; explicit grants
never move.

### Terminal, interrupts and exit

- Foreground statements get the terminal on every stream they would otherwise
  share with the session. Captures, `&` graphs, defers and record loops keep
  pipes; `:tty :false` opts a stage out.
- Ctrl-C reaches the running command only; the entry ends with status 130 and
  the session continues. Ctrl-C at the prompt clears the pending input. A
  second Ctrl-C while an interrupted entry settles kills its processes and
  ends the session with 130, skipping cleanup.
- Ctrl-D on an empty line or `exit N` ends the session: owned work is joined
  and cleanup runs. The exit status is the last entry's status or `N`.
- History keeps 1000 entries in `$KASH_HISTORY`, else
  `$XDG_STATE_HOME/kash/history`, else `~/.local/state/kash/history`;
  multi-line entries are stored with backslash-continued lines.
- Not provided: programmable completion, right or multi-line prompts.

### Jobs

On a terminal every entry is a job: its Kash evaluation plus the `&` graphs it
starts. Ctrl-Z stops the running entry wherever it is (a command, a capture,
an `if` test, plain evaluation) and `fg` resumes it exactly there.

| Entry | Effect |
| --- | --- |
| `jobs` | List `[N]+ STATE  TEXT` (`running`, `stopped`, `done`, `exit S`, `signal NAME`). |
| `fg [%N]` | Resume in the foreground with the job's terminal modes. |
| `bg [%N]` | Resume a stopped job in the background. |
| `kill [-SIG] %N…` | Cancel the job, or send `SIG` to its processes; `kill PID` stays external. |

Background jobs run while you type; reading the terminal stops them
(`stopped (tty input)`). Stops and completions are reported before the next
prompt. The first Ctrl-D or `exit` with stopped jobs lists them; the second
ends the session and terminates them.

### Startup file and prompt

`$KASH_INIT`, else `$XDG_CONFIG_HOME/kash/init.kash`, else
`~/.config/kash/init.kash`, runs as entries before the first prompt (empty
`KASH_INIT` disables it). A `kash-prompt` function receives
`[status: dir: name: jobs:]` and returns the prompt string:

```kash
(kash-prompt info) = (str info.name (if (= info.status 0) "" " !") " $ ")
```

### Editing, completion and highlighting

- The editor follows GNU Readline conventions: emacs bindings by default, vi
  when selected, incremental history search and undo. It reads `$INPUTRC`,
  else `~/.inputrc`, else `/etc/inputrc`, with application name `kash`.
- Tab with only blanks before the cursor inserts a tab, for block bodies.
- Elsewhere Tab completes into a grouped, described menu: `$` bindings,
  functions after `(`, session commands, keywords and `PATH` executables at
  command position, directories after `cd`, and paths otherwise.
- Entries are highlighted by lexical class, and literal command words that
  resolve to nothing are marked as unknown. `NO_COLOR` or `TERM=dumb`
  disables highlighting.

## References

- [Kash skill reference](skills/kame/ref/kash.md): full usage detail, topology and telemetry
- [Kash cookbook](kash-cookbook.md): argv, status policy, retry, cleanup patterns
- Specs: [Kash](spec/017-kash.md), [streaming](spec/042-kash-streaming.md),
  [practical scripting](spec/048-kash-practical-scripting.md),
  [libraries](spec/050-kash-libraries.md),
  [session authority](spec/051-kash-session-authority.md),
  [interactive](spec/052-kash-interactive.md), [CLI](spec/009-cli.md)
