---
identity: 8119739f-52a0-4861-b578-e6faeff1e603
created: 2026-11-25T18:44:05.469561+00:00
updated: 2026-11-25T18:44:05.914850+00:00
---
# Word

A word is one argument position in a Kash command. Words follow typed-argv
rules: values become argv entries without being re-split, re-globbed or
reparsed, and argv is executed directly, with no shell.

| Word form | Meaning |
| --- | --- |
| Bare text, `'…'` | A literal string |
| `"…"` | A string with `$` references and `$(…)` captures |
| `$name`, `$name.field`, `$xs.0` | Reference substitution |
| `${EXPR}`, `(EXPR)` | Value fragment; adjacent fragments compose one word |
| `$...name` | List spread into separate entries |
| `$(…)` | Captured stdout |

## Conversion to argv

- **Standalone unquoted substitution keeps its type** until argv is built:
  - a string, path or pattern gives one argument, even when empty;
  - a number gives its canonical text, and a boolean gives `true` or `false`;
  - a flat list gives one argument per item, and an empty list gives none;
  - `nil` gives no argument;
  - bytes, records, callables, handles, outcomes and nested lists are
    `EXPR_INVALID`.
- **Quoted or mixed words** give exactly one argument. `${EXPR}` uses Kame
  string conversion, so `nil` becomes empty text.
- **`$...xs` is the explicit spread.** At the command head, the first item is
  the executable.
- **Authored wildcards expand.** Unquoted `*`, `**` and `[…]` written in an
  argument expand against the stage cwd. Matches are sorted by bytes and skip
  dotfiles, and no match fails before launch.
  - Command heads, redirection targets, and quoted or substituted text never
    glob.
  - A leading authored `~` or `~/` expands from the unit's `HOME`.

## Boundaries

- **References.**
  - An unbraced `$` reference stops at `/`: `$dir/file.txt` is `$dir`
    followed by `/file.txt`.
  - Qualified names need braces: `${env/HOME}/x`.
  - Upper-case names fall back to the environment, and an unset one fails
    with `ENV_VAR_MISSING`.
- **Syntax that is not what a shell user expects.** An unquoted `(` starts a
  value fragment, never a subshell. `@` is literal text, but unquoted `@{` is
  `PARSE_ERR`.
- **Stage setup.** `KEY: VALUE` before the executable is stage setup:
  - `cwd:`, `timeout:`, `env:`, `tty:` and `foreground:` are reserved;
  - any other key sets an environment variable for that one stage.

  After the executable, `KEY:` is an ordinary argument.

## Open questions

Authored wildcard expansion arrived on 2026-11-22 (commit `3a0a55a5`) and
is specified in `017` §Command values and arguments. Older text still
describes raw wildcards in command words as literal and conflicts with it:

- `023` (`ls ./journal/*` "passes the literal text");
- `038`'s acceptance list;
- `docs/kash-cookbook.md`.

Related: [domain/concept:Kash] [domain/antipattern:textual_reexpansion]

Sources: `docs/spec/017-kash.md` §Words, quoting and argv conversion,
§Command values and arguments, §References;
`docs/spec/055-unified-kame-kash-language.md` §7.1.
