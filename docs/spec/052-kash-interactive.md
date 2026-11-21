---
revision: 3
---

# Interactive Kash

## Purpose

Kash can run as a read-eval-print loop over one live execution session. This
refines `009-cli.md`, `017-kash.md`, and `048-kash-practical-scripting.md` for
interactive sessions only. Batch execution is unchanged.

Kash supports interactive and login-shell invocation, without POSIX shell
language compatibility. It lends the terminal to foreground
commands, keeps a session working directory and child environment, loads a
startup file, completes words, lets a Kash function draw the prompt, and
manages entries as jobs on a terminal (see Job control).

## Invocation

```text
kash                          # stdin is a terminal
kash -i [FILE.kash...] [-- ARG...]
kash --login                  # login profile, then REPL on a terminal
kash -lc 'echo hello'         # login profile, then a noninteractive command
kame do run -i [OPTIONS] [FILE.kash...] [-- ARG...]
```

`-i`/`--interactive` appends an interactive input after any listed sources.
Listed sources load and run first, in order, in the same session, followed by
the startup file (see Startup file). Bare `kash`
with no source starts the REPL when stdin is a terminal; with piped stdin it
still reads stdin as source. `--interactive` cannot be combined with `-` (stdin
as source) and requires the Kash language. Capability, timeout, directory, and
argument options apply as for any Kash session. `--timeout` applies per entry.

An executable invoked as `-kash` is a login shell. Standalone `kash` accepts
`-l`/`--login`; its `-l` no longer selects a language (use `--lang`).
`-lc`, `-ic`, `-il`, and `-li` combine the corresponding flags. The unified
runner accepts `--login` with `--lang kash`; its `-l` still selects a language.
Login does not itself force interactive mode. Ordinary `kash -c TEXT` loads
neither the login profile nor interactive initialization, including when used
by SSH for a remote command.

With a terminal on stdin, entries are edited with a line editor (see Line
editing) behind a prompt, by default `kash DIR> ` naming the session directory
(`~` for home). The editor renders on stdout when it is a terminal, otherwise
on stderr, so redirected output never receives prompts. Lines typed ahead while
a command runs are read as entries.
Otherwise lines are read from stdin without prompts, which is the testable
form of the same contract; terminal handoff then does not apply. Progress
dashboards and taskbar updates are disabled in interactive sessions.

## Entries

An entry is one or more lines submitted together:

- Text that ends inside an expression, such as an unclosed parenthesis or
  quoted word, continues on the next line until it closes.
- Text ending with a control header whose body is still empty (`def`, `if`,
  `for`, `match`, ...) continues until a blank line, since the body extent is
  not known earlier.
- A line consisting only of `&` ends the entry and runs it in the background
  (see Job control). It applies to an entry still being read: an open block
  or expression, or on a terminal any edited entry. A lone `&` line after a
  complete entry is ignored. This also holds for startup-file entries and
  input without a terminal, where the job is joined as owned work.
- End of input submits a pending partial entry before the session ends.

On a terminal the whole entry is one editable buffer: Enter submits it when
these rules find it complete and otherwise inserts a new line.

Each entry is parsed as Kash in the context of the live session and is a source
named `<kash:N>`, numbered from one per session. Diagnostics locate entry text
by that name and entry-relative positions, including traces through functions
defined by earlier entries.

Each entry has independent source storage and local byte spans. Appending an
entry must not concatenate or copy the previous session source. Shared
provenance travels with live syntax and retained diagnostics rather than being
registered in an ever-growing source table. Diagnostic source text is internal
presentation data and must not appear in JSON.

## Session semantics

All entries share one program, engine, top-level scope, capability policy,
and deferred-cleanup scope.

| Declaration | Interactive behavior |
| --- | --- |
| `let NAME = RHS` | Evaluates when submitted. A later `let` of the same name rebinds it for later entries. |
| `def NAME ...` | Registers the function. A later definition of the same name replaces it for later lookups. |
| `set NAME = RHS` | Assigns an existing top-level `let` binding for later entries. |
| `set env/NAME = RHS` | Writes the session child environment (see Session directory and environment). |
| `import PATH`, `from PATH import …` | Binds the module's exports for later entries. Lazy definitions of the module are evaluated at most once per session. |
| Rules, tasks, services, `generate`, `include` | Declarations; any such item reaching an entry is `FEATURE_UNSUP`. Preloaded files keep their ordinary contracts. |

