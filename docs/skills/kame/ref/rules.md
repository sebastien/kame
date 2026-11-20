# Rules and target templates

Declare file rules, tasks, and managed services in a Kame program. Use
[the Make migration example](./make-migration.md#convert-a-small-c-build)
for a complete build.

| Header | Target | Freshness |
| --- | --- | --- |
| `rule ./output : ./input` | File rule | Reuses success when recorded input and output contents and discovered dependencies match. |
| `rule always ./output : ./input` | File rule | Runs whenever reached. |
| `task name : dependency` | Task | Runs whenever reached. |
| `task cached name : dependency` | Cached task | Reuses success while its fingerprint matches. |
| `service name : dependency` | Managed service | Runs while required. Readiness, health, restart, and cleanup follow [spec 024](../../../spec/024-managed-services.md). |

File rules declare one or more explicit paths, such as `./out.o` or
`../public/index.html`. Tasks and services declare one logical target name.
Targetless invocations request a target named `default`.

File freshness depends on byte content. A metadata-only touch preserves
freshness. Changed bytes can invalidate reuse even with preserved timestamps.
A rule with neither declared nor discovered dependencies is always stale.
`--force` bypasses reuse.

A file output can be a symlink, including a dangling link or a link to a
directory. Its identity is its destination text. A content read through the
link tracks the resolved file bytes. Automatic cleanup may unlink unchanged
managed outputs and leaves the symlink referents intact.

## Outputs and inputs

```kame
build-dir = "./build"

rule ${build-dir}/{name}.o : ./src/{name}.c
	cc -c $inputs.0 -o $outputs.0
```

A complete file output can be computed with `(EXPR)` or `${EXPR}`, in a
declaration or a rule constructor. Both forms can produce a list of paths.
Nested lists flatten, and `nil` and empty lists contribute no items. Output
resolution runs in a pure phase before target selection, using the surrounding
scope. Quoted literal paths preserve spaces and wildcard bytes.

```kame
sources = ./src/*.c

rule ./build/sources.txt : ${sources}
	printf '%s\n' $...inputs > $outputs.0
```

Inputs can name files or logical targets, use capture references, or expand
lists from expressions. For example, `task scan : ${(dirs REPOS)}` adds the
directories matched by `REPOS` as prerequisites. Use whitespace within a parallel group and a comma
between sequenced groups. Later groups are requested after earlier groups
complete. Put order-only prerequisites after `|` to schedule them separately
from the inputs used for freshness.

Declare actual file inputs to establish content freshness. For computed
membership, test additions and removals as well as changes to existing files.

Declare sibling file artifacts together, such as
`rule ./out/{name}.js ./out/{name}.map : ./src/{name}.ts`. The rule owns
the declared paths, and reuse validates all sibling outputs. Downstream
freshness observes each consumed artifact independently.
[Output ownership](../../../spec/039-output-ownership.md)

## Captures and target arguments

```kame
rule ./build/{path:**}/{name:*}.o : ./src/{path}/{name}.c
	cc -c $inputs.0 -o $outputs.0
```

`{name}` and `{name:*}` capture a nonempty segment excluding `/`. A
`{path:**}` capture may cross directories. Matching is anchored. Repeated
names must match the same text, and adjacent variable-width captures use
leftmost-shortest matching. Input templates reuse captured text. Anonymous
captures also bind positional names such as `{_0}` and `{_1}`. Regex captures
use `{name:~REGEX}` or `{~REGEX}`.
[Pattern extensions](../../../spec/029-pattern-extensions.md)

Literal outputs win over templates. With a single wildcard capture per
pattern, a narrower matching pattern can win. Equal or incomparable matches
can report `TGT_AMBIG`. Grouped outputs must select the same producer
instance. [Rule precedence](../../../spec/045-rule-precedence.md)

Tasks and services can declare required and defaulted string arguments, as
in `task deploy {environment} {region=us-east}`. Invoke this task with
`kame deploy environment=production`. Defaults are literal text. These
arguments participate in rule-instance identity and are available as recipe
bindings.

## Metadata and recipes

```kame
service cloudflare-dev ; {summary: "Run wrangler dev"}
	wrangler dev

task cloudflare-login ; {summary: "Log in to wrangler"}
	wrangler login
```

Metadata is a record. Built-in fields include `shell`, `env`, `authority`,
`tty`, `foreground`, `executor`, and service lifecycle settings. Use a
literal `summary` string for the target description shown by `kame do list`.

An indented body runs as Kash by default. Use `$inputs.0` or
`$outputs.0` for one path and `$...inputs` to spread normal inputs into
arguments. `changed-inputs` contains unique normal file inputs that changed
since the last success. Missing outputs or a missing success record select
all normal file inputs. `captures` contains output capture text in source
order. Named captures and declared target arguments are also in scope.

`; {shell: "sh"}` or a build-wide `SHELL = "sh"` selects a foreign shell.
Kame renders the body as a document template and passes one script to the
interpreter. Use `@{inputs.0}` to render Kame values and use the shell's
quoting rules at the interpolation site. `$` expressions pass through to
the shell. [Templates](./templates.md)

```kame
rule ./greeting.txt : ./name.txt =
	str "Hello, " (strip (text (read inputs.0))) "!\n"
```

An expression body publishes the returned text or bytes to one file output.
A Kash recipe can also publish text or bytes to one output with
`return VALUE`. File publication adds no automatic newline. Computation
in an expression body and interpolation in a foreign-shell recipe allow
tracked reads and apply read-only phase restrictions. Put process and write
effects in an action block or in the resulting foreign-shell script.

## Inspect a build

```sh
kame do fmt --lang km -n Makefile.km
kame do plan ./build/app
kame do inputs --depth -1 ./build/app
kame do span --expand --depth -1 ./build/app
kame -n ./build/app
kame ./build/app
```

Run the target again to check freshness, then change an input to check
invalidation. [GNU Make migration](./make-migration.md)
