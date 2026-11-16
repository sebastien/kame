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
