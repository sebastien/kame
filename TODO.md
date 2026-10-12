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

## Direct Kash and value contracts

Implement [specification 038](docs/spec/038-direct-kash-and-values.md) in this
order, without weakening its acceptance criteria:

- [x] Add direct Kash recipe AST evaluation and parenthesized expression
  statements/arguments; preserve authored spans, rule frames, generated rules,
  services, suspension, publication, and read-only inspection boundaries.
- [x] Resolve every reached command head as a tracked tool, including captures,
  pipelines, dynamic executable bindings, and `x/NAME` references. Preserve
  startup PATH, overrides, executable identity and scoped authority on both hosts.
- [ ] Add structural interpolated path atoms and signed symbol grammar, then
  wildcard kind masks with link-entry classification and membership/kind watch
  invalidation. Cover empty results, filter identity and literal dynamic glob bytes.
- [ ] Separate scalar `str` concatenation from explicit `json` serialization;
  audit every stdlib operation's action/observation category and migrate action
  names to `!` without changing grants or accepted control-failure phases.
- [ ] Migrate source examples, skills, existing fixtures, formatters, AST/embedding
  consumers and downstream SDK recipes; add native/WASM conformance and migration
  diagnostics. Remove implicit template escaping and serialization workarounds.

## Optional language and diagnostics decisions

- [ ] Update `T013-05-meta-build-mode.sh` version assertions for the runtime
  suffix (`; debug; C)` / `; release; C)`) instead of requiring the mode to be
  the final field. Continue checking independent debug/release/sanitize modes
  and no-relink behavior.
- [ ] Resolve parser/formatter baseline conformance gaps in `T004-02`, `T004-08`,
  `T004-09` and `T018-01`; distinguish malformed interpolation diagnostics,
  AST-preserving canonicalization and composed-header parity. They reproduce
  without automatic cleanup on isolated parent `0a378e15`.
- [ ] Align legacy presentation/inspection assertions in `T006-02`, `T006-04`,
  `T010-11` and `T011-01` with current target-status and external-root contracts,
  while retaining real execution-failure and unknown-logical-name coverage.
- [ ] Resolve the baseline WASM watch-ABI/embedding expectations in `T010-25`
  and `T010-33`, and the pipeline-overflow cleanup failure in `T017-03`. All
  three reproduce on isolated parent `0a378e15`; rerun after grouped-artifact
  fixes before changing their expected results.

- [ ] Fix the retained `callbackProgress` allocation in
  `src/go/kame/operations/collections.go` during host-backed `filter` evaluation
  of computed rule inputs. `T007-12-lib-operation-values.sh` leaks 256 bytes
  under LeakSanitizer; the same minimal direct-Kash fixture leaks on isolated
  parent `0a378e15`. Cover suspended callback teardown without broadening
  cleanup changes into evaluator fixes.

- [ ] Amend specifications 004/005 to support `(include PATH)` as a
  composition-only special form within existing `if` expressions. Preserve
  pure configuration predicates, source-relative paths, selected-branch-only
  loading, cycle detection, authored diagnostics and watch tracking. Decide
  compatibility/deprecation for existing `include` and `when` directives;
  update parsing, formatting, examples and native/WASM conformance tests.
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
  workloads distinct. `src/sh/benchmark-cli.py --samples 5` provides the existing
  CLI baseline; dedicated workloads are needed for retained instances and watch.
