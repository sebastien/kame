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
nor forcing tabs).

`kash` is an improve non-interactive shell that can be used for rules. Kash
is optimised for safety and process coordination, an ideal replacement for bash
glueing and coordinating processes.

## Quick start

`kame` is available in WASM through NPM, which is the easiest way to run
it on all platforms:

```sh
bun x @ssebastien/kame@0.3.1 --version
# or, with npm
npm x --yes @ssebastien/kame@0.3.1 -- --version
```

But for everyday use (on Windows/MacOS/Linux/BSD), you will want to get the
standalone APE from the
[v0.3.1 release](https://github.com/sebastien/kame/releases/tag/v0.3.1):

```sh
curl -fLO https://github.com/sebastien/kame/releases/download/v0.3.1/kame.com
chmod +x kame.com
./kame.com --version
```

Neither route needs a compiler toolchain. The npm package runs on Node.js 18+;
the APE covers Linux, macOS, FreeBSD, NetBSD, and Windows (rename it to
`kame.exe`).

### Makefile.kmk

`kame` offers a [bootstrap Makefile](Makefile) for Make users. It automatically
discovers `Makefile.kmk`, `make.kmk`, or `src/kmk/main.kmk`. A `Makefile.kmk`
looks like this:

```kame
default : ./greeting.txt

./greeting.txt : ./name.txt ; [shell: kame]
	(str "Hello, " (strip (text (read @<))) "!\n")
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
the first input. A Kame body returns text or bytes; the rule atomically publishes
that content to its single file output, without an external command.
File freshness compares content, not timestamps; the bare task is always reached,
but its unchanged file prerequisite can be reused.

## Syntax at a glance

| Syntax | Meaning |
| --- | --- |
| `name = EXPR` | Lazy definition; a bare `name` reads its value. |
| `"text"`, `42`, `:true`, `:false`, `:nil` | Literal values; only false and nil are false in conditions. |
| `["a" "b"]`, `[name: "app"]` | List and record; items are whitespace-separated, not comma-separated. |
| `(f x)`, `([x] (uppercase x))` | Function call and anonymous function. |
| `(files \| map f)` | Value pipe: equivalent to `(map f files)`, not a process stream. |
| `./out : ./in`, `build : ./out` | File rule and always-run bare task; indent the body below the header. |
| `task build : ./in`, `service server :` | Cached task and managed service. |
| `@{name}`, `@(f x)` | Read a value or evaluate a call in a string/template, including shell recipes. |
| `@<`, `@<*`, `@>` | First normal input, all normal inputs, first output in rule context. |
| `{name}`, `{path:**}` | Rule captures: one path segment or a path spanning directories; reuse captures in inputs. |
| `@{x/cc}` | Resolve and track the `cc` executable in a shell recipe. |
| `; [shell: kame]`, `; [shell: kash]` | Header metadata: compute artifact content or run typed Kash commands instead of shell text. |
| Kash `$name`, `$(command)`, `a \| b` | Pass a typed value, capture stdout, or connect process streams. |

Expressions compute values; templates render text; Kash connects processes.
See the [language reference](docs/skills/kame/ref/language.md) for details.

## Design goals

- **Familiar, not Make-compatible.** Keep targets, prerequisites, and recipes;
  distinguish file artifacts, bare tasks, cached tasks, and services explicitly.
- **Clear language boundaries.** Expressions compute values; external-shell
  recipes use `@{ref}` / `@(call)` expansions and run as one script. Direct Kash
  recipes pass typed argv without generating shell text. Kame bodies compute
  artifact content directly. Template expansion is read-only. No `.PHONY` or `.ONESHELL`.
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
sources = ./src/**/*.c
headers = (concat ./src/**/*.h ./lib/h/**/*.h)
objects = (sub ./src/{path:**}.c ./build/{path}.o sources)

default : @{objects}

# Captures connect each output to its source; all headers are prerequisites.
./build/{path:**}.o : ./src/{path}.c @{headers}
	mkdir -p @(dirname @>)
	@{x/cc} -I ./lib/h -c @< -o @>
```

Glob paths track discovery, `sub` remaps path lists, and `@{x/cc}` tracks the
compiler. The recipe creates the output directory and compiles one source;
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
