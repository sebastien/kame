---
identity: defce73d-421a-4740-a4e8-af7efc6252d3
created: 2026-11-25T18:43:42.019340+00:00
updated: 2026-11-25T18:43:42.904621+00:00
---
# CLI (`cli`, `cmd/kame`)

The CLI is the `kame` command. It handles invocation grammar, source
discovery, builds, runner sessions, watch mode, inspection commands,
interactive Kash, presentation (human ANSI, plain text and JSON Lines),
diagnostic rendering, and the cache and clean commands.

## Packages

- **`cli`**: the portable command grammar and presentation shared with the
  JavaScript wrapper. It provides `Parse` → `Invocation`, help, the progress
  dashboard, result JSON and picker classification. It imports `program` for
  progress state.
- **`cmd/kame`**: the native executable. Its parts:
  - `Run` dispatch, `runBuild`, `runWatch`, `runSession` and the `do`
    commands: parse, fmt, plan, inputs, outputs, span, cat, tools, render,
    pick, cache.
  - `runInteractive`, the interactive Kash session ([domain/concept:Session]).
    It has a readline editor with highlighting, history with secret
    filtering, completion from usage specs, job control, options and aliases.
  - The clean command and output ledger, and native diagnostic rendering.

## Entry points

- `kame [TARGET…]` discovers `Kamefile`, `Makefile.km`, `make.km` or
  `src/km/main.km`, then builds `default` or the named targets.
- `kame FILE.km|FILE.kash [ENTRY…] [-- ARG…]` runs explicit sources. It is
  shorthand for `kame do run`.
- `kame do COMMAND …` runs inspection and tooling commands.
- The binary invoked through a symlink named `kash` runs Kash, reading stdin
  when no source is given.

## Interactions

- Drives [domain/component:Program] with a [domain/component:Host], and the
  `ownership` package for automatic clean.
- Reaches [domain/component:Language] for parse and format.

## Invariants

- **Exit status:**
  - 0 for success, inspection and help.
  - 1 for parse, evaluation, build or cache failures.
  - 2 for usage errors.
  - 128+signal for forced termination.

  `do run` preserves an unhandled executable's exit status.
- **Runner sessions** parse every fragment and include before any effect.
  They share one scope and engine, and a failure stops later fragments
  without rollback. They never convert programs to shell text or reparse
  concatenated sources.
- **Configuration precedence:** `--define`/`--set` (last wins), then
  `KAME_<NAME>`, then the authored value, including `?=`. Overrides are
  literal data, never expressions.
- **Presentation** comes in three modes: `ansi`, `text` and `json`.
  - Progress and diagnostics go to stderr; values and artifacts go to stdout
    unchanged.
  - JSON is one unstyled object per line on stdout.
  - Human and JSON output carry the same diagnostics.
- `-n` plans and renders without effects or processes.
- **Inspection** (`plan`, `inputs`, `outputs`, `span`, `tools check`) is
  read-only and never runs producers. `do cat` does run prerequisites.

Sources: `docs/spec/009-cli.md`, `docs/spec/036-cli_ansi.md`,
`docs/skills/kame/ref/cli.md`, `docs/spec/052-kash-interactive.md`.
