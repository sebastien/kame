# A tiny publication

**Question:** How do we describe a set of results instead of a fixed sequence of
commands?

This project publishes two notes as uppercase text pages, each with a shared
banner, and assembles them into an index. It needs only `mkdir`, `cat`, `tr`,
`echo`, and `grep`; no publishing toolchain or downloaded dependencies.

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
the index. The second build prints neither: file outputs are current. The bare
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

`sources` is a value. `pages` transforms that value. The index's rule header
uses the result as dependencies. `{name}` connects each output to its source;
`@<` and `@>` supply the first input and first output. The index also evaluates
`@(pages)` in its recipe, so changes to the page set change the rendered command
as well as the declared dependencies. `@<*` is the shorthand for all inputs;
the explicit value here makes the changing set part of the recipe too.

**Understand:** A computed value can determine the shape of the work graph.

## The language boundary

Rule recipes currently run as one ordinary shell script. They are not Kash.
`@(expression)` enters Kame's value language; shell redirection and `tr` remain
shell operations. See [Kash](../../shell/README.md) for the independent process
language, or [composition](../../composition/publication/README.md) for the
shared value program and rule file.
