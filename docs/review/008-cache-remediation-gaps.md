# Cache implementation gaps against spec 008

This note records remaining implementation and verification gaps found while
reviewing the current working-copy changes against
[`../spec/008-cache.md`](../spec/008-cache.md). It is scoped to cache behavior;
it is not a general code-quality review.

## Status

The open gaps below are resolved in the working copy:

- Manifest construction uses one encoder capped at 16 MiB, including identity,
  rule text, nested definition values, and final encoding. Overflow frees that
  buffer, still executes, and emits one `CACHE_UNUSABLE` warning.
- A missing path is an explicit cacheable marker. Other stat and read failures
  stay uncacheable.
- Only regular files and missing paths are cacheable. Symlinks are not followed.
  Other filesystem kinds warn and never hit.
- Focused tests cover nested-definition overflow, missing-path invalidation,
  symlink rejection, and hashed cache paths.

Several concerns from the initial review had already been addressed:

- File fingerprints now include a kind marker and treat stat/open/read/close
  failures as unusable for caching rather than hashing an error message into a
  candidate fingerprint (`runtime/cache.go:fileFingerprint`).
- The duplicate `CACHE_UNUSABLE` event path was removed; warning emission is
  owned by the fingerprint/record helpers.
- A regression test changes a declared dependency from a directory to a regular
  file and checks for execution rather than cache reuse
  (`runtime/test/runtime.go:TestCachedTaskInvalidatesWhenDependencyKindChanges`).
- Standard SHA-256 vectors, all value tags, and a complete task fingerprint
  vector are already covered by the existing runtime tests.

`so test ./...` and `CC=clang so test -check=sanitize -panic=abort ./...` both
passed after the resolution.

The sections below are the gaps that were open before this resolution. They are
kept as a record of the defect and the test that now pins it.

## Resolved gaps

### 1. The 16 MiB manifest cap does not bound all intermediate allocations

`cacheBudget` is shared by the canonical fingerprint section appenders in
`runtime/cache.go:29` and `cacheFingerprint` (`:156`), so ordinary section
construction stops when its budget is exhausted. However, the limit is not
end-to-end:

- `cacheIdentity` (`:135`) constructs a complete identity—including formatted
  rule text—before charging the task section to the budget.
- `rule.FormatRule` in `cacheFingerprint` (`:170` vicinity) constructs the full
  formatted rule before its bytes are appended.
- `appendDefinitionDependency` (`:259`) still encodes values and recursively
  appends dependency entries without consuming the shared budget.
- Top-level section wrapping and the final fingerprint buffer can temporarily
  duplicate bytes already held in section buffers.

Consequently, an oversized rule, identity, or definition can allocate far more
than 16 MiB before being declared uncacheable. The spec limits the encoded
manifest, and the implementation should enforce a shared budget while
constructing every section and identity, including nested values. Prefer a
bounded encoder that returns an explicit overflow result and releases partial
buffers on every exit.

**Needed test:** create a manifest that exceeds the limit through a nested
definition/dependency (not just one large shell/script string); verify the
task still executes, no successful cache record is created, and verbose mode
emits exactly one `CACHE_UNUSABLE` warning. Ideally verify bounded allocation
or test a budget-injected encoder at just-under/over-limit boundaries without
requiring a very large fixture.

### 2. Missing-path dependencies do not have the specified encoded marker

`fileFingerprint` returns `false` for every `Stat` error (`runtime/cache.go:312`),
which makes the task uncacheable. The spec distinguishes a missing path
dependency and requires its canonical path plus an explicit missing marker.
This difference is conservative for cache hits, but it means missing
dependencies are not represented by the specified canonical fingerprint
format. It also conflates not-found with permission, I/O, and other failures.

Add a distinct missing result/encoding and keep unexpected stat/read failures
uncacheable. Test deterministic missing-marker encoding and verify a later
appearance of the path invalidates the task.

### 3. File fingerprint kind coverage is narrower than general filesystem kind

The implementation currently distinguishes directory from non-directory, but
the spec says the fingerprint includes `kind`. If supported inputs can include
symlinks or other file types, the marker should distinguish those types too
(and define whether symlinks are followed). The directory-to-regular-file test
does not cover symlinks, special files, or the stated policy for them.

Document the supported kind model and add tests for each supported type. If
non-regular inputs are intentionally uncacheable, assert they produce a
warning and never a hit rather than assigning them the same kind marker as a
regular file.

### 4. No focused acceptance test exercises manifest overflow behavior

The existing oversized-record test verifies that a large *record on disk* is
rejected safely. It does not test an oversized *dependency manifest* becoming
uncacheable while recipe execution continues. These are separate limits and
failure paths. Add the manifest-overflow test described above.

### 5. Canonical target/path safety lacks an explicit cache-path regression test

`cachePath` hashes the cache identity before using it as a filename, which is
the desired design. There is no visible acceptance test demonstrating that
target text containing separators, traversal components, or unusual bytes
cannot escape `.littlemake/cache/tasks/`. Add a test that materializes such a
valid task target (where the language permits it), then verifies the only
record is under the cache directory and no path derived from raw target text
was created outside it.

### 6. Missing tests for cache behavior should be distinguished from missing
implementation

The following spec cases appear to have meaningful coverage in the current
test file: captures, rule-body changes, declared file changes, glob membership,
environment dependencies, operation versions, shell/options, bare task
dependencies, failures, cancellation, replay, malformed records, and concurrent
writers. Avoid duplicating these as generic coverage gaps; add focused tests
only where they pin an uncovered edge condition (such as overflow, path safety,
or missing-path encoding).

## Suggested completion order

Completed in this order:

1. Make the 16 MiB budget cover identity, rule, recursive definition values,
   all dependency entries, and final encoding without oversized intermediate
   allocations.
2. Add explicit missing-path encoding while keeping other filesystem errors
   uncacheable.
3. Define and test supported filesystem kinds/symlink handling.
4. Add manifest-overflow and cache-path-safety acceptance tests.
5. Run `so test ./...` and the sanitizer suite after implementation changes.
