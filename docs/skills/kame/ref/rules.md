# Rules and target templates

`rule` parses one header and its indented recipe. `.kmk` programs compose
rules and lazy definitions; see [Scripts](./scripts.md).
`<-` is also accepted as a header separator; formatting canonicalizes it to `:`.

```kame
default : ./build/app

./build/{name}.o : ./src/{name}.c
	cc -c @< -o @>

./build/app : ./build/main.o ./build/util.o
	cc @<* -o @>

clean :
	rm -rf ./build
```

## Target classification

| Header | Kind | Freshness |
| --- | --- | --- |
| `./output : ./input` | File rule | Reuses a successful record when input/output content digests and recorded dependencies match. |
| `name : dependency` | Bare task | Always stale; runs whenever reached. |
| `task name : dependency` | Cached task | Reuses success when its fingerprint matches. |
| `service name : dependency` | Managed service | Runs while required; readiness, health, restart and cleanup follow [spec 024](../../../spec/024-managed-services.md). |

Paths must start with `./`, `../`, or `/`. `out.o` is not a file target;
`./out.o` is. Quoted output paths must retain an explicit prefix. File rules
can have multiple outputs; logical task/service rules have exactly one.
Do not mix path and name outputs. With no selected target, request literal
`default`, not the first rule.

`"./out file"` registers the decoded file path, not a filename containing quotes.
Quoted outputs and prerequisites use expression-string escapes; formatting keeps
their authored spelling. Quoted prerequisites without interpolation stay literal,
including wildcard bytes.
Quoted capture outputs preserve decoded literal backslashes; AST capture spans
refer to authored source, and capture patterns use the `pattern` field.

File freshness uses content, not modification time. Touching unchanged bytes
does not rebuild; changing an input or output's bytes invalidates reuse even
with preserved timestamps. The first run establishes a successful record, and
a file rule without declared or discovered dependencies is always stale.
`always ./output : ./input` and `--force` bypass reuse.

File outputs may be symlinks, including directory links and dangling links.
Their identity is the exact destination text, not the contents of the referent;
removal, retargeting, or replacement by another entry kind invalidates reuse.
Content reads through a link still track resolved file bytes. Automatic cleanup
can unlink unchanged managed symlink outputs and never traverses their referents.

Outputs may also use reference/call expansions or interpolated tokens:

```kame
PATH_BUILD = "./build"
@{PATH_BUILD}/{name}.o : ./src/{name}.c

OUTPUTS = [./build/one [./build/two :nil]]
@{OUTPUTS} : ./seed
```

Outputs resolve purely before target selection. Explicit path prefixes are
checked after expansion; `"build"` is not a replacement for `"./build"`.
Resolved capture syntax becomes active. Standalone expressions flatten nested
lists; nil and empty lists contribute nothing, but a rule must retain at least
one output. Interpolated tokens render one target. Computed logical names obey
the ordinary task/service limits. Host reads, processes, writes, and references
to this rule's captures, arguments or selectors are unavailable during output
resolution.

Inputs may be logical targets, paths, target templates, quoted values, or
reference/call expansions. Lists from input expressions flatten recursively:

```kame
SOURCES = ./src/*.c

./build/sources.txt : @{SOURCES}
	printf '%s\n' @{SOURCES} > @>
```

Declare actual file inputs; a bare task prerequisite alone does not establish
artifact freshness. For aggregates over computed membership, render the
computed set as well as declaring it, and test additions/removals.

Computed-root prerequisites share expression path parsing, so authored capture
references compose with symbol prefixes directly:

```kame
@{PATH_BUILD}/{path:**}.js : @{PATH_SRC}/js/{path}.js
```

The prerequisite resolves `path` from the matched output. An expression such as
`@(nop @{PATH_SRC}/js/{path}.js)` works too. Quoted prerequisite tokens keep
bare `{path}` literal; inserted text is never reparsed as capture syntax.

## Captures

```kame
./build/{path:**}/{name:*}.o : ./src/{path}/{name}.c
	cc -c @< -o @>
```

`{name}` and `{name:*}` capture a nonempty non-`/` segment. `{path:**}` may
cross `/`; `?` and character classes are supported. Matching is anchored,
captures cannot be empty, and repeated names must match identical text.
Adjacent variable-width captures use leftmost-shortest matching. Input
templates reuse the captured text; these are not Make stems or expression
pattern expansion semantics.
Anonymous output captures `{*}`/`{**}` bind positional `_0`, `_1`, etc.; inputs
can reuse them as `{_0}`/`{_1}`. Named captures also occupy positional slots.
Regex captures `{name:~REGEX}` and `{~REGEX}` use the portable bounded matcher;
see [Pattern extensions](../../../spec/029-pattern-extensions.md).

Literal outputs win over templates. For file templates with one `*` or `**`
capture, a structurally narrower literal prefix/suffix takes precedence;
`./www/xml/{path:**}.html` overrides `./www/{path:**}.html`, and `*` narrows
`**`. Equal or incomparable matches remain `TGT_AMBIG`, independent of rule
order. Selection never probes sources or retries a broader rule after a missing
input. Regex and multi-capture overlaps retain ambiguity. Grouped outputs must
all select the same producer instance. See [Rule precedence](../../../spec/045-rule-precedence.md).

