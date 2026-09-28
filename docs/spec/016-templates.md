# Text Templates

## Purpose

This specification makes Kame's template language usable on arbitrary text
files, so a build can render HTML, configuration, source code, or any other
text from data. It adds:

- **Document templates**: a text file may carry Kame directives in the host
  language's comment syntax, giving conditionals, repetition, binding, and
  inclusion without leaving the file's own format.
- **A runtime `render` operation** that parses and evaluates a template value
  or file against a record payload.
- **Supporting primitives**: the `cat` and `text` operations and the `if` and
  `with` special forms.
- **A `kame do render` command** for standalone rendering.

Document templates lower to the expression language: every directive compiles
to `if`, `map`, `join`, `let`, `with`, `cat`, or `render`. There is no second
evaluator, so templates share laziness, capabilities, and dynamic dependencies
with the rest of Kame. Inline `@(...)`, `@{...}`, and selectors remain the
native template form used by recipes and definitions.

## Document Templates

A document template is any UTF-8 text interspersed with directive lines and
inline expansions. Text that is not a directive line is emitted verbatim.

### Directive Lines

A directive line is a whole line carrying exactly one directive, either as the
entire line (`plain` style) or as the entire content of a host-language comment.
The line may be indented. Nothing may precede the comment except whitespace, and
nothing may follow it except whitespace and the line ending.

```text
directive = "@" keyword [ "(" argument+ ")" ]
keyword   = "if" | "elif" | "else" | "for" | "with" | "let" | "include"
          | "raw" | "end"
```

The argument list is parsed by the expression parser, so it may contain nested
parentheses, brackets, lists, records, strings, and lambdas. `@`, the keyword,
and `(` must be adjacent; `@ if` and `@if (` are not directives. Directives with
no arguments are written bare: `@else`, `@end`, `@raw`.

Recognition is keyword-gated and whole-line. A comment line that carries no
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
| `plain` | none | none |
| `none` | directives disabled; inline expansions only |

A mustache-style HTML page needs no rewriting to become a template:

