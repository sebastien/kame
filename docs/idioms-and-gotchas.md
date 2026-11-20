# Idioms and gotchas

Use `.km` for Kame definitions and build declarations, and `.kash` for action blocks.
Ordered file and `-c` operands can share definitions in one `do run` session;
the first fragment establishes capability defaults. See the
[CLI reference](skills/kame/ref/cli.md) for invocation details.

## Values and references

A bare name references a binding. `[a, b]` contains the values of two bindings,
while `["a", "b"]` contains two strings. Use `name = nil` for an absent value
and `name = ""` for empty text. Function application passes each value as one
argument.

```sh
kame do run --lang expr -c 'join "-" ["a", "b"]'
# "a-b"
kame do run --lang expr -c 'count [1, 2, 3]'
# 3
```

Printed values use canonical syntax, including `nil`, `true`, `false`, and
quoted strings. Bytes print directly.

`do run --lang expr` displays the result. Use a definition name to request a
value from a `.km` source or `--print-result` to display a program result.
Program operands after `--` populate the `args` list. Use `args.0` for the
first operand.

`NAME=TEXT`, `--define NAME=TEXT`, and environment overrides configure
strings. Use `kame --set FLAG=false` to pass a boolean and
`kame --set 'MODULES=["js", "py"]'` to pass a list. Typed overrides accept
closed literal values.

## Truth, emptiness, and captured identifiers

Only nil and false are false; zero and empty text, bytes, lists, and records are
true. Use `(not (empty? FILES))` for known collection data, `(nil? VALUE)` for
absence (including opaque values), and `NUMBER == 0` for zero. `empty?` returns
false for nil and non-container kinds. `count` is not valid on nil or numbers.
`"" or "fallback"` returns the empty string because empty text is truthy.

`blank?` tests nil or empty text, not whitespace, false, zero or empty collections.
`split` preserves empty fields; tokenizing a space-delimited setting is an explicit
filter, not a change to splitting semantics:

```kame
# [""]
(split " " "")
# []
(filter-out "" (split " " ""))
# ["py", "js"]
(filter-out "" (split " " " py  js "))
# false
in? "py" ["pypy", "js"]
```

This tokenizer handles ASCII spaces, not tabs/newlines or shell quoting. Prefer
typed lists (`--set 'MODULES=["py", "js"]'`) when the setting is structured data.

Command capture preserves stdout, including every trailing newline.
`(strip $(git rev-parse HEAD))` is an explicit identifier conversion, not a capture
default. For a path tool promising one terminal LF, remove exactly that byte
with `(line TEXT)`, preserving whitespace/newlines belonging to the path and any
preceding CR. Do not use broad whitespace trimming for a path producer.
See [SDK migration boundaries](sdk-migration-boundaries.md) for workflow and
failure-cleanup examples.

## Paths, sources, and authority

`./output.txt` names a file artifact. Write an explicit path for file rules. Declare a phony task with
`task NAME` or a cacheable task with `task cached NAME`. Use `--force` to bypass freshness/cache decisions for an invocation.
File reuse compares recorded input/output content digests, not timestamps:
touching unchanged bytes preserves reuse; changing bytes invalidates even with
preserved metadata. A first successful run establishes the record.
For a persistent file-rule override, write `rule always ./output : ./input`. It keeps
file artifacts and output checks, and reruns whenever a new root reaches it.
Shared prerequisites still run once within a diamond or concurrent root batch.

Grouped file outputs share one recipe and must all be present after execution.
A consumer of one sibling observes only that artifact's bytes: changes to other
siblings, or repairs that restore identical bytes, preserve its reuse. A missing
or invalid cache record can require rebuilding to establish trustworthy facts;
the following unchanged invocation should reuse them.

`-C project -f Makefile.km` selects `project/Makefile.km`, independent of
option order. Absolute source paths stay absolute. Includes are relative to
the containing source, while evaluation paths use the selected working directory.
`joinpath` preserves a leading `./` while cleaning components. The expression
`joinpath ./deps/sdk ./etc/file` returns the path `./deps/sdk/etc/file`.

Value/expression invocations deny host operations by default; grant only what is
needed:

```sh
kame do run --lang expr --allow-read=. -c 'files ./docs/*.md'
```

