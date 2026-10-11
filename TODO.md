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

- [ ] Update `tests/T009-05-cli-plan.sh` missing external-file root assertions
  for specification 037: explicit/unprefixed file roots need no producer, even
  when absent. Its nine `TGT_NO_RULE`/diagnostic-envelope assertions fail on
  parent `0a378e15` too; preserve errors for unknown logical names and cover
  external seed identity and zero execution instead.

- [ ] Amend specifications 004/005 to support `(include PATH)` as a
  composition-only special form within existing `if` expressions. Preserve
  pure configuration predicates, source-relative paths, selected-branch-only
  loading, cycle detection, authored diagnostics and watch tracking. Decide
  compatibility/deprecation for existing `include` and `when` directives;
  update parsing, formatting, examples and native/WASM conformance tests.
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
