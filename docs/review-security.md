# Security review

Reviewed 2026-10-03; cache publication rechecked 2026-10-04. This review covers the native POSIX host, evaluation grants,
JS/WASM forwarding, artifact writes, process cancellation, and diagnostic output.
It is a source review with selected runtime reproductions, not a claim of complete
memory-safety verification. Full repository verification remains in progress.

## Trust boundary

Kame builds and explicitly authorized processes run with the caller's OS
privileges. Grants control language host operations; they do not contain an
executable's subsequent filesystem, network, or environment access. Source files
and includes are loaded before evaluation, as build configuration. The WASM
module has no hosted imports, but its JS wrapper intentionally supplies host I/O.

Filesystem grant checks are lexical, as specified in
[005-evaluation.md](spec/005-evaluation.md#capabilities). Both native and WASM
permit reading a symlink inside a granted root whose destination is outside it.
A reproduction using a temporary directory and the public text
`public-test-marker` returned that text on both hosts. This is a documented limit,
not evidence of filesystem sandboxing. Embedders must supply OS containment for
untrusted builds. Avoid describing `--allow-read=DIR` as a filesystem sandbox.

## Findings

| ID | Severity | Finding | Disposition |
| --- | --- | --- | --- |
| SEC-1 | High when output directories contain attacker-controlled entries | Non-durable native writes opened `OUTPUT.kame-write.tmp` directly, following a pre-created symlink and allowing concurrent writers to share staging data. | Fixed: exclusively create a unique sibling temporary, apply requested permissions, and rename only after successful write and close. |
| SEC-2 | Trust-boundary limitation | Lexical roots permit symlink escape and executable grants do not constrain child effects. | Explicitly specified behavior; require host containment for hostile code. |
| SEC-9 | Availability | An 8 MiB WASM recipe output aborts in transient event JSON allocation before forwarding all bytes. | KB-9 fixed: T012-03 verifies lossless 8 MiB NUL/binary stdout/stderr, bounded failure retention and 64 KiB cache replay on both hosts. All 28 WASM host sanitizer tests pass. True logical-heap exhaustion diagnostics remain open under spec 010. |
| SEC-3 | Verification gap | Portable package sanitizer verification passes; the compiled CLI leak gate remains unverified. | All 359 package tests pass under Clang AddressSanitizer in the current checkout. Keep release completion open until sanitized CLI conformance and external-consumer checks finish. |
| SEC-7 | Memory safety | Returning a definition function from a `let` exposed a freed function and scope to calls and ancestor-store rejection. | Fixed: definition lookups return an owned wrapper retaining the lexical scope. All 65 evaluator sanitizer tests pass, including returned-function calls, result cleanup, and `DEF_ESCAPE` rejection. |
| SEC-8 | Input validation | WASM build-source descriptors accepted incorrectly typed fields and offsets whose source end exceeded the 32-bit span range. | Fixed: require string names/text, integer offsets, and an in-range source end before copying fragments. All 26 WASM host sanitizer tests pass, including malformed descriptors and recovery after rejection. |
| SEC-6 | High when cache directories contain attacker-controlled entries | WASM cache put followed an existing record symlink and overwrote its target. | Fixed: exclusive sibling staging, file sync, and rename; T010-19 preserves a public marker and verifies the published cache hit. |
| SEC-5 | Atomic visibility | JS write requests previously used direct file writes. | Fixed: exclusive staging in a private sibling directory followed by rename; T010-21 covers bytes, zero length, failed publication and cleanup. |
| SEC-4 | Low confidentiality limit | Live process streams, collected shell results, and explicit `out` remain observable, even though diagnostic causes omit captured output. | Intended: omission protects diagnostic serialization, not deliberate output. |

The atomic-write fix also removes the old fixed path-length temporary buffer:
the buffer now scales with the destination directory. Root-directory outputs
retain a sibling temporary in `/`. Durable writes sync before rename; directory
fsync is not implemented, so the durability contract depends on filesystem crash
semantics. Atomic visibility is distinct from persistence across power loss.

## Process and capability review

`host/posix/posix.c` uses `execve` for direct argv execution, process groups for
cancellation, TERM followed by KILL escalation, and `waitpid` to reap children.
Pipeline stages use OS pipes and close their known unused endpoints. The JS host
uses explicit argv execution and private pipeline staging directories; its format
preflight rejects Node's implicit ENOEXEC shell fallback. Executable-format
preflight has a race between inspection and launch and must not be presented as
an atomic executable identity guarantee.

`lang/eval/context.go` normalizes path grants before host submission.
`operations/host.go`, `lang/eval/process*.go`, and the session recipe launch path
apply capability checks. The JS wrapper additionally checks process and
redirection grants when forwarding requests. These checks should remain in
regression coverage when adding source forms or new execution paths.

Diagnostic process causes preserve status, signal, and truncation information
while omitting captured bytes. Keep that policy when adding new error paths.
Cache records are local build data; successful records use temporary writes and
rename. Cross-process cache locking and cache eviction are explicitly deferred
by spec 008.

## Verification

- `cd src/go/kame && so test ./host/posix`: 21 tests pass, including staging
  symlink protection, output permissions, durable output, and temporary cleanup
  after a failed rename.
- `cd src/go/kame && CC=clang so test -check=sanitize -panic=abort ./host/posix`:
  21 tests pass with the atomic-write fix.
- Native/WASM public-marker symlink reproduction confirms the documented lexical
  grant boundary.
- `tests/T010-19-wasm-cache.sh`: 9 assertions pass, including record-symlink replacement, marker preservation, readable cache replay, and staging cleanup.
- Signal, pipeline, redirection, grant, and cancellation suites are part of the
  broader running conformance check; do not infer whole-suite success from the
  targeted results above.

The broad portable-package command `cd src/go/kame && CC=clang so test -check=sanitize -panic=abort ./...` passes all 359 tests across 16 packages. This includes executable dependency tracking and literal wildcard ownership. Full compiled CLI leak verification is a separate gate.

Go's CLI compatibility tests initially failed AddressSanitizer during repeated
command execution. Symbolization located reclaimed `Script.Items` and
`Definition.Words` slices. Solod 0.4.0 allocates pointer-bearing structs as byte
slices in its Go stubs; Go cannot scan those pointers, and its `slices.Append`
stub bypasses the explicit allocator. `GOGC=off` makes the unchanged CLI tests
pass. The Go compatibility gate now states that setting explicitly. This is a
compatibility-test limitation, not evidence that compiled C ownership is safe;
the separate compiled CLI leak gate still verifies actual frees and retained
allocations.

The CLI harness now preserves sanitizer build flags when refreshing `build/kame.sanitize`. A forced harness rebuild was checked for `__asan_init`/report symbols and passed the 94-assertion parse matrix. The earlier run that replaced the executable with a checks-only binary is discarded as leak-gate evidence.