Read, write, run, and environment authority are separate. Filesystem roots are lexical
checks, with the symlink boundary described in
[the capability contract](spec/005-evaluation.md#capabilities). Grants do not
contain a child process's filesystem, network or environment access.

`env/NO_COLOR` reads the current unit's environment and yields `nil` when
the variable is unset. Use `${env/NO_COLOR}` in a string or `@{env/NO_COLOR}` in a template.
Environment reads require the matching grant, such as `--allow-env=NO_COLOR`.
An explicit grant list replaces the defaults, so include the other grants
the invocation needs.

For an authored opt-in color setting:

```kame
COLOR_DISABLED ?= (not (blank? env/NO_COLOR))
```

```sh
NO_COLOR=1 kame --allow-read=. --allow-write=. --allow-run --allow-env=NO_COLOR check
```

This restores the usual build grants and permits only the named environment
read. The definition treats unset/empty `NO_COLOR` as color enabled and nonempty
text as color disabled. The read participates in freshness; a grant alone does
not make a program adopt this setting.

## Build expressions and recipes

Kash is the default recipe language. Use `$inputs.0` and `$outputs.0` for the
first input and output, and `$...inputs` to pass all inputs as separate
arguments. `changed-inputs` contains unique normal file inputs whose content
has changed since the last successful run. It contains all normal file inputs
when the output or reuse record is missing and excludes order-only inputs.

Use `${EXPR}` in a rule header to expand a path or a list of paths. Each expansion
is flattened in authored order. Use captures such as `{stem}` in output patterns
and `(dirname outputs.0)` in a Kash recipe to compute an output directory.

Select a foreign shell with rule metadata, for example
`task show ; {shell: "/bin/sh"}`. The recipe is one shell script. Kame
renders `@{EXPR}` before execution, and the shell interprets its own variables
and command substitutions. A literal tar argument such as `--mtime=@0` needs no interpolation escape.

A backslash followed by a newline continues a declaration header or
expression. Expressions also continue inside a delimiter or after an
unfinished operator.

Use `files ./src/**/*.c` to discover files with a tracked pattern.
`**` matches zero or more directory levels. A wildcard in a command
argument stays literal. See the [library reference](skills/kame/ref/library.md)
for directory and symlink selection.

Kash can compose a scalar argument with `biome --config-path=(js-biome-config)`.
The same fragments work in `cwd: ./build/(variant)` and redirections such as
`> ./out/(name).txt`. Whitespace stays within the argument or path that
contains it. Use `$...list` when a list supplies separate arguments. Quoted parentheses
remain literal. `echo name=(name)` interpolates `name` into one command argument.

## Patterns and expansions

A replacement expansion may be a string, a number, a boolean, or an expansion
pattern. Scalar expansions convert to text:

```sh
kame do run --lang expr -c 'subst ./src/{name:*}.c "1" "./src/demo.c"'
# "1"
```

`subst` leaves an unmatched subject unchanged. Use `match? PATTERN TEXT`
to test for a match, or `(match? PATTERN)` as a predicate for `filter` or
`filter-out`. Use captures in the matching pattern and reference those
captures in the replacement pattern.
Patterns can start with a capture, and bare targets can capture: `{name:*}`
or `aws-shell-{role-account}`. Exact targets win over templates; structurally
nested single-capture file templates follow [spec 045](spec/045-rule-precedence.md).
Other competing templates remain ambiguous. Required/optional standalone target
arguments are implemented separately from captures; see
[spec 022](spec/022-target-arguments.md).

Three-argument pattern replacement accepts a string or any list of strings,
including a filtered wildcard result. `**` captures can backtrack across path
segments. Anchor the source prefix to avoid copying it into the destination:

```kame
sources = files ./src/**/*.txt |> filter-out (source => includes? "draft" (str source))
outputs = subst ./src/{path:**}.txt ./build/{path}.json sources
```

With two arguments, `subst` returns a callable that accepts the subject.
Filter with `match?` before substituting to retain only matching sources.
`resub` returns `nil` when its subject has no match.

## Inspection and reproducibility

Use `do plan TARGET` to inspect a declaration and `-n TARGET` to render a dry run.
A plan can report unknown freshness before dynamic inputs or body dependencies
resolve. `do tools check TARGET` checks selected plans without running recipes;
a missing executable in an unrelated rule should not block that target.

`do fmt` prints the whole canonical source, including line wrapping and header
spacing. `do fmt -n FILE` reports whether that source differs; neither mode
rewrites FILE. Only `do fmt -i FILE` replaces it. Review canonical output before
applying it to a hand-formatted file. Formatting must preserve the parsed AST,
and ordinary inspection/build commands must leave authored source bytes intact.
Pipes stay pipes and nested calls stay nested calls, including placeholder
positions; canonical formatting only changes spacing and layout. There is no
need to keep a deliberately noncanonical pipe to preserve its reading order.

Dynamic discovery is only correct when the aggregate actually rebuilds after
input membership changes. Keep a source-addition check immediately after a build,
without inserting a sleep that hides timestamp precision defects. Declared file
inputs establish freshness; a bare task prerequisite is not a substitute for them.