A parse or registration failure rejects the whole entry before any effect: no
statement runs and the session is unchanged.

Statements of an entry run in order. A failure is reported and ends that entry
only; the session continues with the next entry. The entry status is the
failure's ordinary process status. Completed effects are never replayed.

After the last handle and job root release a completed statement, its engine
node and resumable operation progress are retired. Live published lazy
dependencies retain one shared session root per dependency instead of keeping
every completed statement alive. Pending jobs, deferred cleanup and finalizers
keep their existing ownership and must not be retired while still referenced.

Transient syntax belongs to its queued work until started, then to its engine
node until retirement. Escaping callables and deferred work own their syntax
independently. A finished or abandoned entry releases its queue slots without
moving other jobs' indices; empty trailing slots can be reused. Reusing a slot
must not reuse a still-live statement's engine identity. Ordinary command entries
are not retained as execution history.

Rebinding a top-level eager value or function replaces its binding slot, rather
than retaining shadowed slots and initializer syntax. A saved callable keeps the
implementation and parameters it borrowed even after its name is rebound.
Suspended calls keep their already-resolved callee and do not replay application
head effects. Call scopes retain the code needed by captured lazy bindings,
async work and deferred cleanup, including cleanup after cancellation. Replaced
code is released after its last such owner goes away. Lazy definitions remain
canonical engine resources and cannot be redeclared by shadowing them first.

Completed asynchronous graphs do not become session history. At a driver
boundary, release terminal tasks and their launcher roots once the host has
acknowledged physical reaping and all graph consumers have released them.
Saved process handles retain an independent completion value or diagnostic;
`await` remains repeat-safe after execution state has been retired. Unobserved
failures remain available to the owning job report or invocation join. Awaited,
joined and deliberately cancelled outcomes are not raised again by a later join.
Attribute async starts to jobs before dispatch, including starts reached through
an existing job root. Continue pumping pending host acknowledgements while the
editor is idle after cancellation.

A statement-leading expression displays its value with canonical value display
and a newline, unless the value is nil or a command completion record, whose
output was already delivered.

## Terminal handoff

On a terminal session, a foreground statement graph receives the terminal.
This is the interactive counterpart of `051-kash-session-authority.md`
handoff, enabled by the session rather than rule metadata, and uses the same
host primitive. Stage `tty: false` keeps a stage on pipes, and stage
`tty:`/`foreground:` need no metadata here.

- Eligible graphs are statement and test graphs whose input is not closed:
  captures, deferred cleanup, record-fed graphs, and record-loop bodies keep
  their ordinary pipes. A Kash `&` graph that would otherwise be eligible is
  a background graph: it attaches the terminal without taking its
  foreground (see Background jobs).
- Each standard stream a stage would otherwise give to the session (closed
  stdin, session stdout, session stderr) attaches to the controlling terminal.
  Redirections, pipes between stages, and channels are unchanged.
- Stages keep separate process groups under the existing containment
  contract. One stage becomes the terminal's foreground group: the last stage
  writing stdout to the terminal, otherwise the first stage with terminal
  stdin. Only that stage reads the terminal. The child takes the foreground
  before exec.
- After every stage is reaped, or on launch failure, the session restores its
  own foreground group and the terminal modes saved before launch.
- The session itself is never stopped: it handles `SIGTSTP` (see Job
  control) and ignores `SIGTTIN` and `SIGTTOU`. Children start with the
  default dispositions, so the terminal can stop them.
- Ctrl-C while a foreground command runs reaches that command only. An entry
  ending because a command died of `SIGINT` reports status 130 without a
  diagnostic.

Without a controlling terminal the graph keeps its pipes; there is no failure.

## Job control

On a terminal session, entries are jobs: they can be stopped, resumed, and
moved between the foreground and the background. A job is one whole entry,
its Kash evaluation together with every process graph it started, including
`&` graphs. Pausing uses the root suspension of `002-engine.md` and the
process suspension of `003-posix-process.md`, so a job resumes exactly where
it stopped and no completed effect is repeated.

