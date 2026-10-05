# Native Windows CLI

This specification closes the native-Windows CLI deferral in
`docs/spec/015-distribution.md`. A Windows PE that only launches Node and the
WASM module is the Windows launcher, not the native CLI.

## Requirements

1. `kame.exe` is a self-contained native CLI build of the same Kame evaluator,
   command grammar, operations, and program runtime used by the POSIX CLI. It
   does not require Node, a WASM module, a bundled runtime directory, or network
   access to execute an already installed command.
2. The command surface and observable results match the shared CLI contract in
   specs 005, 009, 015, and 027: target selection, `do` commands, diagnostics,
   human and JSON events, streams, exit codes, environment overrides, working
   directory, retry, timeout, cache, and watch behavior.
3. The Windows host implements every method of `host.ProgramHost`, including
   filesystem metadata and reads, atomic file publication, cache locks, wall
   and monotonic clocks, structured single-process and pipeline execution,
   bounded output retention, cancellation, and disposal.
4. A recipe pipeline connects each stage's stdout directly to the next stage's
   stdin. Intermediate output is not retained as public output. Each stage gets
   its requested working directory and an environment formed by replacing
   inherited values case-insensitively, as Windows environment names require.
5. A timeout or cancellation terminates and reaps every process started for the
   request, including descendants and all pipeline stages. Terminal events
   preserve the request identity, aggregate outcome, per-stage statuses,
   truncation flags, and bounded stdout/stderr prefixes.
6. The CLI resolves executables according to the existing declared-tool and
   `--tool` policy. Windows executable suffixes and path separators are handled
   by the native host without changing the shared command grammar.
7. Source discovery, include loading, cache reads and writes, `read`, `stat`,
   `exists?`, wildcard expansion, and yielded outputs use native Windows file
   operations. Atomic publication and cache locking must not follow symlinks or
   expose partial records.
8. `kame.exe --version` reports the selected release metadata without starting
   a child process. The release bundle and package manifests install this
   native executable as `kame`.
9. The signed `KAME_VERSION=latest` handoff selects and verifies a newer
   Windows-native CLI before command dispatch. Pinned launches remain pinned,
   and invalid signatures, hashes, versions, and disabled downloads fail
   closed as specified by 015.

## Acceptance

The Windows-host suite builds and runs the actual native PE, not a cross-compile
or the Node/WASM launcher. It verifies:

- version output, command parsing, native target execution, file reads and
  writes, and include/source discovery;
- host environment and CLI overrides, working-directory changes, PATH lookup,
  and case-insensitive replacement of an inherited environment name;
- multi-stage pipelines with binary output larger than the retention limit,
  separated stderr, every stage status, and backpressure;
- timeout and cancellation of a parent, child and grandchild, with terminal
  delivery only after the process tree is gone;
- cache hit, invalidation, concurrent miss locking, failed publication,
  interrupted owners, and cleanup;
- watch invalidation for file changes and wildcard membership changes;
- parity for human diagnostics, JSON events, streams and exit statuses against
  the POSIX native CLI for cases whose behavior is platform independent;
- the installed package archive contains the native CLI and required release
  metadata, and its pinned installer digest matches the signed manifest.

Cross-compilation, launcher contract tests, and Node/WASM behavior are useful
supporting checks, but none proves this specification's native-runtime
requirements. Keep this feature open until the Windows-host suite and staged
native release package pass.
