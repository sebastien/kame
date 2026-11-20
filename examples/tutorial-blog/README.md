# Build a personal blog with kame

Build the CLI from this checkout to run the build file and the examples below.

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
../../build/kame.debug do tools
```

You should see `pandoc` and `python3` with resolved paths. If `pandoc`
is missing, install it first — the tutorial stops without it.

All commands below run from this directory
(`examples/tutorial-blog`).

## 1. Build the site

First, build everything:

```sh
../../build/kame.debug
```

The CLI reports target completion and a final summary. The build produces:

```text
build/index.html
build/author.html
build/posts/hello-world.html
build/projects/this-site.html
build/style.css
build/tools/row.sh
```

Now open the result:

```sh
../../build/kame.debug serve
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

Look at the pattern rule in `Makefile.km`:

```kame
rule ./build/posts/{name}.html : ./content/posts/{name}.md ./templates/post.html
```

`{name}` captures the page name once and reuses it for the input and
the output. The recipe renders the page with pandoc:

```sh
x/pandoc $inputs.0 --template ./templates/post.html -o $outputs.0
```

`$inputs.0` passes the Markdown path, and `$outputs.0` passes the HTML path. Edit the Markdown body, rebuild, and reload: that page
and the index rebuild because both depend on the Markdown file. If only the
body changed, the index's contents remain the same.

Check what kame sees for this page:

```sh
../../build/kame.debug do plan ./build/posts/hello-world.html
../../build/kame.debug do inputs ./build/posts/hello-world.html
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
../../build/kame.debug
```

Reload the index. Notice the new post appears first — the index sorts
by `date` descending. Now compare timestamps:

```sh
ls -l --time-style=full-iso build/posts build/index.html
```

Only `incremental-rebuilds.html` is new; `hello-world.html` and
`this-site.html` kept their timestamps. Changing one input's content rebuilt
that page plus the index, nothing else.

The index rule declares its inputs with tracked glob expressions:

```kame
posts = files ./content/posts/*.md
projects = files ./content/projects/*.md
rule ./build/index.html : ${posts} ${projects} ./templates/header.html ./templates/footer.html ./build/tools/row.sh
```

Adding a `.md` file changes the glob result, so the index
rebuilds. See the full dependency closure with:

```sh
../../build/kame.debug do span --expand ./build/index.html
```

## 4. Meet the generated helper

The index rows are built by a helper script that lives inside
`Makefile.km` and is materialized as `./build/tools/row.sh`:

```sh
../../build/kame.debug do cat ./build/tools/row.sh
```

You will see a short shell script: slug and section from the path,
`title`/`date`/`description` from the front-matter with `sed`, one
`<li>` on stdout. The helper supports only quoted single-line fields as shown
above, not full YAML quoting or multiline values. It escapes `&`, `<`, and `>`
in all three fields so metadata stays literal HTML text. Use simple page
filenames (letters, digits, and hyphens): the fixed `posts`/`projects` sections
and those slugs go directly into link URLs, without general URL/attribute
encoding. The Kash index recipe collects the rows in a temporary directory,
sorts each section in descending date order, and removes the directory with
`defer`. Each source path is passed to the helper as one argument.

```kash
for post in posts
	sh ./build/tools/row.sh $post >> $staging/posts
```

The helper rule declares `./Makefile.km` as an input, so editing the helper
rebuilds it and the index. The rule selects `/bin/sh` for the here-document
and inserts the destination path with `@{outputs.0}`. The quoted here-document
delimiter keeps expressions such as `"$f"` in the generated script.

Projects work the same way through their own pattern rule. Open
`content/projects/this-site.md`, change its description, rebuild, and
notice the index row changes while posts stay untouched.

## 5. Style, serve, and clean

`style.css` is a small hand-written baseline (~20 lines): centered
column, system font, post list styling. It is copied verbatim:

```kame
rule ./build/style.css : ./style.css
```

Edit the CSS, rebuild, reload. Deleting the output and rebuilding
restores it.

You have built a static blog: author bio, posts, projects, and an
auto-generated index, all orchestrated by kame. Serve it with
`../../build/kame.debug serve`, publish `build/` anywhere static,
and remove it with `../../build/kame.debug clean`.

Where to go next: `kame do plan`, `kame do inputs`, and
`kame do span --expand` on any target to see what kame knows before it
builds.
