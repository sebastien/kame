# Kame bugs

Verified defects in the Kame build system, each with a reproduction. Missing
features and design gaps live in `TODO-GAPS.md`; this file is only for behaviour
that is wrong, not behaviour that is absent.

Environment for all entries: Kame 0.1.0. KB-1 was recorded at revision around
`2ef00c6` and is fixed in the current working-copy revision; KB-2 through KB-7 are fixed. "Verified" means the reproduction was run against
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

## Reviewed CLI follow-up

- `TODO.md` reports a cluster of CLI/`do expr` inconsistencies (`-C` + relative
  `-f`, capability-gated `wildcard` in `do expr`, the concat deadlock). KB-2
  confirms the first; KB-1 (the concat deadlock) is fixed. The capability-gated
  `wildcard` is intentional; its denial now names `--allow-read=ROOT`, with native/WASM regression coverage.

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
timeout -s KILL 8 kame do run --lang expr --allow-read=. \
  -c '(count (concat (wildcard ./src/go/kame/cli/**/*.go) \
                     (wildcard ./src/go/kame/core/**/*.go)))'
```

- **Expected and actual now:** the combined matched-file count, exit 0 without
  hanging. The current checkout returns `17` on both native and WASM; this count
  grows when source files are added.

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

**Follow-up (fixed).** Pattern replacement expansion now accepts a bare scalar
through text conversion. `TODO-GAPS.md` B5 records the fix and conformance coverage.

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

### KB-7 — Forwarded shell file recipes omit host directory preparation and output checks

- **Severity:** high
- **Status:** fixed

A WASM shell file rule containing `true` completed without its declared output,
while native correctly reported `OUTPUT_MISSING`. A nested shell output failed
because directory preparation only reached the module memory filesystem.

File recipes now send structured host requests carrying their declared outputs.
The host prepares parent directories before launch, and the portable engine
requests and checks each output after successful execution. Recipes with only
explicit writes also verify their output. Dependents wait for those checks.
Existing native parents must be directories; a regular file produces `FS_ERR`
before launching the recipe.

T010-21 passes 25 isolated native/WASM assertions, including missing outputs,
empty shell recipes, nested outputs, explicit writes, blocked parents, and
publication failures. T010-05 pins the recipe/output-check ABI sequence;
24 WASM host tests pass with ASAN/UBSAN.

### KB-8 — Native redirected output and WASM host callbacks delayed live chunks

- **Severity:** high for streaming and watch consumers
- **Area:** CLI stream publication
- **Status:** fixed

A recipe printed stdout/stderr markers and waited for a release file. Native C
stdio buffered redirected output until exit; the WASM primary loop awaited host
service completion before draining queued stream events. A caller waiting for a
marker before releasing the recipe could deadlock. Native watch `out` effects
also remained buffered while the session stayed alive.

Native event drains now flush stdout and stderr. WASM process-start/data callbacks
now drain copied ABI events while the host request is pending. T012-02 verifies
human and JSON markers on both streams before the release file exists and before
the process completes, plus native watch output before termination.

## Open verified defects

### KB-9 — Large WASM recipe output exhausts transient event memory

- **Severity:** high
- **Area:** WASM stream/event allocation and retained output
- **Status:** fixed

A file-backed build containing `default :` and the recipe
`head -c 8388608 /dev/zero` exits 0 and forwards all 8 MiB natively. The WASM CLI
exits 1 with an uncaught `RuntimeError: unreachable` from
`Runtime_NextEventJSON`; this run forwarded only 790,528 bytes. Exact partial
length depends on host chunk boundaries. Redirect stdout to a file when
reproducing; the bytes are NULs.

The original reproduction is retained under `build/review/large-output/`. The instance now uses a bounded heap that splits and coalesces freed blocks,
so event storage can be reclaimed while the compiled program remains alive.
Binary event encoding uses that same allocator. JS reuses per-operation scratch
buffers and retains only the portable process budget plus one byte to preserve
truncation detection at completion.

`T012-03-streams-large-output.sh` passes on native and WASM: each process forwards
8 MiB on stdout and stderr, with NUL and invalid-UTF-8 binary vectors in human
and JSON modes. Successful and failed processes preserve complete stream bytes;
failed diagnostics preserve truncation flags and limits. Cached tasks replay
only the 64 KiB retained prefix with the truncation marker, without rerunning.
All 28 WASM host tests pass under ASAN/UBSAN, including allocator coalescing,
alignment, failed reallocation and 1,000 reuse cycles in a 4 KiB buffer.

This fixes the stream-growth failure. Spec 010's separate requirement to turn
true logical-heap exhaustion into an allocation-free diagnostic is now covered
by T010-22: undersized compilation and host completion return `NO_MEMORY`,
failed slots can be freed and reused, and another instance remains usable.
Repeated module-heap query failures also recover; unrelated traps remain traps.

### KB-10 — Failed mixed WASM sessions report epoch-sized elapsed time

- **Severity:** low
- **Area:** human CLI summary
- **Status:** fixed

A mixed invocation such as `do run -c 'ignored = :nil' Makefile.kmk ./out`
can fail its build entry because the value-first invocation lacks recipe
capabilities. The WASM failure summary reported about 1.79 billion seconds
elapsed: `runSession` reset progress counters but left the start clock at zero.
Primary builds already initialized the clock.

Mixed sessions now initialize both together before execution. The same
reproduction reports an invocation-scale duration (0.047 s in the focused run).
T017-07 checks a failed mixed-session summary is present and below 60 seconds;
all 78 assertions pass with the compiled sanitizer CLI and WASM host.

### KB-11 — Operation-wrapped definition cycles reuse freed diagnostic frames

- **Severity:** memory safety
- **Area:** evaluator diagnostic ownership and engine source scheduling
- **Status:** fixed

```kame
CYCLE = (str CYCLE)
default :
    printf %s @(CYCLE)
```

The compiled pre-fix sanitizer binary reports a heap-use-after-free. A failed
reference borrowed the engine diagnostic, and wrapping the `str` operation
reallocated its frame storage. The scheduler later cloned the dangling frames.
Evaluator results now clone engine failures before wrapping. Source polling also
preserves a failure or cancellation raised during dependency discovery and frees
its discarded result instead of replacing the terminal state. The focused
evaluator test and T006-06 require clean `DEP_CYCLE` failure without recipe effects.

### KB-12 — Stream updates cancel their publishing source and free outer state first

- **Severity:** memory safety and reactive correctness
- **Area:** nested source teardown and pending invocation restart
- **Status:** fixed

The portable `TestStreamUpdateCancelsPendingLibraryCallback` fixture streams
complete batches into `(map ([x] (wait-each x)) feed)`. While a callback waits,
the source publishes its next batch. The previous engine invalidated the
consumer and dropped its dynamic interest immediately, cancelling its sole
publishing source. Materializer teardown then freed the outer definition source
before its nested operation wrapper; `freeOperationStream` wrote to freed outer
state. The compiled ASAN reproduction identifies that write and the preceding
`freeDefinitionSource` allocation release.

Teardown now unwinds nested frames before their outer owners. A reactive restart
retains old dependency interest while the replacement invocation rediscovers its
edges. Rediscovered dependencies adopt that interest; an accepted publication,
terminal result, explicit invalidation or last-consumer cancellation releases
obsolete holds. Removed edges no longer act as reverse dependency subscriptions.

The library regression requires cancellation of the old request, rejection of
its late completion, disposal of partial callback progress and ordered callbacks
for the replacement batch. Core regressions also require branch removal and
release when the consumer disappears before rebinding.
