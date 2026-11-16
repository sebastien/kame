# Kash shell options

## Session command

`option [-l|--local|-g|--global] [NAME [VALUE]]` is a whole-entry session
command, available in interactive input, interactive initialization, and login
profiles. It is not an executable, expression, or batch-script statement.
Parsing/formatting recognizes it without effects; batch execution rejects
session commands before executing earlier statements.

Local means the running shell, never a project directory. Local is the default;
global means persistent user defaults. Scope flags are mutually exclusive.
Names and values are literal whitespace-separated tokens, not evaluated
expressions or substitutions.

- With no operands, list effective settings in name order, including their
  source, allowed values/bounds, and descriptions.
- With one operand, inspect that setting with the same information.
- With two operands, validate and change the setting. Successful changes print
  nothing. Invalid syntax, unknown names, invalid values, and failed writes
  return status 2 without changing options.
- `-l`/`--local` inspection shows effective settings, just like unqualified
  inspection. `-g`/`--global` inspection lists only explicitly saved global
  overrides; an unset global setting has no output.
- Effective precedence is local override, global override, valid invocation
  environment default (where supported), then built-in default.

Global changes apply in the invoking shell unless locally overridden, and in
future shells. Other running shells do not automatically reload global options.
Local changes disappear at exit. Global updates reload the file under lock and
therefore also refresh that shell's global snapshot. Startup/profile `option`
commands execute after loading the global snapshot.

## Registry and first consumers

One built-in registry declares each name, description, default, and validation.
Boolean options use the choices `on|off`; other enumerated choices and bounded
decimal integers use the same registry. No plugins, arbitrary user-defined
options, expression values, or configuration-write CLI flags are provided.

The initial consumers are existing history settings:

| Option | Built-in default | Validation | Environment fallback |
| --- | --- | --- | --- |
| `history-scope` | `local` | `local` or `global` | `KASH_HISTORY_SCOPE` |
| `history-max-age-days` | `15` | Integer 1..106751 | `KASH_HISTORY_MAX_AGE_DAYS` |
| `history-max-bytes` | `1048576` | Positive integer, at most half the host signed-int maximum | `KASH_HISTORY_MAX_BYTES` |

History's local/global **recall view** is independent of option storage scope.
For example, `option -g history-scope local` saves local recall as the user
default. `history scope local|global` and Ctrl-X h update the same session-local
option. `KASH_HISTORY` still controls storage location and explicit disabling;
invalid history environment configuration continues to disable history safely.

History settings affect recall immediately after a successful command and
persistence on the next refresh/write. Lowering age or byte limits can prune
saved entries; increasing limits cannot recover discarded history. Settings
never disable secret filtering. History remains terminal-only.
Option-management entries themselves are not recorded in command history.

Completion offers scope flags, option names, and enumerated values. Completion
and vi/emacs feature switches are separate follow-up work.

## Global storage

Use `$XDG_CONFIG_HOME/kash/options`, falling back to `~/.config/kash/options`.
The path is fixed for the invocation; session `cd` and `export` do not move it.
Do not rewrite authored `init.kash` or profiles.

The file is UTF-8 text, one `name: value` per line. Blank lines and whole-line
`#` comments are accepted. Whitespace around keys/values is ignored. Duplicate
keys, unknown options, invalid values, oversized files (over 64 KiB), and
malformed lines reject the entire file. Values are data, never executed.
Writes emit canonical values and name-sorted keys; comments are not preserved.
Only explicit global overrides are stored.

Missing files mean no global overrides. Read failures produce a diagnostic
and leave the shell usable with local/environment/built-in defaults. A malformed
file is never silently repaired or overwritten by an option update.

Updates hold a stable adjacent `options.lock` across reload, validation, and
read–modify–write. Lock acquisition is bounded. Concurrent shells updating
different options must not lose each other's updates. Write a private temporary
file in the same directory, sync it, then atomically rename it over the store;
normal failure removes the temporary file and leaves session state unchanged.
Created directories use `0700`, files use `0600`. Reject unsafe symlink,
non-regular, hard-linked, or differently owned files and unsafe directories.