Single named tasks can declare required/defaulted arguments:
`deploy {environment} {region=us-east} :`. Invoke with
`kame deploy environment=production`; these string bindings are distinct from
file-pattern captures and participate in rule-instance/cache identity.
Defaults are literal text: `{workspace=TERRAFORM_WORKSPACE}` is that spelling,
not a reference. Resolve computed defaults in the recipe; `auto` is an ordinary
application-chosen sentinel, not a built-in default evaluator. `--set` configures
declared definitions, not these target arguments.

`|` introduces order-only inputs, excluded from normal input selectors and
content freshness. Commas separate input sequence groups: later groups are
evaluated and requested after earlier groups complete, not a global barrier across
targets. `a : @{PREP}, @{BUILD}` splices computed lists into authored groups;
empty expansions preserve barriers. Output registration remains pure.

## Rule metadata

Header metadata is an extensible record. Existing fields such as `shell`, `env`,
file-rule `executor`, and service lifecycle settings keep their built-in behavior;
other fields are accepted as custom metadata. Duplicate fields remain invalid.
Use a literal `summary` string to document a target; `kame do list` displays it.
The target name and rule kind still come from the declaration, not custom
`name` or `kind` fields.

```kame
service cloudflare-dev : ; [summary: "Run wrangler dev" kind: "service"]
	wrangler dev

cloudflare-login : ; [summary: "Log in to wrangler" kind: "task"]
	wrangler login
```

## Recipes and effects

Canonical recipe indentation is one tab; additional shell indentation survives.
The first body line establishes the prefix shared by nonblank body lines.
External-shell recipe lines execute as one script, so `cd`, variables, and shell
control flow persist. These recipes retain document-template expansion;
interpolation does not automatically shell-quote values. Selecting `[shell: kash]`
or `SHELL = kash` parses the authored body directly as Kash. Use `$reference`
and `(EXPRESSION)` for typed argv, without serialization or template escaping.
Header settings `; [shell: kash env: [MODE: mode]]` resolve purely before
prerequisite execution; `shell` overrides build-wide `SHELL`. The legacy suffix
`; env "MODE=release"` is an alternative, not an additional metadata record.

Select `; [shell: kame]` for a value-producing body instead of a process program:

```kame
./greeting.txt : ./name.txt ; [shell: kame]
	(str "Hello, " (strip (text (read @<))) "!\n")
```

Exactly one expression returns text or bytes, and the rule atomically publishes
that content to exactly one file output. No implicit serialization or newline is
added. `SHELL = kame` selects the same producer when a rule has no per-rule
`shell`. The string `"kame"` is an executable, not the selector. Computation is
read-only, including reached lazy definitions and helpers.

Use `@<`, `@<*`, and `@>` for first normal input, all normal inputs, and first
output. In file recipes, `@<?` selects normal file inputs whose content changed
since the successful run; missing outputs or records select all normal file
inputs. Order-only prerequisites after `|` are excluded from input selectors.
See [Templates](./templates.md) for selectors, inline expressions, and recipe
document directives.

The ordinary names `inputs`, `outputs`, and `inputs/changed` expose the lists
corresponding to `@<*`, `@>*`, and `@<?`. `outputs` includes logical task names,
not just file artifacts. Lexical bindings take precedence; missing rule context
and disallowed changed-input access retain selector diagnostics. Use `$inputs`
and `(first outputs)` in direct Kash, or `@{inputs}` in external-shell templates.

| Expression while computing content or expanding a shell template | Build behavior |
| --- | --- |
| `out!`, `err!`, `yield!`, `write!`, processes, clocks | Rejected with `PHASE_INVALID`. Execute actions in Bash or direct Kash instead. |
| Glob paths such as `./src/*.c` / `(read PATH)` | Discover a dependency. |
| `(render PAYLOAD PATH)` | Read/render a tracked template with tracked expression dependencies. |

In direct Kash, `(yield! VALUE...)` remains an explicit content-publication action
requiring exactly one file output. It is not a template expansion.

Known sibling artifacts belong in one grouped declaration, for example
`./out/{name}.js ./out/{name}.map : ./src/{name}.ts`. Configured output lists
must be pure. A directory or manifest does not claim its contents as outputs,
and removing a configured member does not delete its file. See
[output ownership](../../../spec/039-output-ownership.md).

Group reuse validates every sibling, but downstream freshness observes each
consumed artifact independently. A sibling change or producer rerun does not
rebuild a consumer whose declared/read output remains byte-identical.

Planning resolves headers, not body effects or commands. Rendering may discover
dependencies that must complete before final freshness and recipe execution.
Do not demand process substitutions or writes during planning, even with grants.

## Verify before materializing

```sh
kame do fmt --lang script -n Makefile.kmk
kame do plan ./build/app
kame do inputs --depth -1 ./build/app
kame do span --expand --depth -1 ./build/app
kame -n ./build/app
kame ./build/app
```

Run again to check freshness; change an input to check invalidation. Use
`--force` only to bypass freshness/cache intentionally. Preserve GNU Make
builds while migrating and follow [Make migration](./make-migration.md).
