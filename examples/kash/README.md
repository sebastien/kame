# Kash: describe cooperating processes

**Question:** Can commands feel like shell while using the same values and
functions as a Kame value program?

Run these `.kash` lessons with Kame, not Bash. No build file, network access,
credentials, or publication project is required. Use a matching Kame build and
ordinary POSIX tools. Streaming topology and live operands require POSIX.
`T013-05` runs every lesson from isolated copies on native and WASM/Node;
`T013-04` and `T017-23` retain their focused checks.

From this directory, with `kame` on PATH:

```sh
kame do run ./01-values.kash
kame do run ./02-pipelines.kash
kame do run ./03-recovery.kash
kame do run ./04-operations.kash
kame do run ./05-batch.kash -- --prefix checked
kame do run ./06-environment.kash
kame do run ./07-concurrency.kash
kame do run ./08-streaming.kash
kame do run ./09-workers.kash
kame do run ./10-publication.kash
```

`04-operations.kash` adds status validation, finite retry and a temporary
workspace with invocation-scoped `defer`; see the
[operational cookbook](../../docs/kash-cookbook.md) for its contracts and limits.

## 1. Commands receive values

Read `01-values.kash`. `printf` receives two arguments from `$names`, including
one argument containing a space:

```text
[hello world.txt]
[second.txt]
```

The next command applies the ordinary Kame `label` function and prints the
uppercase names. There is no shell word splitting, implicit globbing, or
reparsing of values as commands. `"$names"` would be an error: a quoted word
cannot splice a list.

**Try now:** Capture commands inside a value expression:

```sh
kame do run --lang expr --allow-run -c '(let [names ["hello world.txt" "second.txt"]] $(printf "[%s]\n" $names))'
kame do run --lang expr --allow-run -c '(uppercase (strip $(printf "hello\n")))'
```

These expressions return captured strings, displayed with quotes and escaped
newlines. The second returns `"HELLO"`. Omitting `--allow-run` denies process
execution.

**Change:** Put `a; echo surprise` in the names list. It should remain one
literal argument, not execute another command.

**Understand:** Kash adds process syntax, not a second value language. A granted
executable is not sandboxed; safe argument handling is not process isolation.

## 2. Two meanings of composition

Read `02-pipelines.kash`. Both initial commands should print:

```text
hello
world
```

The first connects three running processes. The second transforms a list with
the Kame value pipe and then starts one process. Capturing the process pipeline
produces the string `hello\nworld\n`, whose count is `12`; the newline is not
silently stripped. Redirection explicitly writes `report.txt`.

**Change:** Reverse the source list. Observe which stages preserve order and
which deliberately discard it. Remove only the generated `report.txt` afterward.

**Understand:** Value transformations and process streams are complementary,
but a value pipe is not a Unix byte stream.

## 3. Recovery has a meaning

Read `03-recovery.kash`. A grep non-match is accepted by `CMD ?`; a fallback
command is selected by `CMD ? ELSE`; a fallback value is selected by `??`.
Neither recovery form makes capability denial or cancellation successful.

**Change:** Add `needle` to a temporary copy of `sample.txt`. The fallback
message disappears. Change `mode` to exercise the failing branch deliberately.

**Understand:** Failure, absent values, and unselected computations are
different situations, not one generic shell exit-code trick.

## 4–10. Local workflows

| Source | Workflow | What to observe / change |
| --- | --- | --- |
| `04-operations.kash` | Validate statuses and retry an idempotent probe | Only status 75 retries; private staging is removed by `defer`. |
| `05-batch.kash` | Label a batch and drain a finite work queue | Change `--prefix`; `for` visits typed values, while `while` runs a fresh command probe. |
| `06-environment.kash` | Run a configured tool in private staging | The final stage override wins; the next stage still sees the original record. No real secrets are used. |
| `07-concurrency.kash` | Run independent checks and produce a background report | Explicitly demanding a lazy handle starts it; `await` is repeat-safe. Deferred checks see background work joined. |
| `08-streaming.kash` | Split an event log into errors and normal events | Add another ERROR line; the filter removes selected records from the main stream, and broadcast retains a full audit copy. |
| `09-workers.kash` | Distribute complete records to two workers | Odd/even records go to different long-running branches; `each` starts one command per record. Strict merge ordering is explicit. |
| `10-publication.kash` | Generate, validate, compare, and publish a report | JSON escaping is handled by `json`; live operands compare streams without capture. Existing destinations are never overwritten. |

`02-pipelines.kash` intentionally leaves `report.txt` in the current directory
and overwrites it on rerun. Other lessons clean only freshly created private
workspaces. `10-publication.kash` publishes within its private workspace, then
cleans it; adapt the destination deliberately to retain a real artifact.

The scripts have executable `kame do kash` shebangs; with Kame on PATH and an
`env` supporting `-S`, `./05-batch.kash -- --prefix checked` also works.
The `--` separates script arguments from Kame's interpreter options.
Pass program arguments after `--` when using `kame do run`.

These examples teach common workflows, not every operator policy. Lossy queues,
telemetry, and failure collection are covered in the
[Kash reference](../../docs/skills/kame/ref/kash.md) and dedicated runtime tests.
Native executables are not sandboxed by Kash capability grants.

Next: [composition](../composition/publication/README.md).
