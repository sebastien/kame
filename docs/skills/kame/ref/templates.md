# String and document templates

Templates evaluate Kame expressions and render values as text. They do not
parse generated text as Kash or automatically quote shell arguments/escape HTML.

## Inline expansions and selectors

```text
Hello, @(name). There are @(count files) files.
```

`@(reference)` is a reference expansion; `@(operation args...)` is an expression
expansion. Do not use `@{reference}`. Lists render with one space between items;
nil renders empty. Records and bytes require explicit conversion (`str` or
`text`) rather than direct interpolation.

| Rule selector | Meaning |
| --- | --- |
| `@<` / `@<*` / `@<#` | First input / all inputs / count. |
| `@<N` / `@<A..B` | Input index / half-open slice. |
| `@>` / `@>*` / `@>#` | First output / all outputs / count. |
| `@>N` | Output index. |

Function selectors are `@_` (first argument), `@*` (all), `@#` (count), and
`@N` (index). Indexes are zero-based; negative indexes count from the end.
Missing context and invalid bounds produce diagnostics, not empty text.
Expression strings also allow `{(EXPRESSION)}` interpolation, but selectors
are template-only. Backslash can escape `@` and `\` in string templates.

## Document directives

Document templates add keyword-gated, whole-line directives in plain text or
host-language comments. Only whitespace may surround the directive/comment.
`@if (` and `@ if` are not directives; `@if(...)` is.

```html
<h1>@(title)</h1>
<ul>
<!-- @for([post] posts) -->
  <li>@(post.title)</li>
<!-- @end -->
</ul>
```

| Directive | Effect |
| --- | --- |
| `@if(TEST)` / `@elif(TEST)` / `@else` | Select one branch using Kame truth (only nil/false are false). |
| `@for([PARAM...] LIST [SEP])` | Render once per item, optionally joining results with `SEP`. |
| `@with(RECORD)` | Bind fields in a nested scope. |
| `@let(NAME EXPR)` | Bind for the rest of the enclosing block; no closing `@end`. |
| `@include(PATH [PAYLOAD])` | Render another template with current scope or explicit payload. |
| `@raw` | Emit enclosed text verbatim, without expansions or directive recognition. |
| `@end` | Close the innermost `if`, `for`, `with`, or `raw` block. |

Directive lines, including their line endings, disappear from output. Other
text is preserved; unknown keywords such as `@media` are ordinary text.
Use `@@` at a directive position, `\@` in body text, or a raw block for literal
template-looking syntax. Unbalanced blocks are errors.

Styles: `html` (`<!-- -->`), `c` (`/* */` or `//`), `hash` (`#`), `dash` (`--`),
`semi` (`;`), `percent` (`%`), `plain` (no wrapper), and `none` (inline only).
Kash `$name`, `${name}`, and `$(command)` remain literal document text;
template directives are not Kash meta-programming forms.

## Render with data

```kame
(render ./page.html [title: "News" posts: [[title: "Hello"]]])
(render ./page.tmpl [title: "News"] "html")
(render (read ./page.html) [title: "News"] "html")
```

`(render SOURCE [PAYLOAD] [STYLE])` returns a string:

- A string starting with `./`, `../`, or `/` is a file path, read under the read
  capability and tracked as a dependency. Other strings and bytes are content.
- File extensions infer style (e.g. HTML/XML/Markdown → `html`, JS/Go/C/CSS →
  `c`, YAML/Python/TOML → `hash`). Unknown extensions need an explicit style.
  Content defaults to `plain`; specify a style for host-comment content.
- Payload must be a record; its fields shadow caller bindings for rendering.
  The caller's scope remains visible as the parent.
- Template reads and evaluated `read`, `wildcard`, or `env` calls contribute
  dynamic dependencies in builds. Recursive includes are diagnosed.

Store raw templates with a verbatim literal, then explicitly render:

```kame
CARD = """
<h1>@(title)</h1>
"""

default : ./build/page.html

./build/page.html : ./page.html
	@(yield (render ./page.html [title: "Hello"]))
```

The literal's content is untouched until `render`; a longer quote delimiter
allows triple quotes in its content. `cat` concatenates renderable values;
`text` converts valid UTF-8 bytes to a string.

## Recipes and verification

Recipes are `plain` document templates rendering to one shell script. You can
use `@if`/`@for` directives there; selected non-directive lines remain shell
text, with recipe-local state persisting between lines.

```sh
kame do run --lang expr --allow-read -c '(render ./page.html [title: "Hello"])'
kame do render --define title=Hello ./page.html
kame do render --check ./page.html
printf '%s\n' 'Hello @(name)' | kame do parse --lang template
```

`--lang template` tools handle the inline template grammar, not arbitrary
host-language document files. `do fmt` must not rewrite document templates;
verbatim literal contents are preserved. `kame do render` renders documents;
repeatable `--define NAME=VALUE` supplies literal string payload fields,
`--comment STYLE` selects the directive comment style, and `--check` validates
syntax without evaluating directives or opening includes.
