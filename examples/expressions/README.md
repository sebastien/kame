# Expressions: describe a value

**Question:** Can a useful program compute a result without running commands or
declaring build rules?

These `.km` programs contain only definitions. Start here even if you have never
used Make or a shell. All commands below run from this directory; `kame` means
the CLI on your PATH (or the absolute path to `build/kame.debug`).

## 1. From an expression to a program

```sh
kame do run --lang expr -c '(map [./notes/hello.txt ./notes/world.txt] (replace ./notes/{name:*}.txt ./public/{name}.txt))'
kame -f ./01-values.km pages
kame -f ./01-values.km publication
```

The first two commands produce the same list:

```text
[./public/hello.txt ./public/world.txt]
```

The third produces a record with a title, that list, and a count of `2`. Paths,
lists, and records are values, not shell text. No `public/` directory is created.

**Change:** Edit `title`, then request `title`, `first-page`, and `publication`
separately. Request `unused`: it fails because `uppercase` requires text, not
`42`. Request `publication` again: it succeeds without evaluating `unused`.

**Understand:** Definitions describe computations. Requesting a result chooses
which computations are needed; textual order is not an execution schedule.

## 2. Compose transformations

```sh
kame -f ./02-functions.km pages
kame -f ./02-functions.km ordered
kame -f ./02-functions.km labels
```

`pages` preserves the source order and duplicate. `ordered` removes the
duplicate and sorts the paths. `labels` is `[HELLO.TXT WORLD.TXT]`.

Read the named `page` function, the value pipe, and the anonymous function side
by side. None introduces a new kind of value.

**Change:** Add another literal source path. Its corresponding page path appears
even if the source file does not exist: this program transforms names, not files.

**Understand:** Functions, patterns, references, and pipes compose value
computations independently of either rules or processes.

## 3. Let the outside world supply a value

```sh
kame -f ./03-resources.km publication
printf 'a new note\n' > ./notes/third.txt
kame -f ./03-resources.km publication
```

The count changes from `2` to `3`. Remove only the `third.txt` you just created
when finished. There are still no generated pages.

Unlike the literal list, `wildcard` records a filesystem dependency. In standalone
expression execution it requires an explicit read grant:

```sh
kame do run --lang expr --allow-read -c '(wildcard ./notes/*.txt)'
```

**Understand:** Resource discovery feeds the same value language; it is not a
special build-only list mechanism.

## File execution

`.km` is the value-program format. The current executable route is `-f FILE`
plus a requested definition, as shown above. Direct `.km` execution is planned;
these examples do not assume its invocation syntax or entry-point policy.

Next: [rules](../rules/README.md), or enter independently through
[Kash](../shell/README.md).
