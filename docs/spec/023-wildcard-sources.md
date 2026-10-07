# Wildcard dependency sources

An unquoted explicit path input containing `*`, `?`, or `[` is a wildcard
source. The rule parser lowers it to the same dependency-tracked filesystem
operation as `(wildcard PATTERN)`. Quoted paths preserve wildcard characters
literally. This applies to rule inputs; Kash command arguments retain their
existing literal semantics unless Kame evaluates an explicit expression.

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
- Quoted paths remain literal; unquoted wildcard inputs expand in sorted order.
- Membership additions and removals invalidate retained consumers on native and
  WASM watch sessions without starting unrelated recipes.
