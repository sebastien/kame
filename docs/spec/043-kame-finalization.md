# Kame Scoped Finalization

## Purpose and scope

`finally!` establishes an explicit execution scope in the Kame value language.
It runs a body callable and then a cleanup callable on success, failure, timeout
or supported graceful cancellation. It is not error recovery, resource
acquisition, file ownership, output publication or rollback.

This contract refines [evaluation](005-evaluation.md),
[standard operations](007-library.md), [diagnostics](011-diagnostics.md) and
[conformance](013-tests.md). It adds no Kash block syntax, `defer!` alias,
ambient finalizer registration, temporary-resource API or runtime output inventory.

## Public operation

```kame
(finally! BODY CLEANUP)
(finally! [timeout-ms: MILLISECONDS] BODY CLEANUP)
```

`BODY` and `CLEANUP` are callable values accepting zero arguments. Existing
lambda syntax supplies them without evaluating their bodies prematurely:

```kame
(finally! [timeout-ms: 2000]
  ([] "result")
  ([] (out! "cleanup\n")))
```

This expression emits `cleanup` and returns `"result"` when both callbacks
complete successfully. The cleanup value is ignored, including nil or false.
Body truthiness is not a success test: nil and false are successful body values.

`finally!` is a callable standard control-effect operation, not a reserved
special form. Ordinary lookup, aliases and explicit function application retain
the same behavior. Its operands evaluate left to right under existing rules.
Resolving a callable is distinct from invoking it; the scope does not protect
effects performed while computing its operands or acquiring resources earlier.

Validate call arity, callback callability and acceptance of zero arguments, and
all options before invoking either callback. Invalid operands use `EXPR_INVALID`
at the offending operand. An explicit options operand must be a record; `[]`
is a list, not an empty options record. The only option is `timeout-ms`, a positive
integer from 1 through 60000 inclusive, defaulting to 5000 when omitted. Unknown
or duplicate keys, nil, booleans, floats, zero, negatives and larger values fail
without printing rejected values. The timeout applies to finalization, not the
body's normal execution. There is no unbounded spelling or new CLI flag.

## Entry, lifetime and completion

A demanded call validates its execution context and arms its finalizer before
invoking `BODY`. A failure before arming runs neither callback. Each reached
scope invokes `CLEANUP` once logically, unless its deadline or forced shutdown
prevents invocation; suspension/resumption does not count as another invocation.
Unused lazy definitions and unselected branches acquire no scope and run nothing.
Retain the validated callbacks and timeout once at entry; do not recompute them
from changed dependencies while unwinding that scope.

The scope retains its callback bindings, accepted body result or failure,
execution context and ownership interest until finalization completes. Merely
freeing an allocator object is not execution of a finalizer. Normal evaluation
cannot publish the body's result or propagate its terminal outcome before this
scope has finished finalization.

Each scope belongs to an evaluation generation and dynamic call path. Reactive
invalidation, watch restart and retry must finish the old scope's teardown before
starting its replacement. Late completions cannot revive an old scope or replay
accepted effects. Successful cached-result reuse does not reacquire a scope or
rerun either callback; armed scopes and callbacks are not persistent cache data.
An unsuccessful finalization cannot publish a successful cached evaluation.

Ordinary materialized body values retain their normal ownership restrictions.
An open stream cannot escape as a deferred scope lifetime: reject that body
result with `EXPR_INVALID`, release its owned work and finalize the scope. An
open cleanup result is likewise an error, not permission to skip finalization.
Returning a pathname does not extend the lifetime of anything cleanup removes.

## Work ownership and ordering

On successful body completion, join body-scope asynchronous work before cleanup.
An unhandled failure in that work becomes a body failure. On body failure,
timeout or cancellation, cancel and reap body-scope work before cleanup can
access the same resources. Cleanup then runs in a separate child execution
scope; its own asynchronous work must finish or be cancelled and reaped before
finalization completes. Nothing is implicitly detached.

Only work owned by the scope is affected. Referencing or awaiting an existing
handle does not adopt its process. Shared dependency nodes retain their original
ownership; release this scope's interest rather than cancelling another
consumer's work. Nested finalizers unwind inside-out. Independent scopes do not
gain a global execution order or ownership of each other's resources.

## Phases and effects

The operation is execution-only. Planning, input/output resolution, source
selection, template/recipe rendering and deferred-effect-only contexts reject
it with `PHASE_INVALID` before invoking either callback, even if the callbacks
look pure. It cannot bracket a later external-shell recipe. A host without the
graceful-finalization driver reports `FEATURE_UNSUP` before arming the scope.

Both callbacks retain the caller's grants, cwd, environment, dependency tracking
and applicable host limits. `finally!` grants no filesystem, process, network or
environment authority. Callback-dependent behavior remains tracked; its
control-effect category is not a declaration that either callback is pure.

Within a supported execution context, the ordering is:

1. Evaluate the body using ordinary effect timing and ownership rules.
2. Join or cancel/reap body-owned work according to its outcome. Settle accepted
   effects before cleanup; handle unaccepted buffered intents under the existing
   success/failure policy. Do not turn discarded intents into committed effects.
3. Invoke cleanup through its own execution effect channel. A successful cleanup
   commits its authorized effects even when the saved body outcome is failure.
4. Settle cleanup-owned work and accepted effects, then return the saved body
   value or report the terminal outcome.

