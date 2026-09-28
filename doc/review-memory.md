# Solod memory + defer audit — src/go/kame

Scope: full `src/go/kame` (115 Go files, `solod.dev v0.4.0`), focus on allocator
ownership and `defer` correctness under Solod semantics.
Reference: https://antonz.org/solod/#defer (`defer` = inline LIFO before
returns/panics/end), https://antonz.org/defer-in-c/.

Solod model used for this audit:

- Stack by default; builtins never heap-allocate. Heap only via `so/mem`
  `Allocator` as first arg (`mem.Alloc/Free`, `slices.Make/Append/Free/Clone`,
  `strings.NewBuilder/Clone`, `mem.AllocSlice/FreeSlice/FreeString`).
- `defer` schedules a plain function/method call only: no closures, no func
  literals, args evaluated at the `defer` site, inlined LIFO at every exit.
- `string([]byte)` / `[]byte(string)` are zero-copy wraps in C, copies in Go.
  `a+b` temporaries do not outlive the call in C.
- Verified: `cd src/go/kame && so translate` succeeds for `./operations`;
  `so test ./lang/expr/... ./lang/eval/...` passes. `go test ./...` has no
  portable tests (`so test ./...` is the harness, `Makefile:20`).

## Defer inventory (production)

All production defers are direct void calls — no `defer func(){}` in
transpiled code. Test-only violations: `cmd/kame/main_test.go:293`,
`cmd/kame/main_test.go:454`, and dozens of `defer os.Remove(dir)` in
`program/test/*` (discards `error`; hosted-only, excluded from
`tools/wasm/check-portable.sh` portable `program`/`cli` tree).

Correct LIFO groups verified:

- `program/planning.go:25`, `program/planning.go:93`, `program/planning.go:162-163`
- `program/render.go:15-16`, `program/render.go:21`
- `program/producer.go:78-80`, `program/producer.go:143-144`
- `program/filesystem.go:14`, `program/filesystem.go:52-54`, `program/filesystem.go:120`
- `program/materialize.go:139`, `program/rule_execution.go:337`, `program/support.go:51`
- `lang/eval/forms.go:41`, `lang/eval/application.go:135`,
  `lang/eval/values.go:15`, `lang/eval/render.go:19`, `lang/eval/registry.go:59`,
  `lang/eval/context.go:198`, `lang/rule/rule.go:195`, `lang/expr/expr.go:808`,
  `lang/expr/pattern.go:242`
- `operations/collections.go:69,110,135`, `operations/general.go:202,209`,
  `operations/path.go:92-93,96`, `operations/text.go:16`, `operations/general.go:35`
- `cmd/kame/main.go:28,36`, `cmd/kame/inspect.go:16,21,51,60,86,91,132,186,191`,
  `cmd/kame/expr_command.go:27,33,40,52`, `cmd/kame/dispatch.go:25`
- `host/posix/*`, `host/wasm/*` `Free` defers

One defer is a double-free (C1 below). All other defers freeze their args at
the site and never reassign the deferred value afterwards.

## C1 — double Free via defer + explicit free — `lang/expr/pattern.go:327-348`

```go
b = strings.NewBuilder(a)
defer b.Free()          // pattern.go:330
...
b.Free()                // pattern.go:347, missing-reference branch
return Expansion{Missing: ownedText(a, name)}
...
return Expansion{Text: ownedText(a, b.String())} // deferred free runs here
```

The missing branch frees explicitly, then the deferred free runs again at
exit (Solod inlines it before `return`). Sibling `CanonicalPattern`
(`pattern.go:239-242`, defer only) and `targetLiteral`/`stringify` (explicit
only) show the intended single-free. Fix: drop the explicit `b.Free()` or the
defer; keep defer-only to match `CanonicalPattern`.

## C2 — `def` value path shares one callable between scope and Result — `lang/eval/forms.go:94-99`

```go
r := p.evaluate(context.Engine, scope, values[1], context)
scope.setValue(values[0].Text, r.Value) // scope.go:56: value.Clone is shallow for Callable
return r                                // same *Function pointer in both owners
```

`core/value.go:90-107` `Clone` deep-copies strings/lists/records but shallow-
copies `Callable`. `Scope.Free` (`scope.go:90-95`) and `Result.Free`
(`ownership.go:28`, via `result.go`) both `freeCallables` → double
`freeTemporaryFunction`/`freeBorrowedWrapper` for e.g. `(def f ([x] x))` in
value position, and for lists/records containing callables. `let`
(`forms.go:51-52`) avoids it by shallow `r.Value.Free` after `setValue`
(transfer). Fix: same move semantics for `def` (free shallow after set, return
Nil or a borrowed non-owning result), or make `setValue` transfer.

## C3 — `attachContextFrames` frees strings the new slice still aliases — `lang/eval/eval.go:40-77`

