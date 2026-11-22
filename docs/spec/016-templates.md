---
revision: 2
---

# Text Templates

## Purpose

This specification makes Kame's template language usable on arbitrary text
files, so a build can render HTML, configuration, source code, or any other
text from data. It defines:

- **Document templates**: a text file may carry Kame directives in the host
  language's comment syntax, giving conditionals, repetition, binding, pattern
  dispatch, and inclusion without leaving the file's own format. The `.kmt`
  suffix names a document template source.
- **A runtime `render` operation** that parses and evaluates a template file or
  template content against a record payload.
- **Supporting primitives**: `template-cat`, `text`, and the `with` special
  form.
- **A `kame do render` command** for standalone rendering.

Document templates lower to the expression language of
`055-unified-kame-kash-language.md` §4: every directive compiles to an `if`
expression, a `match` expression, `map`, `join`, an applied lambda, `with`,
`template-cat`, or `render`. There is no second evaluator, so templates share
laziness, capabilities, and dynamic dependencies with the rest of Kame. Inline
`@{EXPR}` interpolation is the inline form for document templates and
foreign-shell recipes. Kame strings, path atoms, rule headers, and Kash
command words use `${EXPR}`.

## Relationship to Kash

A document template is not a Kash source. Its executable forms are template
directives and inline `@{EXPR}` interpolation, both using the expression
grammar. `$REFERENCE`, `${REFERENCE}`, and `$(COMMAND)` are emitted literally in
document text; process capture is available only inside an evaluated
expression, and only in phases that authorize it.

Templates render `@{EXPR}` values as text. Kash command words accept
`$REFERENCE` lookups and full expressions in `${EXPR}` or `(EXPR)`.
Inside a quoted or mixed Kash word, `${EXPR}` uses Kame string
conversion, including space-separated list items (`017` §6).

## Inline interpolation

```text
Hello @{name}
Project: @{project.name}
Version: @{version ()}
Files: @{join ", " files}
Names: @{files |> map basename |> join ", "}
```

`@{EXPR}` evaluates one expression in value context and renders its value as
text. `EXPR` uses the full expression grammar: references, field access,
applications, operators, `if` expressions, lambdas, sections, `match`, and
value pipes all keep their ordinary meaning. A bare name reads its binding
without invoking it; `@{version ()}` calls a zero-argument function. Tool
references use the qualified spelling, `@{x/NAME}`, with resolution and tracking
from `007-library.md`.

Interpolation renders strings, numbers, booleans, symbols, and paths as text,
and `nil` as empty text. Lists, records, `Bytes`, and callables are
`EXPR_INVALID` unless an explicit operation such as `join`, `json`, `fmt`, or
`text` renders them; the diagnostic names the actual value kind and, for a
callable, suggests an explicit call. Interpolation never applies HTML escaping,
shell quoting, or source quoting.

Strings, document templates, foreign-shell recipes, rule headers, and path
atoms share this interpolation boundary. Their context, value conversion, and
phase restrictions remain distinct. The end of an interpolation is found by the
expression parser, which balances nested strings, records, lists, groups, and
captures; it is never found by text matching. Escapes and raw regions keep
interpolation literal.

## Document Templates

A document template is any UTF-8 text interspersed with directive lines and
inline interpolations. Text that is not a directive line is emitted verbatim.

### Directive Lines

A directive line is a whole line carrying exactly one directive, either as the
entire line (`plain` style) or as the entire content of a host-language comment.
The line may be indented. Nothing may precede the comment except whitespace, and
nothing may follow it except whitespace and the line ending.

```text
directive = "@" "-"? keyword operands? (WS "-")?
keyword   = "if" | "elif" | "else" | "for" | "with" | "let" | "include"
          | "raw" | "match" | "case" | end
end       = "end" (WS ("if" | "for" | "with" | "match" | "raw"))?
operands  = whitespace-separated operands in the expression grammar
```

