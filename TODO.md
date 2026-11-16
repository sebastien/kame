# Remaining work

This is the single consolidated backlog. Specs in [docs/spec](docs/spec/README.md)
define intended behavior and acceptance criteria, not implementation progress.
Completed work and historical audit results are not retained here.

## Release

- [ ] Publish the first actual signed GitHub release. Staged-release checks do
  not complete publication. Attach only `kame.com`, `kame.js`, `kame.wasm`,
  `VERSION`, `PROVENANCE.json`, `SHA256SUMS`, and `SHA256SUMS.sig`, as required by
  [distribution](docs/spec/015-distribution.md). Keep launcher/bootstrap helpers
  and package definitions in source; native CI outputs are development artifacts,
  not release attachments. Verify installation and execution
  from the published assets, including offline reuse and integrity rejection.
- [ ] Before release, rerun native, leak and WASM gates at a stable revision:
  `CC=clang CFLAGS=-O0 make test`, `CC=clang CFLAGS=-O0 make test-leaks`, and
  `CC=clang CFLAGS=-O0 make test-wasm`. Verify sanitizer instrumentation survives
  harness rebuilds and rerun artifact/build-mode checks without changing Git HEAD.
   Validate the exact release tree; development-tree passes do not establish
   published-asset conformance. See [test requirements](docs/spec/013-tests.md).

## Remaining repository-review remediation

- [ ] Validate every element of `sorted`/`unique` before comparing: unsupported
  singleton inputs such as `(sorted [[1]])` and `(unique [[1]])` currently pass.
  Preserve the scalar-only [library contract](docs/skills/kame/ref/library.md).
- [ ] Settle the [streaming boundaries](docs/spec/042-kash-streaming.md#12-unresolved-boundaries-and-extensions)
  for zero-terminal multi-branch capture and whitespace-separated channel stdin.
  Preserve the existing empty-string result for redirected linear capture.
- [ ] Run Windows pipeline stream-error and taskkill/grace cleanup conformance
  on a real host. Natural watch invalidation cancellation is covered on Linux.
- [ ] Validate Windows direct-argv PATH/PATHEXT, cross-drive cwd and resolved-tool
  handoffs on a real host; policy/native Linux/cross-link tests do not establish
  Windows runtime acceptance. Repair launch-time job assignment and verify
  Windows directory durability.
  Containment must cover both graph launch and exec replacement: Cosmopolitan
  4.0.2 reparents replacements to Kame, so assigning the blocked fork child is
  insufficient. Validate a supervisor or supported job-at-creation bridge on Windows.
  Verify non-consuming exit
  observation for macOS/BSD APE hosts before enabling the new descendant-retirement
  path there; the pinned Cosmopolitan BSD waitid dispatch is unsupported, so those
  APE hosts retain legacy observation rather than losing process execution.
- [ ] Decide the initial bootstrap trust model before changing it (explicitly
  deferred).

## Kash shell options

- [ ] Add completion and vi/emacs editing-mode switches through the existing
  [option system](docs/spec/054-kash-options.md). Define editing-mode precedence
  against `.inputrc` and preserve existing behavior by default.

## Agent memory

- [ ] Retry the native-CLI testing guidance consolidation using `node`, not `id`,
  in the `revise` operation. Preview with `memo agent learn --dry` before
  publishing; the failed dry run left existing guidance unchanged. Supporting
  observation: `identity=="kame:observations" item=k-7e3127c193505053ed693b3928a4bc9d619c8e4091a6cf3da19d394dd4d4401e`.

## Performance research

Measure before optimizing; these are profiling tasks, not established defects.

- [ ] Profile retained engine execution and dependency invalidation independently
  of parsing and CLI startup.
- [ ] Profile large recursive wildcard traversal independently of startup;
  selective-subtree pruning is already implemented.
- [ ] Benchmark cold/warm cache throughput and bytes hashed with content signatures,
  plus retained watch invalidation costs. Keep cache hit/miss and file-rule reuse
  workloads distinct. `src/sh/benchmark-cli.py --samples 5` provides the existing
  CLI baseline; dedicated workloads are needed for retained instances and watch.