```go
copy(frames[len(context.Frames):], result.Diagnostic.Frames) // shallow share
if result.Diagnostic.Owned {
    for i := range result.Diagnostic.Frames { mem.FreeString(a, ...) } // frees shared!
}
slices.Free(a, result.Diagnostic.Frames)
```

Context frames are cloned (`owned`), result tail frames are shared then freed:
the new tail dangles whenever an owned diagnostic already has frames. Safe
only when old `Frames` is empty (common `failure` path). Contrast correct
`attachFrame` (`application.go:170-204`), which never frees old frames. Fix:
drop the free loop, only `slices.Free` the old backing (transfer).

## C4 — concat temporary stored in escaping struct — `lang/expr/expr.go:559-563`

```go
name := "_" + strconv.FormatInt(buffer[:], i, 10) // Solod temp
app.Parameters = slices.Append(p.a, ..., Parameter{Name: name, ...})
```

`freeExpr` (`expr.go:112-130`) frees the `Parameters` backing but never per-
element `Name` strings: lambda params are borrowed source slices, while this
one is an owned concat. In Go the heap string survives; in C the temporary
does not outlive the call (cf. `solod-concat-lifetime`). The correct pattern
is one line away in `pattern.go:344-348` (`ownedText`). Fix: `sourceText(p.a,
name)`, and document `Parameter.Name` as borrowed except section synthetics
(which then must be owned + freed, or keep the existing leak-free-but-dangling
choice explicit).

## C5 — `Invocation.Free` leaks grant names — `cli/cli.go:57-61`

```go
for i := range inv.Grants {
    if len(inv.Grants[i].Names) != 0 {
        slices.Free(mem.System, inv.Grants[i].Names) // backing only, no per-string free
    }
}
```

`appendGrant` (`cli.go:600`) `cloneText`s each name (owned). Correct pairing is
in `cmd/kame/expr_command.go:17-20` (per-element `FreeString` + backing free)
and `program/compile.go:291-295`. Every `--allow-read=/x` leaks one string.
Fix: free each `Names[j]` before freeing the backing.

## C6 — `produce` leaks `commands` on mkdir failure — `program/producer.go:236-244`

```go
if !ok {
    c.Fail(failure(p.Alloc, "FS_ERR", "cannot create output directory"))
    return core.ProducerFailed // commands still local, never freed
}
```

`commands` is transferred only at `producer.go:248` (`entry.Script =
commands`). All sibling error paths (`producer.go:166-168,175-177,186-188,
193-195,201-203,208-210`) free `commands` when non-empty; `effects`/
`writePaths` are covered by defers. Fix: `if commands != "" {
mem.FreeString(p.Alloc, commands) }` before `Fail`.

## H1 — intermediate callables discarded shallow — `lang/eval/forms.go:16`, `values.go:26-27`, `render.go:57-58`, `reference.go:23`, `forms.go:111`

`core.Value.Free` is callable-blind by design (`ownership.go:12-27`); discards
must use `freeCallables`/`freeValuesWithCallables`, transfers use shallow
`freeValues`. These discard with shallow free:

- `body` loop (`forms.go:16`): intermediate lambda/section leaks its scope
  retain. Safe counterpart `application.go:46-52` splits shallow vs deep.
- `stringValue` (`eval/values.go:26-27`), `render.go:57-58`: callable/record/
  bytes stringify failure then shallow free leaks wrapper.
- `reference` loop (`reference.go:23`), `evalText` (`forms.go:111`): same shape.

Fix: `freeCallables` on the discard paths, keeping `freeValues` only for
transfer (already correct in `containers.go`, `operation`, `call`).

## H2 — operations return `invalid()` without `FreeCallable`

`opApply` (`operations/general.go:197-215`), `opConcat`/`opSlice`/`opJoinpath`,
`text.go:opReplace` literal branch return `invalid()` (`operations/common.go:14`,
`Owned:false`) without freeing a temporary/borrowed lambda arg. The caller
(`application.go:88-92`) shallow-frees after `Call`, so the scope leaks. The
protocol (`ownership.go:24-26`) defines success/transfer and pre-Call discard,
but not operation-returned-diagnostic-with-callable. Fix: either operations
`FreeCallable` before `invalid()`, or `operation()` discards deeply on
operation-returned diagnostic.

## H3 — signal-abort path skips handle cleanup — `cmd/kame/execution.go:99-103`

```go
if signal < 0 {
    return 128 - signal // handles + backing never freed
}
```

Second early return (`execution.go:150-153`) runs after `slices.Free(handles)`.
The abort path leaks every live `Handle` and the `handles` backing. Process is
exiting on second signal, so impact is leak-checker noise, not a runtime bug;
either free before return or document as intentional abrupt exit.

## H4 — `string([]byte)` zero-copy aliasing (Go vs C divergence)