`@`, the optional leading trim marker and the keyword are adjacent; `@ if` is
not a directive. The keyword ends at whitespace, at `-`, or at the end of the
directive, so `@iffy` and `@include_me` are text. A keyword followed directly
by `-` is still recognized, so `@else-` is a malformed directive (`TPL_PARSE`):
a trailing trim marker is a standalone `-` preceded by whitespace (`@else -`,
`@end if -`). Trim markers and the optional `@end` label are
defined in `030-template-extensions.md`. Operands are written without
surrounding parentheses; a parenthesized operand is an ordinary grouping
expression. Directives without operands are written bare: `@else`, `@end`,
`@raw`.

### Directive operands

| Directive | Operands |
| --- | --- |
| `@if EXPR`, `@elif EXPR` | One expression |
| `@for NAME… in EXPR` | A lambda parameter list, `in`, one expression |
| `@for NAME… in EXPR sep EXPR` | As above, then `sep` and a separator expression |
| `@with EXPR` | One expression |
| `@match EXPR` | One expression |
| `@case PATTERN` | One arm pattern (`055` §4.5) |
| `@let NAME = EXPR` | A name, `=`, one expression |
| `@include PATH` | One source operand |
| `@include PATH EXPR` | A source operand followed by a payload expression |

An expression operand extends to the end of the directive, or to the next
directive keyword for `@for` (`sep` is a keyword only within `@for`). Operands
are application by juxtaposition, so `@if exists? ./file` tests
`exists? ./file`, and `@if enabled` reads `enabled` without calling it.

`@for` parameters follow lambda parameter rules (`055` §4.1), including a rest
parameter: `@for first rest... in rows`. The body is rendered once per item.
With a single name, the name binds the item. With several names, or a rest
parameter, each item must be a list and is destructured positionally, as if the
list were spread into a lambda over those names; arity follows `005`, and a
non-list item is `EXPR_INVALID`. Loops bind no implicit names: positions and
record keys come from explicit data operations (`007`):

```text
@for i x in indexed xs
@{i}: @{x}
@end
@for e in items settings sep ", "
@{e.key}=@{e.value}
@end
```

The `@include` source operand is a single postfix operand: a path atom, a
string, a name with optional field access, or a parenthesized expression. The
rest of the directive, if any, is the payload expression:

```text
@include ./row.html
@include ./row.html {post}
@include ./pages/@{name}.html {title: page.title, post}
@include (template-for kind) payload
```

Recognition is keyword-gated and whole-line; comment styles also accept a
complete directive comment inline (`030-template-extensions.md`). A comment line that carries no
known keyword is ordinary text and is emitted unchanged. This keeps host text
such as `@media`, `@override`, and `@example.com` untouched without escaping.

### Comment Styles

A style names how a directive is wrapped. Block styles require the line to be
one complete comment; line styles require the directive to be the only content
after the prefix.

| Style | Block form | Line prefixes |
| --- | --- | --- |
| `html` | `<!-- @... -->` | |
| `c` | `/* @... */` | `//` |
| `hash` | | `#` |
| `dash` | | `--` |
| `semi` | | `;` |
| `percent` | | `%` |
| `powershell` | `<# @... #>` | `#` |
| `batch` | | `REM`, `::` |
| `plain` | none | none |
| `none` | directives disabled; inline interpolation only |

A mustache-style HTML page needs no rewriting to become a template:

```html
<ul>
<!-- @for post in posts -->
  <li><a href="@{post.url}">@{post.title}</a>
<!-- @if post.pinned -->
    <span class="pin">pinned</span>
<!-- @else -->
    <span>new</span>
<!-- @end -->
  </li>
<!-- @end -->
</ul>
```

Opened raw, the directives are ordinary comments and the file remains valid
HTML. Rendered, they disappear and the list expands.

### Directives

- `@if EXPR` opens a conditional block. The block renders when `EXPR` is true.
  Only `nil` and `false` are false.
