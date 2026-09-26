# LittleMake Rewrite Roadmap

This directory specifies the Solod rewrite of LittleMake. The legacy project in
`deps/littlemake-legacy` is a behavioral reference, not an architecture to port.

When references disagree, compatibility follows this order:

1. Behavior covered by legacy tests.
2. Behavior used by `deps/littlemake-legacy/Makefile.lmk` and examples.
3. The latest legacy specifications.
4. Older specifications and untested implementation details.

New specifications in this directory override every legacy source.

## Goals

- A portable, embeddable reactive engine with lazy and dynamic computation
  graphs.
- Independently usable expression, template, and rule languages.
- A stream-aware standard library whose operations remain ordinary functions.
- Reliable native process orchestration with streaming output and cancellation.
- A familiar LittleMake build language without preserving legacy internals.
- A freestanding WebAssembly target after the native vertical slice works.

## Non-goals

- Source or API compatibility with the TypeScript packages.
- JavaScript promises, thenables, async generators, or `OperationCell` behavior.
- Unbounded replay of stream history.
- A generic VFS, remote execution, SQLite, plugins, or watch mode in the first
  implementation.
- C-style expression calls, arrow lambdas, or JavaScript object syntax.
- Dynamic loading of JavaScript modules.

## Specifications

| Spec | Subject | Depends on |
| --- | --- | --- |
| `001-architecture.md` | Package boundaries and portability | none |
| `002-engine.md` | Values, graph, scheduling, ownership | 001 |
| `003-posix-process.md` | Hosted process execution and lifecycle | 001 |
| `004-language.md` | Template, expression, rule, and script syntax | 001 |
| `005-evaluation.md` | Scopes, operations, selectors, and lifting | 002, 004 |
| `007-library.md` | Standard operations and effects | 003, 005 |
| `006-runtime.md` | Planning, resources, freshness, execution | 002, 003, 005, 007 |
| `008-cache.md` | Cached tasks, fingerprints, and records | 006, 007 |
| `009-cli.md` | Native command-line interface and diagnostics | 006, 008 |
| `010-wasm.md` | Freestanding host ABI | 002, 004, 005, 006, 007, 008 |
| `011-diagnostics.md` | Diagnostic model, reporting, and code registry | 001 |
| `012-streams.md` | Source protocol, batches, materialization | 002 |
| `013-tests.md` | End-to-end test suite and its contracts | all |
| `014-patterns.md` | Placeholder sections and pattern values | 004, 005, 007 |

Every implementation specification also depends on the code registry in 011.

## Delivery Order

### Milestone 1: platform proof

- Create a Go module pinned to `solod.dev v0.4.0`.
- Compile and test one portable package.
- Prove POSIX process spawning, pipe streaming, process groups, cancellation,
  and child reaping.

### Milestone 2: engine proof

- Implement values, source materialization, nodes, updates, dynamic
  dependencies, invalidation, and a deterministic single-threaded scheduler.
- Test fan-out using latest-value plus future-update semantics.
- Use a fake executor; no parser or operating-system dependency is needed.

### Milestone 3: native vertical slice

- Parse scalar definitions, simple rules, paths, selectors, and recipes.
- Plan and execute a dependency graph through the engine and POSIX host.
- Support the equivalent of the legacy repository's `Makefile.lmk`.
- Add mtime freshness and the minimal `littlemake` CLI.

### Milestone 4: language and library

- Complete the specified expression, template, rule, and script languages.
- Implement lexical evaluation and the standard operations required by the
  legacy core example.
- Capture dynamic filesystem dependencies during evaluation.

### Milestone 5: production execution

- Run independent ready nodes concurrently.
- Add timeouts, retries, signal forwarding, bounded logs, and cached tasks.
- Add formatter and inspection CLI commands.

### Milestone 6: WebAssembly

- Translate and link portable packages for freestanding WebAssembly.
- Drive evaluation and graph execution through a host callback ABI.

## Work Rules

- An agent implements one specification or one named milestone slice at a time.
- A specification is complete only when all acceptance tests in it pass.
- Public APIs may use different names than the conceptual names in these specs,
  but must preserve their contracts.
- Do not add deferred features as placeholders, interfaces, or configuration.
- Keep `core`, portable language packages, and their transitive imports free of
  `os`, `conc`, `sync`, and native C dependencies.
- Every owning type has an explicit `Free` operation or an allocator-scoped
  lifetime documented at its declaration.

## Verification

The eventual project-level verification commands are:

```sh
so test -check=sanitize -panic=abort ./...
so build -check=warn ./cmd/littlemake
so translate-test -o generated ./core/...
```

Commands may be introduced incrementally as packages are created. Generated C
belongs in ignored build output and is not source.
