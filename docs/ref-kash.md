# Kash reference

Kash is Kame's process language. It builds argv from typed values and
connects commands, functions, and stream operators into process graphs.
Scripts use `.kash` or `.ksh`. Indented Kame recipes use Kash by default.
On a terminal, running `kash` opens the interactive shell.

| Term | Meaning |
| --- | --- |
| Statement | A command graph, definition, binding, or control block. |
| Command graph | Stages connected by pipes or stream operators. |
| Stage | An executable or function invocation, with its setup and redirections. |
| Capture | `$(COMMAND)` collects a graph's stdout as text. |
| Outcome | `$?(COMMAND)` retains invocation status, captured stdout, and failure details. |
| Execution scope | A boundary that owns its async work and deferred cleanup. |

## Running

```sh
kash ./publish.kash preview
kash -c 'printf "%s\n" $...args' one two
kash -i ./lib.kash
kash --login
kash -lc 'echo hello'
kame do run ./publish.kash -- preview
kame do run --lang kash -c 'echo hello'
kame do kash -c 'echo hello'
kame do parse --lang kash ./publish.kash
kame do fmt --lang kash -n ./publish.kash
```

`kame do kash` starts the unified runner in Kash, like `do run --lang kash`.
Invoking the executable as `kash` selects Kash and binds operands after the
source to `args`. A `#!/usr/bin/env kash` shebang works in a file. A terminal
with no source opens the REPL. Piped stdin with no source is read as Kash.

Ordered fragments share one execution session. Kash-first sessions receive
build-like default grants plus environment reads. Appending Kash to a value session retains that
session's grant policy. Capability, timeout, and `-C` options apply across
the session. [CLI reference](skills/kame/ref/cli.md)

## Language

### Bindings and functions

```kash
def name = "Ada Lovelace"
def greet who = str "Hello, " who
let workspace = line $(mktemp -d)
defer rm -rf $workspace

def ready dir
	if $?(test -f "${dir}/ready").ok
		return true
	return false

echo (greet name)
echo (ready workspace)
```

`def NAME = EXPRESSION` defines a lazy value. `let NAME = EXPRESSION`
evaluates when execution reaches it. `set NAME = EXPRESSION` updates an
existing eligible `let` binding in the same invocation. A read before its
initializer reports `BINDING_UNINITIALIZED`.

Functions can have an expression body or an indented action block. Write
`def NAME ()` for a zero-parameter header and `rest...` for a final rest
parameter. A bare command head can call a function, and a value call keeps
its return value. In value position, use `NAME ()` to call with no arguments.
Function stdout and stderr can participate in pipes and redirections.

