# Kash performance audit — 2026-11-25

**The combined implementation reduces CPU in record loops, memory in large collections, and time waiting for the next interactive prompt.** The table below compares release builds on identical inputs. The later sections preserve the original audit of Kame 0.7.2 at `366d6c5662a6` and its separate prototype experiments.

## Combined implementation

Native Linux/amd64, baseline → final working tree. Medians of five fresh processes, alternating executable order; separate RSS runs. Both executables had to produce the expected bytes for every workload.

| Workload | CPU ms | Wall ms | Peak RSS MiB |
|---|---:|---:|---:|
| Empty script | 2.91 → 2.63 | 2.60 → 2.44 | 8.55 → 9.02 |
| 1k builtin loop | 100.58 → 103.81 | 89.42 → 93.90 | 16.11 → 16.29 |
| 300 external processes | 244.47 → 235.19 | 225.43 → 217.04 | 15.68 → 15.77 |
| 100 pipelines | 244.12 → 244.70 | 120.47 → 119.30 | 15.68 → 15.63 |
| 256 MiB stdout | 61.43 → 61.19 | 60.85 → 60.67 | 10.05 → 10.14 |
| 1k list references | 40.53 → 38.05 | 32.45 → 30.72 | 23.80 → 22.89 |
| 1k short file records | 134.95 → 107.56 | 115.57 → 99.52 | 15.43 → 16.05 |
| Map/filter 100k strings | 1,329.75 → 1,145.40 | 1,160.08 → 1,040.18 | 140.80 → 124.24 |
| Warm 64 MiB tool | 4.86 → 4.50 | 4.38 → 4.07 | 10.05 → 10.14 |

The **10k short-record** workload allocated **1,206 → 440 MiB** over the run (64% less), while peak RSS stayed around **16 MiB**. On **100k map/filter**, allocation fell from **412 to 270 MiB** (about 200k fewer allocations). With the session still alive after a forced GC, live heap fell from **60.25 to 54.16 MiB**; `core.Value` went from **304 to 272 bytes** on amd64. These are medians of three separately instrumented runs. The two materialized lists still dominate live heap; callback binding and `Context.Call` together accounted for 49% of sampled allocated bytes in one final profile.

**Controls and variability:** The 1k short-record case used **20–26% less CPU** across two five-sample sweeps. The 10k case did **not** show a repeatable default-runtime speedup: median CPU went **2,171 → 1,688 ms** in the first sweep, **1,974 → 2,180 ms** in the final sweep. The 1k builtin loop likewise moved in opposite directions, although allocation consistently fell **45.1 → 38.2 MiB**. The 128 × 32 KiB long-record control used **37.1 → 33.2 ms CPU**, and 1,000 module references used **24.8 → 24.3 ms** in the final sweep. Pipeline and stdout throughput were essentially flat.

To check what changed in the 10k case, tracing the record descriptor found **10,001 positional reads** in both releases, but only **2,556,032 bytes** returned in the combined build versus **386,959,120** before. A separate nine-pair control with `GOMAXPROCS=1` for both binaries gave **1,243 → 1,010 ms CPU**. This control does not establish a speedup at the default scheduler setting.

**Interactive and Node/WASM:** At 2,000 submissions, the two baseline PTY sessions had median Enter→prompt times of **8.70 and 8.73 ms**; the combined build measured **4.63 and 5.24 ms**. Both versions held about 127 KiB of history and 17–18 MiB idle RSS. In three alternating Node/WASM samples, the same 100k map/filter script took **13.48 → 10.35 s CPU**, **8.57 → 7.14 s wall**, and **584 → 430 MiB peak RSS** for the whole Node process.

**What changed:** Regular-file cursors probe 256 bytes at the current offset and reuse scratch for long records. REPL history reuses screened entries only after reading the locked file and matching its digest, retention settings and timestamps. Module metadata is stored once per module. The evaluator lends one empty operand pair at a time across calls; suspended calls keep their operands. A finished process request shares its immutable payload with the queue.

**Correctness:** The full Go suite passed with the native CLI enabled. The native/Node suites passed 82 file-record, 111 stdin-record, 68 module and 92 output/status checks; the Node embedding and WASM signal-cancellation suites passed too. The record test rewrites the next line on the same inode. A separate reentrant test returned **0 instead of 42** with the prototype's context-local operand slice; the program-owned slot passed with copied contexts, suspension, invalidation and resumption. History tests cover disk edits preserving file size and mtime, clock rollback, idle expiry, and recovery from a failed write without republishing memory-only commands.