### Foreground and stopping

The running entry is the **foreground job**. Ctrl-Z stops it wherever it is:

- While one of its graphs holds the terminal, the terminal delivers `SIGTSTP`
  to that graph's foreground group; the host stops the rest of the graph and
  reports the stop, and the session suspends the entry.
- Otherwise, while Kame evaluates or runs a capture, test, or cleanup graph,
  the session receives `SIGTSTP` itself and suspends the entry, which stops
  its running graphs.

The session then saves the terminal modes the job left, restores its own
foreground group and modes, reports `[N]+ stopped  TEXT`, and shows the
prompt. The last status becomes 128 plus `SIGTSTP`. The entry's own status is
decided only when it finishes. Ctrl-Z at the prompt has no effect.

### Starting in the background

A final line consisting only of `&` (blanks around it allowed) submits the
whole entry as a background job: it starts running and the prompt returns at
once. The line is not Kash and is not part of the job's text. It completes
the entry by itself, including a block entry, so no blank line follows it.
On a terminal, Ctrl-X Enter inserts a new line without submitting, so a
single-line entry can be followed by the `&` line.

```kash
for f in *.log
	gzip $f
&
```

Kash `&` inside an entry keeps its meaning: that graph runs asynchronously,
as a background graph of the entry's job. An entry whose evaluation finishes
while such graphs still run becomes a running background job, the prompt
returns, and the job finishes when its last graph does. A one-line entry
`cmd &` is therefore a background job, and `fg` gives `cmd` the terminal as
it would after Ctrl-Z. An evaluated job's status is that of its last failed
`&` graph, by the ordinary statement rules, otherwise its evaluation's.

### Background jobs

A background job keeps running while later entries run and while the prompt
is shown; the session keeps servicing it between keystrokes.

- Its graphs never take the terminal foreground. A graph eligible for
  handoff, and a background `&` graph, still attaches its standard streams to
  the terminal, so a read stops it with `SIGTTIN`, which stops the job and is
  reported; writes are allowed. Mode changes stop it with `SIGTTOU`. When
  `fg` resumes a job whose several graphs hold the terminal, the first to
  reclaim it takes the foreground and the others continue in the background.
- Output and value displays the session itself writes for a background job
  while an entry is being edited are printed above the edited entry, which
  is then redrawn.
- When a background job stops or finishes, the session reports
  `[N]M STATE  TEXT` (as `jobs` lists it) before the next
  first-line prompt. A finished job's
  status is observed by that report: it never becomes the last status and
  never fails the invocation join.
- Bindings and definitions a background job makes are visible to later
  entries when they are made; entries share one session scope.

### Job commands

Jobs are numbered from 1 in the order they become jobs; a number is reused
only after every job is gone. Live jobs are ranked: stopped jobs from the
most recently stopped, then running jobs from the most recently started. The
first is the **current** job (`+`), the second the **previous** job (`-`).
Five builtins (`055` §6) act on jobs when written as whole single-line
entries: `jobs`, `fg` and `bg` are interactive builtins, and `wait` and `kill`
are the general builtins applied to job operands. Operands are ordinary command
words. `%N` selects job `N`, `%+` or `%%` the current job, and `%-` the previous
job; no operand selects the current job.

| Entry | Effect |
| --- | --- |
| `jobs` | Lists jobs, oldest first, as `[N]M STATE  TEXT`, where `M` is `+`, `-`, or a blank. `STATE` is `running`, `stopped`, `stopped (tty input)`, `stopped (tty output)`, `done`, `exit S`, `signal NAME`, or `killed` for a job ended by `kill %N` or by the session; `TEXT` is the entry's first line, followed by `...` when it has more. Listing a finished job forgets it. Status 0. |
| `fg [%N]` | Writes the job's text, then makes it the foreground job: restores its saved terminal modes, gives the terminal foreground to its graph that held it, resumes it, and waits until it finishes, including its `&` graphs. The status is the job's status, or 128 plus `SIGTSTP` if it stops again. |
| `bg [%N]` | Resumes a stopped job in the background. Status 0. |
| `kill [-SIGNAL] %N...` | Without `-SIGNAL`, cancels each job as an interrupt cancels the foreground entry, reaping its processes; the job finishes with status 130. With `-SIGNAL` (a name such as `-HUP`, with or without `SIG`, or a number), delivers that signal to every process group the job currently owns, continuing a stopped job for a non-stop signal; the job's Kash evaluation then sees each graph's outcome by the ordinary rules. `-STOP` stops the job and `-CONT` is `bg`. Status 0 when every job was signalled. |
| `wait [%N...]` | Waits until each job has finished or stopped; without operands, until no job runs. The status is the last operand's: its status when finished, 128 plus its stopping signal when stopped; 0 without operands. A finished job's status is observed by `wait`: it is forgotten and not reported. An interrupt or Ctrl-Z ends the wait with 128 plus that signal and leaves the jobs as they are. |

