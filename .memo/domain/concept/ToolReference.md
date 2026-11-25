---
identity: 91b95fd3-a299-453b-b3d7-78f4ce9fdbe6
created: 2026-11-25T18:44:04.704999+00:00
updated: 2026-11-25T18:44:05.168378+00:00
---
# Tool reference

A tool reference is a resolved, tracked executable. Rule freshness and cache
fingerprints record the content digest of the resolved file, so replacing a
compiler invalidates dependent work.

Forms:

- `x/NAME` (or `x/"python3.12"`) in an expression or command head. The `x/`
  namespace bypasses all lexical bindings and builtins.
- `$x/NAME` in Kash words, and `@{x/NAME}` in foreign-shell recipes.
- `(tool NAME)` as an operation. `tool "git" ?? nil` treats a missing tool
  (`TOOL_MISSING`) as `nil`.

## Resolution

- Named lookup uses the driver's startup `PATH` and `--tool` overrides. In a
  Kash unit, command-head lookup uses the environment store's `PATH`
  ([domain/concept:ProcessUnit]).
- Every Kash command head that is reached acquires an executable dependency
  automatically. Explicit executable paths resolve from the stage `cwd`.
- `kame do tools` reports the resolved set.

## Boundaries

- Ordinary command names inside foreign-shell recipe text are resolved by
  that shell and are not tracked. Use `@{x/NAME}` to track them.
- In command position, the standard library is never consulted:
  `basename $f` runs the executable, and `(basename f)` calls the library
  function.

Related: [domain/concept:Freshness] [domain/concept:Cache]
[domain/decision:kash_typed_actions]

Sources: `docs/spec/055-unified-kame-kash-language.md` §3.2, §6;
`docs/spec/038-direct-kash-and-values.md`; `docs/spec/006-runtime.md`;
`docs/glossary.md` (Tool reference).