- `@elif EXPR` adds another condition to the innermost open `@if`.
- `@else` renders the fallback body of the innermost open `@if`, or of the
  innermost open `@match`.
- `@for NAME… in EXPR` opens a repetition block. The body renders once per item
  with the parameters bound, and the results concatenate with no separator.
  `@for NAME… in EXPR sep SEP` puts `SEP` between results.
- `@with EXPR` opens a scope block. `EXPR` must be a record; its fields bind as
  names while the body renders.
- `@match EXPR` opens a pattern dispatch block of `@case PATTERN` clauses and an
  optional terminal `@else` (`030-template-extensions.md`).
- `@let NAME = EXPR` binds `NAME` to `EXPR` for the remainder of the enclosing
  block. It has no `@end`.
- `@include PATH` renders `PATH` as a document template in the current scope.
  `@include PATH PAYLOAD` renders it with `PAYLOAD` bound instead.
- `@raw` opens a verbatim block. Text through the matching `@end` is emitted
  unchanged: no interpolation and no directive recognition.
- `@end` closes the innermost open block: `@if`, `@for`, `@with`, `@match`, or
  `@raw`. An optional label (`@end for`) must name the block it closes
  (`030-template-extensions.md`).

`@elif` has no meaning outside an `@if`, `@case` none outside a `@match`, and
`@else` none outside either; `@end` has no meaning without an open block.

### Lowering

A body is a sequence of literal text, inline interpolations, and nested blocks.
Write `L(x)` for the lowering of `x`. Literal text lowers to a string literal.
An inline `@{EXPR}` lowers to `EXPR`, parenthesized. A body `B1 ... Bn` lowers
to `template-cat L(B1) ... L(Bn)`, and an empty body lowers to `""`. Blocks
lower as follows:

```text
L(@if c A @elif c2 B @else C @end)  = (if c then L(A) elif c2 then L(B) else L(C))
L(@if c A @end)                     = (if c then L(A) else "")
L(@for p in xs A @end)              = (xs |> map (p => L(A)) |> join "")
L(@for p... in xs A @end)           = (xs |> map (e => (p... => L(A)) ...e) |> join "")
L(@for p... in xs sep s A @end)     = (xs |> map (e => (p... => L(A)) ...e) |> join s)
L(@with r A @end)                   = (with r L(A))
L(@match s @case P A @else B @end)  = (match s
                                          P    => L(A)
                                          else => L(B))
L(@let n = e rest)                  = ((n => L(rest)) e)
L(@include p)                       = (render p)
L(@include p y)                     = (render y p)
L(@raw ... @end)                    = the raw text as a string literal
```

In the `@for` rows, `p` is a single name, `p...` stands for several names or a
rest parameter, and `e` is a fresh name. A `@match` without `@else` lowers with
`else => ""`. Blocks nest arbitrarily.
Lowering happens when the template is parsed, so a malformed template fails
before any body text is produced. Lowered expressions retain the authored spans
of their directives and interpolations.

### Foreign-Shell Recipe Bodies

A rule body whose `shell` resolves to an external shell is a document template
in `plain` style whose rendered output is the shell script
(`055-unified-kame-kash-language.md` §10.2). Directive lines are recognized by
the same keyword-gated, whole-line rule as any document template, and each is
removed together with its rule indent and line ending. Selected body lines
render as shell text, so indentation inside a branch is preserved. Inline
`@{EXPR}` interpolation keeps its template meaning; `$` belongs to the shell and
passes through. Rule context is reached through interpolation, for example
`@{inputs.0}` and `@{outputs.0}`. A non-directive line such as a comment
carrying `@media` is emitted unchanged. The escaping rules below (`@@` and
`@raw`) apply to foreign-shell recipes identically. Kash bodies parse directly
as Kash and do not use this template layer (`038-direct-kash-and-values.md`).

