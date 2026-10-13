# Kame language fixtures

Representative Kame source covering the syntax in
`docs/spec/004-language.md`. The `lang/expr`, `lang/template`, `lang/rule`, and
`lang/script` package tests consume shared fixtures, alongside the CLI parse
matrix (T004-01), invalid-diagnostic checks (T004-02), canonicalization
(T004-06), and format/AST stability checks (T004-08). Native and WASM consumers
reuse the same bytes; each runner defines its supported fixture subset.

## Layout

```text
expr/                    one expression per file
template/string/         one string template per file
template/target/         one target template per file
rule/definition/         grouped definitions
rule/rules/              grouped rules
script/                  grouped scripts (composition)
invalid/                 diagnostic inputs
invalid/MANIFEST.tsv     expected code and severity per invalid fixture
```

The three independently-parseable surfaces are `expr`, `template`, and `rule`.
`script` adds composition. `invalid/` holds inputs that must produce a
diagnostic; the expected code, severity, and recovery mode are recorded in
`invalid/MANIFEST.tsv` (codes are registered in `docs/spec/011-diagnostics.md`).
Invalid fixture prefixes mirror the valid tree: `expr-`, `template-string-`,
`template-target-`, and `rule-`.

These fixtures are shared rather than living next to each parser's test code, so
the native, CLI, and wasm golden tests can consume the same bytes.

## Conventions

- Fixtures cover specified syntax as well as deliberately retained legacy and
  migration inputs. Their presence is not a claim that every consumer supports
  every input; check the consuming runner and diagnostic manifest.
- `expr/`, `rule/`, and `script/` files end with a trailing newline because the
  parsers ignore surrounding whitespace. Invalid `expr-` and `rule-` fixtures
  follow the same rule.
- `template/string/` and `template/target/` files have **no** trailing newline:
  their bytes are the template. Their invalid `template-string-*` and
  `template-target-*` counterparts follow the same rule. `multiline.km` carries
  its newlines deliberately.
- Canonical goldens must match their expected formatted bytes. General parse
  fixtures need not already be canonical: T004-06 checks that formatting reaches
  a fixed point, and T004-08 checks AST stability. Deprecated interpolation
  fixtures deliberately change spelling during formatting; preserve those inputs.
- Recipe lines are indented with one tab. `rule/rules/indent.km` keeps extra
  indentation inside recipe text on purpose.
- LF line endings only.
- Malformed `@(` / `@{` expansions are warnings that keep the text literal, not
  hard rejections; `MANIFEST.tsv` records `severity` and `recovery` for these.
- `script/core-example.km` keeps the legacy example's lower-case `manifest` and
  `bundle` definitions; the mixed case is intentional, not a naming error.

## Coverage

- `expr/`: literals, all number forms (`0`, integer, negative, underscore,
  binary, octal, hex, float, signed exponent), string escapes and interpolation
  (`@(...)`, deprecated `{(...)}` input, nested interpolation, escaped braces), lists, records,
  names (`?`, `!`, leading `_`), references (index, negative index, slice, open
  slice, mixed components, selection), applications, lambdas (plain, no-arg,
  rest), pipes (deferred and placeholder), and the `?` (`special-optional`),
  `let`, `def`, `eval` special forms.
- `template/string/`: literal text, `@(expression)`, legacy `@{reference}` migration inputs, the full
  selector table including a negative input index, escapes, mixed expansions,
  nested expressions, multiline text.
- `template/target/`: `{name}`, `{name:*}`, `{name:**}`, `?`, character classes,
  negated classes, escapes, multiple and repeated captures, literal-only targets.
- `rule/definition/`: scalar, list, quoted, expression, multiline expression, and
  function definitions, plus interleaved comments.
- `rule/rules/`: file rules (inputs, no inputs, multiple outputs), tasks, cached
  tasks, services, all output (`outputs.km`) and input (`inputs.km`) forms,
  selectors in recipes, indentation.
- `script/`: comments in order, definitions plus rules plus top-level
  expressions, and the adapted legacy core example.
- `invalid/`: number forms (leading/trailing dot, single and consecutive
  underscores, base-prefix sign, int64 overflow, hexadecimal float), empty
  application, mixed record/rule outputs, single quotes, rest parameters
  (not-last, repeated), malformed template expansions, malformed target groups,
  classes, and pattern separators, orphan indentation, file-looking task names,
  malformed recipe interpolation.
