# Language contract migration note

This note explains the specified syntax switch; it is not an implementation or
release-status report. Runtime code, executable examples and fixture bytes were
not migrated by the documentation update. Verify backend support before using
the new forms. Implementation work is tracked only in [TODO.md](TODO.md#language-contract-implementation).

This is a clean break: no legacy parser mode, compatibility aliases, migration
command or formatter migration flag is required. Update authored sources
explicitly; ordinary formatting must not guess an older source's intent.

## References and calls

Templates and expression strings distinguish lookup from invocation:

```text
Name: @{name}
Project: @{project.name}
Version: @(version)
Files: @(join ", " files)
```

`@` prefixes the complete parenthesized Kame expression. Thus `@(version)`
evaluates `(version)`, a zero-argument call, rather than reading `version`.
Reading a callable through `@{version}` does not invoke it and cannot render it
as text. There is no runtime-type fallback in either direction.

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
verbatim strings keep their own rules. In the new grammar, nested applications
may intentionally invoke computed callables; do not remove their parentheses as
though every nested form were an obsolete wrapper.

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

Value pipes remain pipes; nested calls remain nested calls. Formatting only
normalizes their spacing/layout and preserves placeholder positions. Remove
workarounds that deliberately leave pipes noncanonical only after verifying the
formatter implements this contract.

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

Coordinate runtime changes with executable sources and fixture/golden updates.
Review parser ownership before editing text, especially raw/verbatim content and
external-shell versus direct-Kash recipes. Then check parsing/formatting,
planning/dry-run, targeted behavior, and the native/leak/WASM gates. Documentation
checks alone do not establish runtime conformance.

Authoritative contracts: [language](docs/spec/004-language.md),
[templates](docs/spec/016-templates.md), [CLI](docs/spec/009-cli.md),
[Kash](docs/spec/017-kash.md), [source style](docs/spec/018-source-style.md),
and [diagnostics](docs/spec/011-diagnostics.md#language-boundary-recovery).
