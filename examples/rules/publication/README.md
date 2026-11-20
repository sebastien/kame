# A tiny publication

These commands match the executable rule file in this checkout. Use a matching
CLI build; older installed versions may use earlier syntax.

**Question:** How do we describe a set of results instead of a fixed sequence of
commands?

This project publishes two notes as uppercase text pages, each with a shared
banner, and assembles them into an index. Its recipes use `cat`, `tr`, `echo`, and `grep`.

Run these commands from this directory with `kame` on PATH, or substitute the
absolute path to the repository's `build/kame.debug`.

## Inspect, then request

```sh
kame do plan ./public/hello.txt
kame do inputs ./public/hello.txt
kame -n ./public/hello.txt
kame ./public/hello.txt
cat ./public/hello.txt
```

The plan and dry run do not execute recipes. The requested page contains:

```text
LITTLE PUBLICATION
HELLO FROM KAME
```

`world.txt` has not been published: it was not needed.

Now request the whole publication:

```sh
kame do plan
kame
kame
kame check
```

The first build prints `rendered` for the remaining page and `assembled` for
the index. The second build prints neither: file outputs are current. The named
`check` task runs whenever requested, while its file prerequisite can stay fresh.

**Understand:** `./public/hello.txt` is a file artifact; `check` is an action.
Explicit paths and bare names communicate that distinction.

## Change one thing

1. Edit `notes/hello.txt`, then run `kame`: only that page and the index render.
2. Edit `banner.txt`, then run `kame`: both pages and the index render.
3. Create `notes/third.txt`, then run `kame`: a new page joins the publication.
4. Edit an unrelated file outside `notes/*.txt`, then run `kame`: no recipe runs.

The extra `rendered` and `assembled` messages expose work without making you
interpret timestamps. Remove only your experimental third note when finished.
The examples' regression test performs these experiments in temporary copies.

`notes/index.txt` is reserved: its output would collide with the assembled
`public/index.txt`. The `pages` definition rejects it with an explicit error.

`sources` is a value. `pages` transforms that value. In the index's `rule`
header, `(index-path)` computes the output and `${pages}` expands the path list
into dependencies. `{name}` connects
each output to its source. In a Kash recipe, `$inputs.0` and `$outputs.0` pass
the first input and output paths. The index passes every dependency to `cat`
with `$...inputs`, so adding a page changes both the graph and the command's
argument list.

**Understand:** A computed value can determine the shape of the work graph.

## The language boundary

The rule headers describe dependencies, and the Kash recipes describe the
commands that produce the files. See [Kash](../../kash/README.md) for standalone
process lessons, or [composition](../../composition/publication/README.md) to
share the publication definitions with a rule file.
