---
revision: 2
---

# Wildcard dependency sources

`038-direct-kash-and-values.md` extends this contract with interpolated glob
atoms and signed entry-kind filters. Filter masks participate in consumer
identity, and entry-kind changes invalidate filtered membership even when path
names are unchanged. Symlinks are classified without following their targets.

A path atom containing authored, unescaped `*`, `**`, `?` or a bracket
character class is a `PathPattern` value (`055` §9.1). Valid capture groups
(`{name}`, `{name:**}` and the other group forms of `014`/`029`/`041`) also make
a `PathPattern`, but never initiate filesystem enumeration. Wildcard bytes
contributed by `${…}` interpolation and wildcard characters inside quoted
strings remain literal data. Ordinary expression evaluation never converts a
`PathPattern` into a list of paths: membership is observed as a rule input,
through an explicit discovery operation, or in a runtime Kash `for` source.

```kame
sources-journal = files ./journal/*
product-by-name = subst ./journal/{name:*} "${name}" sources-journal
```

`files`, `dirs` and `wildcard` (`007-library.md`) are the discovery
operations. Each returns a sorted, duplicate-free `List[Path]`: zero matches give
`[]`, and one match remains a one-item list. `wildcard` also accepts a list of
already-discovered `Path` values without interpreting their names as new
patterns. As a rule input, a wildcard path atom names the dynamic membership of
its pattern; the rule depends on that membership as described below.

Kash command words are not expression atoms: `ls ./journal/*` passes the
literal text `./journal/*` as one argv entry. A command that needs discovered
paths spreads an explicit list:

```kash
let journal = files ./journal/*
ls $...journal
```

All reached scans retain ordinary read authorization, phase policy and authored
diagnostic spans; glob syntax does not confer host authority. Parsing and
formatting never scan, and unused definitions or unselected branches do not
request a source. In declaration context a discovery result is a snapshot taken
at planning time, and a discovery pattern that overlaps a declared output is
rejected with `DISCOVERY_OVERLAP` (`055` §9.4).

Each authored pattern has one shared glob resource key in the program engine,
including patterns whose current result is empty. Evaluation is lazy: a source
is requested only when the selected plan or runtime reaches it. Results are
sorted, duplicate-free paths relative to the root directory (`055` §9.2). The
dependency records the pattern membership, not only the files returned by the
current scan, so adding or removing a matching path invalidates consumers,
including declarations that consumed a declaration-time snapshot.

On native and WASM watch sessions, an invalidated glob source is reevaluated and
its retained consumers rebuild from the latest membership. Multiple consumers
of the same pattern share that source. A source update uses ordinary graph
invalidation and does not start an unrelated recipe or poll from inside the
portable runtime.

## Acceptance

- Recursive and empty patterns retain one shared lazy dependency source.
- `files`, `dirs` and `wildcard` discover in definitions, application arguments,
  containers and declaration-time loops, and wildcard path atoms discover as
  rule inputs, preserving empty/singleton list shape and sorted order.
- A runtime Kash `for` source discovers a `PathPattern` and binds its named
  captures per match (`017`, Loops). Ordinary value evaluation does not scan.
- Quoted wildcard text and dynamic interpolation bytes remain literal;
  parse/format performs no filesystem access.
- `wildcard` over already-discovered lists preserves equivalent membership and
  flags without re-globbing filenames.
- Denied or phase-forbidden glob reads issue no host request; unused definitions
  and unselected branches acquire no glob dependency.
- Declaration-time discovery overlapping a declared output fails with
  `DISCOVERY_OVERLAP`, naming the pattern and the output declaration.
- Membership additions and removals invalidate retained consumers, including
  declarations built from a discovery snapshot, on native and WASM watch
  sessions without starting unrelated recipes.
