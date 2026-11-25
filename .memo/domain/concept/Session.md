---
identity: 7d0e422d-6489-42a8-8b6d-3ec53476d63b
created: 2026-11-25T18:44:03.062266+00:00
updated: 2026-11-25T18:44:03.493720+00:00
---
# Interactive session

An interactive Kash session is a REPL over one live program, engine,
top-level scope, capability policy and cleanup scope. Start it with `kash`
when stdin is a terminal, `kash -i [FILE.kash…]`, `kash --login`, or
`kame do run -i`. The session is the [domain/concept:ProcessUnit] for state:
`cd` and `set env/…` persist across entries, and `exit` in an entry ends the
session.

## Parts

- **Entry.** The lines submitted together, named `<kash:N>`.
  - An entry continues while an expression is open, or after a block header
    until a blank line.
  - It is parsed completely before any effect. A failing statement ends only
    its own entry.
  - Rules, tasks, services, `generate` and `include` are `FEATURE_UNSUP`.
- **Job.** An entry plus every graph it started. Ctrl-Z suspends it, and
  `fg` or `bg` resumes it without repeating effects. Ctrl-C reaches only the
  foreground command and gives status 130.
- **Terminal.** Foreground graphs get the terminal. Captures, cleanup and
  record-fed graphs keep pipes. Without a controlling terminal, everything
  stays on pipes.
- **Builtins.** `jobs`, `fg`, `bg`, `alias`, `unalias`, `history`, `option`,
  `explain` and `complete` are interactive-only, and `PARSE_ERR` elsewhere.
  `source PATH` and `source SHELL PATH as NAME` also work in direct scripts.
- **History** (`053`). Scoped per repository and stored as local plaintext.
  Entries matching a secret are rejected whole. Defaults are 15 days and
  1 MiB per scope.
- **Options** (`054`). The `option` builtin, with precedence local, then
  global (`$XDG_CONFIG_HOME/kash/options`), then environment, then built-in
  defaults.
- **Completion** (`058`). Built from hand-written KDL specs, user rules, fish
  data and man pages. No discovered code runs, specs are cached build products
  of a Kame program, and Tab never builds.

## Boundaries

- **Not POSIX-shell compatible.** There is no `export` or `unset`; use
  `set env/NAME = VALUE` and `set env/NAME = nil`.
- **Grants.** Default read and write grants follow `cd`. Explicit grants
  never move or broaden.
- **Foreign shells are native-only** ([domain/component:Host]).

## Open questions

- UI interactions (`pick!`, `choose!`, `confirm!`, `ask!`, spec 057) are
  specified but not implemented: none of these names occurs in `src/` as of
  2026-11-26.
- `docs/ref-kash.md` still lists "programmable completion" as not provided,
  which predates spec 058.

Implementation: `cmd/kame/repl*.go` (editor, highlighting, history,
completion, jobs, options, aliases), `program/session.go`,
`lang/eval/interactive.go`.

Related: [domain/concept:Kash] [domain/component:Cli]

Sources: `docs/spec/052-kash-interactive.md`, `docs/spec/053-kash-history.md`,
`docs/spec/054-kash-options.md`, `docs/spec/056-kash-foreign-shells.md`,
`docs/spec/057-kash-ui.md`, `docs/spec/058-kash-completion.md`.
