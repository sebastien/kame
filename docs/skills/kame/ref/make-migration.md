# Migrating from Make to Kame

This guide is for a staged migration from GNU Make to Kame. Kame borrows the
idea of named targets and shell recipes, but it is not a Makefile parser or a
drop-in replacement for GNU Make.

Keep the existing `Makefile` while adding `Makefile.kmk`. Kame discovers only
Kame sources: `Makefile.kmk`, `make.kmk`, and `src/kmk/main.kmk`, in that order.

## Translate the build model first

Before editing, classify every Make target:

| Make construct | Kame model |
| --- | --- |
| A target that produces a file | A rule with explicit `./` or absolute output paths |
| `.PHONY` target | Bare Kame task; it is always stale |
| Expensive task whose result can be reused | `task NAME : ...`; Kame caches a successful result using rule, inputs, dynamic dependencies, and execution configuration |
| First Make target / `.DEFAULT_GOAL` | A literal Kame target named `default` |
| Prerequisite | Rule input after `:` |
| Pattern rule | Rule header with named captures such as `{name}` |
| Recipe | Indented Kame rule body, rendered as one shell script |

Use `task` deliberately. A bare task is the Kame equivalent of a conventional
phony target and runs each time it is reached. A `task` is a cached task, not a
synonym for `.PHONY`.

Kame does **not** select the first rule as the default. With no requested
target, it runs a target literally named `default`; if none exists, it lists
the available literal targets.

## Syntax mapping

| GNU Make | Kame | Notes |
| --- | --- | --- |
| `VAR = value` / `VAR := value` | `VAR = value` | Kame definitions are lazy. Use Kame expressions where computation is needed; do not assume Make's recursive versus immediate expansion distinction. |
| `$(VAR)` in a recipe | `@(VAR)` | Evaluates and renders a Kame definition. |
| `$@` | `@>` | First output. |
| `$<` | `@<` | First input. |
| `$^` | `@<*` | All inputs, rendered space-separated. |
| `$?`, `$*`, `$(@D)`, `$(<D)` | No general direct equivalent | Model the required path/capture explicitly; do not paste Make automatic variables into a Kame recipe. |
| `out: in` | `./out : ./in` | Filesystem paths must be explicit. |
| `%.o: %.c` | `./build/{name}.o : ./src/{name}.c` | The same named capture must match consistently. |
| `make target` | `kame target` | Multiple targets can be requested in one invocation. |
| `make -f FILE target` | `kame -f FILE target` | Use a `.kmk` source. |
| `make -C DIR target` | `kame -C DIR target` | Sets Kame's working directory. |
| `make -j N target` | `kame -j N target` | Limits concurrent graph nodes. |
| `make -n target` | `kame -n target` | Kame plans and renders, without process or effect execution. |
| `include rules.mk` | `include ./rules.kmk` | Merges the included Kame source depth-first; paths are relative to the including source. |

## Example conversion

GNU Make:

```make
.PHONY: all clean

all: build/app

build/%.o: src/%.c
	cc -c $< -o $@

build/app: build/main.o build/util.o
	cc $^ -o $@

clean:
	rm -rf build
```

Kame:

```kame
default : ./build/app

./build/{name}.o : ./src/{name}.c
	cc -c @< -o @>

./build/app : ./build/main.o ./build/util.o
	cc @<* -o @>

clean :
	rm -rf ./build
```

The `./` prefixes classify the outputs and inputs as file resources. `default`
and `clean` are bare targets and therefore run every time they are selected.
Kame creates parent directories for declared file outputs before executing a
file-rule recipe.

For a split Makefile, migrate shared declarations into `.kmk` fragments and
include them explicitly:

```kame
include ./rules/common.kmk
include ./rules/platform.kmk
```

Kame merges the included definitions and rules into the caller's program.
Unlike GNU Make, repeated inclusion is an error rather than an implicit
idempotent no-op; organize common declarations behind one shared fragment.

## Convert incrementally

1. Copy the smallest useful Make target and its prerequisite closure into
   `Makefile.kmk`.
2. Change every file path in rule headers to an explicit `./path`, `../path`, or absolute
   path. Keep logical task names bare.
3. Translate only the automatic variables used by the initial recipe. Replace
   Make functions and generated Make syntax with explicit Kame expressions or
   ordinary shell code.
4. Format and inspect before executing:

   ```sh
   kame do fmt -n Makefile.kmk
   kame do plan ./build/app
   kame do inputs --depth -1 ./build/app
   kame do outputs --depth -1 ./build/app
   kame -n ./build/app
   ```

5. Run the target, then run it again. A file rule with current outputs should
   skip; a bare task should run again. Use `--force` only when intentionally
   bypassing freshness and cached-task hits.
6. Add patterns, generated inputs, and cached tasks only after the direct
   graph is correct.

## Redesign rather than transliterate

The following GNU Make constructs have no initial Kame equivalent and should
be redesigned:

- `-include`, `define`, `eval`, and Make-generated rule syntax;
- built-in implicit rules, suffix rules, and Make's rule-search algorithm;
- order-only prerequisites (`|`), target-specific variables, and Make exports;
- `$(shell ...)` and other Make functions that execute during parsing;
- assumptions that each recipe line starts a separate shell.

Use Kame definitions and expression operations for deterministic build data;
use explicit prerequisite rules to generate files; and use the normal rule
recipe as the process interface. A collected `shell` expression is available
only for standalone expression execution, not during normal build rendering.

For dynamic source discovery, prefer an explicit definition:

```kame
SOURCES = (wildcard ./src/*.c)

./build/sources.txt : @(SOURCES)
	printf '%s\n' @<* > @>
```

`wildcard` records a dependency on the glob result, so later membership changes
can invalidate the consuming rule.

## Migration review checklist

- Every filesystem input and output in a rule header starts with `./`, `../`, or `/`.
- The intended default is named `default`.
- Always-run targets are bare; only cacheable work uses `task`.
- Recipes use `@<`, `@<*`, and `@>` rather than Make automatic variables.
- Make-only metaprogramming has been removed or explicitly redesigned.
- `kame do plan`, `inputs`, and `outputs` describe the intended graph.
- `kame -n TARGET` succeeds without writes, effects, or processes.
