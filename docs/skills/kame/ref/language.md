# Kame language reference

Kame has independently parseable expression, template, and rule languages.
The script parser composes definitions, expressions, and rules. Kash adds a
process-oriented outer syntax, using the same Kame expression/value language.

## Read by task

- [Expressions](./expressions.md): compute or transform a value.
- [Scripts](./scripts.md): bind lazy values and compose program sources.
- [Templates](./templates.md): expand text or generate a document.
- [Rules](./rules.md): declare artifacts, dependencies, and recipes.
- [Kash](./kash.md): orchestrate commands and streaming process graphs.
- [Library](./library.md): operation signatures, capabilities, and effects.
- [CLI](./cli.md): run, inspect, parse, and format.
- [Make migration](./make-migration.md): translate a build model, not Make syntax.

## Boundary checklist

| Context | Interpretation |
| --- | --- |
| `(f x)` in an expression | Call `f` with the value of `x`. |
| `@(f x)` in template text | Evaluate `(f x)` and render its value as text. |
| `@(f x)` in a Kash word | Evaluate `(f x)` and supply a typed argument value. |
| `$(command)` in an expression or Kash word | Parse the contents as Kash and capture stdout, subject to execution policy. |
| `$(command)` in a plain Kame string/document | Literal text unless explicitly inside expression interpolation. |
| `$(command)` in opaque recipe text | Shell-owned command substitution. |
| `{name}` in a rule target | Capture a nonempty target segment and reuse it in input templates. |
| `{name}` in an expression pattern | Expansion reference, not a matcher; use `{name:*}` to match. |
| `@if(...)` on a template directive line | Template control, not a Kash statement or shell conditional. |

Do not use `@{reference}`; the inline reference spelling is `@(reference)`.
Selectors require their rule/function context. Template rendering is text
conversion, not shell quoting, HTML escaping, or typed argv conversion.

## Source and tools

Sources are UTF-8; diagnostics retain byte spans. Value/rule sources accept
whole-line `#` and `//` comments. Kash uses word-start `#` comments and treats
`//` literally. Canonical formatting uses LF and retains source comments.

Use `do parse` and `do fmt` with an explicit language when inspecting a
fragment. `expr`, `template`, and `rule` select individual grammars; `script`
selects the composite grammar; `kash` selects process scripts. Executable
`do run` languages are only `km`, `kmk`, `kash`, and `expr`. The specification
also names `km`/`kmk` parse/format modes, but current CLI tooling does not
accept those names; script tooling may also lag value-statement support. Use
`expr` for individual expressions and the runner to verify `.km` execution.

Parsing and formatting never execute processes. A capability grant does not
override phase restrictions: planning must not perform writes or process
execution. Validate implementation/backend support before presenting a
specified feature as runnable.
