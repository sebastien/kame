# String and document templates

Templates render Kame values as text. Interpolation leaves HTML and shell
quoting to the surrounding document.

## Interpolation

~~~text
Hello @{name}. There are @{count files} files. Version @{version ()}.
~~~

Use any value expression inside `@{EXPR}`, including field access, calls,
operators, and value pipes. A bare name reads its value. Write `name ()` to
call a zero-argument function.

Strings, numbers, booleans, symbols, and paths render as text. Nil renders as
empty text. Use `join` for lists, `json` or `fmt` for structured values, and
`text` for UTF-8 bytes. `@@` emits one at-sign. At the directive position it
also makes the remainder of that line literal. Use `@raw` for verbatim blocks.

## Document directives

Place each directive on a whole line in plain templates. In HTML, C, and
PowerShell, a complete block-comment wrapper can also delimit an inline block
directive. Directive lines disappear with their line endings.

~~~html
<h1>@{title}</h1>
<ul>
<!-- @for post in posts -->
  <li>@{post.title}</li>
<!-- @end for -->
</ul>
~~~

| Directive | Effect |
| --- | --- |
| `@if TEST`, `@elif TEST`, `@else` | Select the first true branch. Nil and false are false. |
| `@for NAME… in EXPR [sep EXPR]` | Render each list element. Multiple parameters or a rest parameter destructure an element list positionally. |
| `@with RECORD` | Bind fields while rendering the block. |
| `@let NAME = EXPR` | Bind a value through the end of the enclosing block. |
| `@include PATH [PAYLOAD]` | Load a template relative to the containing template. Group a path-producing call, as in `@include (str "pages/" name) payload`. |
| `@raw` | Emit verbatim text through `@end` or `@end raw`. |
| `@match EXPR`, `@case PATTERN`, `@else` | Render the first matching branch with its named captures in scope. |
| `@end [LABEL]` | Close the innermost block. A label must match its opening keyword. |

Use `indexed LIST` for explicit position–value pairs and `items RECORD` for
explicit key–value records in storage order. Loops bind their declared names.

A hyphen after `@` trims whitespace on the left. A standalone trailing hyphen
trims whitespace on the right. For example, write `@-if ready -` or
`@end if -`. Use an actual space before the trailing hyphen.

Styles include `html`, `c`, `hash`, `dash`, `semi`, `percent`, `powershell`,
`batch`, `plain`, and `none`. Style `none` enables interpolation only. Style
`auto` uses a registered file extension or detects a unique comment style in
the source.

## Render with data

~~~kame
page = render ./page.html {title: "News", posts: [{title: "Hello"}]}
other = render {title: "News"} "html" ./page.tmpl
cached = render {title: "News"} "html" (read ./page.html)
~~~

`render SOURCE PAYLOAD?` returns a string. Use `render STYLE SOURCE` or
`render PAYLOAD STYLE SOURCE` to choose a style. A `Path` loads a template
under the read capability and registers a file dependency. `String` and
`Bytes` values provide inline content, with plain style by default. Paths
infer style from their extension, including `.kmt` for plain templates. An
explicit style overrides inference. A payload record binds its fields in the
template scope.

Triple-quoted literals hold template source until `render` is called.

~~~kame
CARD = """
<h1>@{title}</h1>
"""

rule ./build/page.html = render {title: "Hello"} "html" CARD
~~~

Foreign-shell recipes render as plain document templates. Shell expressions
such as `$name` and `$(command)` pass through to the shell. Kash recipes use
Kash syntax. [Kash](./kash.md)

~~~sh
kame do run --lang expr --allow-read -c 'render ./page.html {title: "Hello"}'
kame do render --define title=Hello ./page.html
kame do render --check ./page.html
kame do fmt --lang template --comment html ./page.html
~~~

`do render --check` validates syntax without evaluating expressions or opening
includes. Template formatting canonicalizes directives and interpolated
expressions while preserving literal text and raw bodies.
