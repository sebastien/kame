# TODO

Yes, but remaining work is higher-risk architectural refactoring:
- Introduce a portable process-host contract so program no longer depends on host/posix.
- Replace CLI dispatch/help duplication with declarative command metadata.
- Split remaining evaluator orchestration from expression/container evaluation.
- Split program/materialize.go lifecycle/producer orchestration further.
- Reorganize large runtime tests by planning, execution, cache, and events.
- Reconcile Makefile workflows and shell-test bootstrap duplication.
-

Governing specs: `docs/spec/003-posix-process.md`,
`docs/spec/011-diagnostics.md`.

## Callable Ownership Cycles (pre-existing, found by 014 work)

Both reproduce under `mem.Tracker` leak checks and stem from temporary
functions retaining their defining scope while the only other holder is a
binding inside that scope. Fixing them needs an ownership audit with
sanitizer-backed cleanup tests; do not ship a quick release.

- [ ] A `let`-bound lambda leaks its scope chain: the lambda retains the let
      scope and the binding is only released by that scope's own `Free`, so
      references never reach zero (`(let [f ([x] x)] "x")`).
- [ ] A callable inside a rule input expression leaks across plan and build
      resolution: `@((map ([s] s) sources))` leaks the lambda and its scope
      through `resolveInputs`/`planInputExpression` (`014-patterns.md`
      sections in recipes and three-argument `replace` are unaffected).

## POSIX Process Host Integration

- [ ] Run POSIX host polling outside graph workers once the runtime exists.
- [ ] Connect engine submissions and completions to POSIX host requests and events.
- [ ] Map engine cancellations to process-group cancellation.
- [ ] Cancel active processes when the runtime event consumer disappears.
- [ ] Add runtime retry policy, recipe source mapping, and joined recipe scripts.
- [ ] Map CLI `SIGINT` and `SIGTERM` to root cancellation and wait for shutdown.
- [ ] Extract a portable host contract package for requests, events, and
      diagnostics; `host/posix` currently owns those types.

## POSIX Host Hardening

- [ ] On setup failure, retry `waitpid`; emit `HOST_FAIL` if reaping cannot be confirmed.