Previously committed effects are never rolled back. Callback failure does not
retroactively commit its unaccepted effects. No cleanup effect may be silently
lost merely because the enclosing body failed. A context that can only collect
effects for a later recipe cannot provide this ordering: reject it rather than
flushing unrelated queues or changing `write!`/`yield!` timing. Ordinary operation
and recipe effect semantics outside the scope are unchanged.

Parsing and formatting never execute callbacks. Dry-run may validate available
operands but invokes neither callback, creates no scope-owned host resources
and submits no cleanup requests. It does not manufacture a successful body
value; work requiring that unavailable value remains deferred under dry-run rules.

## Finalization deadlines and cancellation

Use the host's monotonic clock. On a normal successful body exit, the configured
budget begins when cleanup starts; body execution and its successful joins remain
subject to the ordinary invocation timeout. Nested scopes entered during cleanup
inherit the remaining cleanup deadline, and may only shorten it.
That enclosing deadline covers their bodies as well as their finalizers.

When failure, timeout or cancellation exits several armed scopes, establish one
unwind deadline from the configured budget of the outermost scope being exited.
It begins when that unwind starts, including body-work quiescence, not separately
when each callback starts. Finalize inner scopes first. Each also retains its
own configured limit starting when its cleanup callback starts, capped by the
remaining shared deadline. No scope can extend that deadline, even if
its own timeout is larger. A locally handled error does not itself exit an outer
scope or run that outer scope's finalizer.

An already active enclosing cleanup deadline or an earlier host shutdown
deadline further caps the budget. Repeated cancellation and finalizers nested
inside cleanup cannot restart the clock. A body timeout does not automatically
make the cleanup budget zero: finalization uses this separate deadline, while
preserving the original body timeout or cancellation outcome.

Only the designated cleanup work is allowed to proceed despite the saved
cancellation of its body. Normal work stays cancelled, no retry or later source
fragment starts, and grants and unrelated host limits remain enforced. Cleanup
is not a general cancellation shield offered to arbitrary concurrent work.

At deadline exhaustion, stop launching cleanup actions, cancel pending cleanup
requests and process graphs, and perform mandatory host close/reap handling.
Service the deadline while awaiting host requests or backpressured sinks, not
only between callback expressions.
Native public output must not leave a buffered flush outside this bound. A
timed-out sink must not be retried indefinitely while reporting the failure or
disposing the invocation; bytes already accepted by the sink are not rolled back.
Report `FINALIZE_TIMEOUT`, including when an outer finalizer could not start
because an inner one consumed the shared budget. Not every finalizer is
guaranteed to run when a budget is exhausted. Reaping follows the host's safety
contract; the callback budget is not a hard wall-clock guarantee against an
unresponsive OS operation or host failure.

Graceful CLI shutdown and embedding cancellation/disposal must retain the event
driver and required state through finalization. Signal handlers only notify;
they never invoke user code. Forced escalation, process crash, SIGKILL and fatal
loss of execution state may abandon callbacks. No durable recovery or cleanup
after those events is promised.

## Results and diagnostics

| Body outcome | Cleanup outcome | Result |
| --- | --- | --- |
| Success | Success | Return the body value |
| Success | Failure or cleanup timeout | Fail with the cleanup diagnostic |
| Failure or body timeout | Success | Preserve the body diagnostic |
| Failure or body timeout | Failure or cleanup timeout | Preserve the body diagnostic and add cleanup context |
| Cancellation | Any supported cleanup outcome | Preserve cancellation and add cleanup failure context when needed |

Preserve the primary diagnostic's code, source/span, message and cause. For an
already failing body, retain cleanup code, message and available authored
location through existing `notes`/`related` fields, in unwind order; do not
replace the primary cause, invent a location or serialize captured output.
Cleanup-only failures retain their own normal diagnostic structure. Frames
identify the `finally!` call and cleanup callback without exposing their values.

Capability denial and cancellation remain unsuppressible control outcomes,
including when first raised during cleanup. Retaining an earlier ordinary body
diagnostic as primary must not let an enclosing recovery form swallow a cleanup
capability denial or cancellation. A cancellation that arrives during cleanup
retains the appropriate cancellation/signal exit behavior even if a body
diagnostic was established earlier. `finally!` itself never converts failure to
success and never automatically retries either callback.

## Acceptance

- Validate all operands and options before either callback, including calls
  through aliases and `apply`; preserve normal lambda and formatter grammar.
- Return body values, including nil and false, only after successful cleanup;
  ignore cleanup values and preserve the outcome table and secondary locations.
- Cover body failure, cleanup failure, both failing, capability denial and
  cancellation during either callback without suppressing protected outcomes.
- Verify default/range validation, shorter inner budgets, longer inner budgets
  capped by the outer deadline, exhausted outer finalizers and repeated signals.
- Observe normal joins and failure/cancellation reaping before cleanup, cleanup
  joins before return, and isolation from existing handles and shared dependencies.
- Suspend/resume at each host boundary without rearming, double cleanup or
  replay; invalidate/retry with fresh scopes only after old teardown completes.
- Verify ordered accepted effects, discarded unaccepted intents and cleanup
  effects after body failure; refuse deferred-only contexts before callbacks.
- Parsing, formatting, planning and dry-run perform no cleanup or resource
  acquisition; unavailable dry-run body values are not fabricated.
- Exercise native and forwarded WASM graceful shutdown/disposal, backpressure,
  stale completions, timeout, unavailable host support and repeated allocator-
  tracked/sanitized lifetimes using harmless callbacks and fake processes.
