# Security review

Reviewed 2026-10-03. This review covers the native POSIX host, evaluation grants,
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
| SEC-3 | Verification gap | The full sanitizer suite and all memory-ownership paths have not yet been verified in this review. | Keep release completion open until broad checks finish; targeted POSIX sanitizer tests pass. |
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

- `cd src/go/kame && so test ./host/posix`: 20 tests pass, including staging
  symlink protection, output permissions, durable output, and temporary cleanup
  after a failed rename.
- `cd src/go/kame && CC=clang so test -check=sanitize -panic=abort ./host/posix`:
  20 tests pass with the atomic-write fix.
- Native/WASM public-marker symlink reproduction confirms the documented lexical
  grant boundary.
- Signal, pipeline, redirection, grant, and cancellation suites are part of the
  broader running conformance check; do not infer whole-suite success from the
  targeted results above.