```kame
rule ./build/app.tar : ./build/app ; {shell: "bash"}
	set -euo pipefail
	@if release
	strip "@{inputs.0}"
	@end
	tar -cf "@{outputs.0}" "@{inputs.0}"
```

### Whitespace

A directive line is removed together with its indentation, its comment
delimiters, and its line ending. Directive lines therefore leave no blank-line
debt and do not shift surrounding lines. Non-directive text, including a comment
that carries no directive, is emitted byte for byte. Inline interpolation never
removes surrounding whitespace.

### Escaping and Raw Regions

- `@@` emits a literal `@`. At the directive position it makes the rest of the
  line ordinary text: `@@if x` emits `@if x`.
- `@raw` through the matching `@end` emits the enclosed lines verbatim, which is
  the escape hatch for text that would otherwise look like a directive or an
  interpolation.

### Errors

An unterminated block, a stray `@end`, an `@elif` without an enclosing `@if`, a
`@case` without an enclosing `@match`, and an `@else` outside both are
`TPL_BLOCK`. A malformed directive or operand, or a `@raw` without `@end`, is
`TPL_PARSE` at the offending span. Parsing a document consumes each line exactly
once, so a directive keyword inside a `@raw` region is never recognized.

## Rendering

### Source Resolution

`render` accepts a `Path`, `String`, or `Bytes` template source:

- A `Path` is a template file. Kame reads it under the `read` capability and
  registers it as a dependency, so editing the template invalidates its
  consumers.
- A `String` or `Bytes` value is template content. A `String` is never treated
  as a path, whatever its text.

Use `Bytes` to render text that was already read:

```kame
page = render {title} "html" (read ./page.html)
```

### Style Selection

For a `Path` source, the comment style is inferred from the extension:

| Style | Extensions (case-insensitive) |
| --- | --- |
| `html` | html, htm, xml, svg, vue, md, markdown |
| `c` | c, h, cc, cpp, hpp, java, js, mjs, ts, tsx, jsx, go, rs, css, scss, less, php, swift, kt |
| `hash` | sh, bash, zsh, yaml, yml, py, rb, toml, ini, conf, properties, pl, r |
| `dash` | sql, lua, hs, elm, ada |
| `semi` | lisp, clj, cljs, el, scm, asm |
| `percent` | tex, erl, hrl, m |
| `powershell` | ps1, psm1, psd1 |
| `batch` | bat, cmd |
| `plain` | kmt |

A `STYLE` string immediately before the source overrides the inferred style
(`render STYLE SOURCE` or `render PAYLOAD STYLE SOURCE`):

```kame
page = render {title} "html" ./page.tmpl
```

An unknown or absent extension with no override is `TPL_STYLE`. Content defaults
to the `plain` style; `"none"` disables directives and renders inline
interpolation only. Style names are matched literally and case-insensitively.

### Payload

`render PAYLOAD SOURCE` binds `PAYLOAD`, a record, for the duration of
rendering. The payload is applied with the semantics of `with`, so `@{title}`
resolves to its field. The lexical scope of the caller remains visible as the
parent, so a template may also reference definitions and captured values. With
no payload the caller scope is used unchanged.

### Dependencies

Reading a template path registers a file dependency. Operations evaluated by the
template register their own dependencies, so `files ./posts/*.md`,
`read ./data.txt`, and `env/RELEASE` inside a template participate in freshness
exactly as they do elsewhere. Rendering may suspend on those dependencies and
resume when they complete; nested dynamic dependencies are honored, not dropped.
Rendering never parses or runs its output, and expressions evaluated during
planning cannot run processes (`055` §14).

## Primitives

### template-cat

The lowering primitive `template-cat VALUE...` renders each argument with the
interpolation rules above (scalars and paths as text, `nil` as empty) and
concatenates the results. A list, record, `Bytes`, or callable argument is
`EXPR_INVALID`, matching inline interpolation. The result is a `String`; zero
arguments produce the empty string.

