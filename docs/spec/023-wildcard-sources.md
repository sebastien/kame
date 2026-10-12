# Wildcard dependency sources

`038-direct-kash-and-values.md` extends this contract with interpolated glob
atoms and signed entry-kind filters. Filter masks participate in consumer
identity, and entry-kind changes invalidate filtered membership even when path
names are unchanged. Symlinks are classified without following their targets.

An unquoted explicit path containing authored, unescaped `*`, `?`, or a bracket
character class is a glob expression wherever Kame accepts an expression, not
only in rule inputs (`004-language.md`). Valid capture/expansion pattern groups
take precedence and never initiate filesystem enumeration. Quoted paths and
wildcard bytes contributed by interpolation remain literal data. Raw Kash
command words retain their literal semantics; Kash definition values and
embedded Kame expressions use ordinary glob-expression evaluation.

```kame
SOURCES_JOURNAL = ./journal/*
PRODUCT_BY_NAME = (map (replace ./journal/{name:*} "@(name)") SOURCES_JOURNAL)
```

Glob expressions and explicit `wildcard` calls use the same dependency-tracked
filesystem source. A glob expression always produces a list: zero matches give
`[]`, and one match remains a one-item list. `(wildcard ./journal/*)` accepts
the already-expanded list without interpreting its filenames as new patterns.
All reached scans retain ordinary read authorization, phase policy and authored
diagnostic spans; glob syntax does not confer host authority. Parsing and
formatting never scan, and unused definitions or unselected branches do not
request a source.

Each authored pattern has one shared glob resource key in the program engine,
including patterns whose current result is empty. Evaluation is lazy: a source
is requested only when the selected plan or runtime reaches it. Results are
sorted, duplicate-free, cwd-relative paths. The dependency records the pattern
membership, not only the files returned by the current scan, so adding or
removing a matching path invalidates consumers.

On native and WASM watch sessions, an invalidated glob source is reevaluated and
its retained consumers rebuild from the latest membership. Multiple consumers
of the same pattern share that source. A source update uses ordinary graph
invalidation and does not start an unrelated recipe or poll from inside the
portable runtime.

## Acceptance

- Recursive and empty patterns retain one shared lazy dependency source.
- Glob expressions expand in definitions, application arguments, containers and
  rule inputs, preserving empty/singleton list shape and sorted order.
- Quoted/escaped wildcard bytes and dynamic interpolation bytes remain literal;
  capture patterns do not scan, and parse/format performs no filesystem access.
- Explicit `wildcard` calls preserve equivalent membership and flags without
  re-globbing already-expanded filenames.
- Denied or phase-forbidden glob reads issue no host request; unused definitions
  and unselected branches acquire no glob dependency.
- Membership additions and removals invalidate retained consumers on native and
  WASM watch sessions without starting unrelated recipes.