`wait` and `kill` with async-handle operands keep their ordinary `017`/`049`
meaning. The external `kill` executable is reached as `x/kill`. An
unknown job, no job, `bg` of a running job, a malformed operand, or an
unknown signal fails with status 1 and a message, without other effect.

### Ending the session

A job's processes stay in their own process groups under the session's
containment contract; Kame does not detach them. At the end of the session
(end of input or `exit`), when stopped jobs exist, the first attempt reports
them and continues the session with status 1; a second consecutive attempt
ends it. Ending then cancels stopped jobs, resuming them so they observe
termination, and joins running jobs as owned work. An interrupt cancels only
the foreground job.

## Session directory and environment

The session is the process-like unit of every entry (`055` §5.3), so its
working directory and child environment persist across entries:

| Statement | Effect |
| --- | --- |
| `cd [DIR]` | Changes the session directory for later statements and entries. No operand selects `$HOME`; `-` the previous directory; `~` and `~/...` expand to `$HOME`; relative paths resolve from the session directory. A missing directory fails with status 1. |
| `set env/NAME = VALUE` | Sets `NAME` in the child environment that later lookups and processes see. `VALUE` is a scalar (string, number or path). |
| `set env/NAME = nil` | Removes a name added in this session, not a name inherited at invocation (`051`). |
| `set env/${NAME-EXPR} = VALUE` | Computes the name and applies the same write or removal rules. |
| `source PATH` | Runs native Kash and publishes environment and directory changes only on success (`055`). |

`cd` is the builtin of `055` §6 and is available in any statement position,
including functions, control blocks and command lists. Its operand is an
ordinary command word. `set env/…` is the ordinary block statement; its
right-hand side is an expression evaluated in value context. A write made by a
function called from an entry stays visible after the function returns.
Invalid operands or values fail the statement without changing the session.

The child environment starts as the invocation environment. It is the
environment store read by `env/NAME`, bare `ENV-NAME` references and `$NAME`
words; reads keep their capability rules. Kame's own process environment is
unchanged. When the session uses the default capability policy, its read and
write grants move with `cd`; explicit grants never move or broaden.

Interactive sessions and login-shell invocations resolve executable names and
`x/NAME` lookups against the current session `PATH`, not the original
invocation `PATH`. `set env/PATH = …`, `set env/PATH = nil`, and `cd` affect later lookups,
including aliases, captures, and pipelines. Relative PATH directories resolve
against the lookup's working directory; empty entries (including an empty or
unset PATH) search that directory. `--tool NAME=PATH` overrides still take
precedence, and run grants still authorize the selected executable.

Resolved absolute paths are tracked as tool dependencies. A PATH change does
not invalidate old tool nodes, cancel running jobs, or replay earlier effects.
Already evaluated lazy definitions keep their values. Stage-local `env: PATH`
overrides affect the child environment, not session lookup. Batch scripts and
recipes resolve against their own unit's `PATH` under the same rule, which is
the startup `PATH` until the unit writes it.

## Termination and interrupts

- End of input (Ctrl-D on an empty line) ends the session. Outstanding owned
  work is joined, cleanup and finalization run, and the exit status is the
  last entry's status.
- `exit [N]` in an entry ends the session (`055` §8.4) with status `N`
  (default 0), under the ordinary exit and finalization contract. `exit`
  inside a `$(…)` or `$?(…)` capture ends only that capture.