**Go source compatibility:** `core.Value.Module` is now a `*core.ModuleRef`: its `Handle` holds the former opaque module and its `Signature` holds the content signature. Direct users of `ModuleOwner` or `ModuleSignature` must update; the evaluator checks the owning program on the handle. The Kash language and Node/Python embedding interfaces are unchanged.

## Native baseline

Five fresh processes per cell, median. CPU is user + system time including waited-for children; wall time includes launch. RSS is the largest single-process high-water mark reported by GNU time, **not** the sum of a pipeline or the heap left after collection.

| Checked workload | CPU ms, Kash / Bash | Wall ms, Kash / Bash | Peak MiB, Kash / Bash |
|---|---:|---:|---:|
| Empty script | 3.05 / 1.09 | 3.18 / 1.25 | 8.59 / 3.47 |
| 1,000 `if` + `echo` iterations | 122.17 / 9.37 | 117.84 / 9.63 | 16.22 / 3.38 |
| 300 `cat /dev/null` processes | 269.90 / 208.72 | 250.64 / 202.36 | 15.64 / 3.48 |
| 100 `printf \| sort \| uniq` pipelines | 293.07 / 261.28 | 176.31 / 181.77 | 15.96 / 3.52 |
| Stream 256 MiB of stdout | 62.04 / 60.30 | 61.56 / 61.26 | 10.21 / 3.37 |
| 1,000 references to a 20k-item list | 41.61 / 67.04 | 33.60 / 68.92 | 23.84 / 5.42 |
| Read and echo 1,000 file records | 173.57 / 8.21 | 163.23 / 8.51 | 15.93 / 3.40 |
| Map 100k strings, filter, count | 1,399.95 / 797.05 | 1,228.81 / 799.88 | 143.21 / 37.69 |
| Run a 64 MiB executable, warm digest store | 4.84 / 2.35 | 4.43 / 2.51 | 10.09 / 3.48 |

The largest native slowdowns are **per item or per command**, not per byte of stdout. Kash already beat Bash on the list-reference workload. The large-tool row is deliberately warm: seven separate trials on the same file with an empty versus primed *digest store* took **312.3 vs 6.6 ms CPU**. This was not a cold filesystem-cache test.

## Original experiments (one change at a time)

### 1. Reuse record storage; read short records at their current offset

[`ReadRecord` and `km_record_next`](../src/go/kame/host/posix/record.go) allocate a 65,537-byte buffer for each regular-file record, including EOF. They discard everything after the first delimiter and re-read it on the next iteration. `ReadRecord` alone was 59.7% of sampled allocated bytes on 10k records.

* **Smallest change:** keep the full-size buffer on the cursor until close. A matched 10k run went from **1,845 to 1,401 ms CPU**, and from **1,200 to 497 MiB** total Go allocation. Clear the vacated reader slot on close so the backing array cannot pin the buffer.
* **Reduce bytes copied too:** try a 256-byte positional read first; only if it fills without a delimiter, retry the same offset using the cursor's full-size buffer. On an 80,000-byte file of 8-byte records, `strace` counted **10,001 `pread64` calls** in both versions, returning **386,959,120 vs 2,556,032 bytes**. These are bytes returned by the kernel, not physical disk traffic. In this paired run CPU was **1,662 → 1,305 ms**, wall **1,487 → 1,231 ms**, total Go allocation **1,200 → 497 MiB**, peak RSS **16.28 → 16.48 MiB**. At 1k records CPU was **133 → 112 ms**; at 128 × 32 KiB it was **41 → 37 ms**. Buffer reuse accounts for virtually all the allocation win; the short probe's distinct benefit is less re-reading.
* **Reject unqualified read-ahead.** A `bufio.Reader` needed just two nonempty record reads, but returned `old!!` after the loop rewrote the next line in place; the baseline and short probe returned `new!!`. This passed the existing record suites undetected. Keep the same open descriptor, re-read at the current offset on each iteration, and leave borrowed stdin's handoff alone.

**Gate:** add the in-place rewrite case, run both [file](../tests/T048-08-kash-records.sh) and [stdin](../tests/T048-09-kash-stdin-records.sh) suites, including NUL/CRLF, byte limits, final unterminated records, break and cancellation. The probe passed all 78 file and 111 stdin checks across native and Node, plus the rewrite case on native. Require one large scratch allocation **at most per cursor**, and remeasure both short and long records.

### 2. Avoid redundant history processing on Enter

[`replHistory.update`](../src/go/kame/cmd/kame/repl_history.go) reads, decodes, prunes and encodes the store under lock. An eligible submission records the entry, then the next prompt refreshes the same store. Pruning also marshals individual entries to enforce byte quotas.

