# Kame patches to reeflective/readline

This is github.com/reeflective/readline v1.3.0 (d3aedcb78a33), used through
the `replace` in `src/go/kame/go.mod`. Remove the fork once an upstream
release contains these fixes.

## Keep keys typed during the cursor-position probe

`internal/core/keys_unix.go`, `GetCursorPos`: when the probe reads stdin
itself, keys typed before the terminal's `ESC[row;colR` answer can arrive in
the same read as the answer. Upstream treated such a read as the answer alone
and dropped the keys, so typing right as a prompt appeared lost input. The
patch appends the bytes before and after the answer to the key buffer.

## Defer package-initialization work

Kame links readline into every `kame` and `kash` process, including
non-interactive scripts, so package initialization is paid by each invocation.
Regular expressions (`internal/color`, `internal/display`, `internal/core`)
compile on first use through `sync.OnceValue`, and the default keymaps and
options in `internal/keymap` are constructor functions called when an engine
loads its defaults. Each engine therefore also receives fresh keymap maps
instead of mutating shared package-level ones.

## Probe the cursor position once per line, not per keystroke

`internal/display`: every refresh queried the terminal with `ESC[6n` and
waited for the answer, so each keystroke cost a terminal round trip (an SSH
round trip remotely) and typed-ahead or pasted input was redrawn key by key.
The engine now caches the probed input-line start and re-queries only when
the primary prompt is printed again (`ui.Prompt.PrimaryPrints`), the terminal
size changes, or completion/hint helpers were rendered (they may scroll the
window). Scrolling done by `ensureInputSpace` adjusts the cached row.
