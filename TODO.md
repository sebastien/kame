# Remaining work

This is the single consolidated backlog. Specs in [docs/spec](docs/spec/README.md)
define intended behavior and acceptance criteria, not implementation progress.
Completed work and historical audit results are not retained here.

## Release

- [ ] Publish the first actual signed GitHub release. Staged-release checks do
  not complete publication. Include the pinned bootstrap and launcher, native
  platform artifacts (including Windows PE and OpenBSD native), WASM bundle,
  package-manager manifests, signed checksums and provenance required by
  [distribution](docs/spec/015-distribution.md). Verify installation and execution
  from the published assets, including offline reuse and integrity rejection.
- [ ] Before release, rerun native, leak and WASM gates at a stable revision:
  `CC=clang CFLAGS=-O0 make test`, `CC=clang CFLAGS=-O0 make test-leaks`, and
  `CC=clang CFLAGS=-O0 make test-wasm`. Verify sanitizer instrumentation survives
  harness rebuilds and rerun artifact/build-mode checks without changing Git HEAD.
  Historical passes predate the latest signature/revalidation changes and do not
  establish current-tree conformance. See [test requirements](docs/spec/013-tests.md).

## Optional language and diagnostics decisions

- [ ] Decide whether unquoted wildcard paths in expressions should be shorthand
  for `(wildcard PATTERN)`. Literal rule-input expansion already exists; broader
  expression shorthand is not a current conformance requirement. Specify quoting,
  lazy dependency tracking and watch updates before adding it.
- [ ] Review concrete confusing diagnostic messages and add actionable tips with
  regression coverage. Preserve stable registered codes; any public-code rename
  needs an explicit compatibility decision and coordinated spec/fixture changes.

## Performance research

Measure before optimizing; these are profiling tasks, not established defects.

- [ ] Separate WASM CLI startup from reused embedding-instance evaluation costs.
- [ ] Profile large reused engines and wide/deep lexical scopes independently of
  parsing and CLI startup; prepared indexed lookups already avoid tracked allocation.
- [ ] Profile large recursive wildcard traversal independently of startup;
  selective-subtree pruning is already implemented.
- [ ] Benchmark cold/warm cache throughput and bytes hashed with content signatures,
  plus retained watch invalidation costs. Keep cache hit/miss and file-rule reuse
  workloads distinct. `tools/benchmark-cli.py --samples 5` provides the existing
  CLI baseline; dedicated workloads are needed for retained instances and watch.