This rendering primitive is distinct from scalar `str` conversion in
`038-direct-kash-and-values.md`. `cat` is not a Kame string operation; in Kash
command position `cat` names the external executable.

### text

`text VALUE` converts `Bytes` to a UTF-8 `String` and returns a `String`
argument unchanged. Invalid UTF-8 is `EXPR_INVALID`.

### Conditionals

Template conditionals lower to `if … then … elif … then … else …` expressions
(`055` §4.5). Only the selected arm is evaluated, so a branch may reference a
name that is valid only when selected. Only `nil` and `false` are false.

### with

`with RECORD BODY` is a special form. It evaluates `RECORD`, requires a record,
creates a child scope, binds each field key as a name, and evaluates `BODY` in
that scope, returning its value. A duplicate key re-binds, so the last field
wins. A non-record `RECORD` is `EXPR_INVALID`.

### render

`render PAYLOAD? STYLE? SOURCE` parses `SOURCE` as a document template and
returns the rendered `String`. Source resolution, style selection, payload
binding, and dependency behavior are defined above. `@include` lowers to
`render`, so recursive inclusion is possible; a template that includes itself,
directly or transitively, is `TPL_CYCLE` before any output is produced.

## Inline Templates

A definition may hold a multi-line template without escaping by using a verbatim
string literal delimited by three or more double quotes:

```kame
CARD = """
<article>
<!-- @if post.pinned -->
<span class="pin">pinned</span>
<!-- @end -->
<h2>@{post.title}</h2>
</article>
"""

task default : ./build/post.html

rule ./build/post.html = render {post: {title: "Hello, world", pinned: true}} "html" CARD
```

The content between the opening and closing runs of `"""` is raw: no escape
processing, no interpolation, and no directive recognition. It is an ordinary
`String` value, so it may be passed to `render` with an explicit style, used as
a definition, or concatenated. To include `"""` in the content, use a longer
delimiter such as `""""`. A run of four or more quotes opens a verbatim literal
whose closing run has exactly the same length.

## Command-Line Interface

```text
kame do render [-c TEXT | FILE] [--define NAME=VALUE]...
               [--comment STYLE] [--check] [CAPABILITY OPTIONS]
```

`kame do render` renders one template to stdout with no added trailing newline.
With no `FILE` it reads stdin. `--define NAME=VALUE` adds a string field to the
payload and repeats. `--comment STYLE` overrides extension inference. `--check`
parses and reports diagnostics without producing output. Capability options and
grants are those of expression evaluation with `kame do run`: `read` is denied
by default and granted with `--allow-read`.

## Diagnostics

This specification registers:

| Code | Severity | Meaning |
| --- | --- | --- |
| `TPL_PARSE` | error | Malformed directive, directive operand, or verbatim literal |
| `TPL_BLOCK` | error | Unterminated, stray, or mismatched block directive |
| `TPL_STYLE` | error | Missing or unknown comment style |
| `TPL_CYCLE` | error | Recursive template inclusion |

Diagnostics point at the template source. When the template is a file, spans
refer to that file rather than to the caller. Failures inside a directive
operand or an interpolation keep the authored span of that expression.

## Formatting

Document templates are data, not script sources. `kame do fmt` on a Kame or
Kash source does not rewrite template text. Explicit `kame do fmt --lang
template` formats document directives while preserving host document bytes,
under `030-template-extensions.md`; select `--comment STYLE` when required.
Directive operands and interpolated expressions use canonical expression
formatting (`055` §15). A verbatim string literal used as a template is
preserved byte for byte inside a definition. Formatting a source that contains a
verbatim literal is idempotent.

## Related Specifications

`055-unified-kame-kash-language.md` defines the expression grammar, interpolation
and directive overview, `004-language.md` defines grammar detail,
`005-evaluation.md` defines lazy evaluation and scope/dependency behavior, this
specification defines `template-cat`, `text`, `with`, and `render`, `009-cli.md`
defines `do render`, and `011-diagnostics.md` registers the diagnostic codes.

