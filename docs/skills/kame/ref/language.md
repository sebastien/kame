# Kame language reference

Kame programs contain lazy definitions, build declarations, and expressions.
Kash uses the same values and expressions in commands and action blocks.
Templates render values and calculations as text.

| Task | Reference |
| --- | --- |
| Compute or transform a value | [Expressions](./expressions.md) |
| Bind lazy values or compose program sources | [Scripts](./scripts.md) |
| Expand text or generate a document | [Templates](./templates.md) |
| Declare artifacts, dependencies, and recipes | [Rules](./rules.md) |
| Orchestrate commands and process graphs | [Kash](./kash.md) |
| Look up operation signatures, capabilities, or effects | [Library](./library.md) |
| Run, inspect, parse, or format | [CLI](./cli.md) |
| Translate a GNU Make build | [Make migration](./make-migration.md) |

## Context determines syntax

| Context | Interpretation |
| --- | --- |
| `f x` in an expression | Call `f` with the value of `x`. Use `f ()` for a call with no arguments. |
| `@{f x}` in a template | Evaluate and render the result of `f x`. |
| `(f x)` in a Kash word | Evaluate a typed argument or the scalar fragment of a mixed word. |
| `rule ./out = EXPR` | Publish the calculated text or bytes to one output. |
| An indented recipe | Run a Kash action block. A foreign `shell` setting selects a document template that renders to one shell script. |
| `$(command)` in an expression or Kash word | Capture stdout from a Kash process, subject to execution policy. |
| `$(command)` in plain text | Keep the text literal. A foreign shell interprets it if it appears in the rendered recipe. |
| `{name}` in a rule output | Capture a target segment. The input side may reuse its value. |
| `{name:*}` in an expression pattern | Match a segment and bind it under `name`. Use `{name}` in the expansion pattern. |
| `@if EXPR` in a document directive | Conditionally render a whole-line or inline template block. |

Use `object.field` for property access and `module/name` for an imported
name. `env/NAME` reads a permitted environment variable. Template
interpolation performs text conversion. Quote interpolated shell arguments
and escape document content for the target format as appropriate.

## Sources and tools

The source is UTF-8, and diagnostics use byte spans. Canonical formatting uses
LF and retains source comments. Select `expr`, `km`, `kash`, or `template`
for parse and format probes. `do parse --lang template` inspects inline syntax.
`do fmt --lang template --comment STYLE` formats document directives while
preserving other document bytes.

Parsing and formatting run without executing processes. Planning and
template evaluation apply phase restrictions even when a capability is granted
for execution. Check runnable examples with the intended native or WASM host.
