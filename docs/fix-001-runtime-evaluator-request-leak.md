# Fix 001: Runtime evaluator-request ownership leak

## Status

Open. The runtime supports evaluator host requests while rendering a rule body,
but a successful `read` request leaks tracked allocations when it resumes the
rule producer. The functional behavior is correct; allocator ownership is not.

This document is intentionally a handoff for a follow-up agent. Do not remove
the regression merely to make the suite green. The test is the reproduction.

## Reproduce

From the repository root:

```sh
so test ./runtime
```

The failing regression is
`runtime/test/runtime.go:TestReadOperationResumesDuringRendering`.

It creates `input` containing `abc`, then materializes this rule with a read
grant:

```littlemake
./output :
	@(yield (str (count (read "./input"))))
```

Functional expected result:

- materialization succeeds;
- `output` contains `3`.

Ownership expected result:

- the Solod test allocator reports no outstanding allocations.

Actual result at the time of writing:

```text
TestReadOperationResumesDuringRendering
    memory leak: 6 unfreed allocation(s), 368 byte(s)
```

Use this sanitizer command after every ownership change:

```sh
CC=clang so test -check=sanitize -panic=abort ./runtime
```

Run the normal suite before sanitizer, too. `so test ./...` executes the Solod
tests; `go test ./...` does not provide equivalent behavioral coverage.

## Why the rule must resume

`read` is an evaluator operation, not a recipe shell command:

1. `lib.opRead` calls `request` in `lib/library.go`.
2. `eval.Context.Submit` queues a correlated host request and marks the active
   engine node submitted.
3. `runtime.Program.drainRequests` services the request and calls
   `Engine.Complete` with the file bytes.
4. The engine schedules the same rule node again with that completion.
5. The evaluator consumes it, `count` returns `3`, and `yield` writes output.

The runtime originally treated **every** completion on a rule node as a finished
recipe process. That skipped rerendering and failed with `OUTPUT_MISSING`.

The necessary functional distinction is in `runtime/materialize.go`:

```go
if c.Completion().RequestID != 0 && entry.Script != "" {
    // This is a completed recipe shell process.
}
```

`entry.Script` is set only immediately before a rendered shell recipe is
started. With no script, a completion belongs to an evaluator request and the
producer must continue through normal rendering.

Keep a regression for this behavior even if the implementation later switches
to an explicit request-kind marker.

## Ownership map

All values below are allocator-owned unless stated otherwise.

### Request payload

| Boundary | Owner / action |
| --- | --- |
| `lib.request` | Creates `host.FilePayload`, calls `Context.Submit`, then frees its local payload. |
| `eval.Context.Submit` | Borrows its payload for the duration of the call. |
| `host.Queue.Submit` | Clones payload into its queue item. |
| `host.Queue.Next` | Transfers the queued payload to the caller. |
| `runtime.drainRequests` | Must free the transferred `host.Request` exactly once. |

The queue behavior is covered by
`host/test/host.go:TestQueueReleasesRecordPayload`, which passes. Do not free
the backing array of the queue after `Next`: queue-item movement aliases that
storage and an earlier attempt caused a double free in core tests.

### Completion value

| Boundary | Owner / action |
| --- | --- |
| `runtime.fileCompletion` | Creates completion bytes. |
| `core.Engine.Complete` | Clones the value for its completion queue, then frees the runtime-local original. |
| `core.Engine.accept` | Transfers the queued clone to `Node.Completion`. |
| `core.Engine.run` | Transfers `Node.Completion` to `EngineContext.completion`. |
| `lib.request` | Calls `TakeCompletion`, clones the value for its `eval.Result`, and must release the consumed completion value. |

`lib.request` currently frees the consumed completion value after cloning it.
That change is required but did **not** remove the reported 368-byte residual
leak. Do not assume the completion value is the only owner without rerunning
the regression.

Diagnostics follow the same transfer pattern. A completion diagnostic must be
cloned into the result before freeing its consumed completion-owned copy. Be
careful: diagnostics may borrow parser/evaluator text; only an owned clone may
be freed. See the active `eval-diagnostic-ownership` project learning.

## Confirmed non-fixes

These were tried and must not be reintroduced without a new ownership proof:

1. **Freeing queue backing storage when the final item is popped** caused a
   double free in `core.TestStaleCompletionIsDiscarded`.
2. **Freeing `[]core.RecordField` in host payload builders** caused ASan
   `attempting free on address which was not malloc()-ed`; Solod places those
   composite literals on the stack.
3. **Moving `request.Free` into `completeRequest`** did not change the leak.
   The final state uses a single `request.Free` in `drainRequests` after both
   request dispatch paths.
4. **Removing the read regression** hides a real ownership failure and is not
   an acceptable resolution.

## Investigation checklist

1. Start with only the focused runtime test, if supported by the runner:

   ```sh
   so test -run TestReadOperationResumesDuringRendering ./runtime
   ```

2. Compare allocation lifetime for:
   - `host.FilePayload` and its queue clone;
   - `fileCompletion` byte value;
   - the completion queue slice and node completion;
   - `render`'s temporary context, builder, effect slice, write-path slice,
     input/output value lists, and line spans.

3. Audit all early returns from `runtime.Program.render`. It allocates an
   `eval.Context` manually because a Go-style deferred conditional cleanup is
   unsafe in this Solod codebase. Every return after that allocation must free
   context-owned effects and write paths exactly once.

4. Audit ownership of the `Result` returned by nested operations during resume:
   `read` returns bytes, `count` consumes them, `str` returns a string, then
   `yield` clones the string data into an effect. Each intermediate result must
   be freed by its caller on both completed and waiting paths.

5. Add a small regression before each suspected boundary change. Good focused
   tests include:
   - successful record-payload queue round trip (already present);
   - read completion consumed by a standalone evaluator expression;
   - read completion consumed by nested `count` and `str`;
   - read completion consumed while rendering a rule body (the required test).

6. Do not change the core completion queue's transfer semantics casually. Core
   tests cover stale completions, completion ordering, and cancellation. Run
   `so test ./core` after any core ownership modification.

## Resolution criteria

The fix is complete only when all are true:

```sh
so test ./runtime
CC=clang so test -check=sanitize -panic=abort ./runtime
so test ./...
CC=clang so test -check=sanitize -panic=abort ./lib
CC=clang so test -check=sanitize -panic=abort ./lang/eval
```

The read-resume regression must remain in `runtime/test/runtime.go` and must
pass without a memory-leak report. If a full sanitizer suite is run, note the
known POSIX timeout-classification flake separately rather than attributing it
to this fix.