- An interrupt while an entry runs cancels that entry and reaps its processes,
  then the session continues with status 130. Work started by earlier entries
  is not cancelled. No diagnostic is reported; on a terminal the session
  ends the line on which `^C` was echoed. A second interrupt while the entry
  is settling escalates to the ordinary forced shutdown: every process group
  is killed and the session ends with status 130 without finalization.
- An interrupt at the prompt discards the whole entry being edited, closing
  any completion menu, and sets the session status to 130.
- `SIGTERM` and `SIGHUP` keep the ordinary session cancellation contract,
  including while waiting at the prompt: cancel/reap owned work and finalize
  before returning `128 + signal`.

## Failure reports

A failed entry reports its diagnostic, except on a terminal when the failure
is only that a process exited unsuccessfully or died of a signal
(`RECIPE_FAIL` with a process cause). The program has printed its own error,
so, like other shells, the session reports nothing and keeps the status. Kash
errors such as parse, name, and type errors are still reported in full.

`explain` renders the latest reported or withheld diagnostic of the session in
full: its source excerpt, cause, and frames. It keeps the latest one until a
later failure replaces it, so successful entries in between do not clear it.
Status 0, or 1 with `explain: no failure to explain` before any failure.
`explain` takes no operand.

## Shell options

`option [-l|--local|-g|--global] [NAME [VALUE]]` inspects or changes shell
settings under [054 Kash options](054-kash-options.md). Local (the default)
means this running shell; global saves user defaults in the XDG configuration
directory. Initial settings cover history recall scope, retention, and byte
budget. Settings and enumerated values are completed; invalid changes leave
the options unchanged.

## History

Terminal sessions provide scoped, secret-filtered history under
[053 Kash history](053-kash-history.md). That contract defines configuration,
local/global recall, 15-day default retention, a default 1 MiB budget per
repository/CWD scope, safe concurrent persistence, and legacy-history disposal.
Startup-file entries and prompt calls are not recorded.

## Startup file

### Login profile

Login sessions load `$KASH_PROFILE`, otherwise
`$XDG_CONFIG_HOME/kash/profile.kash`, otherwise
`~/.config/kash/profile.kash`, before listed sources or `-c` commands.
An empty `KASH_PROFILE` disables loading. Missing default files are skipped;
unreadable explicit files are reported. Profiles use the same entry, failure,
source-location, and builtin contracts as interactive initialization.
`exit` in a profile ends the invocation.

Interactive login sessions then load the interactive startup file after any
listed sources. Noninteractive login sessions do not load that file.
Kash never evaluates `/etc/profile`, `~/.profile`, or other shells' startup files.
Registering an installed executable in `/etc/shells` and selecting it with
`chsh` are administrator/user operations, not actions performed by Kash.

This does not add POSIX shell language compatibility.

### Interactive initialization

Every interactive session, with or without a terminal, loads a startup file
after listed sources and before the first entry. The file is `$KASH_INIT`
when that variable is set, otherwise `$XDG_CONFIG_HOME/kash/init.kash`,
otherwise `~/.config/kash/init.kash`, read from the invocation environment.
`KASH_INIT` set to the empty string disables the startup file.

- A missing default file is skipped silently. A file named by `KASH_INIT`
  that cannot be read is reported as `HOST_FAIL` with status 1, and the
  session continues.
- The text is divided into entries exactly as typed input is, including
  continuation rules, builtins and aliases, and each entry runs under the
  entry contracts above. A failing entry ends only that entry; later entries
  of the file still run. `exit` ends the session.
- Diagnostics name the file path and locate positions in the file, not in the
  entry. Startup entries do not consume `<kash:N>` numbers.

Both default paths use `$XDG_CONFIG_HOME` when it is set to a nonempty value,
including a relative one, and `$HOME/.config` otherwise. If `$HOME` cannot be
resolved either, no default path exists and no file is loaded. Only these two
files are read during startup; nothing else in their directory is loaded at
startup.

## Command aliases

`alias` and `unalias` are interactive builtins (`055` §6), available as whole
entries in interactive input, interactive initialization, and login profiles:

