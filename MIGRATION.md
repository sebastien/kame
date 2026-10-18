# Language contract migration note

This note maps older source syntax to this checkout's language contract. Use a
matching executable and update downstream sources explicitly: there is no legacy
parser mode or automatic formatter migration. Formatting preserves meaning; it
does not guess an older source's intent.

## References and calls

Templates and expression strings distinguish lookup from invocation:

```text
Name: @{name}
Project: @{project.name}
Version: @(version)
Files: @(join ", " files)
```

`@(version)` evaluates `(version)`, a zero-argument call. `@{version}` reads a
binding; if that binding is callable, it cannot render as text. Neither form
changes meaning based on the value's runtime type.

When updating a source written for the prior grammar, review each expansion:

| Prior spelling and intent | Current spelling |
| --- | --- |
| `@(name)` — lookup | `@{name}` |
| `@(project.name)` — field lookup | `@{project.name}` |
| `@(x/NAME)` — tracked tool reference | `@{x/NAME}` |
| `@((version))` — zero-argument call | `@(version)` |
| `@((join ", " files))` — wrapped call | `@(join ", " files)` |
| `@(join ", " files)` — call with arguments | unchanged |
| `@([1 2])` — literal list value | `@(nop [1 2])` |

Apply the same distinction in computed rule headers and interpolated paths:

```kame
@{OUTPUTS} : @{SOURCES}
@{BUILD_DIR}/{name}.o : ./src/{name}.c
OUTPUT = (nop @(output-root)/main.js)
```

Bare `{(...)}` no longer interpolates in expression strings or documents. It
is literal text unless it independently matches existing pattern syntax; rewrite
an intended lookup or call explicitly. Bare `{name}` pattern/capture groups keep
their separate meaning. Do not confuse them with `@{name}` lookups.

Directive argument slots are unchanged: `@if(enabled)` reads a value and
`@if((predicate))` calls without arguments. Selectors, escapes, raw regions and
verbatim strings keep their own rules. Nested applications can invoke computed
callables; do not remove their parentheses as though every nested form were an
obsolete wrapper.

## Environment references

`env.NAME` reads the named environment variable in Kame as well as Kash,
including `@{env.NAME}` interpolation. It uses the same grants, phase checks and
dependency tracking as `(env "NAME")`; that call spelling is unchanged. It does
not expose a whole-environment record. If existing Kame code uses a lexical record
named `env`, rename that record or use `(get "NAME" env)` for its fields instead.

## Typed configuration

Replace `--define-value NAME=LITERAL` with `--set NAME=LITERAL`:

```sh
kame MODE=release
kame --set FLAG=:false
kame --set 'SDK_MODULES=["js" "py"]'
```

`NAME=TEXT`, `--define NAME=TEXT` and `KAME_NAME` still supply strings.
`FLAG=:false` therefore supplies the truthy string `:false`; only the typed
literal supplies false. `--set` accepts closed data, never references, calls or
interpolation. It configures declared value definitions, not target arguments,
template-render payloads, child environments or environment-read grants. Existing
override ordering and the portable `Defines` encoding remain unchanged.

## Kash word composition

Use expressions directly instead of constructing ordinary scalar words with
`str` or extra bindings:

```kash
biome --config-path=(js-biome-config)
:cwd ./build/(variant) :MODE release-(variant) tool > ./out/(name).txt
tool < (input-name).txt
```

Mixed words require scalars and preserve whitespace inside one value.
Standalone unquoted argv substitutions can still splice lists; mixed words,
redirection paths and scalar setup values cannot. Destination-specific type,
path and capability checks remain in force. Typed `:env` records and definition
RHSs retain their value grammar; a statement-leading expression still evaluates
for effects rather than becoming a computed executable.

Kash references remain `$name` / `${name}`. Quoted parentheses are literal:
`"--config-path=(js-biome-config)"` does not call the function. Quote or escape
literal unquoted parentheses. Values are never reparsed as source. Inside a
delegated Kame expression, path interpolation still uses `@{ref}` / `@(call)`.

## Formatting and boundary checks

Formatting preserves authored pipes, nested calls, and placeholder positions;
only spacing and layout change. Noncanonical-pipe workarounds are unnecessary
with a matching formatter.

Use `joinpath` for paths, not concatenation to save a prefix: it preserves an
explicit relative anchor, for example `(joinpath ./sdk "src")` is `"./sdk/src"`.
Verify joined artifact paths in actual rule headers as well as value evaluation.

Literal `@0` in a template recipe still needs escaping, such as `--mtime=\@0`;
shell quotes do not protect it from the template parser. Rule metadata belongs
after the colon/inputs: `show : ; [shell: kash]`.

Diagnostics must explain the actual kind and boundary without silently changing
interpretation or authority. Definition configuration and `--env` do not grant
`(env NAME)` access. Reading a computed record can use `(get "stdout" (sh! COMMAND))`
or a bound `result.stdout`; no postfix call-result field syntax is introduced.

Validate migrated sources with parsing/formatting, planning/dry-run, and targeted
execution. Preserve raw/verbatim content and distinguish external-shell from
direct-Kash recipes. Runtime changes also require fixture updates and the
native/leak/WASM gates; documentation checks alone do not establish conformance.

Authoritative contracts: [language](docs/spec/004-language.md),
[templates](docs/spec/016-templates.md), [CLI](docs/spec/009-cli.md),
[Kash](docs/spec/017-kash.md), [source style](docs/spec/018-source-style.md),
and [diagnostics](docs/spec/011-diagnostics.md#language-boundary-recovery).
