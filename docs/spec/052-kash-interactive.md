# Interactive Kash

## Purpose

Kash can run as a read-eval-print loop over one live execution session. This
refines `009-cli.md`, `017-kash.md`, and `048-kash-practical-scripting.md` for
interactive sessions only. Batch execution is unchanged.

This is a REPL, not a login shell. It lends the terminal to foreground
commands and keeps a session working directory and child environment, but it
does not manage jobs or complete words; see Non-goals.

## Invocation

```text
kash                          # stdin is a terminal
kash -i [FILE.kash...] [-- ARG...]
kame do run -i [OPTIONS] [FILE.kash...] [-- ARG...]
```

`-i`/`--interactive` appends an interactive input after any listed sources.
Listed sources load and run first, in order, in the same session. Bare `kash`
with no source starts the REPL when stdin is a terminal; with piped stdin it
still reads stdin as source. `--interactive` cannot be combined with `-` (stdin
as source) and requires the Kash language. Capability, timeout, directory, and
argument options apply as for any Kash session. `--timeout` applies per entry.

With a terminal on stdin, entries are edited with a line editor (history,
cursor movement) behind a `kash DIR> ` prompt naming the session directory
(`~` for home). Lines typed ahead while a command runs are read as entries.
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
- End of input submits a pending partial entry before the session ends.

Each entry is parsed as Kash in the context of the live session and is a source
named `<kash:N>`, numbered from one per session. Diagnostics locate entry text
by that name and entry-relative positions, including traces through functions
defined by earlier entries.

## Session semantics

All entries share one program, engine, top-level scope, capability policy,
and deferred-cleanup scope.

| Declaration | Interactive behavior |
| --- | --- |
| `let NAME = RHS` | Evaluates when submitted. A later `let` of the same name rebinds it for later entries. |
| `def NAME ...` | Registers the function. A later definition of the same name replaces it for later lookups. |
| `NAME = RHS` (lazy) | Registered once. Redefinition is `DEF_INVALID` with a `let` tip; an evaluated lazy value is never replayed. `?=` against an existing name is ignored. |
| `NAME += RHS` | `DEF_INVALID`: contributions would mutate an aggregate that may already be evaluated. |
| Rules, includes, composition | `FEATURE_UNSUP` within entries. Preloaded files keep their ordinary contracts. |

A parse or registration failure rejects the whole entry before any effect: no
statement runs and the session is unchanged.

Statements of an entry run in order. A failure is reported and ends that entry
only; the session continues with the next entry. The entry status is the
failure's ordinary process status. Completed effects are never replayed.

A statement-leading expression displays its value with canonical value display
and a newline, unless the value is nil or a command completion record, whose
output was already delivered.

## Terminal handoff

On a terminal session, a foreground statement graph receives the terminal.
This is the interactive counterpart of `051-kash-session-authority.md`
handoff, enabled by the session rather than rule metadata, and uses the same
host primitive. Stage `:tty :false` keeps a stage on pipes, and stage
`:tty`/`:foreground` need no metadata here.

- Eligible graphs are statement and test graphs whose input is not closed:
  captures, background (`&`) graphs, deferred cleanup, record-fed graphs, and
  record-loop bodies keep their ordinary pipes.
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
- The session ignores `SIGTSTP`, `SIGTTIN`, and `SIGTTOU`, and children
  inherit those dispositions, so terminal access never stops the session.
- Ctrl-C while a foreground command runs reaches that command only. An entry
  ending because a command died of `SIGINT` reports status 130 without a
  diagnostic.

Without a controlling terminal the graph keeps its pipes; there is no failure.

## Session directory and environment

Three whole single-line entries are session commands, not Kash statements:

| Entry | Effect |
| --- | --- |
| `cd [DIR]` | Changes the session directory for later entries. No operand selects `$HOME`; `-` the previous directory; `~` and `~/...` expand to `$HOME`; relative paths resolve from the session directory. A missing directory fails with status 1. |
| `export NAME=VALUE...` | Sets variables of the child environment that later processes inherit. Names use letters, digits and `_`, not starting with a digit. |
| `unset NAME...` | Removes variables from that child environment. |

Operands split on blanks. Single quotes are literal. Unquoted and double-quoted
text expands `$NAME` and `${NAME}` from the session child environment; a
missing name expands to empty. Malformed operands fail with status 2 without
changing the session.

The child environment starts as the invocation environment. Kame's own process
environment and the Kash `env` namespace are unchanged; reads keep their
capability rules. When the session uses the default capability policy, its
read and write grants move with `cd`; explicit grants never move or broaden.

## Termination and interrupts

- End of input (Ctrl-D on an empty line) ends the session. Outstanding owned
  work is joined, cleanup and finalization run, and the exit status is the
  last entry's status.
- `exit` ends the session with the ordinary exit and finalization contract.
- An interrupt while an entry runs cancels that entry and reaps its processes,
  then the session continues with status 130. Work started by earlier entries
  is not cancelled. A second interrupt while the entry is settling escalates to
  the ordinary forced shutdown.
- An interrupt at the prompt discards the line or partial entry.
- `SIGTERM` keeps the ordinary session cancellation contract.

## History

Terminal sessions keep up to 1000 recent lines. They append to
`$KASH_HISTORY`, otherwise `$XDG_STATE_HOME/kash/history`, otherwise
`~/.local/state/kash/history`. A history file that cannot be opened disables
persistence, not the session.

## Non-goals

- Job control: stopping, resuming, and listing jobs, and moving background
  graphs to the terminal.
- Completion, prompt customization, and startup files.
- Expressions as session-command operands, and Kash `export`/`unset`
  statements (`051`), which remain scoped to recipes.
- Redefining lazy definitions or rules.

## Conformance

| Contract | Required executable checks |
| --- | --- |
| Invocation | `-i` with and without preloaded sources; bare `kash` on a terminal; conflict with stdin source. |
| Entries | Open-expression continuation, block continuation ended by a blank line, partial entry at end of input. |
| Session | `let` and `def` rebinding, lazy redefinition rejected without effects, failure ends only its entry, value display, diagnostics named `<kash:N>`. |
| Termination | `exit` status, end-of-input status, interrupt of a running entry continues the session, interrupt at the prompt clears the line. |
| Terminal | Under a pseudo-terminal: foreground commands see a terminal on every stream and own the foreground group; captures keep pipes; Ctrl-C ends only the command without a diagnostic; typed-ahead entries run; the session regains the terminal with unchanged modes. |
| Session state | `cd` with relative, `-`, and home forms; redirections after `cd` honor moved default grants and explicit grants deny them; `export` expansion and single-quote literals; `unset`; malformed operands. |
