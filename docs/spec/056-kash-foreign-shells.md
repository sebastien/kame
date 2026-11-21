---
revision: 1
---

# Kash foreign-shell integration

Kash can source an unmodified script in another shell, invoke its functions,
and share exported environment changes without sharing its parser or prompt.
Bash is the first adapter; the execution and synchronization rules below do
not depend on Bash syntax.

```kash
source bash ./setup.sh as tools
tools/configure "two words"
```

## Source and invocation

`source SHELL PATH as NAME [unset ENV-NAME …]` is a command-position builtin
in direct Kash scripts and interactive sessions, not an `import` or an
expression. The two-word `source PATH` form runs native Kash (`055`).
`SHELL`, `as`, `NAME`, and the optional `unset` clause are literal
words, checked when the action is reached. `NAME` is an unreserved Kash
namespace name. `PATH` is one word that must yield a nonempty local path.
Relative paths resolve against Kash's current directory; `~/` uses the
current child `HOME`. Loading requires read authority for that path and permission
to run the shell as a bare executable. This is trusted code execution, not
sandboxing. The shell is looked up through Kash's tool overrides and effective
`PATH`. Unsupported adapters or hosts fail before launch.

The adapter starts one noninteractive shell per namespace with Kash's child
environment and directory. It sources the file once, in the worker itself,
without startup files. A successful load publishes the environment changes
and the functions defined by the script together. A live namespace cannot
be sourced again. A worker whose failure Kash has observed can be replaced
by sourcing again. The owning session closes the workers when it ends.

Literal `NAME/FUNCTION` command heads select the published functions after
lexical lookup. Arguments are Kash argv values, never shell source. Calls
run in the same worker as the initial source and retain its private shell
state. The namespace is not a Kash value or module. A missing function
fails without an executable fallback.

The initial implementation accepts sequential foreground commands (including
the `?` exit-status suffix). It does not accept stage settings, redirections,
pipelines, captures, recovery branches, background jobs, deferred commands
or build recipes. Standard input is closed; stdout and stderr stream
separately from the worker protocol. There is no terminal handoff. A sourced
script must not replace the worker, close its control descriptors, or leave
background processes writing to the worker's streams.

## Synchronization

Before each call the adapter sets the worker's directory to Kash's current
directory and applies changes made to Kash's exported environment since the
previous boundary. It leaves unrelated private state alone. Directory changes
within the worker do not move Kash. Adapter bookkeeping (for example Bash's
`SHLVL`, `PWD` and `OLDPWD`) is not synchronized.

The adapter observes exports immediately before and after each source or
call. Kash applies only the resulting delta to the owning environment store.
Empty strings and removals are distinct. The next command lookup uses the new
`PATH`, and environment reads still require their ordinary grants. The delta
does not mutate Kame's host environment or another unit's store. Future child
units inherit the updated store through the normal Kash rules.

A namespace may remove a name it introduced while absent, provided its last
published value is still current. To remove a preexisting or subsequently
changed name, authorize it explicitly with the optional `unset` clause. This
clause only grants removals from that namespace; it does not change the rules
for `set env/NAME = nil`. The entire delta is validated before any of it is
published.

An initial source that returns nonzero publishes nothing. An ordinary function
return, including nonzero, publishes a valid delta and independently reports
its command status. A failed exchange, interruption, timeout, or invalid
delta publishes nothing and discards the affected worker. This does not roll
back output or filesystem effects. Dry-run does not start a worker.

## Adapters and verification

Kash owns naming, environment policy, scheduling, and worker lifetime. An
adapter implements native source and function invocation, snapshots of exported
scalars, and a private, bounded exchange. The initial implementation supports
Bash on the native POSIX host; arbitrary executables and other shells do not
automatically implement this protocol. There is no public adapter plugin ABI.
The Bash adapter ignores startup hooks and imported Bash functions. It rejects
an incoming environment edit that would require Bash to interpret the value
as an expression, coerce it, follow a name reference, or modify a readonly
variable. Return status is collected in a conditional context, so Bash's
implicit `errexit` does not abort the worker for an ordinary failed call.

Verify with shell fixtures: source once, preserve private state and argv
boundaries, transfer multiline and empty values, synchronize additions and
removals without flattening unrelated variables, use the current directory,
update executable lookup, and distinguish a normal nonzero return from an
interrupted or rejected exchange. Foreign prompt hooks, aliases and completion
are not imported.
