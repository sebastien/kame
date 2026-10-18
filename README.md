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

`kame` uses a Lisp-like expression language that embeds nicely into shell. Its
rule language is close to `make` with fewer annoyances (no shell-clashing `$`,
nor forcing tabs). Rules can be written in *Kash* a modern non-interactive shell
optimised for safety and process coordination, an ideal replacement to bash
fro scripting.

Use a CLI built from matching sources. For older syntax, follow
[MIGRATION.md](MIGRATION.md); formatting does not migrate code.

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

For released binaries, Node/WASM, npm, and verified launcher setup, see
[installation and backends](docs/ref-installation.md).

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
  recipes use `@{ref}` / `@(call)` expansions and run as one script. Direct Kash
  recipes pass typed argv without generating shell text. No `.PHONY` or `.ONESHELL`.
- **Demand-driven computation.** Lazy definitions, dynamic dependencies, streaming,
  and watch invalidation share one engine, informed by
  [Build Systems à La Carte](https://dl.acm.org/doi/10.1145/3236774).
- **Inspectable reuse.** Content signatures and tracked dependencies govern
  freshness. Inspect plans and dry runs before executing; reproducibility still
  depends on the tools and environment you orchestrate.

## Build example

Compile C sources under `src/`, tracking headers in both `src/` and `lib/h/`:

```kame
# Lazy definitions compute the source, header, and output sets.
sources = (wildcard ./src/**/*.c)
headers = (concat (wildcard ./src/**/*.h) (wildcard ./lib/h/**/*.h))
objects = (map (sub ./src/{path:**}.c ./build/{path}.o) sources)

default : @{objects}

# Captures connect each output to its source; all headers are prerequisites.
./build/{path:**}.o : ./src/{path}.c @{headers}
	mkdir -p @(dirname @>)
	@{x/cc} -I ./lib/h -c @< -o @>
```

`wildcard` tracks discovery, `sub` remaps paths, and `@{x/cc}` resolves and tracks
the compiler. The recipe creates the output directory and compiles one source;
changing a declared header invalidates dependent objects.

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