## Template Extensions

`030-template-extensions.md` defines labeled block endings, pattern matching
blocks, trim markers, inline blocks, content-based style inference, PowerShell
and batch comment conventions, and canonical formatting.

## Acceptance Tests

- `@{name}` and `@{project.name}` render values; `@{version ()}` invokes a
  zero-argument function, and `@{join ", " files}` invokes with arguments.
  Interpolations containing nested strings, records, and groups retain their
  authored spans.
- Unknown references fail with `REF_MISSING`; rendering a callable, list,
  record, or `Bytes` value is `EXPR_INVALID` and names the actual value kind.
- `if` expressions, `match`, lambdas, sections, and `|>` pipes inside `@{…}`
  have ordinary expression semantics. Nested strings, captures, zero-argument
  calls, escapes, and raw regions parse and format consistently across strings,
  documents, recipes, headers, and paths.
- `@if enabled` reads a value; `@if ready? ()` calls a zero-argument predicate;
  `@if exists? ./file` applies `exists?`.
- A `hash` document with `# @if cond` renders its consequent when `cond` is
  true and its `@else` body otherwise.
- `@elif` chains select the first true branch; a false chain renders `@else` or
  nothing, and the chain lowers to one `if … elif … else` expression.
- A foreign-shell recipe body removes `@if`/`@else`/`@end` directive lines with
  its rule indent, renders only the selected branch lines as shell text, and
  passes `$` through.
- `@for x in xs` renders its body once per item in order; a `sep` separator is
  inserted between results and nowhere else.
- `@for` binds lambda parameter lists, including a rest parameter; with several
  names, `@for i x in indexed xs` destructures each pair positionally, and a
  non-list item is `EXPR_INVALID`. No implicit `index` or `key` name is bound.
- `@-for x in xs` and `@end for -` trim adjacent whitespace; `@else-` is
  `TPL_PARSE`; `@end if` closing a `@for` is `TPL_BLOCK`.
- `@with {a: 1}` binds `a` in its body; a non-record operand is `EXPR_INVALID`.
- `@let n = count xs` binds `n` for the rest of the enclosing block.
- `@include ./row.html` renders the included file in the current scope;
  `@include ./row.html {post}` binds the payload.
- `@match` selects the first matching `@case`, binds its named captures, and
  falls back to `@else` or nothing.
- `@raw` through `@end` emits `@if x` and `@{x}` literally; `@@` emits `@`.
- A non-directive comment line, `@media`, `@iffy`, and `@example.com` pass
  through unchanged.
- A directive line's indentation, delimiters, and line ending are removed.
- An unterminated block, a stray `@end`, and `@elif` outside `@if` are
  `TPL_BLOCK`; a malformed operand is `TPL_PARSE`.
- `render` accepts a `Path`, a `String`, and `Bytes`; a path is read and
  registered as a dependency; a `String` beginning with `./` is content, not a
  path; `String` and `Bytes` content without an explicit style uses `plain`.
- Extension inference selects `html`, `c`, `hash`, `dash`, `semi`, `percent`,
  and `plain` for `.kmt`; an unknown extension without an override is
  `TPL_STYLE`.
- `template-cat "a" nil 1` is `a1`; a list, record, or `Bytes` argument is
  `EXPR_INVALID`.
- `text (read ./page.html)` yields its UTF-8 string; invalid UTF-8 is
  `EXPR_INVALID`.
- A template conditional evaluates only the selected branch.
- A template rendered from a file rebuilds when the file changes and when a
  `files`, `read`, or `env/NAME` inside it changes.
- A self-including template is `TPL_CYCLE`.
- A verbatim literal round-trips through parse and format byte for byte.
- `kame do render --define k=v page.html` prints the rendered text; `--check`
  reports diagnostics without output.
