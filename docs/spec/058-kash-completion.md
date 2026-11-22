---
revision: 3
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
whether the value is required or may only be attached, `global` inheritance by
subcommands), positional arguments (optional, variadic), `choices` for flag
values and arguments, descriptions, and completers of `type="file"`,
`type="dir"` or `type="none"`. Kash reads and writes the subset of Usage KDL
that expresses this model:

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

KDL v2 syntax is read, including comments, slashdash, raw and multi-line
strings, and the v1 keywords `true`, `false` and `null`. Other Usage nodes are
ignored without rejecting the file, and hidden (`hide=#true`) commands, flags
and arguments are left out. A named `complete "NAME" type=…` node applies to
arguments and flag values of that name. A `run=` completer is never executed:
running programs is only authorized through [user rules](#user-rules) and
[live protocols](#live-protocols). Malformed KDL in a hand-written file is
reported by `complete --refresh`, never during Tab.

Hand-written specs are `NAME.usage.kdl` files in
`$XDG_CONFIG_HOME/kash/completions/` (default `~/.config`), then
`kash/completions/` under each `$XDG_DATA_DIRS` entry (default
`/usr/local/share:/usr/share`). The first file found for a name wins. The
working directory is never searched. Discovery roots are fixed from the
invocation environment; relative XDG entries are ignored.

## Sources and precedence

The spec for a command merges these sources, highest first. A higher source
wins for every subcommand, flag and argument it declares; a lower source adds
missing subcommands, flags and arguments, and spellings and descriptions
missing from a flag it shares:

1. A hand-written Usage file.
2. The output of a [user rule](#user-rules) for the command.
3. Static fish declarations: `$XDG_CONFIG_HOME/fish/completions`, then
   `fish/vendor_completions.d` and `fish/completions` under `$XDG_DATA_DIRS`,
   then `$XDG_CACHE_HOME/fish/generated_completions`. The first file found is
   used.
4. Man pages: the first `NAME.1` or `NAME.8` page, and as subcommands the
   `NAME-SUB.1` pages whose synopsis starts with the words `NAME SUB`. A
   page such as `systemd-keyutil(1)`, whose synopsis names its own executable,
   is not a subcommand of `systemd`.

At Tab, Kash reads the spec and walks the words already entered: it enters
subcommands (inheriting `global` flags), skips the values of flags that take a
separate value, and counts positional arguments; `--` ends options. It then
offers, for the word at the cursor:

| Word | Candidates | Files |
| --- | --- | --- |
| After a flag that requires a value | The flag's choices | When the flag has no choices and its completer allows files |
| `--NAME=VALUE` | The flag's choices, kept attached | Same as above |
| Starting with `-` | Flags of the command and inherited global flags; only long spellings after `--` | No |
| Anything else | Subcommands at the first position, then the positional argument's choices | When the argument allows files, or the command declares neither arguments nor subcommands |

Path candidates follow the existing [path rules](052-kash-interactive.md#completion);
a `dir` completer offers directories only.

### Man pages

Man pages are found by name in sections `1` and `8` of the directories of
`$MANPATH` (an empty entry stands for the defaults), or else
`/usr/local/share/man` and `/usr/share/man`. Locale subdirectories are not
used. Uncompressed, gzip and bzip2 pages are read; a page in another format,
or that cannot be decoded, contributes nothing. A page that only contains
`.so man1/OTHER.1` is followed to that page in the same man directory tree.

The parser recognizes option lists in the common page styles without running
`man`, `groff` or `mandoc`:

- man(7): `.TP`, `.HP` and `.IP` items whose tag starts with a dash, such as
  `\fB\-a, \-\-all\fP`, `\fB\-\-block\-size\fR=\fISIZE\fR` or `--color[=WHEN]`.
- DocBook, asciidoc and Sphinx output: an option line followed by an indented
  description (`.RS`/`.RE`, `.INDENT`/`.UNINDENT`).
- mdoc(7): `.It Fl x Ar file`, `.It Fl \-long Ns = Ns Ar value`, `.It Xo … .Xc`.

From each item it takes every spelling (separated by `,`, `|` or ` / `), the
value placeholder and whether the value is attached (`=`), optional (`[=`) or
separate, and the first sentence of the description. It decodes the usual
escapes and special characters, and drops font changes, hyperlinks and other
formatting. The NAME section gives the command's description and the first
words of the SYNOPSIS name the command.

### Fish files

`.fish` files are data, not scripts. Only top-level literal `complete`
declarations for the command are used: `-c`, `-s`, `-l`, `-o`, `-d`, `-r`,
`-f`, `-x`, `-F`, `-k` and a literal `-a` word list, with quoting, comments,
line continuations and clustered short options. A declaration inside a
function or other block, or with a variable, command substitution (including
in a quoted `-a`), pipeline, `-n` condition, `-w` wrap or other unsupported
option, is skipped whole. A file whose statements cannot be split, such as one
with an unclosed quote, contributes nothing.

## Kame-built cache

Completion specs are build products of a Kame program that is part of Kash.
Its build root is `$XDG_CACHE_HOME/kash/completions/` (default `~/.cache`),
which holds the program as its `Kamefile`, its own `.kame/` state, and
`specs/NAME.usage.kdl` for each command name. Kash writes the `Kamefile`
before each build when its text differs, so edits to it are replaced; the
discovery roots are literal values in it.

The program's rule for a name is an expression body that reads the sources
through `exists?`, `wildcard` and `read`. These record every file it read and
every location it found absent, so an installed, changed or removed page or
completion file rebuilds the spec, and an unchanged command reuses it. The
`Kamefile` names the Kame version and build, so a different `kame` rebuilds
every spec, as does a change of discovery roots. A
command with no sources still gets a spec naming it, so it is not rebuilt on
every Tab.

The parsers are pure library operations:

| Operation | Use | Result |
| --- | --- | --- |
| `parse-usage TEXT` | Usage KDL text or bytes | A command spec record; malformed KDL is `PARSE_ERR` |
| `render-usage SPEC` | A spec record | Canonical Usage KDL |
| `merge-usage SPECS` | A list of specs, highest first; `nil` items are skipped | The merged spec |
| `parse-man PAGE` | A man page, compressed or not | A command spec record, empty for an unreadable page |
| `man-redirect PAGE` | A man page | The `.so` target of a page that only includes another, or `nil` |
| `parse-fish-completions NAME TEXT` | A fish completion file | The spec of `NAME`'s static declarations |

A spec record has the fields `name`, `help`, `aliases`, `flags`, `args` and
`commands`. A flag has `names` (with dashes), `help`, `value`, `optional`,
`global`, `choices` and `complete`; an argument has `name`, `help`,
`optional`, `variadic`, `choices` and `complete`; commands are spec records.

### Refresh

Tab reads `specs/NAME.usage.kdl`, and a hand-written Usage file when the build
product is missing; it never builds in the editor. Refreshes run a `kame` child
process in its own process group, with no terminal and its output discarded,
granted reads and the processes of user rules:

- After an entry runs, for the commands it named that resolve to executables
  on the session `PATH`. An entry refreshes a command at most once every ten
  minutes per session.
- When Tab finds no build product for a command on the session `PATH`. A
  refresh that finishes within 100 ms is used by the same Tab; otherwise the
  next Tab uses it.

One refresh runs at a time per session; requests made meanwhile are combined
into the next. Concurrent shells share the build root through Kame's cache
locks. Background failures are not reported.

`complete` is an interactive builtin with the same placement rules as `option`
([054](054-kash-options.md#builtin)):

- `complete --refresh NAME…` builds the named specs now and reports failures,
  such as malformed hand-written files.
- `complete --refresh --all` builds specs for every executable on the session
  `PATH` that has a man page, fish file, Usage file or user rule output, like
  `fish_update_completions` but incrementally.
- `complete --show NAME` prints the path of the cached spec and its KDL.

### User rules

`$XDG_CONFIG_HOME/kash/completions.km` is included by the program when it
exists. A rule in it producing `./user/NAME.usage.kdl` supplies source 2 for
`NAME`; it is built when the spec is. This is how a tool that prints its own
spec is used, and writing the rule is the user's permission to run it:

```kame
rule ./user/mise.usage.kdl : /home/me/.local/bin/mise
	mise usage > $outputs.0
```

Because the rule depends on the executable, the tool runs once per installed
version, not per Tab. User rules run with the user's privileges; they are not
sandboxed. The program's own rule is an expression body, which cannot run
processes, so it never runs a command it found by discovery.

## Live protocols

Some tools compute candidates from the current line. `complete --live cobra
PATH` or `complete --live clap PATH`, at the prompt or in the interactive
startup file, enables a live protocol for one absolute executable path in the
session; `complete --live` lists the registrations. A command word resolving
to that path, through the session directory or `PATH`, uses it:

- [Cobra](https://github.com/spf13/cobra/blob/main/site/content/completions/_index.md):
  `EXE __complete ARGS PREFIX`, with an empty `PREFIX` when the word is empty.
  Candidate lines carry optional tab-separated descriptions and the final
  `:DIRECTIVE` line is required. The no-file and directory directives are
  honoured, a file-extension filter offers all files, and other directives
  are ignored.
- [clap `CompleteEnv`](https://docs.rs/clap_complete/latest/clap_complete/env/struct.CompleteEnv.html):
  `EXE -- WORD ARGS PREFIX` with `COMPLETE=fish`, where `WORD` is the command
  word as typed, reading candidate lines with tab-separated descriptions.

Kash passes the literal argument texts as argv: no editor text, no expansion,
and no call when an argument contains `$`, a group, a glob or `~`. The child
runs in the session directory with the session `PATH` and `HOME`, no terminal
and no stdin, within 500 ms and 1 MiB of output; on timeout its process group
is killed. Candidates not starting with the typed word are dropped. A reply
replaces the cached spec for that Tab; an error directive, malformed reply,
failure or timeout falls back to it. Replies are not cached or logged.

## Safety

Candidates containing control characters are dropped, and control characters
are removed from descriptions. Inserted candidates are escaped as Kash words,
and a candidate can never submit the entry. Completion still evaluates no
session definitions.

Out of scope: running fish code or bash-completion, Carapace, parsing
`--help`, man sections other than 1 and 8, subcommands nested below the first
level in man pages, and Cobra's no-space and extension-filter directives.
