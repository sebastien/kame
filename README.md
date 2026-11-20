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

Kame expressions compute typed values. Kash uses those values to pass arguments
directly to functions and executables and to compose process pipelines. Build
recipes use Kash by default. Standalone `.kash` scripts use the same action syntax.

## Quick start

Use a matching native Linux or macOS binary, the Windows launcher bundle, or
the Node.js/WebAssembly package. The Windows bundle and npm package require
Node.js 18 or later. The [installation guide](docs/ref-installation.md) covers
release verification and [building from source](docs/ref-contributing.md).

### Makefile.km

Kame searches for `Kamefile`, `Makefile.km`, `make.km`, and `src/km/main.km`
in that order. Put this in `Makefile.km`. The [bootstrap Makefile](Makefile)
can forward GNU Make goals to Kame.

```kame
task default : ./greeting.txt

rule ./greeting.txt : ./name.txt = str "Hello, " (strip (text (read inputs.0))) "!\n"
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

`inputs.0` selects the first prerequisite. The `= EXPR` body returns text or
bytes for Kame to publish atomically to the file output. The `default` task
runs whenever reached, and its file prerequisite is reused while its inputs
and output have the same content.

## Syntax at a glance

| Syntax | Meaning |
| --- | --- |
| `name = EXPR` | Lazy definition; a bare `name` reads its value. |
| `"text"`, `42`, `true`, `false`, `nil` | Literal values. Only `false` and `nil` are false in conditions. |
| `["a", "b"]`, `{name: "app"}` | List and record. A comma or newline separates items. |
| `f x`, `(x => uppercase x)` | Function call and anonymous function. |
| `paths \|> map f` | Value pipe, equivalent to `map f paths`. |
| `rule ./out : ./in`, `rule (output-path) : ./in` | File rules with a literal or computed output. |
| `task build`, `task cached check`, `service server` | Always-run task, cached task, and managed service. |
| `${name}`, `${f x}` | Substitute a value in a Kame string, path, rule header, or Kash command word. |
| `@{name}`, `@{f x}` | Interpolate a value in a document template or foreign-shell recipe. |
| `inputs.0`, `inputs`, `outputs.0` | First normal input, all normal inputs, and first output in rule context. |
| `{name}`, `{path:**}` | Rule captures: one path segment or a path spanning directories; reuse captures in inputs. |
| `x/cc` | Resolve and track the external `cc` executable in a Kash recipe. |
| `; {shell: "sh"}`, `= EXPR` | Use a foreign shell for the indented recipe, or publish the result of an expression body. |
| `cwd: ./build MODE: release ./app` | Set the directory and environment for one Kash stage. |
| Kash `$name`, `$(command)`, `a \| b` | Pass a typed value, capture stdout, or connect process streams. |

Expressions compute values; templates render text; Kash connects processes.
See the [language reference](docs/skills/kame/ref/language.md) for details.

## Design goals

- **Familiar, not Make-compatible.** Keep targets, prerequisites, and recipes;
  distinguish file artifacts, bare tasks, cached tasks, and services explicitly.
- **Clear language boundaries.** Expression bodies compute artifact content.
  Kash recipes pass typed arguments directly to processes. Foreign-shell
  recipes use `@{EXPR}` interpolation and run as a single script.
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
sources = files ./src/**/*.c
headers = concat (files ./src/**/*.h) (files ./lib/h/**/*.h)
objects = subst ./src/{path:**}.c ./build/{path}.o sources

task default : ${objects}

# Captures connect each output to its source; all headers are prerequisites.
rule ./build/{path:**}.o : ./src/{path}.c ${headers}
	x/cc -I ./lib/h -c $inputs.0 -o $outputs.0
```

`files` tracks discovery, `subst` maps source paths to object paths, and `x/cc`
tracks the compiler. Kame creates the output directory before running the
recipe. Changing a declared header invalidates the dependent objects.

## Find your way

- [Documentation index](docs/README.md): language/CLI references, glossary, and specs.
- [Runnable lessons](examples/README.md): values, rules, Kash, and composition.
- [Migrating from Make](docs/skills/kame/ref/make-migration.md) and
  [idioms and gotchas](docs/idioms-and-gotchas.md).
- [Contributing](docs/ref-contributing.md) and [embedding APIs](docs/ref-embedding.md).
- [Remaining work](TODO.md) and
  [release publication](docs/spec/015-distribution.md#publication).

The name `kame` is *verlan* for `make`: start from the desired result—a makefile
without the quirks—and build the language/system to support it.