```kash
alias ls = x/eza ?? nil
alias ll = ["ls", "-lah"]
ll ./src
ll ./src | head -n 10
alias ll
alias
unalias ll
```

`alias NAME = EXPR` evaluates EXPR once in the current session. A string
becomes a one-element argv prefix, including a resolved tool string such as
`x/eza`; a list supplies the prefix as separate strings. The prefix must be
nonempty, its first string must be nonempty, and no string may contain NUL.
A `nil` result removes the alias, succeeding even if it was already absent.
Names contain ASCII letters, digits, `_`, `-`, or `.`, cannot start with `-`,
and cannot replace reserved keywords or builtins. Failed evaluation or validation
leaves aliases unchanged. Ordinary value bindings do not define command aliases.

`alias` lists declarations sorted by name; `alias NAME` shows one.
`unalias NAME...` removes named aliases, validating all names before mutation.
Unknown names report an error. Aliases appear in command completion and are
recognized by highlighting.

A literal command head matching an alias expands once into that prefix,
followed by the caller's typed arguments. After evaluation, prefix strings are
never reparsed, split, interpolated, or globbed. Pipelines, captures, command
lists, redirections, and capability checks retain their ordinary behavior.
Expansion does not recurse: `alias ls = ["ls", "-lah"]` invokes `ls` once. Computed
command heads (`$command`, `$...argv`) and explicit paths bypass alias lookup.
Aliases take precedence over executable lookup, not reserved syntax.

An alias whose prefix starts with `cd` runs the `cd` builtin. Alias
declarations and removals keep their whole-line restriction: they are not
usable in pipelines, functions, or compound entries. The Kash parser
represents them as `session-command` items with a `text` header and an
optional typed RHS `expression`. Parsing and formatting never execute them.
Like every interactive-only builtin, they are `PARSE_ERR` in a script or rule
body (`055` §6).

The formatter preserves typed string arguments, canonicalizes declaration
spacing and multiline lists, and formats surrounding Kash normally:

```sh
kame do fmt --lang kash -i ~/.config/kash/init.kash
kame do fmt --lang kash -i ~/.config/kash/profile.kash
```

Formatting is idempotent; malformed declarations fail without overwriting
the source. Alias declarations/removals are whole top-level entries, not
function bodies, control-block statements, pipelines, or command lists.

## Prompt

Before each first-line prompt of a terminal session, when the session defines
a function named `kash-prompt`, the REPL calls it with one record argument:

| Field | Value |
| --- | --- |
| `status` | Status of the last entry, as an integer. |
| `dir` | Absolute session directory. |
| `name` | Directory display of the default prompt (`~`, `/`, or the base name). |
| `jobs` | Number of jobs, running or stopped, as an integer. |

A string result is the prompt, verbatim; terminal style sequences are allowed
and do not count toward the cursor column. Any other result uses the default
prompt. A failing call is reported as a diagnostic named `<kash-prompt>` and
also uses the default prompt. The call is not an entry: it consumes no entry
number and leaves the session status unchanged. Continuation prompts and
sessions without a terminal never call it.

```kash
def kash-prompt info
	let mark = if info.status == 0 then "" else " !"
	return "${info.name}${mark} $ "
```

A function named `kash-rprompt` is called the same way, after `kash-prompt`,
with the same record. Its string result is the right prompt: drawn flush
with the right edge of the terminal on the entry's last line, and omitted
while that line leaves no room for it. Any other result, or a failure,
reported as a diagnostic named `<kash-rprompt>`, shows no right prompt.

```kash
def kash-rprompt info
	return if info.jobs == 0 then "" else "[${info.jobs} jobs]"
```

## Line editing

The terminal editor follows GNU Readline conventions: emacs bindings by
default, vi bindings when selected, incremental history search, undo, and
editing across the lines of an entry. It reads the user's inputrc
(`$INPUTRC`, otherwise `~/.inputrc`, otherwise `/etc/inputrc`) with the
application name `kash`, so `$if kash` sections apply. Tab is bound to
completion as specified below, except that Tab with only blanks before the
cursor on its line inserts a tab, as indented block bodies need.

Except on `TERM=dumb`, the editor enables bracketed paste: pasted text is
inserted at once, line breaks included, and is submitted only by a following
Enter.

