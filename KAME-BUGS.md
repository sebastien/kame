# Kame bugs

Verified defects in the Kame build system, each with a reproduction. Missing
features and design gaps live in `TODO-GAPS.md`; this file is only for behaviour
that is wrong, not behaviour that is absent.

Environment for all entries: Kame 0.1.0. KB-1 was recorded at revision around
`2ef00c6` and is fixed in the current working-copy revision; KB-2 is verified
against the current tree. "Verified" means the reproduction was run against
this tree.

## Open

### KB-2 — `-f` ignores `-C` when resolving a relative source path

- **Severity:** medium
- **Area:** CLI option handling (`cmd/kame`)
- **Status:** open
- **Related:** `TODO-GAPS.md` D2

GNU Make changes directory for `-C` before reading makefiles, so
`make -C sub -f Makefile` reads `sub/Makefile`. Kame resolves a relative `-f`
against the original working directory instead.

```sh
mkdir -p sub
printf 'default :\n\t@(out "ok\\n")\n' > sub/Makefile.kmk
kame -C sub -f Makefile.kmk default
```

- **Expected:** builds `sub/Makefile.kmk`.
- **Actual:** `error FS_ERR: cannot read source: Makefile.kmk`.

**Workaround.** Qualify the path relative to the original directory, or drop
`-f`:

```sh
kame -f sub/Makefile.kmk -C sub default   # works
kame -C sub default                       # autodiscovery works
```

The defect is specifically that `-C` does not affect resolution of a relative
`-f`.

**Regression tests to add.** `-C` with a relative `-f` in both option orders,
plus `-C` with autodiscovery (already passing).

## Reported (needs confirmation)

- `TODO.md` reports a cluster of CLI/`do expr` inconsistencies (`-C` + relative
  `-f`, capability-gated `wildcard` in `do expr`, the concat deadlock). KB-2
  confirms the first; KB-1 (the concat deadlock) is fixed. The capability-gated
  `wildcard` is intentional; only revisit if the denial does not name the grant
  to add.

## Fixed

### KB-1 — `concat` of two non-empty wildcard globs deadlocked

- **Severity:** was critical (hang)
- **Area:** engine / read-only host requests over dependency-bearing glob resources
- **Status:** fixed in the current working-copy revision
- **Related:** `TODO-GAPS.md` B2 (now historical); memo learning
  `kame-concat-wildcards` (evidence #93)

Concatenating per-directory `wildcard` results is the natural way to translate
`$(shell find dir1 dir2 ... -name '*.go')`. At the recorded revision it hung;
the current tree completes it. Exact reproduction:

```sh
timeout -s KILL 8 kame do expr --allow-read=. \
  -c '(count (concat (wildcard ./src/go/kame/cli/**/*.go) \
                     (wildcard ./src/go/kame/core/**/*.go)))'
```

- **Expected and actual now:** `12` (2 cli + 10 core), exit 0, well under a
  second.

**Root cause.** Each read-only host request in `operations/host.go` was
submitted without retaining its completion; a replay could resubmit or consume
a sibling call's completion, and the repeated work never converged. The fix
caches the request identity, id, and value in per-operation state
(`readRequestState` via `Context.OperationState`) for one engine generation, so
a completed read is replayed from the cache instead of re-entering the host loop.

**Verified matrix (current tree).** `concat` of two, three, four, and nested
wildcards all return the right count; wildcard plus literals works; empty
wildcard members return `0`; mixed-extension globs work; and the build-path
variant completes (`kame -n`/`do plan` over the concatenated inputs). Regression
coverage lives in `tests/T011-03-diag-context.sh` (`two_reads`, `two_globs`) and
`tests/T010-13-wasm-tools.sh`.

**Follow-up (not a hang).** A pattern replacement expansion still rejects a bare
scalar (`(replace P 1 S)` → `PAT_INVALID`) while `(replace P "1" S)` works; that
is tracked separately as `TODO-GAPS.md` B5.