- `host/posix/posix.go:156-161` `cloneString` = `string(cloneBytes(...))`: in C
  the string wraps the clone (single owner, freed via `FreeString` in
  `FreeEnvironment` — correct); in Go the conversion copies and the clone
  backing leaks.
- `cmd/kame/expr_command.go:31-33` (`string(readData)` + `defer
  freeCommandBytes(data)` while `text`/`command` live), `format.go:42,62`,
  `inspect.go:113,158`, `source.go:116,152` (`Text: string(data)` + `Data`
  retained, `freeBuildSource` frees `Data` only — correct single-owner borrow
  in C).
- `diagnosticSource` (`execution.go:362-368`): `string(data)` then
  `FreeSlice(data)` while `loaded` source borrows it — same shape, safe only
  under wrap semantics.

No change requested, but any Tracker/asan run under Go will false-positive
here. Pin the contract: `string(bytes)` transfers ownership to the string in
Solod; document at each site.

## M1 — `len != 0` vs unconditional `slices.Free`

Guarded (noise if `Free(nil)` is legal): `cli/cli.go:59-83`,
`program/types.go:97-120`, `program/support.go:41`,
`program/compile.go:261-309`, `host/posix/posix.go:245-252`,
`eval/context.go:141,150`, `eval/values.go:96`, `diagnostic/diagnostic.go:178-192`.

Unguarded on possibly-empty/nil: `operations/collections.go:28`,
`operations/path.go:64,70`, `operations/common.go:25`, `host/queue.go:54`,
`core/update.go:97`, `core/engine.go:430-443`, `host/wasm/memory_host.go:234-235`.

Per `solod-slice-free-after-pop`, a slice popped to length zero still owns its
backing — guarding on `len` leaks. The popped slices in this tree
(`target.go:213`, `pattern.go:283`, `context.go:106`) are already
unconditional; keep that. Recommendation: standardize on unconditional free
(or `!= nil`), never `len != 0`.

## M2 — allocator pairing is correct but fragile

- `eval/forms.go:83` `mem.Alloc[Function](context.Run)` stored in scope and
  freed via `s.Alloc` (`scope.go:99-114`): safe only while `context.Run ==
  scope.Alloc` (true today via `definition.go:341`, `newScope(context.Run)`).
- `freeCallbackState` frees with `c.Run` (`collections.go:28-32`) but
  `ClearOperationState` frees with `Program.Alloc` (`context.go:102-104`):
  same equality assumption; flag if wasm/forwarding ever diverges them.
- `eval/eval.go:21` `failure(mem.System, ...)` on nil context, freed by caller
  with a different allocator on mismatch; `application.go:176` at least keeps
  both sides on System.
- `template/target.go:50-102`: `Capture{Name,Pattern}` borrow caller `text`,
  not `s.Text`; `Free` (`target.go:33-48`) is correct only while caller text
  outlives `Target`. Document the borrow.
- `cloneExpr` (`expr.go:688-706`): `Text`/`Parts`/`Pattern` owned,
  `Reference`/`Parameters`/`Field.Key` shallow borrows of source text. Correct
  while script source outlives the clone; `freeExpr` correctly does not free
  them.
- `format/format.go:90-97` `cloneText` uses `slices.Make[byte]+string` while
  `owned`/`sourceText`/`ownedText` use `mem.AllocSlice[byte]+string`. Single
  idiom preferred.

## Notes verified as non-issues

- `operations/path.go:69` `path.Join(c.Run, values...)`: spread into a non-
  `slices.Append` variadic. `so translate ./operations` succeeds and emits
  `path_Join(c->Run, values)` (`operations.c:965`). The `solod-no-spread-
  variadic` ban covers `slices.Append(a, s, xs...)`; this site is fine.
- `program/*` early `return diagnostic` without `Value.Free` relies on the
  `eval` contract that `Value` is empty when `Diagnostic != ""` — consistent
  across `planning.go:182-183`, `render.go`, `rule_execution.go`.
- `graph.go:86-204` pending/visited aliasing (`visited` takes ownership of
  `pending[cursor].Target`, `pending` freed as array only) is subtle but
  single-free via `FreeStrings`.
- `main.go:106-134` double `session.Free()` on compile failure is safe only
  because `Free` zeroes `*s`; intentional but fragile.

## Recommended fix order

1. C1 (mechanical, one-line), C5 (mechanical), C6 (mechanical).
2. C3 (transfer, no free loop), C4 (`sourceText`), C2 (move semantics for
   `def`).
3. H1/H2 (deep-vs-shallow audit per call site), M1 (unconditional frees),
   M2 (document borrow/allocator assumptions).
4. Re-run `so test ./...` + `CC=clang so test -check=sanitize -panic=abort
   ./...` (`Makefile:56-57`) and the wasm portable check
   (`tools/wasm/check-portable.sh`).
