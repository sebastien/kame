---
identity: dcabca06-e6b7-4d61-b599-4d6a48f507ac
created: 2026-11-25T18:43:56.151978+00:00
updated: 2026-11-25T18:43:56.621898+00:00
---
# Module

A module is a source file loaded through `import` or `from … import`. It is
loaded once per normalized source identity, and its `export` list is exposed
as a namespace (`c/compile`) or as selected names.

- `import ./lib/c` binds the namespace `c/`. `as` renames it, and is required
  when the file stem is not a valid name.
- `from ./lib/c import compile, object-path` binds selected names, optionally
  renamed with `as`.
- `export NAME, …` (top level only) declares the public interface. A module
  without `export` exports nothing, and a module cannot re-export imported
  names. A facade defines an alias instead (`def f = lib/f`).
- A suffixless path tries `.km`, `.kame`, `.kash` and `.ksh`. Several matches
  are `RES_INVALID`, and no match is `FS_ERR`.
- A `.kash` module may contain only comments, `def`, `import`, `from`,
  `export` and unconditional `include`. Anything else is `DEF_INVALID`.

## Import is not include

| | `import` / `from` | `include` / `include?` |
| --- | --- | --- |
| Scope | New module scope; only exports are visible | Splices declarations into the caller's scope |
| Declarations | Registers none. "Imports do not build." | Registers them in the caller's declaration transaction |
| Repetition | Loaded once | Registers again; ordinary collision rules apply |
| Cycles | Rejected | Active cycles are `DEP_CYCLE` |

A module can export functions that return `Rule` values. The importer
registers them with `generate` ([domain/decision:declarations_as_data]).

## Paths and freshness

- Paths in `include` and `import` resolve against the current source file.
  Other relative paths resolve against the root (entry-file directory for
  Kame, working directory for Kash, or `-C`). `module/dir` reaches the
  module's own files.
- Imported names resolve before executables in command position, without
  gaining effect authority.
- Source identity and freshness of loaded modules follow
  `docs/spec/050-kash-libraries.md`.

Runtime `source PATH` (direct scripts and sessions) is not a module load. It
runs a Kash file in a private copy of the unit state and publishes the
changes back only on success.

Related: [domain/concept:ProcessUnit] [domain/concept:Session]

Sources: `docs/spec/055-unified-kame-kash-language.md` §9.2, §11;
`docs/spec/050-kash-libraries.md`.