```html
<ul>
<!-- @for([post] posts) -->
  <li><a href="@(post.url)">@{post.title}</a>
<!-- @if(post.pinned) -->
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

- `@if(EXPR)` opens a conditional block. The block renders when `EXPR` is true.
  Only `:nil` and `:false` are false.
- `@elif(EXPR)` adds another condition to the innermost open `@if`.
- `@else` renders the fallback body of the innermost open `@if`.
- `@for([PARAM...] LIST)` opens a repetition block. The body renders once per
  list item with the parameters bound, and the results concatenate with no
  separator. `@for([PARAM...] LIST SEP)` uses `SEP` between results.
- `@with(EXPR)` opens a scope block. `EXPR` must be a record; its fields bind as
  names while the body renders.
- `@let(NAME EXPR)` binds `NAME` to `EXPR` for the remainder of the enclosing
  block. It has no `@end`.
- `@include(PATH)` renders `PATH` as a document template in the current scope.
  `@include(PATH PAYLOAD)` renders it with `PAYLOAD` bound instead.
- `@raw` opens a verbatim block. Text through the matching `@end` is emitted
  unchanged: no inline expansion and no directive recognition.
- `@end` closes the innermost open block: `@if`, `@for`, `@with`, or `@raw`.

`@elif` and `@else` have no meaning outside an `@if`; `@end` has no meaning
without an open block.

### Lowering

A body is a sequence of literal text, inline expansions, and nested blocks.
Write `L(x)` for the lowering of `x`. Literal text lowers to a string literal,
and an inline `@(E)` or `@{R}` lowers to `E` or `R`. A body `B1 ... Bn` lowers to
`(cat L(B1) ... L(Bn))`, and an empty body lowers to `(cat)`, the empty string.
Blocks lower as follows:

```text
L(@if(c) A @elif(c2) B @else C @end) = (if c L(A) (if c2 L(B) L(C)))
L(@for([p...] xs) A @end)            = (join (map ([p...] L(A)) xs) "")
L(@for([p...] xs sep) A @end)        = (join (map ([p...] L(A)) xs) sep)
L(@with(r) A @end)                   = (with r L(A))
L(@let(n e) rest)                    = (let [n e] L(rest))
L(@include(p))                       = (render p)
L(@include(p y))                     = (render p y)
L(@raw ... @end)                     = the raw text as a string literal
```

Blocks nest arbitrarily. Lowering happens when the template is parsed, so a
malformed template fails before any body text is produced.

### Whitespace

A directive line is removed together with its indentation, its comment
delimiters, and its line ending. Directive lines therefore leave no blank-line
debt and do not shift surrounding lines. Non-directive text, including a comment
that carries no directive, is emitted byte for byte. Inline expansions never
remove surrounding whitespace.

### Escaping and Raw Regions

- `@@` at the directive position emits a literal `@` and the rest of the line as
  text.
- `\@` in body text emits a literal `@`.
- `@raw` through the matching `@end` emits the enclosed lines verbatim, which is
  the escape hatch for text that would otherwise look like a directive.

### Errors

An unterminated block, a stray `@end`, and an `@elif` or `@else` without an
enclosing `@if` are `TPL_BLOCK`. A malformed directive or argument list, or a
`@raw` without `@end`, is `TPL_PARSE` at the offending span. Parsing a document
consumes each line exactly once, so a directive keyword inside a `@raw` region is
never recognized.

## Rendering

### Source Resolution

`(render SOURCE)` accepts a `String` or `Bytes` template source:

- A `String` beginning with `./`, `../`, or `/` is a file path. Kame reads it
  under the `read` capability and registers it as a dependency, so editing the
  template invalidates its consumers.
- Any other `String`, and every `Bytes` value, is template content.

Use `Bytes` to render text that was already read:

```kame
(render (read ./page.html) [title: title] "html")
```

### Style Selection

For a file path, the comment style is inferred from the extension:

| Style | Extensions (case-insensitive) |
| --- | --- |
| `html` | html, htm, xml, svg, vue, md, markdown |
| `c` | c, h, cc, cpp, hpp, java, js, mjs, ts, tsx, jsx, go, rs, css, scss, less, php, swift, kt |
| `hash` | sh, bash, zsh, yaml, yml, py, rb, toml, ini, conf, properties, pl, r |
| `dash` | sql, lua, hs, elm, ada |
| `semi` | lisp, clj, cljs, el, scm, asm |
| `percent` | tex, erl, hrl, m |

A third `render` argument overrides the inferred style:

```kame
(render ./page.tmpl [title: title] "html")
```

An unknown or absent extension with no override is `TPL_STYLE`. Content defaults
to the `plain` style; `"none"` disables directives and renders inline expansions
only. Style names are matched literally and case-insensitively.

### Payload

`(render SOURCE PAYLOAD)` binds `PAYLOAD`, a record, for the duration of
rendering. The payload is applied with the semantics of `with`, so `@{title}`
and `@(title)` resolve to its fields. The expression scope of the caller remains
visible as the parent, so a template may also reference definitions and captured
values. With no payload the caller scope is used unchanged.

### Dependencies

Reading a path registers a file dependency. Operations evaluated by the
template register their own dependencies, so `@(wildcard ./posts/*.md)`,
`@(read ./data.txt)`, and `@(env "RELEASE")` participate in freshness exactly as
they do in a recipe. Rendering may suspend on those dependencies and resume when
they complete; nested dynamic dependencies are honored, not dropped.

## Primitives

### cat

`(cat VALUE...)` stringifies each argument with the rendering rules of
`005-evaluation.md` (scalars and lists; a list joins its items with one ASCII
space; `:nil` is empty) and concatenates the results. A record or `Bytes`
argument is `EXPR_INVALID`, matching inline template rendering. The result is a
`String`; zero arguments produce the empty string.

### text

`(text VALUE)` converts `Bytes` to a UTF-8 `String` and returns a `String`
argument unchanged. Invalid UTF-8 is `EXPR_INVALID`.

### if

`(if COND THEN [ELSE])` is a special form. It evaluates `COND`, then exactly one
of `THEN` or `ELSE`, and returns that value. With no `ELSE` a false `COND`
returns `:nil`. Only `:nil` and `:false` are false. The unused branch is not
evaluated, so a branch may reference a name that is only valid when selected.

### with

`(with RECORD BODY...)` is a special form. It evaluates `RECORD`, requires a
record, creates a child scope, binds each field key as a name, and evaluates the
body in that scope, returning the body's final value. A duplicate key re-binds,
so the last field wins. An empty body returns `:nil`.

### render

`(render SOURCE [PAYLOAD] [STYLE])` parses `SOURCE` as a document template and
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
<!-- @if(post.pinned) -->
<span class="pin">pinned</span>
<!-- @end -->
<h2>@{post.title}</h2>
</article>
"""

default : ./build/post.html

./build/post.html :
	@(yield (render CARD [post: [title: "Hello, world" pinned: :true]] "html"))
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
grants are those of `kame do expr`: `read` is denied by default and granted with
`--allow-read`.

## Diagnostics

This specification registers:

| Code | Severity | Meaning |
| --- | --- | --- |
| `TPL_PARSE` | error | Malformed directive, argument list, or verbatim literal |
| `TPL_BLOCK` | error | Unterminated, stray, or mismatched block directive |
| `TPL_STYLE` | error | Missing or unknown comment style |
| `TPL_CYCLE` | error | Recursive template inclusion |

Diagnostics point at the template source. When the template is a file, spans
refer to that file rather than to the caller.

## Formatting

Document templates are data, not Kame sources. `kame do fmt` does not rewrite
them, and a verbatim string literal used as a template is preserved byte for
byte inside a definition. Formatting a definition or script that contains a
verbatim literal is idempotent.

## Related Specifications

- `004-language.md`: add the `if` and `with` special forms, the verbatim
  multi-line string literal, and a pointer to this specification for document
  templates.
- `005-evaluation.md`: specify lazy `if` and scope-binding `with`, and the
  dependency behavior of `render`.
- `007-library.md`: add `cat`, `text`, and `render`.
- `009-cli.md`: add the `do render` command.
- `011-diagnostics.md`: register the codes above.

## Deferred

- Block labels such as `@end(for)` for stronger matching diagnostics.
- Whitespace trim markers around directive lines.
- Inline (non-line) block directives.
- Automatic comment-style detection from content.
- Additional host comment conventions.
- Template formatting and normalization.
- An index or key in `@for` beyond the parameter bindings.

## Acceptance Tests

- A `hash` document with `# @if(cond)` renders its consequent when `cond` is
  true and its `@else` body otherwise.
- `@elif` chains select the first true branch; a false chain renders `@else` or
  nothing.
- `@for([x] xs)` renders its body once per item in order; an optional separator
  is inserted between results and nowhere else.
- `@for` binds lambda parameter lists, including a rest parameter.
- `@with([a: 1])` binds `a` in its body; a non-record argument is
  `EXPR_INVALID`.
- `@let(n (count xs))` binds `n` for the rest of the enclosing block.
- `@include(./row.html)` renders the included file in the current scope;
  `@include(./row.html [post: post])` binds the payload.
- `@raw` through `@end` emits `@if(...)` and `@(...)` literally.
- A non-directive comment line, `@media`, and `@example.com` pass through
  unchanged.
- A directive line's indentation, delimiters, and line ending are removed.
- An unterminated block, a stray `@end`, and `@elif` outside `@if` are
  `TPL_BLOCK`; a malformed argument list is `TPL_PARSE`.
- `render` accepts a path, content, and `Bytes`; a path is read and registered as
  a dependency; a `Bytes` source requires an explicit style.
- Extension inference selects `html`, `c`, `hash`, `dash`, `semi`, and `percent`;
  an unknown extension without an override is `TPL_STYLE`.
- `(cat "a" :nil 1)` is `a1`; a record or `Bytes` argument is `EXPR_INVALID`.
- `(text (read ./page.html))` yields its UTF-8 string; invalid UTF-8 is
  `EXPR_INVALID`.
- `(if c a b)` evaluates only the selected branch.
- A template rendered from a file rebuilds when the file changes and when a
  `wildcard`, `read`, or `env` inside it changes.
- A self-including template is `TPL_CYCLE`.
- A verbatim literal round-trips through parse and format byte for byte.
- `kame do render --define k=v page.html` prints the rendered text; `--check`
  reports diagnostics without output.
- Rendering under `mem.Tracker` leaks no values.
