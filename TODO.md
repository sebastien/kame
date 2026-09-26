# TODO

No outstanding work.

## Recently completed

Callable ownership cycles (found by 014 work, fixed with sanitizer-backed
cleanup tests).

Governing specs: `docs/spec/003-posix-process.md`,
`docs/spec/011-diagnostics.md`.

- `let`-bound lambda scope cycle: `(let [f ([x] x)] "x")` releases through
  `Scope.breakCallableCycle` (`lang/eval/test/evaluation.go`
  `TestLetBoundLambdaReleasesItsScope`, alias and escape variants).
- Rule input lambda cycle: `@((map ([s] s) sources))` releases across plan
  and build (`program/test/runtime_planning.go`
  `TestPlanAndBuildInputLambdaReleasesItsScope`).
- Transfer-vs-discard audit: `freeValues` (transfer) vs
  `freeValuesWithCallables` (discard) in `lang/eval/ownership.go`; error
  paths in `call`, `list`, `record` discard deeply.
- Native sections share no Scope retain: `replace` sections must be used
  immediately, never let-escaped (see `FunctionBorrowed` docs).
