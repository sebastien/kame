# Build a personal blog with kame

In this tutorial we will build a minimalist personal blog — an author
page, blog posts, and projects — from Markdown files into plain
HTML and CSS. Along the way we will use file rules, target templates,
wildcards, generated helpers, and index collection with kame.

We start from a profile page and scale to a complete blog, following
the [Alpine](https://vercel.com/templates/nuxt/alpine) layout: a hero,
a posts list, and a projects list. No JavaScript, no framework.

You need the `kame` binary built (`make build/kame.debug` from the
repository root), plus `pandoc` and `python3` on your `PATH`. Check
with:

```sh
../../../build/kame.debug do tools
```

You should see `pandoc` and `python3` with resolved paths. If `pandoc`
is missing, install it first — the tutorial stops without it.

All commands below run from this directory
(`examples/tutorial-blog`).

## 1. Build the site

First, build everything:

```sh
../../../build/kame.debug
```

You will see one line per target and a final summary:

```text
[./build/index.html] complete
[./build/author.html] complete
[./build/posts/hello-world.html] complete
[./build/projects/this-site.html] complete
[./build/style.css] complete
[default] complete
Summary: ... targets complete
```

Now open the result:

```sh
../../../build/kame.debug serve
```

Visit <http://localhost:8000>. You will see the hero, one post, one
project, and an About page. Notice that `build/` contains only static
files: open `build/index.html` in an editor and confirm the posts list
is plain `<li>` markup.

Run the build again. Notice it finishes without rewriting anything —
compare `ls -l build/posts build/index.html` before and after.

## 2. Render one page from Markdown

Open `content/posts/hello-world.md`. Every content file uses the same
trimmed front-matter:

```md
---
title: "Hello, world"
date: "2026-09-20"
description: "Why I switched my blog to plain Markdown and kame."
---
```

Look at the pattern rule in `Makefile.kmk`:

```kame
./build/posts/{name}.html : ./content/posts/{name}.md ./templates/post.html
```

`{name}` captures the page name once and reuses it for the input and
the output. The recipe renders the page with pandoc:

```sh
pandoc @< --template ./templates/post.html -o @>
```

`@<` is the first input (the `.md` file), `@>` is the first output
(the `.html` file). Edit the Markdown body, rebuild, and reload: only
that page changes.

Check what kame sees for this page:

```sh
../../../build/kame.debug do plan ./build/posts/hello-world.html
../../../build/kame.debug do inputs ./build/posts/hello-world.html
```

## 3. Add a second post and watch the index update

Create `content/posts/incremental-rebuilds.md` with this content:

```md
---
title: "Incremental rebuilds"
date: "2026-09-27"
description: "Changing one file rebuilds only what is needed."
---

Edit one file, rebuild, and only that page plus the index update.
```

Now rebuild:

```sh
../../../build/kame.debug
```

Reload the index. Notice the new post appears first — the index sorts
by `date` descending. Now compare timestamps:

```sh
ls -l --time-style=full-iso build/posts build/index.html
```

Only `incremental-rebuilds.html` is new; `hello-world.html` and
`this-site.html` kept their timestamps. Touching one input rebuilt
that page plus the index, nothing else.

The index rule declares its inputs with the stdlib:

```kame
posts = (wildcard ./content/posts/*.md)
./build/index.html : @(posts) @(projects) ./templates/header.html ./templates/footer.html ./build/tools/row.sh
```

Adding a `.md` file changes the `wildcard` result, so the index
rebuilds. See the full dependency closure with:

```sh
../../../build/kame.debug do span --expand ./build/index.html
```

## 4. Meet the generated helper

The index rows are built by a helper script that lives inside
`Makefile.kmk` and is materialized as `./build/tools/row.sh`:

```sh
../../../build/kame.debug do cat ./build/tools/row.sh
```

You will see a short shell script: slug and section from the path,
`title`/`date`/`description` from the front-matter with `sed`, one
`<li>` on stdout. The index recipe calls it once per file and sorts:

```sh
for f in @(posts); do sh ./build/tools/row.sh "$f"; done | sort -r
```

Kame tracks the helper itself (`./build/tools/row.sh :
./Makefile.kmk`), so editing the script rebuilds the index. Shell
quotes stay single-`$` here — unlike Make, kame never expands `$`, so
`"$f"` is written as-is.

Projects work the same way through their own pattern rule. Open
`content/projects/this-site.md`, change its description, rebuild, and
notice the index row changes while posts stay untouched.

## 5. Style, serve, and clean

`style.css` is a small hand-written baseline (~20 lines): centered
column, system font, post list styling. It is copied verbatim:

```kame
./build/style.css : ./style.css
```

Edit the CSS, rebuild, reload. Deleting the output and rebuilding
restores it.

You have built a static blog: author bio, posts, projects, and an
auto-generated index, all orchestrated by kame. Serve it with
`../../../build/kame.debug serve`, publish `build/` anywhere static,
and remove it with `../../../build/kame.debug clean`.

Where to go next: `kame do plan`, `kame do inputs`, and
`kame do span --expand` on any target to see what kame knows before it
builds.
