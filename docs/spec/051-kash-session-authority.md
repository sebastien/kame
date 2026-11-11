# Kash session authority

## Purpose

A Kash recipe may receive a named, opt-in authority over the child environment
and the controlling terminal. This specification refines `003-posix-process.md`,
`004-language.md`, `006-runtime.md`, `009-cli.md`, `011-diagnostics.md`,
`017-kash.md`, and `048-kash-practical-scripting.md`. Its clauses take
precedence for the behavior specified here. Elsewhere those contracts are
unchanged.

This is not Bash compatibility. It does not mutate the host environment, replace
the Kame process, grant ambient environment access, or define job suspension,
terminal-attribute restoration, word splitting, or globbing. Bash export quoting
and vendor installer protocols are not part of this contract.

The two clauses are independent. An implementation may provide environment
authority without terminal handoff, or the reverse. A host that cannot provide
one clause rejects that clause before launch; it does not weaken the other.

## Vocabulary

- An **authority policy** is a record of environment names a recipe may observe
  or remove. It does not contain environment values.
- The **child overlay** is the environment a scope's children inherit. It starts
  as the invocation child environment after existing rule `env` assignments. It
  is not the host process environment and not Kame's `env` namespace.
- A **foreground handoff** attaches one foreground process graph to the
  controlling terminal and, when requested, moves that terminal's foreground
  process group for the duration of the wait.
- The **invocation environment** is the child environment the runtime already
  constructed for the recipe attempt, before any overlay statement in that
  attempt. Rule `env` metadata has already been applied. Host variables that
  were not copied into the request are not part of it.

## Declaration

Rule metadata gains three keys, valid for every rule kind. They do not replace
`shell` or `env`, and they are not service or executor keys.

```kame
ENV_PROJECT = [read: ["PATH" "PYTHONPATH" "NODE_PATH"]]
ENV_AWS_SSO = [unset: ["AWS_ACCESS_KEY_ID" "AWS_SECRET_ACCESS_KEY" "AWS_SESSION_TOKEN" "AWS_SECURITY_TOKEN"]]

shell : ; [authority: ENV_PROJECT tty: :true foreground: :true]
aws-shell {account=auto} : ; [authority: ENV_AWS_SSO tty: :true foreground: :true]
```

`authority` evaluates to a record whose only keys are `read` and `unset`. Each
present key is a list of strings matching `[A-Za-z_][A-Za-z0-9_]*`. Absent keys
are empty lists. Empty lists grant nothing. Duplicate names in one list, names
of any other type, NUL, and unknown keys are `EXPR_INVALID`. A name may appear
in both lists. The record is evaluated with other header metadata, under
planning policy, before prerequisite recipes execute. The expression must not
read environment values, launch processes, or write files. A reference to a
policy definition is an ordinary planning demand.

`tty` and `foreground` are booleans. Only `:true` and `:false` are valid;
absence is `:false`. `foreground: :true` without `tty: :true` in the same
metadata record is `EXPR_INVALID` before prerequisites run. These flags
authorize a handoff. They do not grant environment reads or removal.

Authority is recipe-scoped and additive. It does not pass through `--allow-env`
and does not replace the session capability set. Read, write, and run grants
already held by a build recipe remain. A direct `do run` session has no rule
metadata: this specification grants it neither unset nor a terminal.
`--allow-env` remains a named read grant only, with its existing replacement
policy, and does not authorize `unset` or a handoff.

Child processes are not sandboxed. Authority constrains Kash observation and
overlay edits. A launched executable still receives the effective child overlay
and may read any name that overlay still contains.

## Environment authority

`export` and `unset` are statement keywords at statement start, like `let`.
A quoted, escaped, or path-qualified `export` or `unset` is a command, not a
statement.

```kash
export PATH = (join ":" [(str run "/bin") (env "PATH")])
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
```

Each `export` has one name, `=`, and one value. The value uses
stage-environment value grammar: a string without NUL, with no implicit
conversion, splitting, or globbing. `unset` takes one or more names and no
values. Validate every name before changing the overlay; one denied or invalid
name changes nothing.

