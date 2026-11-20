# Migrating from GNU Make to Kame

Move one target and its prerequisites at a time. Keep the GNU Make
`Makefile` while adding a Kame program in `Makefile.km`. Kame searches for
`Kamefile`, `Makefile.km`, `make.km`, and `src/km/main.km` in that order.

## Map the build graph

| GNU Make | Kame |
| --- | --- |
| File target | `rule ./out : ./in` with explicit filesystem paths |
| `.PHONY` target | `task NAME`, which runs whenever reached |
| Reusable successful work | `task cached NAME`, keyed by inputs, dynamic dependencies, and execution configuration |
| First target or `.DEFAULT_GOAL` | `task default` for targetless invocations |
| Normal prerequisite | Input after `:` |
| Order-only prerequisite | Input after `\|`, which is scheduled without contributing to content freshness |
| Pattern rule | Named captures in the output, such as `./build/{name}.o` |
| Recipe | Indented Kash action block by default |
| Included makefile | `include ./rules/common.km` |
| Optional included makefile | `include? ./local.km` |

Prepend `./` or `../` to relative filesystem paths in rule headers. Keep
logical task names bare after the `task` keyword. Use a comma between
prerequisite groups when the first group must finish before the next starts.

## Translate variables and recipes

| GNU Make | Kash recipe or Kame expression |
| --- | --- |
| `VAR = value` or `VAR := value` | `VAR = "value"` for text. Definitions are lazy expressions and can also hold typed values. |
| `$(VAR)` in a recipe | `$VAR` for a typed argument, or `$...VAR` to spread a list |
| `$@` | `$outputs.0` |
| `$<` | `$inputs.0` |
| `$^` | `$...inputs` |
| `$?` | `$...changed-inputs`, computed from file content |
| `$*` | `$stem`, given an output capture named `{stem}` |
| `$(@D)` or `$(<D)` | `(dirname outputs.0)` or `(dirname inputs.0)` |

For a recipe that needs POSIX shell syntax, attach `; {shell: "sh"}` to the
declaration. Kame renders that recipe as a document template and runs the
resulting shell script. Use `@{VAR}`, `@{inputs.0}`, or `@{outputs.0}` to
interpolate Kame values. Use shell quoting and expansions for shell variables.

## Convert a small C build

GNU Make:

```make
.PHONY: all clean

all: build/app

build/%.o: src/%.c
	mkdir -p $(@D)
	cc -c $< -o $@

build/app: build/main.o build/util.o
	cc $^ -o $@

clean:
	rm -rf build
```

Kame:

```kame
task default : ./build/app

rule ./build/{name}.o : ./src/{name}.c
	cc -c $inputs.0 -o $outputs.0

rule ./build/app : ./build/main.o ./build/util.o
	cc $...inputs -o $outputs.0

task clean
	rm -rf ./build
```

Kame creates parent directories for declared file outputs before running the
recipe. When splitting a build across files, include declarations using literal
paths relative to the including source. Use imports when you want to bind
specific exported names or a namespace.

```kame
include ./rules/common.km
include? ./rules/local.km
```

## Inspect and exercise the converted build

The `-f FILE`, `-C DIR`, `-j N`, and `-n` options select a source, set the
working directory, limit concurrent work, and dry-run a build respectively.
Multiple targets can be requested in one invocation.

```sh
kame do fmt -n Makefile.km
kame do plan ./build/app
kame do inputs --depth -1 ./build/app
kame do outputs --depth -1 ./src/main.c
kame -n ./build/app
kame ./build/app
kame ./build/app
```

The second invocation can reuse a file rule when its recorded inputs and
outputs still match. Touching a file without changing its bytes preserves
content freshness. Changed bytes invalidate the rule even when the timestamp
is preserved. Use `--force` to bypass freshness and cached-task hits.

## Convert Make metaprogramming

- Use explicit rules for built-in implicit and suffix rules.
- Use lazy values, declaration-time conditionals and loops, and typed
  `generate` for `define`, `eval`, and generated Make syntax.
- Use named target arguments for per-target parameters and recipe `env`
  metadata for process environment. Use `?=` and `--define` for
  configuration defaults and overrides.
- Move process work from `$(shell ...)` during graph construction into a
  recipe. Planning and foreign-shell template rendering run under read-only
  restrictions.
- Use a recipe attribute or an explicitly invoked script for workflow code
  that depends on a specific shell or toolchain.
  [SDK migration boundaries](../../../sdk-migration-boundaries.md)

For dynamic source discovery, use a tracked glob to declare dependencies:

```kame
sources = ./src/*.c

rule ./build/sources.txt : ${sources}
	printf '%s\n' $...inputs > $outputs.0
```

A membership change can invalidate the consuming rule. Check that
`kame do plan`, `kame do inputs`, and `kame do outputs` describe the intended
graph and that `kame -n TARGET` previews it before running the recipes.
