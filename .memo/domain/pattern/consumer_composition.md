---
identity: e5de8b1a-bdd3-42ee-9afb-3d1a4f3b18ff
created: 2026-11-25T18:44:13.072456+00:00
updated: 2026-11-25T18:44:13.555203+00:00
---
# Consumer composition

Configure a reusable Kame rule module from the program that consumes it,
using bindings only: no hooks, mutation or reordering.

```kame
# Kamefile (consumer)
build-dir = ./build/custom     # settings before the include they configure
include ./sdk/rules.km
checks += ["project-check"]    # contributions after the include

task project-check
	./scripts/check
```

```kame
# sdk/rules.km (module)
build-dir ?= ./build           # documented default
checks ?= []                   # contribution binding
task check : ${checks}
```

## How it works

- **`?=` registers a default only if the name is still unregistered.** The
  consumer's earlier `=`, or a CLI override, wins, and later defaults are
  skipped without being evaluated.
- **`+=` contributes list items to a configurable list.** Items are appended
  in source order, with includes expanded depth-first. It is not sequential
  assignment.
- **Overrides replace the whole binding** without evaluating it:
  `--define`, `--set`, `KAME_NAME` or `NAME=V`.

## Context

Use this pattern for shared rule libraries (an SDK, a company build module)
that projects pull in with `include`, so they register into the consumer's
declaration transaction.

## Trade-offs

- "No declaration reordering, mutable hooks or process execution is implied"
  (`004` §8.9). Ordering is plain source order.
- `include` is not include-once. A repeated include registers again and
  collides on duplicate rules.
- A contribution refers to the final binding, so `checks += checks` is
  `DEP_CYCLE`.
- `?=` and `+=` exist only at the Kame top level: not in `.kash` sources and
  not for functions.
- To reuse functions without registering anything, use `import`
  ([domain/concept:Module]).

Related: [domain/concept:Definition]

Sources: `docs/spec/004-language.md` §5.2, §5.3, §7.1, §8.9;
`docs/spec/018-source-style.md` §File Structure; `docs/idioms-and-gotchas.md`.