Before each prompt the editor turns off terminal input reports it does not
read: mouse tracking, focus events, and kitty keyboard flags. A full-screen
program that exits without restoring them, for example after a crash, leaves
no mouse or focus reports echoed or inserted into the line.

## Completion

On a terminal, Tab completes the word before the cursor. The word extends
back to the nearest unescaped blank or `(` `)` `[` `]` `|` `;` `&` `<` `>`
`=` `"` `'`, within the current line. Candidates depend on its position, first
match wins; each group has a heading and candidates carry descriptions:

| Position | Group | Candidates and descriptions |
| --- | --- | --- |
| Word starting with `$` | `bindings` | `$NAME` for top-level session bindings that are not functions, described as `let`, `lazy`, or `value`. |
| Directly after `(` | `functions` | Session functions, described by their parameters, and library operations, described by their arity. |
| Command position, word without `/` | `builtins`, `keywords`, `commands` | Builtins and aliases, Kash statement keywords, and executables found on the session child environment's `PATH`, described by their directory. |
| First operand of `cd` | `files` | Directories. |
| Argument of a literal command | Command-specific | [Options and arguments](058-kash-completion.md), with path fallback as permitted. |
| Anything else | `files` | Paths. |

Command position is the start of a line, or follows `|`, `;`, `&`, `$(`, or
the keywords `if`, `elif`, and `while`.

Paths resolve from the session directory; a leading `~/` resolves from `$HOME`
and is kept as typed. Names starting with `.` are candidates only when the
typed name starts with `.`. Inserted path text escapes blanks and Kash
metacharacters with a backslash, and typed backslash escapes are honored when
matching. Directories are inserted with a trailing `/` and no space.

- One candidate replaces the word, followed by a space unless it is a
  directory.
- Several candidates extend the word to their longest common prefix and are
  listed below the entry in a menu, grouped under their headings; Tab again
  and the arrow keys select a candidate, which is inserted as typed.
- No candidate leaves the entry unchanged.

A horizontal separator above the completion groups follows terminal width and
disappears when the menu closes. It is dim on color terminals and uses ASCII
on `TERM=dumb`. The menu marks directories with `/` (blue), executables with
`*` (green), and symlinks with `@` (cyan, with a further `/` for directory
targets). FIFOs use `|`, sockets `=`, and devices `#`, with yellow styling
and a type description. Regular files keep their plain names. These marks and
styles are display-only: inserted text stays unchanged. `NO_COLOR` and
`TERM=dumb` disable type colors, not the marks. Selection uses reverse video
by default; an explicit inputrc selection style is preserved.

Built-in completion does not evaluate session definitions or run commands.
[Command-specific completion](058-kash-completion.md) adds specs discovered
from man pages and completion files, cached by a Kame build, and opt-in live
protocols.

Explicit file, command and history pickers use the backends defined by
[057 Kash UI](057-kash-ui.md). Picking edits the current entry without
submitting it. Tab completion and incremental history search are unchanged.

## Highlighting

On a color terminal the editor highlights the entry as it is typed, by Kash
lexical class: command words, keywords, defined names, options, quoted text,
references and captures, operators and redirections, comments, and, inside
Kame expressions, call heads, constants, and numbers. Unclosed quotes and
expressions are highlighted to the end of the entry. Highlighting never
changes the text.

A literal command word is marked as unknown when it is not a builtin, alias, or session function,
does not exist as a path when it contains `/`, and names no executable file in
a directory on the session `PATH`. Words built from substitutions are not
judged. The directory, `PATH`, and aliases are those in effect when the entry's
prompt is shown. `NO_COLOR` set to a non-empty value, or `TERM=dumb`, disables
highlighting.

Highlighting runs on every keystroke and must not wait on running jobs or rescan
`PATH` directories that have not changed since an earlier entry.

## Non-goals

- `disown`, `$!`, and job specifications other than `%N`, `%+`, `%%`, and
  `%-`. Job commands inside other statements. Without a terminal, only the
  `&` line applies; there is no stopping and no job command, and `&` graphs
  keep their pipes.
- Multi-line prompts.
- Semantic highlighting beyond command words, such as validating references
  or expressions.
