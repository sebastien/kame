# Template Extensions

## Purpose

The template language supports matched block labels,
pattern matching, whitespace trim markers, inline blocks, content-based style
inference, more host comment conventions, canonical template formatting, and
loop position/key bindings. This specification defines their syntax and
acceptance behavior on native and WASM.

Existing template behavior remains the default. In particular, document text
without an explicit style still uses `plain`; inference is requested with the
`auto` style. Existing bare `@end` remains valid.

## Matched block endings

`@end` may include the kind of the block it closes:

```text
@end(if)
@end(for)
@end(with)
@end(raw)
@end(match)
```

The label must match the innermost open block. A mismatched label is `TPL_BLOCK`
at the closing directive. Bare `@end` closes any block. `@raw` only recognizes
`@end` and `@end(raw)`; all other bytes inside a raw region remain literal.

## Pattern matching blocks

`@match(EXPR)` evaluates its subject once. It accepts the same string, path or
pattern values as the `(match ...)` expression form. A match block contains one
or more `@case(PATTERN)` clauses and an optional terminal `@else` clause. The
first directive after `@match` must be `@case`:

```html
<!-- @match(post.path) -->
<!-- @case(./posts/{slug:*}.md) -->
<a href="@(slug)">@(post.title)</a>
<!-- @case(./drafts/{slug:*}.md) -->
<span class="draft">@(slug)</span>
<!-- @else -->
<span>unclassified</span>
<!-- @end(match) -->
```

Patterns follow 014 and 005. The first matching case renders in a child scope
where named captures are bound. Anonymous captures do not bind names. Only the
selected body is evaluated. A missing match without `@else` emits nothing. A
case after `@else`, duplicate `@else`, or empty case list is `TPL_BLOCK`; a
malformed pattern is `PAT_INVALID` at the case directive.

## Inline blocks and whitespace trimming

Block directives may appear within a text line as well as on their own line.
The same `@if`, `@elif`, `@else`, `@for`, `@with`, `@match`, `@case`, `@raw`,
and labeled `@end` forms apply. In comment styles, inline directive tokens use
the complete comment wrapper for that style, such as `<!-- @if(ok) -->`; a
comment wrapper may not contain surrounding text. In `plain` style the token
is the directive spelling itself. Inline `@(EXPR)` remains an expression and
does not open a block.

An optional `-` immediately after `@` trims adjacent output whitespace on the
left. An optional `-` immediately before the token's closing comment wrapper
or, for a parenthesized directive, immediately after `)` trims adjacent output
whitespace on the right. For bare directives, the right marker follows the
keyword, as in `@else-` or `@end(if)-`. Trimming removes the maximal run of
ASCII spaces, tabs, CR and LF from the rendered output at that boundary; it
does not alter source spans or whitespace inside an expression. Markers do not
change the source text used by diagnostics.

## Style inference and comment conventions

`auto` selects the style from the file extension when the extension has a
registered mapping. Otherwise it scans the source for complete directive
comment tokens. Exactly one detected style is selected; multiple styles yield
`TPL_STYLE` with an ambiguity message. With no detected style, `auto` uses
`plain`. Explicit styles always take precedence. Omitted style retains 016's
existing behavior.

The `powershell` style recognizes line comments beginning with `#` and complete
block comments `<# @... #>`. It is inferred for `.ps1`, `.psm1`, and `.psd1`.
The `batch` style recognizes `REM @...` and `:: @...` lines, case-insensitively
for `REM`; it is inferred for `.bat` and `.cmd`. Existing styles and extension
mappings remain unchanged.

## Loop position and record keys

`@for([PARAM...] LIST [SEP])` retains its current bindings and adds two
reserved read-only names in the loop body: `index` is the zero-based integer
position and `key` is the corresponding record key when iterating a record.
For list input, `key` is `:nil`. Record iteration follows source key order;
the value is passed to the declared parameter list. The index counts entries
in that iteration order. Existing parameter names shadow the implicit names.
Other input kinds are `EXPR_INVALID`.

## Canonical formatting

`kame do fmt --lang template [--comment STYLE] [FILE]` formats directive
syntax while preserving every non-directive byte. It canonicalizes directive
keyword spelling, expression argument spacing, block labels, comment wrappers,
and trim marker placement, and is idempotent. It reports source-located
diagnostics without writing a partially formatted file. `--check` validates
canonical form without output; `--in-place` writes only after the whole source
parses successfully. Inferred style follows the `auto` rules above.

## Acceptance tests

- Matching labels close the corresponding block; mismatched labels, including
  nested blocks, report `TPL_BLOCK` at the closing token.
- Match blocks select the first matching case, bind named captures, skip
  unselected effects, honor a terminal else, and match `(match ...)` behavior.
- Inline blocks work in plain and comment styles across text boundaries;
  nested and malformed blocks preserve authored diagnostic spans.
- Left and right trim markers remove only adjacent ASCII whitespace, including
  CRLF boundaries, in both selected and unselected branches.
- `auto` uses known extensions, detects one content style, falls back to plain,
  and rejects ambiguous content; explicit style overrides inference.
- PowerShell and batch directives render and format, while non-directive host
  comments pass through unchanged.
- List loops expose index and nil key; record loops expose source-order index,
  key and value. Nested loops restore outer bindings.
- Template formatting preserves literal bytes, formats directives canonically,
  reports diagnostics before output, and is idempotent on native and WASM.
- All paths release parser and evaluator values after success, error, no-match,
  cancellation and formatting failure.
