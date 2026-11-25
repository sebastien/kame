---
identity: 551e8622-67b5-4c2d-9ccd-217cdc125e05
created: 2026-11-25T18:43:53.037947+00:00
updated: 2026-11-25T18:43:53.460649+00:00
---
# Freshness

Freshness is the file-rule decision to reuse an artifact, or to render and
execute its recipe. Reuse requires an accepted proof from the last successful
run that still matches ([domain/decision:content_freshness]).

## The proof

A file rule is "a function of its declared inputs and of the dependencies
discovered while rendering and executing it" (`006` §Rendering). The proof
records:

- every declared and discovered file, as its canonical path and SHA-256 (a
  missing file is its own identity);
- each glob's pattern and sorted membership;
- environment names read with their values, plus, for a body that runs
  processes, a digest of the whole effective child environment;
- resolved tools by executable digest, and the interpreter's identity;
- the recipe source and execution settings;
- for every output, its existence and the digest written by the successful
  run.

## Rules

- **Skipping requires the whole proof.** Render and execution are skipped
  only when every element matches. Identical command text is not enough. "A
  later invocation must not render in order to decide whether to render."
- **When a rule always runs:**
  - With no record, it renders once.
  - `rule always` and `--force` always execute.
  - A rule with no declared or discovered data dependency is always stale.
- **Order-only prerequisites** must finish, but they are not content
  observations.
- **Inputs that change during production** produce one `INPUT_UNSTABLE`
  warning, and the affected results never become accepted proofs.
- **Reuse reasons** distinguish a detected change, a missing or unverifiable
  proof, and an explicit policy. Timestamps and notifications are not evidence
  of change.
- `changed-inputs` binds the normal file inputs whose digest changed.

## Boundaries

- **Not the [domain/concept:Cache].** Freshness treats the file on disk as
  the result; the cache treats a record as the result. Both store engine
  `SignatureRecord`s (`KSR2`).
- **Not timestamps.** Metadata only gates digest memoization and watch hints,
  never identity.
- **Not ownership.** A fresh artifact is not thereby safe to delete (`040`).
- Phony tasks are never fresh, and services have no freshness.

Implementation: `program/file_context.go`, `program/preflight.go`,
`program/reasons.go`, `core/signature_record.go`, `core/observations.go`.

Sources: `docs/spec/006-runtime.md` §Rendering, §Content identity, §When to
render, §When to execute, §File Rules, §Inputs Changing During Production;
`docs/spec/026-runtime-performance.md` §File content signatures;
`docs/glossary.md` (Freshness).
