---
revision: 2
---

# Kash command completion

Tab in [interactive Kash](052-kash-interactive.md#completion) also completes
subcommands, options, option values, and positional arguments of external
commands. Completion data is discovered automatically from installed files,
chiefly man pages, and kept current by a Kame build. Every source is converted
into one model shaped like the [Usage specification](https://usage.jdx.dev/spec/),
which is also the authoring format.

## Model and authoring

A **command spec** describes one executable: its subcommands (with aliases),
flags (short, long, and old-style single-dash spellings, a value placeholder,
whether the value is required, `global` inheritance by subcommands),
positional arguments, `choices` for flag values and arguments, descriptions,
and completers of `type="file"` or `type="dir"`. Kash reads and writes the
subset of Usage KDL that expresses this model:

```kdl
bin "report"
flag "-f --format <format>" help="Output format" {
    choices "text" "json" "csv"
}
flag "--config <file>" help="Configuration file" {
    complete type="file"
}
cmd "publish" help="Publish a report" {
    alias "pub"
    arg "<target>"
}
```

Other Usage nodes are ignored without rejecting the file. A `complete run=`
node is ignored: running programs is only authorized through
[completion rules](#user-rules). Unknown or malformed KDL rejects the whole
file with a diagnostic on the next explicit refresh, never during Tab.

Hand-written specs are `NAME.usage.kdl` files in
`$XDG_CONFIG_HOME/kash/completions/` (default `~/.config`), then
`kash/completions/` under each `$XDG_DATA_DIRS` entry (default
`/usr/local/share:/usr/share`). The first file found for a name wins. The
working directory is never searched. Discovery roots are fixed from the
invocation environment.

## Sources and precedence

The cached spec for a command merges these sources, highest first. A higher
source wins for any subcommand, flag, or argument it declares; lower sources
only add what is missing:

1. A hand-written Usage file.
2. The output of a user [completion rule](#user-rules) for the command.
3. Static fish declarations: `$XDG_CONFIG_HOME/fish/completions`, then
   `fish/vendor_completions.d` and `fish/completions` under `$XDG_DATA_DIRS`,
   then `$XDG_CACHE_HOME/fish/generated_completions`. The first file found is
   used.
4. Man pages: `NAME(1)` and, for each subcommand page named `NAME-SUB(1)`
   or `NAME_SUB(1)`, that subcommand.

At Tab, Kash reads the cached spec. When the cursor is at an option value or
argument whose spec allows files, or no spec applies, existing path
completion follows. A value with `choices` or a non-file completer does not
fall back to files.

### Man pages

Man pages are found by name and section `1`, then `8`, in the directories of
`$MANPATH` (an empty entry stands for the defaults), or else
`/usr/local/share/man` and `/usr/share/man`. Locale subdirectories are not
used. Uncompressed, gzip, and bzip2 pages are read; others are skipped.

The parser recognizes option lists in both common macro packages:

- man(7): `.TP`, `.IP` and `.PP`/`.HP` items whose tag line starts with a
  dash, such as `\fB\-a, \-\-all\fP` or `\fB\-\-block\-size\fR=\fISIZE\fR`.
- mdoc(7): `.It Fl x Ar file` and `.It Fl \-long Ns = Ns Ar value` items.

From each item it extracts every spelling, the value placeholder and whether
it is attached (`=`/`[=`) or required, and the first sentence of the
description. It decodes the usual escapes (`\-`, `\(aq`, `\fB`…), drops
font changes and hyperlinks (`\X'…'`), and follows a `.so` redirection only
to a page in the same man directory tree. Items outside a recognizable option list are
ignored. The parser never runs `man`, `groff`, or `mandoc`.

### Fish files

`.fish` files are data, not scripts. Only standalone literal `complete`
declarations are used: `-c`, `-s`, `-l`, `-o`, `-d`, `-r`, `-f`, `-x`, `-F`,
and a literal `-a` word list, with quoting, comments, and line continuations.
A declaration with a variable, command substitution, `-n` condition, or
unsupported flag is skipped whole. A file whose statement boundaries cannot
be determined is ignored.

## Kame-built cache

Completion specs are build products of a Kame program that is part of Kash.
It runs in the build root `$XDG_CACHE_HOME/kash/completions/` (default
`~/.cache`), which holds its own `.kame/` state, and produces
`specs/NAME.usage.kdl` for each command name.

The rule for a name depends on the resolved executable, every man page,
fish file, and hand-written Usage file that discovery selected for it, and
the files it found absent at higher-precedence locations. A change to the
binary, a page, or a completion file therefore rebuilds the spec, and an
unchanged command reuses it. The cache signature includes the Kame version,
so an upgrade rebuilds all specs.

The parsers are pure library operations, so their results follow ordinary
Kame caching:

| Operation | Use | Result |
| --- | --- | --- |
| `parse-man BYTES` | A man page, compressed or not | A command spec record |
| `parse-fish-completions BYTES` | A fish completion file | A command spec record |
| `parse-usage TEXT` | Usage KDL | A command spec record |
| `merge-usage SPECS` | Specs in precedence order | A merged spec record |
| `render-usage SPEC` | A spec record | Canonical Usage KDL |

The program is embedded in the Kame executable and not editable. It grants
itself read access to the discovery roots and the `PATH` directories, and write
access only to its build root.

### Refresh

Tab reads `specs/NAME.usage.kdl` and never builds. Kash refreshes in a
background child `kame` process that has no terminal and does not take part in
job control:

- After an entry runs, for the literal command names it invoked.
- When Tab finds no spec for a command. A refresh that finishes within the
  completion budget (100 ms by default) is used by the same Tab; otherwise the
  next Tab uses it.

At most one refresh runs at a time; later requests are coalesced. Refresh
failures are not reported at the prompt; `complete --refresh NAME…` runs one
in the foreground and reports diagnostics. `complete --refresh --all` builds
specs for every executable on the session `PATH` that has a man page, fish
file, or Usage file, like `fish_update_completions` but incrementally.
`complete --show NAME` prints the cached spec and the sources it came from.
`complete` is an interactive builtin with the same placement rules as
`option` ([054](054-kash-options.md#builtin)).

Concurrent shells share the build root through the usual Kame cache locks.

### User rules

`$XDG_CONFIG_HOME/kash/completions.km` is included by the embedded program when
it exists. A rule in it producing `./specs/NAME.user.kdl` supplies source 2 for
`NAME`. This is how a tool that prints its own spec is used, and writing the
rule is the user's permission to run that tool:

```kame
rule ./specs/mise.user.kdl : /home/me/.local/bin/mise
	mise usage > $outputs.0
```

Because the rule depends on the executable, the tool runs once per installed
version, not per Tab. User rules run with the user's privileges; they are not
sandboxed. The embedded program never runs a command it found by discovery,
including `--help`.

## Live protocols

Some tools compute candidates from the current line. An `option` per
resolved absolute executable path enables a live protocol for it, off by
default:

- [Cobra](https://github.com/spf13/cobra/blob/main/site/content/completions/_index.md):
  `EXE __complete ARGS PREFIX`, with an empty `PREFIX` when the word is empty.
  Parse candidate lines with tab-separated descriptions and the final
  `:directive`; honour no-file, directory, extension-filter, no-space, and
  keep-order directives. An error directive or malformed reply is ignored.
- [clap `CompleteEnv`](https://docs.rs/clap_complete/latest/clap_complete/env/struct.CompleteEnv.html):
  `EXE -- EXE ARGS PREFIX` with `COMPLETE=fish`, reading candidate lines with
  tab-separated descriptions.

Kash passes literal argv only: no editor text, no substitution expansion, and
no call when an earlier word on the line is not literal. The child runs in the
session directory and child environment, with no terminal or stdin, within the
completion budget and an output bound. A stale request is cancelled and its
process group killed. Replies are not cached or logged. Live candidates come
before the cached spec's candidates at the same position.

## Safety

Displayed text from any source is stripped of control characters. Inserted
candidates are escaped as Kash words, and a candidate can never submit the
entry. Completion still evaluates no session definitions.

## Implementation phases

1. **Model and Tab.** The spec model and Usage KDL subset (`parse-usage`,
   `render-usage`, `merge-usage`). In `repl_complete.go`, find the command
   segment, its literal words, and the position at the cursor (subcommand,
   flag, attached or separate flag value, argument), through aliases,
   pipelines, and redirections, without running the entry. Read hand-written
   specs directly. Tests: flags, `--name=value`, short clusters, global flags in
   subcommands, choices, file fallback, partial quotes, mid-token cursor, and a
   PTY check that Tab does not submit.
2. **Man pages.** `parse-man` for man(7) and mdoc(7), compressed pages, and
   subcommand pages, with fixtures from real GNU, BSD, and generated pages.
3. **Kame cache.** The embedded program and build root, background refresh,
   `complete --refresh`/`--show`, and invalidation when the executable, a page,
   or a higher-precedence file changes. First confirm that captured rules can
   depend on computed absolute paths and recorded absences.
4. **Fish files and user rules.** `parse-fish-completions` with fixtures (no fish
   installation), and the `completions.km` include.
5. **Live protocols.** Cobra and clap, tested with fixture executables for
   argv boundaries, empty prefixes, directives, malformed replies, timeouts,
   and that a disabled executable is never launched.

Out of scope: running fish code or bash-completion, Carapace, parsing
`--help` from discovery, and man sections other than 1 and 8.