| Operation | Authority | Effect |
| --- | --- | --- |
| `export NAME = VALUE` | none for a computed value | Set `NAME` in the current scope overlay. |
| `(env NAME)` or `$env.NAME` | `read` contains `NAME`, or an existing session read grant | Observe the effective value. |
| `unset NAME` | `unset` contains `NAME` | Remove `NAME` from the current scope overlay. |

An `export` value expression that reads the environment uses the read rule
above. Export does not itself require the name to be listed. Replacing `PATH`
with a computed string is grant-free; prepending to the current `PATH` is a
read. Unset does not observe the removed value. Empty string remains a present
value, not removal. A missing name and a removed name both read as nil when the
read is authorized. An unauthorized read or unset is `CAP_DENIED` before the
overlay changes and before any child launches. Denial is not recoverable by
`?` or `??`.

Stage `:env` and `:KEY` overrides are unchanged. They apply to one stage, do
not modify the scope overlay, and do not authorize a later read. `with :env`
merges its record over the overlay already in force for that block only.

Overlay statements belong to the execution scope that reaches them:

- The recipe body and each `with` block or function call starts from the
  enclosing effective overlay. Nested mutations end when that scope ends,
  including by failure, return, exit, `break`, and `continue`.
- A function does not mutate its caller's overlay. Children it launches see the
  function overlay.
- Cleanup sees the overlay of the scope that registered the deferred action.
- Dry-run does not evaluate export values, read environment values, or edit an
  overlay. Planning evaluates only the authority name lists.

`(env NAME)` observes the effective overlay: an assignment in an enclosing
scope, otherwise the invocation environment, otherwise nil after an enclosing
unset. Export and unset do not write the host environment, the parent scope, or
the `env` namespace seen by another recipe attempt. Kame's process environment
is unchanged.

## Lookup

Outside an overlay that has changed `PATH`, bare-name resolution remains the
startup `PATH` and `--tool` overrides. Stage `:PATH` still affects only that
child, not resolution of that stage's executable.

Inside a scope whose effective `PATH` differs from the invocation `PATH`,
bare-name resolution for commands reached in that scope uses the effective
`PATH`. `--tool NAME=PATH` still wins. Resolution happens before stage
environment overrides are applied to the child, so a stage `:PATH` does not
select the executable. Track the resolved executable by the existing tool
observation. An overlay does not make a scoped run grant accept bare names;
scoped run grants continue to require explicit executable paths.

## Foreground terminal handoff

Terminal handoff is opt-in per recipe. The default spawn contract remains
separate stdout and stderr pipes, a new process group, and no controlling
terminal. Interactive data stdin is not a handoff.

When the metadata authorizes `tty` and no stage in the recipe sets `:tty`, each
sequential foreground process graph of that attempt is a handoff. When any
stage sets `:tty`, metadata only authorizes: a stage attaches if and only if it
sets `:tty :true`. `:tty :false` opts that stage out. The same rule applies to
`:foreground`. A stage that requests either facility without metadata
authorization is `CAP_DENIED` before launch. A stage `:foreground :true` whose
effective tty is false is `EXPR_INVALID` before launch.

A handoff applies to the unique foreground graph of one statement. Pipeline
stages keep their pipes except the stage ends that would have used the recipe's
standard streams; those ends attach to the terminal instead. Captures,
background graphs, cleanup, and asynchronous work do not take the terminal.
Two overlapping graphs that both require the terminal fail before launch with
`EXPR_INVALID`. No child is left running.

On a capable host the runtime:

1. Opens the controlling terminal. Failure, including no controlling terminal,
   is `HOST_FAIL` before spawn. There is no pipe fallback and no successful
   empty status.
2. Spawns the child in a new process group with the terminal attached to the
   selected standard streams. The parent remains the waiting process. This
   specification does not replace the Kame process.
3. If foreground is effective, makes that process group the terminal foreground
   before the child reads the terminal. The parent ignores `SIGTTOU` only for
   that call, then restores its previous disposition.
4. Waits, cancels, and reaps under the existing process-group contract.
5. Restores the previous foreground group after the direct child is reaped, and
   on spawn failure if the group was changed. Restore runs on success, child
   failure, timeout, and cancellation.

