---
revision: 2
---

# Template Extensions

## Purpose

The template language supports matched block labels, pattern matching, whitespace
trim markers, inline blocks, content-based style inference, more host comment
conventions, canonical template formatting, and destructuring loops.
This specification defines their syntax and acceptance behavior on native and
WASM. Directive operands use the expression grammar of
`055-unified-kame-kash-language.md` §4 without surrounding parentheses
(`016-templates.md`).

`String`/`Bytes` template content without an explicit style uses `plain`;
inference is requested with the `auto` style. A `Path` template with no
explicit style whose extension has no registered style is still `TPL_STYLE`. Bare `@end` closes any block.

## Matched block endings

`@end` may name the kind of the block it closes:

```text
@end if
@end for
@end with
@end raw
@end match
```

The label is a keyword operand, not an expression. It must match the innermost
open block. A mismatched label is `TPL_BLOCK` at the closing directive. Bare
`@end` closes any block. `@raw` only recognizes `@end` and `@end raw`; all other
bytes inside a raw region remain literal.

## Pattern matching blocks

`@match EXPR` evaluates its subject once. It accepts the same subjects and arm
patterns as the `match` expression (`055` §4.5): path patterns, string patterns,
and literals. A match block contains one or more `@case PATTERN` clauses and an
optional terminal `@else` clause. The first directive after `@match` must be
`@case`:

```html
<!-- @match post.path -->
<!-- @case ./posts/{slug:*}.md -->
<a href="@{slug}">@{post.title}</a>
<!-- @case ./drafts/{slug:*}.md -->
<span class="draft">@{slug}</span>
<!-- @else -->
<span>unclassified</span>
<!-- @end match -->
```

Patterns follow `014` and `005`. The first matching case renders in a child
scope where named captures are bound. Anonymous captures do not bind names. Only
the selected body is evaluated. A missing match without `@else` emits nothing. A
case after `@else`, duplicate `@else`, or empty case list is `TPL_BLOCK`. A
syntactically malformed pattern is `TPL_PARSE` and an invalid group
combination is `PAT_INVALID`, both at the case directive.

## Inline blocks and whitespace trimming

In comment styles, a block directive may appear within a text line as well as
on its own line. The same `@if`, `@elif`, `@else`, `@for`, `@with`, `@match`,
`@case`, `@raw`, and labeled `@end` forms apply. An inline directive is one
complete comment wrapper for that style, such as `<!-- @if ok -->`; the wrapper
delimits the operands and may not contain surrounding text:

```html
<p><!-- @if ok -->yes<!-- @else -->no<!-- @end if --></p>
```

In `plain` style, directives are recognized on whole lines only, and the only
inline form is `@{EXPR}`. Inline `@{EXPR}` interpolation remains interpolation
and never opens a block.

An optional `-` directly after `@` trims adjacent output whitespace on the left
(`@-for x in xs`). An optional trailing `-` trims adjacent output whitespace on
the right; it is a standalone token preceded by whitespace, after any operands
or label (`@if ok -`, `@else -`, `@end if -`, `<!-- @-for x in xs - -->`). The
keyword ends at whitespace, `-`, or the end of the directive, so `@else-` is
`TPL_PARSE`. A trailing `-` is never read as a subtraction operator, because a
binary operator requires a right operand. Trimming removes the maximal run of
ASCII spaces, tabs, CR and LF from the rendered output at that boundary; it does
not alter source spans or whitespace inside an expression. Markers do not change
the source text used by diagnostics.

## Style inference and comment conventions

`auto` selects the style from the file extension when the extension has a
registered mapping. Otherwise it scans the source for complete directive
comment tokens. Exactly one detected style is selected; multiple styles yield
`TPL_STYLE` with an ambiguity message. With no detected style, `auto` uses
`plain`. Explicit styles always take precedence. An omitted style follows
`016`.

The `powershell` style recognizes line comments beginning with `#` and complete
block comments `<# @... #>`. It is inferred for `.ps1`, `.psm1`, and `.psd1`.
The `batch` style recognizes `REM @...` and `:: @...` lines, case-insensitively
for `REM`; it is inferred for `.bat` and `.cmd`.

## Loop positions and record keys

`@for` binds only the names it declares. Positions and record keys come from
explicit data-last operations (`007`): `indexed LIST` yields `[position, item]`
pairs and `items RECORD` yields `{key, value}` entries in stored field order.
With several names, `@for` destructures each element positionally (`016`):

```html
<!-- @for e in items settings sep ", " -->@{e.key}=@{e.value}<!-- @end for -->
<!-- @for i x in indexed xs -->@{i}: @{x}<!-- @end for -->
```

## Canonical formatting

`kame do fmt --lang template [--comment STYLE] [FILE]` formats directive
syntax while preserving every non-directive byte. It canonicalizes directive
keyword spelling, operand formatting, block labels, comment wrappers, and trim
marker placement, and is idempotent. Operands and interpolated expressions use
canonical expression formatting (`055` §15). Authored parentheses are
preserved; operands use canonical expression spacing (`018`), for example
`@if exists? ./file` and `@if ready? ()`.
Escaped directives and raw body text remain unchanged. It reports
source-located diagnostics without writing a partially formatted file.
`--check` validates canonical form without output; `--in-place` writes only
after the whole source parses successfully. Inferred style follows the `auto`
rules above.

## Acceptance tests

- Matching labels close the corresponding block; mismatched labels, including
  nested blocks, report `TPL_BLOCK` at the closing token.
- Match blocks select the first matching case, bind named captures, skip
  unselected effects, honor a terminal else, and agree with `match` expression
  behavior.
- Inline blocks work in comment styles across text boundaries; in plain style
  a directive is recognized only on a whole line; nested and malformed blocks
  preserve authored diagnostic spans.
- Left and right trim markers remove only adjacent ASCII whitespace, including
  CRLF boundaries, in both selected and unselected branches; a trailing `-`
  marker is not parsed as subtraction; `@else-` is `TPL_PARSE`.
- A syntactically malformed case pattern is `TPL_PARSE`; an invalid group
  combination is `PAT_INVALID`.
- `auto` uses known extensions, detects one content style, falls back to plain,
  and rejects ambiguous content; explicit style overrides inference.
- PowerShell and batch directives render and format, while non-directive host
  comments pass through unchanged.
- Loops bind no implicit `index` or `key`; `@for i x in indexed xs` and
  `@for e in items r` expose positions and stored-order keys. Nested loops
  restore outer bindings.
- Template formatting preserves literal bytes, formats directives and operands
  canonically, reports diagnostics before output, and is idempotent on native
  and WASM.
- All paths release parser and evaluator values after success, error, no-match,
  cancellation and formatting failure.
