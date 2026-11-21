# Kame specifications

These documents define what Kame should be: its language, runtime behavior,
host boundaries, public interfaces and conformance requirements. They are
normative contracts, independent of implementation progress or delivery order.
Acceptance criteria describe required behavior, not a report that tests passed.

Specifications do not contain roadmaps, task lists, completion status, dated
verification or implementation history. A missing implementation does not weaken
a requirement. Related specifications refine the contracts they reference;
host-specific boundaries remain explicit.

## Revisions

Each specification starts with front matter that records the major language
revision it conforms to:

```yaml
---
revision: 2
---
```

A major revision is a clean break in source syntax or core semantics. All
specifications in this directory belong to the same revision. Earlier
revisions are not supported or described: there are no compatibility modes,
legacy parsers or migration notes. Within a revision, specifications are
edited in place.

Revision 2 is the unified Kame/Kash language. Start with
[055](055-unified-kame-kash-language.md): it is the normative overview of the
grammar, contexts, name resolution, bindings, invocations, paths, rules,
modules and templates. The other specifications refine their own areas, and
055 governs any conflict.

## Index

| Specification | Contract |
| --- | --- |
| [001 Architecture](001-architecture.md) | Package boundaries, portability, ownership and determinism |
| [002 Engine](002-engine.md) | Values, resources, graph scheduling, invalidation and subscriptions |
| [003 POSIX processes](003-posix-process.md) | Execution, streams, signals, retries and process-tree cleanup |
| [004 Language](004-language.md) | Detailed grammar: lexical structure, expressions, statements, commands, rules, modules and source files |
| [005 Evaluation](005-evaluation.md) | Scopes, lazy and eager bindings, name resolution, phases, outcomes and capabilities |
| [006 Runtime](006-runtime.md) | Selection, planning, dependencies, content freshness and execution |
| [007 Library](007-library.md) | Standard operations and effects, data-last signatures |
| [008 Cache](008-cache.md) | Identity, signatures, records, publication, locking and eviction |
| [009 CLI](009-cli.md) | Invocation, inspection, execution, output and exit status |
| [010 WebAssembly](010-wasm.md) | Freestanding ABI, host requests, buffers and wrapper behavior |
| [011 Diagnostics](011-diagnostics.md) | Codes, severity, source locations and rendering |
| [012 Streams](012-streams.md) | Sources, batches, subscriptions, backpressure, generators and ownership |
| [013 Conformance tests](013-tests.md) | Harness, fixtures, determinism and memory-safety requirements |
| [014 Patterns](014-patterns.md) | Placeholder sections, path kinds, matching, capture and bind |
| [015 Distribution](015-distribution.md) | Artifacts, integrity, launchers, bootstrap and packaging |
| [016 Templates](016-templates.md) | Directives, payloads, rendering, styles and dependencies |
| [017 Kash](017-kash.md) | Action context: words, typed argv, process graphs, functions, control flow and lifetime |
| [018 Source style](018-source-style.md) | Canonical formatting and author conventions |
| [020 Watch](020-watch.md) | Retained roots, resource invalidation and source reload |
| [021 Compiler provisioning](021-cosmocc-provisioning.md) | Pinned Cosmocc archives and verified installation |
| [022 Target arguments](022-target-arguments.md) | Required/defaulted named arguments and identity |
| [023 Wildcard sources](023-wildcard-sources.md) | Shared lazy glob dependencies and membership updates |
| [024 Managed services](024-managed-services.md) | Provisioning, readiness, health, restart and shutdown |
| [025 Generated declarations](025-generated-declarations.md) | Rule values, `generate`, declaration-time control flow, tracked discovery and provenance |
| [026 Performance](026-runtime-performance.md) | Borrowing, lookup allocation and traversal requirements |
| [027 Presentation](027-job-presentation.md) | Bounded job/process display and lifecycle output |
| [028 Embedding APIs](028-embedding-apis.md) | JavaScript/WASM and asynchronous Python/CLI interfaces |
| [029 Pattern extensions](029-pattern-extensions.md) | Anonymous, positional and regex captures |
| [030 Template extensions](030-template-extensions.md) | Labels, matching, trimming, inference and loop bindings |
| [031 Resource protocols](031-resource-protocols.md) | Canonical file/memory URIs, grants and host mappings |
| [032 Remote execution](032-remote-execution.md) | Executor selection, artifacts, publication and cancellation |
| [033 Plugins](033-plugins.md) | Registration, value protocol, adapters and lifetime |
| [035 Native Windows CLI](035-native-windows-cli.md) | Native PE behavior and Windows-host conformance |
| [036 CLI presentation](036-cli_ansi.md) | ANSI, text and JSON presentation, progress and diagnostics |
| [037 Build inspection](037-build-inspection.md) | Recursive resource inventories, producer provenance, dependency stages and completeness |
| [038 Direct Kash and values](038-direct-kash-and-values.md) | Kash rule bodies, expression statements, tracked command heads, text/JSON separation, path kinds and action names |
| [039 Output ownership](039-output-ownership.md) | Grouped artifacts, configured membership, manifests, directory boundaries and safe cleanup |
| [040 Automatic clean](040-automatic-clean.md) | Historical output ledger, conservative deletion authority, protection, rule-change boundaries and race-resistant cleanup |
| [041 Pattern predicates and substitution](041-pattern-predicates-and-substitution.md) | Boolean matching and nonmatch-preserving substitution |
| [042 Kash streaming](042-kash-streaming.md) | Process topology, framing, channels, multiplexing, stream operands and bounded buffering |
| [043 Kame finalization](043-kame-finalization.md) | Execution-only finally! callbacks, effect ordering, bounded cleanup, shared unwind deadlines and error precedence |
| [044 Value-producing rule bodies](044-value-producing-rule-bodies.md) | Expression bodies, Kash `return VALUE` publication and read-only rendering |
| [045 Rule precedence](045-rule-precedence.md) | Ordering and ambiguity between matching rules |
| [046 Kash scripting and publication](046-kash-scripting-and-publication.md) | Script arguments, loops and safe file publication |
| [047 Kash output ports and command lists](047-kash-output-ports-and-command-lists.md) | Output ports, command lists and exit fidelity |
| [048 Practical Kash](048-kash-practical-scripting.md) | Non-interactive scripting: standard streams, `with` scopes and record iteration |
| [049 Process inspection](049-process-inspection.md) | Typed process capture, invocation outcomes and optional executable lookup |
| [050 Modules](050-kash-libraries.md) | `import`, `from`, `export` and `include`, source identity and freshness |
| [051 Kash session authority](051-kash-session-authority.md) | Environment authority, the unit environment store, and foreground terminal handoff |
| [052 Interactive Kash](052-kash-interactive.md) | Interactive sessions, builtins, completion and highlighting |
| [053 Kash history](053-kash-history.md) | History scopes, storage and secret filtering |
| [054 Kash options](054-kash-options.md) | Shell option registry, defaults and validation |
| [055 Unified Kame/Kash language](055-unified-kame-kash-language.md) | Revision 2 language overview: contexts, grammar, resolution, bindings, outcomes, paths, rules, modules and templates |
| [057 Kash UI](057-kash-ui.md) | Pickers, choices, confirmations and text entry across terminal and client-native backends |
| [058 Kash command completion](058-kash-completion.md) | Usage-shaped command specs from man pages and completion files, cached by a Kame build |