A prototype compared a digest of the **freshly locked, read** file against the last successfully committed snapshot. When bytes, options and validity times still matched, it reused the screened entries; a refresh with no change skipped JSON processing. Two real PTY sessions per version, in opposite order, produced these ranges of per-session medians:

| At 2,000 submissions, 128 KiB cap | Baseline | Prototype |
|---|---:|---:|
| Enter → next prompt (20 commands/session) | 9.44–19.91 ms | **4.70–4.97 ms** |
| Ordinary keystroke → redraw (400 keys/session) | 0.127–0.269 ms | 0.129–0.130 ms |
| Idle process RSS | 17.62–18.06 MiB | 17.67–17.87 MiB |

Both kept 452 entries (~127 KiB) at 1k and 2k. Idle RSS started around 9.4 MiB and changed by less than 1 MiB from 1k to 2k in each run. This supports a latency fix, not a leak diagnosis. **Gate:** retain permission checks, locking, atomic replacement, cross-session clear/append, scope changes, quota, expiry and secret screening. The prototype passed `TestHistory*`; also exercise a persistence failure followed by recovery. A path or unchanged file size alone does not establish that it is safe to reuse entries.

### 3. Reduce resident collection size and callback churn separately

With the session still alive just before `session.Free()`, a forced-GC snapshot of map/filter showed **60.25 MiB live** (empty script: 0.20 MiB). `opSplit` and `transform` each accounted for ~29 MiB of sampled live space: the script keeps both its input list and its mapped list. Conversely, in the allocation profile, `argumentValuesMode`, `Scope.storeBinding` and `Context.Call` accounted for **64.7% flat**. Fixing the second set does not remove the first.

| Unchanged 100k script; separate paired experiments | CPU ms | Total allocated MiB | Peak RSS MiB | End-of-script live MiB |
|---|---:|---:|---:|---:|
| Put module-only metadata behind a pointer | 1,632 → 1,442 | 412 → 380 | **141.36 → 130.92** | **60.25 → 54.16** |
| Reuse one two-operand slice on the evaluation context | 1,392 → 1,244 | **412 → 290** | 141.87 → 143.44 | — |
| Set `GOGC=50` instead of the default | 1,392 → 1,530 | — | **141.87 → 111.32** | — |

**For footprint:** move rare metadata off [`core.Value`](../src/go/kame/core/value.go) before considering a general value-representation rewrite. Every list element, even a string or integer, currently carries the module handle, owner and signature. Moving just those fields saved 32 bytes/element on amd64. The prototype passed core/eval tests and 68 native/Node module checks. Review the exported Go struct fields, including any external embedders, module identity/signatures and owner lifetime; reuse one metadata object per module rather than allocating one per reference. Re-run module-heavy and collection-heavy workloads.

**For CPU/churn:** in [`argumentValuesMode`](../src/go/kame/lang/eval/operation_memo.go) and [`operation`](../src/go/kame/lang/eval/application.go), lend scratch to one call at a time and return it only after synchronous completion and cleanup. The prototype eliminated ~200k allocations (3.71M → 3.51M). Eval and operation tests passed. Before shipping, test copied contexts, nested and reentrant calls, aliases, escaping callables, spread, suspension/resumption and cancellation; never recycle an operand still held by a memo. Profile callback binding creation again afterwards. `GOGC=50` is a measured per-job tradeoff, not a recommended global default.

### 4. Remove copies from the ordinary command path

Native Kash already runs supported system `echo` arguments in-process: a trace of 100 iterations saw **zero `echo` execs**, plus one `seq`. In a 10k-iteration allocation profile, [`captureRequest`](../src/go/kame/lang/eval/process.go) accounted for 56.4% cumulatively; [`shellValue`](../src/go/kame/program/host_events.go) and [`processResult`](../src/go/kame/lang/eval/process_expression.go) built separate completion records (10.1% and 8.6% flat). Cumulative and flat percentages must not be added.

**First change:** mark the finished payload shareable immediately before `c.Submit`, so [`Queue.Submit`](../src/go/kame/host/queue.go) acquires an owner instead of deep-cloning a record the caller is about to free. Seven paired runs of the unchanged 1k builtin loop: **111 → 102 ms CPU**, **45.0 → 40.0 MiB allocated** and **222k → 203k allocations**. A 1k straight-line script went **139 → 130 ms CPU**; the pipeline control was essentially flat (**254 → 255 ms**). The eval, program and host Go tests passed. **Gate:** verify ownership across queue transfer, rejected submission, cancellation and forwarding to the portable host. Preserve PATH overrides, tool tracking, grants, redirections, output ordering and failure/signal/per-stage results before tackling completion records.