Restore failure is `HOST_FAIL` and must not report success. It does not hide an
already observed child failure. Terminal attributes are not saved or restored.
Stopped jobs, `SIGTSTP`, and a child that creates a new session have no
additional contract; session escape remains outside process-group containment.

A host without the terminal primitive, including a Windows or WebAssembly host
that cannot open a controlling terminal and move its foreground group, reports
`FEATURE_UNSUP` before spawn. Environment authority does not depend on that
primitive and remains available.

## Freshness

Authority name lists are source. Evaluating them records no environment value.

A read that resolves to the invocation environment records that name and value
under the existing named-read observation. A read that resolves to an assignment
this attempt wrote records no external environment input. A read that resolves
to a name this attempt removed records absence and must not persist the
invocation value. Digests never store plaintext.

Process-executing rules fingerprint the effective child environment after
overlay edits and stage overrides, as they already fingerprint the request
environment after rule `env`. Removed names are absent. The fingerprint is a
digest. An overlay `PATH` that changes bare-name resolution records the resolved
executable through the existing tool observation.

A terminal handoff is an execution effect, not a cache input. It does not make
an otherwise pure rule process-executing. Requesting it on a rule that launches
no process is a no-op, not a failure.

## Diagnostics

Use existing codes. Diagnostics name the facility and, for environment denial,
the name. They omit values, overlay contents, and terminal attributes.

| Failure | Code | When |
| --- | --- | --- |
| Unauthorized read, unset, or handoff | `CAP_DENIED` | Before overlay edits or spawn. Not recoverable. |
| Malformed policy, boolean, or conflicting handoff | `EXPR_INVALID` | Planning for metadata; before launch for a stage. |
| Capable host, no controlling terminal, or restore failure | `HOST_FAIL` | Before spawn, or after reap if restore fails. |
| Host has no handoff primitive | `FEATURE_UNSUP` | Before spawn. |

A denied read tip identifies rule `authority` `read`, or `--allow-env=NAME` for
a direct session. Child `env` assignments, `export`, and definition
configuration do not grant that read. A tip must not imply that an explicit CLI
grant preserves the default capability set.

## Non-goals

The following are outside this contract:

- Mutation of the host process environment, cwd, or `env` namespace across
  recipe attempts.
- Replacement of the Kame process, or transferring Kame's own process group
  away permanently.
- A session-wide or build-wide environment grant.
- A closed child environment that drops every unlisted inherited name.
- Bash `%q`, indirect expansion, or emission of sourceable shell text.
- Implicit word splitting, globbing, and alias expansion.
- Saving or restoring terminal attributes, and managing suspended jobs.
- Sandboxing an executable's own environment reads.
- Redefining metadata `env`, or spelling records with braces.

## Conformance

Acceptance requires package and compiled CLI/host checks under
[013 Conformance tests](013-tests.md). Native and Node/WASM hosts share the
environment cases. Terminal cases run on a host with a controlling terminal;
hosts without the primitive assert `FEATURE_UNSUP` before spawn rather than a
weaker handoff.

| Contract | Required executable checks |
| --- | --- |
| Policy shape | Missing keys, empty lists, duplicate names, invalid names, unknown keys, non-booleans, foreground without tty, planning-time environment read denied, prerequisites not started after a malformed policy. |
| Grants | Additive to build read/write/run; no effect on another rule; direct session unset and tty denied; `--allow-env` does not unset or attach a terminal and still replaces the default set when explicit. |
| Overlay | Computed export without a read grant; prepend requires read; unset without observing the value; empty string is present; unauthorized multi-name unset changes nothing; nested `with` and function mutations do not escape; host environment and a later recipe are unchanged; dry-run performs none of these effects. |
| Lookup | Scope `PATH` selects the bare name; startup `PATH` remains outside that scope; stage `:PATH` does not select the executable; `--tool` wins; scoped run grants still reject bare names. |
| Handoff | Metadata applies only when no stage sets `:tty`; explicit stage request requires authority; capture, background, and cleanup do not attach; missing terminal is `HOST_FAIL` with no child; group restored after success, failure, and cancellation; parent process remains; no terminal-attribute claim. |
| Freshness | Invocation read recorded; self-export read records no external value; unset-then-read does not persist the removed value; process digest changes when a name is removed; diagnostic text contains no value. |
