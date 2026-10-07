# Kame specifications

These documents define what Kame should be: its language, runtime behavior,
host boundaries, public interfaces and conformance requirements. They are
normative contracts, independent of implementation progress or delivery order.
Acceptance criteria describe required behavior, not a report that tests passed.

Specifications do not contain roadmaps, task lists, completion status, dated
verification or implementation history. A missing implementation does not weaken
a requirement. Related specifications refine the contracts they reference;
host-specific boundaries remain explicit.

| Specification | Contract |
| --- | --- |
| [001 Architecture](001-architecture.md) | Package boundaries, portability, ownership and determinism |
| [002 Engine](002-engine.md) | Values, resources, graph scheduling, invalidation and subscriptions |
| [003 POSIX processes](003-posix-process.md) | Execution, streams, signals, retries and process-tree cleanup |
| [004 Language](004-language.md) | Expressions, definitions, rules, scripts and source syntax |
| [005 Evaluation](005-evaluation.md) | Scopes, functions, lazy evaluation, lifting and capabilities |
| [006 Runtime](006-runtime.md) | Selection, planning, dependencies, content freshness and execution |
| [007 Library](007-library.md) | Standard operations and effects |
| [008 Cache](008-cache.md) | Identity, signatures, records, publication, locking and eviction |
| [009 CLI](009-cli.md) | Invocation, inspection, execution, output and exit status |
| [010 WebAssembly](010-wasm.md) | Freestanding ABI, host requests, buffers and wrapper behavior |
| [011 Diagnostics](011-diagnostics.md) | Codes, severity, source locations and rendering |
| [012 Streams](012-streams.md) | Sources, batches, subscriptions, backpressure and ownership |
| [013 Conformance tests](013-tests.md) | Harness, fixtures, determinism and memory-safety requirements |
| [014 Patterns](014-patterns.md) | Placeholder sections, matching, expansion and replacement |
| [015 Distribution](015-distribution.md) | Artifacts, integrity, launchers, bootstrap and packaging |
| [016 Templates](016-templates.md) | Directives, payloads, rendering, styles and dependencies |
| [017 Kash](017-kash.md) | Typed process arguments, graphs, control flow and lifetime |
| [018 Source style](018-source-style.md) | Canonical formatting and author conventions |
| [020 Watch](020-watch.md) | Retained roots, resource invalidation and source reload |
| [021 Compiler provisioning](021-cosmocc-provisioning.md) | Pinned Cosmocc archives and verified installation |
| [022 Target arguments](022-target-arguments.md) | Required/defaulted named arguments and identity |
| [023 Wildcard sources](023-wildcard-sources.md) | Shared lazy glob dependencies and membership updates |
| [024 Managed services](024-managed-services.md) | Provisioning, readiness, health, restart and shutdown |
| [025 Generated declarations](025-generated-declarations.md) | Typed rule batches, validation, provenance and replacement |
| [026 Performance](026-runtime-performance.md) | Borrowing, lookup allocation and traversal requirements |
| [027 Presentation](027-job-presentation.md) | Bounded job/process display and lifecycle output |
| [028 Embedding APIs](028-embedding-apis.md) | JavaScript/WASM and asynchronous Python/CLI interfaces |
| [029 Pattern extensions](029-pattern-extensions.md) | Anonymous, positional and regex captures |
| [030 Template extensions](030-template-extensions.md) | Labels, matching, trimming, inference and loop bindings |
| [031 Resource protocols](031-resource-protocols.md) | Canonical file/memory URIs, grants and host mappings |
| [032 Remote execution](032-remote-execution.md) | Executor selection, artifacts, publication and cancellation |
| [033 Plugins](033-plugins.md) | Registration, value protocol, adapters and lifetime |
| [035 Native Windows CLI](035-native-windows-cli.md) | Native PE behavior and Windows-host conformance |
