# Kame bugs

Verified defects in the Kame build system, each with a reproduction. Missing
features and design gaps live in `TODO-GAPS.md`; this file is only for behaviour
that is wrong, not behaviour that is absent.

Environment for all entries: Kame 0.1.0. KB-1 was recorded at revision around
`2ef00c6` and is fixed in the current working-copy revision; KB-2 is fixed; KB-3 is fixed. "Verified" means the reproduction was run against
this tree.

## Fixed

### KB-2 — `-f` ignores `-C` when resolving a relative source path

- **Severity:** medium
- **Area:** CLI option handling (`cmd/kame`)
- **Status:** fixed
- **Related:** `TODO-GAPS.md` D2

GNU Make changes directory for `-C` before reading makefiles, so
`make -C sub -f Makefile` reads `sub/Makefile`. Before the fix, Kame resolved a relative `-f`
against the original working directory instead.

```sh
mkdir -p sub
printf 'default :\n\t@(out "ok\\n")\n' > sub/Makefile.kmk
kame -C sub -f Makefile.kmk default
```

- **Expected:** builds `sub/Makefile.kmk`.
- **Historical actual:** `error FS_ERR: cannot read source: Makefile.kmk`.

**Historical workaround (before the fix).** Qualify the path relative to the
original directory, or drop `-f`:

```sh
kame -f sub/Makefile.kmk -C sub default   # works
kame -C sub default                       # autodiscovery works
```

The defect is specifically that `-C` does not affect resolution of a relative
`-f`.

**Verification.** `tests/T009-02-cli-source.sh` passes 53 assertions, including
both option orders, absolute sources, inspection commands, and relative includes.
`tests/T010-06-wasm-cli.sh` passes 21 assertions, including native/WASM parity
for both option orders, absolute sources, autodiscovery, and planning. Both native
source loaders and the JS unified runner resolve explicit sources beneath `-C`.

## Reported (needs confirmation)

- `TODO.md` reports a cluster of CLI/`do expr` inconsistencies (`-C` + relative
  `-f`, capability-gated `wildcard` in `do expr`, the concat deadlock). KB-2
  confirms the first; KB-1 (the concat deadlock) is fixed. The capability-gated
  `wildcard` is intentional; only revisit if the denial does not name the grant
  to add.

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

## Additional fixed source defects

### KB-3 — WASM discovered builds and inspections ignore includes

- **Severity:** high
- **Area:** JavaScript host source loading
- **Status:** fixed

A `Makefile.kmk` containing `include ./child.kmk` and a default recipe that
references a definition from `child.kmk` works natively.
`node dist/kame.js -C PROJECT default` instead fails `REF_MISSING`.
`do plan -C PROJECT -f Makefile.kmk default` succeeds but reports spans from
unexpanded text, differing from the native plan. Explicit file execution through
the unified runner already expands includes. Shared inspection/build loading must
expand them too and preserve authored source locations in diagnostics.

WASM now expands includes through the shared build loader and passes copied
source fragments to portable `CompileMany`. Diagnostics retain authored source
names, offsets, frames, and complete parser diagnostic lists.

**Verification.** T010-20 passes 24 native/WASM parity assertions covering primary
autodiscovery, explicit files, `-C`, plan/graph/tools/cat, nested includes, cycles,
missing files, malformed sources, duplicates, repeated nonrecursive includes,
and inline include rejection. The WASM host sanitizer suite passes 21 tests.

## Additional fixed defects

### KB-4 — Subsecond input changes were treated as fresh

- **Severity:** high
- **Area:** POSIX filesystem timestamps / file-rule freshness
- **Status:** fixed

`tests/T013-04-meta-layer-examples.sh` failed in both the broad suite and an
isolated run: after rebuilding publication, immediately adding a new note built
the new page but skipped aggregate `public/index.txt`. The new page had a newer
filesystem mtime, but both timestamps were in the same second.

Solod 0.4.0 `so/os/os.c` sets `modNsec=0` for stat and lstat. Kame now reads
native nanosecond timestamps in its POSIX host on Linux and macOS layouts.
`TestStatPreservesSubsecondModificationTimes` pins distinct timestamps within
one second, for both Stat and Lstat. The POSIX sanitizer suite passes 21 tests;
T013-04 now passes all 102 assertions, including immediate graph growth.

### KB-5 — WASM declared file inputs checked the module memory filesystem

- **Severity:** high
- **Area:** forwarded build dependencies
- **Status:** fixed

A host wildcard returned existing files, but a file rule consuming them failed
`TGT_NO_RULE`: external file producers and recipe preflight checked the empty
in-module filesystem. Forwarded external file producers now request existence
from the embedding host, and recipe preflight uses the completed dependency's
value. A missing file remains an observed nil value and fails only when required.

T007-09 passes 10 assertions including native/WASM shell recipes over union file
inputs. The WASM host sanitizer suite passes 22 tests, including successful and
missing declared file inputs with exactly one forwarded request.

### KB-6 — WASM declarative file effects do not publish to the host filesystem

- **Severity:** high
- **Area:** forwarded build effects
- **Status:** fixed

A file recipe containing `yield` reports success but writes only into the module
memory host. Primary execution leaves no output on disk, and `do cat` fails if
another backend has not already created it. Native-first comparisons masked this
failure by leaving the artifact behind. Verification must isolate output paths
between backends and cover yielded bytes, explicit writes, host failures, and
ordering before dependent recipes.

Deferred writes and concatenated yields now suspend the recipe until embedding
host completion, then resume remaining effects once. JS publication uses atomic
rename with private sibling staging. T010-21 passes 15 assertions for isolated
native/WASM binary bytes, explicit writes, dependent recipe ordering, `cat`,
zero-byte yields, failure propagation, and staging cleanup. The WASM host sanitizer
suite passes 23 tests, including write payload ordering and host failure.
