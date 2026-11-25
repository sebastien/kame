---
identity: aa2cb2f9-10f6-4fa8-b4fb-4140e4a4fea4
created: 2026-11-25T18:43:45.619502+00:00
updated: 2026-11-25T18:43:46.036059+00:00
---
# Host (`host`, `host/posix`, `host/wasm`)

The host layer is the portable request contract and its two implementations.
It performs filesystem, process, environment, clock, lock and watch work on
behalf of the engine. It never decides graph policy.

## Packages

- **`host`** ("portable requests issued by evaluators and runtimes"): request
  kinds and payloads for files, discovery, processes and process-graph
  topology, cache get/lock, services, foreign shells and memory resources,
  plus the `ProgramHost` interface and request queue.
- **`host/posix`**: the native host, built on Go OS APIs. It covers process
  supervision (process groups, stage topology, terminal handoff, signals),
  files, tool lookup over `PATH`/`PATHEXT`, file locks, watchers (inotify on
  Linux, polling on macOS), the Bash bridge for foreign shells, and pickers.
- **`host/wasm`**: the WASI reactor runtime, with ABI handles (slot plus
  generation), an event queue with fixed 48-byte headers, a reclaiming heap,
  a memory host and presentation. JavaScript performs file, process and watch
  work through `src/js/kame.js` ([domain/component:Embedding]).

## Interactions

- Receives [domain/concept:HostRequest]s from [domain/component:Evaluator]
  and [domain/component:Program].
- Returns completions that the [domain/component:Engine] validates.

## Invariants

- The host transports facts and opaque record bytes. Freshness, caching and
  scheduling decisions are not its concern
  ([domain/decision:portable_host_boundary]).
- Cancellation reaps whole process groups (SIGTERM, then SIGKILL after a
  grace period), and teardown waits for native process-group cleanup.
- Native and WASM backends must produce the same output.

## Known gaps

- Foreign shells (`source SHELL PATH as NAME`) are native-only.
- `host` imports `lang/expr` for discovery patterns, and `host/posix` imports
  `core` and `host`. The architecture spec does not list either dependency
  ([domain/architecture:kame_system]).

Sources: `docs/spec/003-posix-process.md`, `docs/spec/010-wasm.md`,
`docs/spec/020-watch.md`, `docs/spec/056-kash-foreign-shells.md`,
`src/c/wasm/kame_wasm.h`.