- Redefining lazy definitions or rules.

## Conformance

| Contract | Required executable checks |
| --- | --- |
| Invocation | `-i` with and without preloaded sources; bare `kash` on a terminal; conflict with stdin source. |
| Entries | Open-expression continuation, block continuation ended by a blank line, partial entry at end of input; under a pseudo-terminal, a block entered with Tab indentation runs as one entry. |
| Session | `let` and `def` rebinding, `set` of an existing binding, a declaration entry rejected without effects, failure ends only its entry, value display, diagnostics named `<kash:N>`. |
| Termination | `exit` status, end-of-input status, an interrupt received by the session during an entry reaps it, reports 130 without output, and continues the session; a second interrupt while settling ends the session with 130 and kills its processes; interrupt at the prompt clears the line. |
| Terminal | Under a pseudo-terminal: foreground commands see a terminal on every stream and own the foreground group; captures keep pipes; Ctrl-C ends only the command without a diagnostic; typed-ahead entries run; the session regains the terminal with unchanged modes. |
| Session state | `cd` with relative, `-`, and home forms, including `cd` inside a function persisting after it returns; redirections after `cd` honor moved default grants and explicit grants deny them; `set env/NAME` visible to later entries, processes and `PATH` lookup; `set env/NAME = nil`; writes inside a capture do not escape; non-scalar values rejected; interactive-only builtins rejected in a preloaded script. |
| History | Eligible entries persist and round-trip across sessions under the scope, secret filtering, retention, concurrency, and migration requirements of `053-kash-history.md`. |
| Startup file | Definitions, aliases, `cd` and `set env/NAME` apply before the first entry; diagnostics locate file positions; failures do not stop later startup entries; empty `KASH_INIT` disables loading; an unreadable explicit file is reported. |
| Job control | Under a pseudo-terminal: Ctrl-Z stops a foreground command and every stage of its pipeline, and also stops an entry during a capture, an `if` test, and pure evaluation; the session restores its modes and reports the job; `fg` resumes the entry where it stopped, runs its remaining statements once, restores the job's modes, and returns its status; `bg` continues it while later entries and the prompt run, and its completion is reported before the next prompt; a background read of the terminal stops the job; a final `&` line starts a block entry as a background job and returns the prompt at once, and a one-line `cmd &` entry is a running job until the command ends; a `cmd &` graph reading the terminal stops its job, `fg` gives it the terminal, and `fg` of a running `&` job waits for it to finish; `wait %N` returns the job's `&` graph status and forgets it, and `wait` without operands returns 0; `jobs` marks the current and previous jobs, `%+` and `%-` select them, and a cancelled job is listed as `killed`; a session ending on `--timeout` cancels its background jobs; `kill %N` cancels, `kill -SIG %N` delivers to every group and continues a stopped job, and `x/kill PID` runs the external command; the first end of input with a stopped job reports it and continues, the second ends the session and reaps it; job-command operand errors. |
| Prompt | Under a pseudo-terminal, a `kash-prompt` string replaces the prompt and sees the last status; a failing or non-string prompt falls back to the default; a `kash-rprompt` string is drawn as the right prompt and sees the last status, and a failing one is reported. ANSI colours and wide Unicode use display columns; the right prompt hides before overlapping input, returns after deletion, and follows resizing; wrapped cursor editing and completion preserve submitted argv. |
| Completion | Word extraction and escapes; groups and descriptions for `$` bindings, functions after `(`, command position with builtins, aliases, keywords, and `PATH` executables, `cd` directories, and paths; hidden-file rule; under a pseudo-terminal, a unique completion and a grouped menu. |
| Highlighting | Lexical classes of complete and unclosed input; highlighting preserves text; unknown literal command words, existing paths, builtins, and aliases; `PATH` listings follow installs, removals, and mode changes between entries; under a pseudo-terminal, an unknown command is marked, and a `PATH` change, an alias, and a `cd` in the previous entry are reflected. |
| Line editing | Under a pseudo-terminal, a bracketed multi-line paste is not submitted before Enter and then runs as one entry; a keystroke latency probe reports kash and, when installed, bash on the same terminal without a timing threshold. |
