# One publication, three descriptions

**Question:** What happens when values, dependencies, and processes describe
different aspects of the same work?

| Source | Describes | Status |
| --- | --- | --- |
| `publication.km` | The publication's source and output sets | Runnable through `-f` |
| `Makefile.kmk` | How the requested files are produced | Runnable |
| `publish.kash` | Build, validate, and summarize with processes | Staged; standalone Kash required |

Commands run from this directory with `kame` on PATH, or the absolute path to
the repository's `build/kame.debug`.

## First, compute without publishing

```sh
kame -f ./publication.km publication
kame -f ./publication.km pages
```

You get a record and a list of page paths. You do not get a `public/` directory.
The value program is useful on its own: calculating outputs is not producing
them.

## Then, reuse the value program unchanged

```sh
kame do inputs ./public/index.txt
kame do plan
kame
kame do cat ./public/index.txt
kame -f ./Makefile.kmk publication
```

The rule file includes `publication.km` directly. Its `pages` definition is now
used to discover dependencies. The same `publication` value is still available
through the combined source. No JSON serialization, generated build file, or
second implementation of the page-name transformation is needed.

**Change:** Add `notes/third.txt`. Request `publication`, then build. The source
set, page set, graph, and output all grow from the same definition.

**Understand:** A value program can describe part of a meta-program: a value
determines which computations must be orchestrated.

## Finally, orchestrate processes

Read `publish.kash`. Once standalone Kash is supported, its invocation will be:

```sh
kame do run ./publish.kash
```

It invokes the build, validates it, then summarizes its files. Its definitions
and argument expansions use the same expression language as `publication.km`.
Its process pipe is different from the value pipe in the expression lessons.

**Boundary:** `include ./publication.km` is native source composition in the
rule program. The `kame` commands in `publish.kash` are subprocesses, each with
its own invocation and engine. The current Kash specification does not define
an include/import facility or an in-process rule invocation. This example does
not imply that those subprocesses share a live reactive graph.

Three views help explain the complementarity:

```text
Values:        source paths -> page paths -> publication record
Dependencies:  note + banner -> page; pages -> index
Processes:     build, then validate, then stream a summary
```

You can use each entrance independently. Combining them adds orchestration,
not a requirement to learn all three before doing anything useful.

Next: [the reactive reveal](../../reactive/README.md).
