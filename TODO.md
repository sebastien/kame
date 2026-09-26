# TODO

The remaining work is the higher-risk callable ownership audit below.

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