Large *source files* also have a different footprint from a loop running one statement many times. With 100 / 1,000 / 4,000 separately authored `echo x` statements, peak RSS was **14.6 / 18.6 / 37.5 MiB**; after GC with the session alive it was **0.50 / 3.11 / 11.87 MiB of heap**. The 4k live profile attributed ~91% cumulatively to the command-graph parser. If generated scripts are a priority, measure AST size and retained source spans before changing the executor.

### Improvements available in scripts today

* When the goal is **only to print these bytes**, replacing `echo line $i` with `(out! (str "line " i "\n"))` in the same 1k loop took **124 → 50 ms CPU**, with identical stdout. This is not a general replacement for a command with redirection, status or tool-lookup semantics.
* When the goal is **only the count**, the benchmark's “append `!`, filter suffix `0!`, count” can filter the original strings for suffix `0` and count. Same answer (10,000); **1,392 → 746 ms CPU**, **142 → 80 MiB peak RSS**, **60.25 → 29.73 MiB live**. This demonstrates the cost of materializing an unused intermediate, not a general optimizer that can reorder callbacks.

## Node/WASM is a different target

Fresh Node 24.18.0 processes, three checked samples, using a newly built WASI module and the matching source wrapper:

| Workload | CPU ms | Wall ms | Peak MiB (whole Node process) |
|---|---:|---:|---:|
| Empty script | 470 | 321 | 116 |
| 1k `if` + `echo` | 11,264 | 8,607 | 180 |
| 100k map/filter | 13,747 | 9,114 | 583 |

On the same 100-iteration trace, the Node host **executed `echo` 100 times**. If shell loops matter on this backend, benchmark a Node fast path for the same narrowly supported utility arguments as [native](../src/go/kame/host/posix/builtins.go), retaining execution of custom PATH programs. This is a hypothesis, not a measured speedup. The value-layout prototype **did** lower map/filter's Node peak from **582 to 545 MiB** in a separate paired run. These RSS figures include V8, the host and WASM; they do not measure live Go objects or WASM linear memory alone.

## How to reproduce

Linux/amd64, Core i7-5557U (2 cores / 4 threads), Go `1.27.4-X:nodwarf5`, Bash `5.3.15`. The native release was built with `CGO_ENABLED=0 -trimpath -ldflags '-s -w -X main.artifactMode=release'` and matched `dist/kame` byte-for-byte (SHA-256 `9039ee96e76add0b82ef135af0c09a8792c44ea7d7609c897027c6377b035609`). From the repository root:

```sh
make dist
home=$(mktemp -d)
# Compare the combined build with the saved original binary (both named kash).
env -i PATH=/usr/bin:/bin LC_ALL=C HOME="$home" \
  python3 src/sh/benchmark-kash.py --kash dist/kash --baseline-kash /path/to/original/kash --samples 5 --rss
# Omit --baseline-kash for the Bash comparison:
env -i PATH=/usr/bin:/bin LC_ALL=C HOME="$home" \
  python3 src/sh/benchmark-kash.py --kash dist/kash --samples 5 --rss
KAME_KASH_BENCH=1 KAME_NATIVE_CLI_TEST_BINARY="$PWD/dist/kame" \
  go -C src/go/kame test ./cmd/kame -run '^TestKashInteractiveBenchmark$' -count=2 -v
```

The [harness](../src/sh/benchmark-kash.py) checks exit status and exact stdout before timing (including all 256 MiB of zeroes), then times with output to `/dev/null`. It alternates executable order; use a path named `kash`, not its resolved symlink target, because dispatch depends on `argv[0]`. The RSS pass uses a thin `/usr/bin/time` launcher: direct Python-child `wait4` RSS showed a ~13 MiB floor even for `/usr/bin/true` on this machine. CPU and RSS are collected in different executions.

For the original prototype experiments, build releases from the baseline source with an independent Go overlay per change. Use 8-byte numbered lines at 1k and 10k, and 128 lines of 32 KiB including LF for the long-record control. Keep the same stdout sink and environment. For allocation counts take `runtime.ReadMemStats` deltas around dispatch (three separate native processes); for attribution take `runtime/pprof` profiles and inspect with `go tool pprof -top -alloc_space` or `-inuse_space`. Collect the latter **after GC, with the session kept alive**, outside timed runs. Compare only medians within the same A/B: this was a shared machine, and wall time in particular varied. Use byte, ownership and output checks as hard gates; repeat timing on a quiet release machine before setting budgets. See the [runtime performance contract](spec/026-runtime-performance.md).

Local replay bundles: `build/kash-audit-366d6c5662a6.tar.gz` (original fixtures and instrumentation), and `build/kash-optimisations-366d6c5662a6.tar.gz` (combined patch, scripts, profiles, traces and both measurement passes).
