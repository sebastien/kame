# Kash: describe cooperating processes

**Question:** Can commands feel like shell while using the same values and
functions as a Kame value program?

These `.kash` sources follow [the Kash specification](../../docs/spec/017-kash.md).
The CLI now accepts standalone Kash through `do run`, but these complete lessons
remain staged: `01-values.kash` currently reports `EXEC_CANCELLED` when reusing
lazy definitions across statements. Do not execute them with Bash. No build
file or publication project is required.

The invocation for these lessons is:

```sh
kame do run ./01-values.kash
kame do run ./02-pipelines.kash
kame do run ./03-recovery.kash
```

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

**Try now:** Embedded command capture is available in the current CLI:

```sh
kame do run --lang expr --allow-run -c '(let [names ["hello world.txt" "second.txt"]] $(printf "[%s]\n" $names))'
kame do run --lang expr --allow-run -c '(uppercase (strip $(printf "hello\n")))'
```

These exercise the actual expression/process boundary without pretending to run
a standalone `.kash` source. The first prints the two bracketed names; the
second prints `HELLO`. Omitting `--allow-run` denies process execution.

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

Next: [composition](../composition/publication/README.md). For async processes,
setup keys, and lifecycle ownership, continue with the linked specification
rather than assuming Bash background-job semantics.