`return VALUE` exits the function with a value. A generator uses `yield VALUE`
to produce a lazy stream element and a bare `return` to end the stream.
Yielded values go to a value consumer such as `for` or `collect!`. Stdout
travels through the process output channel. Early closure runs the generator's
pending defers. [Generator specification](spec/012-streams.md#generator-streams)

A statement beginning with `(EXPRESSION)` evaluates it for effects. A
word-start `#` begins a comment. `//` is literal.

### Words and argv

| Form | Behavior |
| --- | --- |
| `$name`, `$files.0` | Read a value. |
| `${EXPR}`, `(EXPR)` | Compute a value for an argument or a fragment of a mixed word. `${name}.c` appends a suffix. |
| `$...files`, `$...{project.command}` | Spread a flat list into separate argv entries. |
| `$(COMMAND)` | Capture stdout from a graph or command list. |
| `${env/HOME}` | Read one environment variable under an environment grant. |
| `"…"` | Group one word with `$` substitutions. |
| `'…'` | Group a literal word. |

A scalar contributes one argument, including any whitespace. A standalone
`nil` contributes no argument. In quotes or a mixed word it contributes empty
text. A `${EXPR}` fragment in quotes or a mixed word renders like a Kame
string. For example, `echo "Repos ${(dirs REPOS)}"` passes one argument
containing the paths separated by spaces. Use `$...files` to pass each path in
its own argument. Authored unquoted `*`, `**` and character classes in arguments
expand to separate paths in the command's working directory. No matches is an
error. Quote or escape a wildcard to pass it literally; substitutions are never
globbed. Unquoted leading `~` or `~/` uses the session's `HOME`.

An unbraced reference stops at `/`. In `$dir/file.txt`, only `dir` is
substituted. `${EXPR}` accepts qualified names such as `${lib/name}` and calls
such as `${(dirs REPOS)}`. An unbound
uppercase name can read the environment when permitted. Use `x/NAME` in
command position to select an external executable directly.

### Processes and outcomes

```kash
cat < ./input.txt | sort > ./output.txt
build >> ./build.log 2>&1
let environment = {MODE: "release"}
cwd: ./build timeout: 30 env: $environment npm test
let result = $?(accept-exit: [1, 7] checker --report)
let job = $&(worker --once)
let finished = $?(wait $job)
```

`|` streams stdout to stdin with backpressure. Use `>` or `>>` for stdout and
`2>` or `2>>` for stderr. `2>&1` copies stdout's destination at that point.
`>&out` and `>&err` route output to the current invocation's sinks.
Stage settings precede the executable. `timeout:` is in seconds.
Ordinary `NAME: VALUE` settings add child environment assignments.

Reached external command heads resolve through the current unit's `PATH`
and tool overrides. Resolved
executables become build dependencies. Missing executables report
`TOOL_MISSING`.

`$(COMMAND)` preserves UTF-8 stdout, including trailing newlines.
`$?(COMMAND)` retains invocation status and captured output under `value`.
Its `state` describes how the invocation ended. A generator waiting for its
next pull keeps its suspension state internally.
Use `capture! POLICY …` to capture selected stdout and stderr as bytes.
A terminal `&` starts a graph asynchronously. `$&(COMMAND)` also returns a
handle. The owning scope joins or cancels its work. [Process inspection](spec/049-process-inspection.md)

### Failure and control flow

| Form | Behavior |
| --- | --- |
| `A && B` | Run `B` when `A` succeeds. |
| `A \|\| B` | Run `B` after an eligible process exit failure. |
| `CMD ?` | Accept an ordinary nonzero exit while retaining its raw status. |
| `CMD ? FALLBACK` | Run the fallback after a recoverable command failure. |
| `accept-exit: [1, 7]` | Accept the specified ordinary nonzero exits for one stage. |
| `A ?? B` | Use `B` when the value `A` is nil or a lookup is missing. |

An accepted exit has `state == :completed` and `effectiveStatus == 0`.
Its raw nonzero status leaves `ok` false. Capability denial and cancellation
of the owning invocation propagate. [Selected exits](spec/048-kash-practical-scripting.md)

```kash
if $?(test -f ./out.txt).ok
	echo exists
elif options.release
	echo release
else
	echo missing

match target
	./src/{name:*}.c =>
		echo $name

for file in files
	convert $file
while $?(probe --pending).ok
	worker --one
for line from ./events.jsonl :lines
	handle $line
```

Conditions use Kame truthiness. Test a command with `$?(COMMAND).ok`
or use `.state == :completed` to count accepted exits. List and stream loops receive typed values. Record
loops read `:lines` or `:nul` records from a file or `:stdin`. `break` and
`continue` target the nearest loop in the same invocation. Each iteration
has fresh bindings.

### Scopes, cleanup, and exit

```kash
let environment = {MODE: "release"}
with cwd: ./project env: $environment
	defer rm -f ./scratch
	configure && compile
```

`with` owns its cwd, child environment, work, and defers. It leaves the
host's cwd and environment intact. `defer COMMAND` snapshots command
operands at registration. `defer (EXPRESSION)` evaluates at cleanup. Pending
defers run in reverse order under the cleanup budget.

`exit STATUS` ends the innermost process-like boundary, including the
script, recipe, capture, or interactive entry. It runs cleanup while unwinding.
`return` exits a function or value-producing body.

Recipe metadata can grant scoped environment reads and removals through
`{authority: {read: […], unset: […]}}`. Use `{tty: true, foreground: true}`
to hand the terminal to foreground recipe commands when supported.
`set env/NAME = VALUE` updates the unit's child environment. Use
`set env/NAME = nil` to remove a variable under the applicable authority.
Use `set env/${name} = VALUE` when a string expression supplies the name,
and `env name` to read a variable by its computed name.
Direct sessions can remove names added during the session, but cannot use
`set env/NAME = nil` to remove inherited names.

### Modules and recipes

```kash
include ./lib/checks.kash
import ./lib/archive.km as archive
def config = joinpath module/dir "defaults.json"
check-config $config
archive/create ./dist/app.tgz
```

`include` binds definitions in the caller's scope. Use `include?` to
tolerate a missing file. Kash sources include other Kash sources. Import
exports from either language with `import` or `from … import …`.
Relative source paths use the directory of the containing source.
`module/dir` and `module/file` identify a definition's authoring source.

In a direct script or session, `source $profile` runs a Kash file selected at
runtime. It can see caller bindings but does not publish its own. Its environment
and directory changes persist only if it succeeds. `source bash $profile as
tools` instead loads a Bash file into a separate worker namespace.
[Modules](spec/050-kash-libraries.md)

Recipes bind `inputs`, `outputs`, `changed-inputs`, `captures`, and named
captures or arguments. Use `$inputs.0` for the first input and `$...inputs`
to pass each input as a separate argv entry. `args` holds the current script
or function arguments. Use a rule's `; {shell: "sh"}` metadata for a
foreign-shell recipe whose text is rendered using `@{EXPRESSION}`.
[Advanced process topology](skills/kame/ref/kash.md#explicit-process-topology-posix)

## Interactive shell

Shell settings use `option [--local|--global] [NAME [VALUE]]` (`-l`/`-g`).
Local is the running shell and is the default; global saves user defaults in
`$XDG_CONFIG_HOME/kash/options` (fallback `~/.config/kash/options`), a simple
`name: value` file. `option` lists effective settings and their sources;
`option -g` lists saved overrides. Initial settings are `history-scope`
(`local|global`), `history-max-age-days`, and `history-max-bytes`.
Local overrides beat global overrides, then environment and built-in defaults.
For example, `option -g history-max-age-days 15` saves retention globally.
Lowering retention or size limits can prune saved history on the next refresh.
See [shell options](spec/054-kash-options.md).

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
| `set NAME = …` | Updates an existing eligible `let` binding. |
| `import PATH`, `from PATH import …` | Binds exports for later entries. |
| Rules, includes | `FEATURE_UNSUP` in entries; preload them as files instead. |

### Session commands

These statements update state for subsequent work in the interactive session.

| Statement | Effect |
| --- | --- |
| `cd [DIR]` | Change the session directory. No operand selects `$HOME`. A single dash selects the previous directory. |
| `set env/NAME = VALUE` | Set a scalar in the child environment. |
| `set env/NAME = nil` | Remove a variable from the child environment. |

With default grants, read and write grants follow `cd`. Explicit grants stay
scoped as supplied. `cd` and environment writes from a function stay visible
after return. Captures own child units, so their writes stay local.

An updated PATH affects later lookups, including aliases, pipelines, and
captures. Relative and empty PATH entries resolve against the lookup
directory. Tool overrides still take precedence and run grants still apply.
A stage-local environment override changes only that child.
[Session directory and environment](spec/052-kash-interactive.md#session-directory-and-environment)

### Aliases

Put shortcuts in `~/.config/kash/init.kash`, then invoke their bare names:

```kash-repl
alias ll = ["ls", "-lah"]
ll ./src | head -n 10
alias ll                         # show one
alias                            # list all, sorted by name
unalias ll
```

The nonempty literal string list is an argv prefix. Caller arguments append
without splitting, globbing, interpolation or reparsing. Expansion happens once,
so `alias ls = ["ls", "-lah"]` invokes the real `ls`, not itself recursively.
Computed command heads and explicit executable paths bypass aliases.
Ordinary value bindings are not aliases.

Redefinition replaces an alias; invalid declarations leave it unchanged.
Reserved keywords and builtin names cannot be aliases. An alias whose prefix
starts with `cd` invokes the directory builtin.

Alias declarations and removals are whole top-level session entries. They can
use multiline lists and are understood by `do parse` and `do fmt`:

```sh
kame do fmt --lang kash -i ~/.config/kash/init.kash
```

An interactive-only alias declaration in an ordinary script reports
`PARSE_ERR`. Formatting a startup file leaves its commands unevaluated.

### Terminal, interrupts and exit

- Foreground statements get the terminal on every stream they would otherwise
  share with the session. Eligible `&` graphs attach without taking the
  foreground. Captures, defers and record loops keep pipes; `tty: false` opts
  a stage out.
- Ctrl-C reaches the running command only; the entry ends with status 130 and
  the session continues. Ctrl-C at the prompt clears the pending input. A
  second Ctrl-C while an interrupted entry settles kills its processes and
  ends the session with 130, skipping cleanup.
- Ctrl-D on an empty line or `exit N` ends the session: owned work is joined
  and cleanup runs. The exit status is the last entry's status or `N`.
- SIGHUP and SIGTERM cancel/reap owned jobs and run cleanup, including when
  waiting at the prompt; exit status is `128 + signal`.
- History recalls up to 1000 entries in the current repository scope (nearest
  `.git`/`.jj` root), or CWD outside repositories. Ctrl-X h toggles local/global
  recall without changing input; `history scope [local|global]` reports or
  selects the view. `history clear local|global` deletes retained entries.
- History defaults to 15 days and 1 MiB **per scope**, controlled by
  `KASH_HISTORY_MAX_AGE_DAYS` and `KASH_HISTORY_MAX_BYTES`.
  `KASH_HISTORY_SCOPE=global` selects global recall initially.
- Persistence uses `$KASH_HISTORY`, else `$XDG_STATE_HOME/kash/history`, else
  `~/.local/state/kash/history`. An explicitly empty `KASH_HISTORY` disables
  recording and recall. Legacy untimestamped history is discarded without a
  backup. Storage files are private; an explicit store needs a private parent
  directory. Expired entries are removed on the next maintenance operation,
  not by a background service.
- Likely secrets and space/tab-prefixed entries are excluded from history.
  Detection recognizes common token formats, private keys, password URLs,
  authorization headers, and sensitive literal assignments/options; it is
  best-effort, not a security guarantee. Use leading whitespace or disable
  history for sensitive commands. Deletion cannot erase external backups or
  terminal scrollback. See [history](spec/053-kash-history.md).
- On a terminal, a command that exits non-zero or dies of a signal is not
  reported further: its own output and the status say what happened. `explain`
  shows the latest failure in full (source excerpt, cause, frames). Kash's own
  errors are always reported.
- Not provided: programmable completion or multi-line prompts.

### Jobs

On a terminal every entry is a job: its Kash evaluation plus the `&` graphs it
starts. Ctrl-Z stops the running entry wherever it is (a command, a capture,
an `if` test, plain evaluation) and `fg` resumes it exactly there.

| Entry | Effect |
| --- | --- |
| `jobs` | List `[N]+ STATE  TEXT` (`running`, `stopped`, `done`, `exit S`, `signal NAME`, `killed`); `+` marks the current job, `-` the previous one. |
| `fg [%N]` | Resume in the foreground with the job's terminal modes; waits for its `&` commands too. |
| `bg [%N]` | Resume a stopped job in the background. |
| `kill [-SIG] %N…` | Cancel the job, or send `SIG` to its processes; `kill PID` stays external. |
| `wait [%N…]` | Wait for the jobs to finish or stop (all running jobs without operands); status of the last one. |

`%N` names job `N`, `%+` or `%%` the current job, `%-` the previous one.
Background jobs run while you type; a one-line `cmd &` starts one, and
reading the terminal stops it (`stopped (tty input)`) until `fg` hands it the
terminal, as in `vim notes.txt &` then `fg`. Stops and completions are
reported before the next prompt. The first Ctrl-D or `exit` with stopped jobs lists them; the second
ends the session and terminates them.

### Startup file and prompt

Standalone `kash -l`/`--login`, or invocation with argv[0] `-kash`, loads
`$KASH_PROFILE`, else `$XDG_CONFIG_HOME/kash/profile.kash`, else
`~/.config/kash/profile.kash`, before supplied files or commands. Empty
`KASH_PROFILE` disables the profile. Interactive initialization follows only
in interactive sessions; `kash -lc COMMAND` loads the profile but not init.
Ordinary `kash -c COMMAND` loads neither, including SSH remote-command use.
Use `--lang`, not standalone `kash -l`, for language selection; the unified
runner's `-l` still means language.

Kash profiles contain Kash session entries, not POSIX shell code; `/etc/profile`
and `~/.profile` are not read. Installing the executable, registering it in
`/etc/shells`, and selecting it with `chsh` are separate user/admin actions.

To switch, first test `/absolute/path/to/kash --login -i` in another terminal.
Have an administrator register that exact stable path in `/etc/shells`, then
run `chsh -s /absolute/path/to/kash`. The installed command must be named `kash`,
even if it is a symlink to `kame`. Keep your existing shell open for recovery
and verify your SSH/remote-tool workflows before logging out: remote command
strings are Kash, not POSIX shell source.

`$KASH_INIT`, else `$XDG_CONFIG_HOME/kash/init.kash`, else
`~/.config/kash/init.kash`, runs as entries before the first prompt (empty
`KASH_INIT` disables it). A missing default file is skipped; the directory is
`$XDG_CONFIG_HOME` when nonempty, otherwise `$HOME/.config`, and an
unresolvable `$HOME` leaves no default path. Only this file and the profile are
read; other files in the directory are not. A `kash-prompt` function receives
a record with `status`, `dir`, `name`, and `jobs` fields and returns the prompt string; `kash-rprompt`
receives the same record and returns a right prompt, drawn at the right edge
of the entry's last line:

```kash
def kash-prompt info = str info.name (if info.status == 0 then "" else " !") " $ "
def kash-rprompt info = if info.jobs == 0 then "" else str "[" info.jobs " jobs]"
```

### Editing, completion and highlighting

- The editor follows GNU Readline conventions: emacs bindings by default, vi
  when selected, incremental history search and undo. It reads `$INPUTRC`,
  else `~/.inputrc`, else `/etc/inputrc`, with application name `kash`.
- Tab with only blanks before the cursor inserts a tab, for block bodies.
- Elsewhere Tab completes into a grouped, described menu: `$` bindings,
  functions after `(`, aliases, session commands, keywords and `PATH` executables at
  command position, directories after `cd`, and paths otherwise.
- A separator sets the menu apart from input. Directory `/`, executable `*`,
  symlink `@`, FIFO `|`, socket `=`, and device `#` marks are display-only.
  Directories are blue, executables green, symlinks cyan, and special files
  yellow; `NO_COLOR` or `TERM=dumb` disables type colours.
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
