```

       ▄▄▄
     ▄▒▒▒▀
    ░░░▀  ▄    ▄░▒▄▄                   ▄░▒▄▄
   ░░░▀▄▄▄▓░  ▓▒░░░░▄   ▄░░▀▄▄ ▄▄▄   ▄█▀▄░░▓▌
 ▄▒▒▒▒░░▒▓▀  █▒▒▀▐░▒▓ ▄▒▒▒▒░░█▄░░█   ██▓█▀▒▒▌
 ▓▓▓▓▀▒▒█    █▓▌ ▒▒▓▌ ▓▓▓▓▀▒▒▄▒▒▒█  ▐█▓▌▀▀▀
▓██▓▀ ▓▓▓▄█ ▄██▄▄▓▀█▌▓██▓▀ ▓▓▓█▓▓▓▄█▐██▄▄▓▓▄▄
▀██▌ ▐██▀▀  ▐▀▀▀▀▀  ▀▀██▌ ▐██▀▀██▀▀ ▐▀▀▀▀▀▀▀▀
      ▀▀                   ▀▀  ▀▀
```

# Kame

`kame` (*kah-meh*) is a spiritual successor to `make`: a general-purpose tool
for builds, tests, provisioning, and deployment, with familiar dependency rules
and fewer shell/expression clashes. It combines file artifacts, always-run and
cached tasks, and managed services on one incremental engine.

Use its Lisp-like expressions, value scripts, templates, build rules, or typed
Kash process scripts independently, then compose them as a project grows.

## Get started

From a checkout, install Go 1.26.0+, Solod 0.4.0, GNU Make, Clang, and Python 3
as described in the [contributor guide](docs/ref-contributing.md), then build:

```sh
CC=clang make dist
export PATH="$PWD/dist:$PATH"
kame --version
kame do run --lang expr -c '(join " " ["hello" "world"])'
# "hello world"
```

For released binaries, the Node/WASM pair, npm, and verified launcher setup,
see [installation and backends](docs/ref-installation.md). A version in the
source tree does not establish that its release has been published.

### Your first build

In an empty directory, save this as `Makefile.kmk`. Indent the recipe with a tab:

```kame
default : ./greeting.txt

./greeting.txt : ./name.txt
	@(yield! (str "Hello, " (strip (text (read @<))) "!\n"))
```

```sh
printf 'Ada\n' > name.txt
kame -n                    # inspect without writing greeting.txt
kame                       # build the default target
cat greeting.txt           # Hello, Ada!
kame                       # reuse the unchanged file output
printf 'Lin\n' > name.txt
kame                       # rebuild: greeting.txt now contains Hello, Lin!
```

`./greeting.txt` is a file target, `default` is a bare task, and `@<` selects
the first input. `yield!` supplies the output bytes without an external command.
File freshness compares content, not timestamps; the bare task is always reached,
but its unchanged file prerequisite can be reused.

## Design goals

- **Familiar, not Make-compatible.** Keep targets, prerequisites, and recipes;
  distinguish file artifacts, bare tasks, cached tasks, and services explicitly.
- **Clear language boundaries.** Expressions compute values; external-shell
  recipes use `@(...)` expansions and run as one script. Direct Kash recipes
  pass typed argv without generating shell text. No `.PHONY` or `.ONESHELL`.
- **Demand-driven computation.** Lazy definitions, dynamic dependencies, streaming,
  and watch invalidation share one engine, informed by
  [Build Systems à La Carte](https://dl.acm.org/doi/10.1145/3236774).
- **Inspectable reuse.** Content signatures and tracked dependencies govern
  freshness. Inspect plans and dry runs before executing; reproducibility still
  depends on the tools and environment you orchestrate.

## Build example

For a C project with sources/headers under `src/` and includes under `lib/h/`:

```kame
# Definitions lazily bind names to expressions/values
cc = "gcc"
include-path = ./lib/h

# Paths and wildcards are first class: wildcard expansions are values
sources-c = (wildcard ./src/**/*.c)
headers = (wildcard ./src/**/*.h)

# Every definition RHS is an expression; combine referenced lists explicitly
sources = (concat sources-c headers)

# Pattern substitution remaps paths, and map applies it to every source
objects = (map (sub ./src/{path:**}.c ./build/{path}.o) sources-c)

# Default rule will be triggered when no argument is given
default : build

# Inputs can be paths, target names, or values from expression expansions
build : @(objects)
	echo @(count objects) products built from @(count sources) inputs

# Target templates capture the matching target and expand it in inputs
./build/{path:**}.o : ./src/{path}.c @(headers)
	# No clash with the shell's $, more natural shorthands @< inputs @> outputs.
	mkdir -p @(dirname @>)
	@(cc) -I @(include-path) -c @< -o @>
	echo compiled @< to @>

archive : @(sources)
	tar cvfz archive-$(date +%F).tar.gz @<*
	echo created archive of size $(du -shc @<*)

# Shows statistics about this project.
stats :
	echo @(count sources) input files
	du -shc @(sources)
	echo @(count objects) object files
	du -shc @(filter ([path] (exists? path)) objects)
```

## Find your way

- [Documentation index](docs/README.md): language/CLI references, glossary, and specs.
- [Runnable lessons](examples/README.md): values, rules, Kash, and composition.
- [Migrating from Make](docs/skills/kame/ref/make-migration.md) and
  [idioms and gotchas](docs/idioms-and-gotchas.md).
- [Contributing](docs/ref-contributing.md) and [embedding APIs](docs/ref-embedding.md).
- [Remaining work](TODO.md) and
  [release publication](docs/spec/015-distribution.md#manual-publication).

The name `kame` is *verlan* for `make`: start from the desired result—a makefile
without the quirks—and build the language/system to support it.
