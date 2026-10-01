# The reveal: maintain a requested computation

**Question:** Is this a fixed script we rerun, or a computation whose current
result we maintain?

The publication lessons expose incremental behavior between CLI invocations.
To experience a persistent reactive engine **today**, run the existing engine
demo from the repository root:

```sh
cd examples/engine
so run ./demo
```

This requires the `so` toolchain. It executes the same scenarios as the engine
tests, not a simulated terminal transcript. Read
[`engine_build.go`](../engine/engine_build.go), especially `Build.Run`, alongside
its output.

## What actually happens

1. The graph exists, but runs no producers before a result is requested.
2. Requesting the root propagates demand backward to its dependencies.
3. Values flow forward; the root publishes a record for three source files.
4. A subscriber observes its current value and completion.
5. The same engine invalidates the source node; its next production contains
   four files.
6. Dependent computations run again, and the subscriber observes invalidation
   followed by revision `2` of the result.

The source producer deliberately models a changed input; this is **not a
filesystem watcher**. It proves persistent demand, invalidation, and replacement
values without requiring a watch-mode CLI. The second demo also demonstrates
streamed values and host-driven resumption.

**Understand:** The program describes a computation graph. An invocation can
materialize a result once; a persistent host can maintain requested results as
tracked resources change. These are two uses of the reactive model, not two
unrelated programming models.

## The live publication experience to add

A user-facing live runner is not currently exposed by the CLI. When it is,
reuse the [composition project](../composition/publication/README.md) rather
than inventing a separate showcase. Keep the index requested while showing
both its current result and graph events.

| Experiment | Observable result |
| --- | --- |
| Edit `notes/hello.txt` | Its page and the index become stale and update |
| Edit `banner.txt` | Both dependent pages and the index update |
| Add a note | Discovery changes the page set and required work |
| Edit an unrelated file | No dependent work runs |
| Cause a recipe failure, then correct it | Failure is visible; the corrected generation recovers |

Show `current -> invalidated -> recomputing -> current` and result revisions,
not just a refreshing output. Each experiment should run in the same engine
session: an external loop repeatedly launching `kame` would not demonstrate
persistent subscriptions.

Source hot replacement is a separate capability from resource invalidation.
Do not promise that changing definitions or rule files updates a running graph
until the live host supports it. Likewise, reactivity is not permission to replay
arbitrary effects: keep deployment an explicit action, not a watched recipe.

The intended moment of recognition:

> I am not executing a fixed list of instructions again. I am maintaining a
> requested computation as its dependencies change.
