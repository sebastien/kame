---
identity: a4d7d79c-4d2e-4a90-b367-088a9e149057
created: 2026-11-25T18:43:48.905777+00:00
updated: 2026-11-25T18:43:49.492541+00:00
---
# Cache (cached tasks)

The cache lets a `task cached` skip repeated successful work while its
implementation and every observed input stay equivalent. It is "a runtime
feature, not a generic virtual filesystem" (`008`).

- **Key.** The canonical task target, its selected captures and the
  rule-declaration fingerprint. Records live under `.kame/cache/tasks/` with
  hashed names, so user text never becomes a path.
- **Fingerprint.** A canonical binary encoding, hashed with SHA-256, of:
  - identity and captures;
  - the canonical rule header and body AST;
  - declared inputs;
  - dynamic file, glob, resource and environment dependencies from the
    accepted render;
  - resolved tools and operation versions;
  - execution settings: shell, cwd, environment, timeout and retries.
- **Record.** The fingerprint, the dependency manifest, the exit status, and
  stdout/stderr capped at 64 KiB per stream by default, with truncation flags.

## Rules

- **Hit.** Schema, identity and the full fingerprint must all match. On a
  hit, the retained logs are replayed as cached output.
- **Commit.** Only on success: temp file, then sync, then atomic rename.
  Failures and cancellations never create or replace a record.
- **Phony dependencies.** A phony task is never a cacheable dependency, so a
  cached task that depends on one is always stale.
- **Unusable records.** Malformed records, oversized ones (more than 16 MiB
  of observations) and unverifiable ones are misses with a `CACHE_UNUSABLE`
  warning, never failures.
- **Concurrency.** Native hosts use 256 advisory lock stripes and re-read
  under the lock. JavaScript hosts use one lock directory per key.
- **Pruning.** At most 1024 records per backend; the oldest are pruned.

## Boundaries

- **Not file-rule [domain/concept:Freshness].** File rules keep the freshness
  rules of `006`; the cache is for tasks.
- **Not authentication.** SHA-256 is used for change detection only.
- **Opaque stdin disables caching**, because consumption cannot be reproduced
  by a cache hit.
- **Timing history is advisory** and is not part of a record.

Implementation: `program/cache.go`, `program/cache_encode.go`,
`program/cache_write.go`, `cmd/kame/cache_command.go` (`kame do cache`).

Sources: `docs/spec/008-cache.md`; `docs/spec/009-cli.md` §Run options;
`docs/glossary.md` (Cache identity, fingerprint, and manifest).
