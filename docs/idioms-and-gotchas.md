# Idioms and gotchas

Use `.km` for values, `.kmk` for build rules, and `.kash` (or `.ksh`) for
processes. These are independent entry points that can share a program through
ordered file and `-c` operands. `do expr` and `do kash` have been replaced by
`do run` with an explicit language where needed.

## Values and references

A bare name references a binding. So `[a b]` is a list of two references, while
`["a" "b"]` contains two strings. An empty definition such as `name =` is a
parse error; use `name = :nil` or `name = ""` to state what you mean.

Application arguments are Kame values, not shell words:

```sh
kame do run --lang expr -c '(join ["a" "b"] "-")'
# "a-b"
kame do run --lang expr -c '(count [1 2 3])'
# 3
```

Printed values use canonical syntax: `:nil`, `:true`, `:false`, and quoted
strings. Bytes print directly. Do not infer string splitting from display.

## Paths, sources, and authority

`./output.txt` names a file artifact; `output.txt` without an explicit path is
not automatically a file rule. A bare task runs when requested; `task NAME` is
a cached task. Use `--force` to bypass freshness/cache decisions for an invocation.
File reuse compares recorded input/output content digests, not timestamps:
touching unchanged bytes preserves reuse; changing bytes invalidates even with
preserved metadata. A first successful run establishes the record.
For a persistent file-rule override, write `always ./output : ./input`. It keeps
file artifacts and output checks, and reruns whenever a new root reaches it.
Shared prerequisites still run once within a diamond or concurrent root batch.

`-C project -f Makefile.kmk` selects `project/Makefile.kmk`, independent of
option order. Absolute source paths stay absolute. Includes are relative to
the containing source, while evaluation paths use the selected working directory.

An initial value/expression invocation denies host operations by default:

```sh
kame do run --lang expr --allow-read=. -c '(wildcard ./docs/*.md)'
```

Read, write, run, and environment authority are separate. Later fragments do not
implicitly broaden the first fragment's policy. Filesystem roots are lexical
checks, with the symlink boundary described in
[the capability contract](spec/005-evaluation.md#capabilities). Grants do not
contain a child process's filesystem, network or environment access.

## Build expressions and shell recipes

A `.kmk` recipe is one shell script. Shell variables and `cd` persist between
its lines. Port Make recipes that relied on one shell per line carefully.

Kame selectors use `@>` for outputs, `@<` for the first input, and `@<*` for
all normal inputs. `@<?` selects unique normal file inputs whose content digest
changed since the successful run, or all normal file inputs when any output or
the record is missing. Forced execution does not invent changed inputs. It
excludes order-only prerequisites and is available only inside file recipes.
Use explicit `{stem}` pattern captures for stems, `@(dirname @>)` for the output
directory and `@(dirname @<)` for the first input directory.

Kame expressions use `@(EXPRESSION)` in recipes. Build headers accept multiple whole input expressions and paths such as
`@(DIRECTORY)/suffix`, interleaved with literal or quoted paths. Each whole
expression is evaluated and flattened independently, in authored order.

Long definitions and rule headers may continue with backslash-newline; indented
continuation lines belong to that declaration. Use multiline expressions or a
verbatim quoted string for multiline values. Recipe continuations remain shell syntax.

Quote shell text held in a Kame definition:

```kame
WASM_SDK = "$(mise where asdf:mise-plugins/mise-wasi-sdk)/wasi-sdk"
WASM_CC = "@(WASM_SDK)/bin/clang"
```

The plain quoted `$(...)` stays literal until the recipe shell sees the rendered
text. This is a workaround, not a dependency-tracked resolver: it can run once
per use and is not reflected as a resolved executable in the plan. Unquoted
`$(...)` is now an embedded Kash process expression and a trailing `/suffix`
is not part of that atom. Do not use Make's `$$` habit blindly: raw recipe text
is shell-owned, so `$HOME` is a shell expansion; `$$` means the shell process ID.

A literal wildcard in Kash argv stays literal. Ask Kame to discover files with
an explicit wildcard expression when that is intended. `wildcard` accepts one or more patterns and returns a sorted, duplicate-free
union; filter results with collection operations. `**` matches zero or more directory levels.

## Patterns and expansions

A replacement expansion may be a string, a number, a boolean, or an expansion
pattern. Scalar expansions convert to text:

```sh
kame do run --lang expr -c '(replace ./src/{name:*}.c "1" "./src/demo.c")'
# "1"
```

A missing pattern match returns `:nil`. Keep match patterns and expansion
patterns distinct; their capture/reference groups have different roles.
Patterns can start with a capture, and bare targets can capture: `{name:*}`
or `aws-shell@{role-account}`. Exact targets win over templates; two matching
templates remain ambiguous. Required/optional standalone target arguments are
implemented separately from captures; see [spec 022](spec/022-target-arguments.md).

## Inspection and reproducibility

Use `do plan TARGET` to inspect a declaration and `-n TARGET` to render a dry run.
A plan can report unknown freshness before dynamic inputs or body dependencies
resolve. `do tools check TARGET` checks selected plans without running recipes;
a missing executable in an unrelated rule should not block that target.

Dynamic discovery is only correct when the aggregate actually rebuilds after
input membership changes. Keep a source-addition check immediately after a build,
without inserting a sleep that hides timestamp precision defects. Declared file
inputs establish freshness; a bare task prerequisite is not a substitute for them.
