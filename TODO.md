# TODO

Governing specs: `docs/spec/003-posix-process.md`,
`docs/spec/011-diagnostics.md`.

## POSIX Process Host Integration

- [ ] Run POSIX host polling outside graph workers once the runtime exists.
- [ ] Connect engine submissions and completions to POSIX host requests and events.
- [ ] Map engine cancellations to process-group cancellation.
- [ ] Cancel active processes when the runtime event consumer disappears.
- [ ] Add runtime retry policy, recipe source mapping, and joined recipe scripts.
- [ ] Map CLI `SIGINT` and `SIGTERM` to root cancellation and wait for shutdown.
- [ ] Extract a portable host contract package for requests, events, and
      diagnostics; `host/posix` currently owns those types.

## POSIX Host Hardening

- [ ] On setup failure, retry `waitpid`; emit `HOST_FAIL` if reaping cannot be confirmed.
