---
identity: 01602c5f-a5f1-4db4-9b2d-c8bc4fab99aa
created: 2026-11-25T18:44:06.219171+00:00
updated: 2026-11-25T18:44:49.632886+00:00
status: accepted
date: "2026-10-07"
---
# Content-digest freshness instead of timestamps

Status: accepted.
Date: 2026-10-07 (content identity added to spec 006 in commit `4235d092`,
with `3c6eeffe` and `9ee3f8ad`). Before that date, a file rule was fresh when
its oldest output was no older than its newest input (spec 006 as of
2026-09-13).

## Context

A reuse decision should reflect what a recipe actually consumed: file bytes,
glob membership, environment values, resolved tools and execution settings.
Timestamps describe none of these reliably.

## Alternatives

- **Modification-time comparison** (Make, and Kame's initial model).
  Replaced.
- Metadata observations (size, mtime, kind) are still recorded, but only as
  observations. They never prove reuse.

## Decision

[domain/concept:Freshness] requires an accepted proof: the SHA-256 of every
consumed file and every output, recorded glob membership, environment values
(persisted as digests), resolved tool digests, and the rule and execution
settings. A missing output or an unverifiable proof forces execution.

## Rationale

"Size and modification time cannot prove that previously observed bytes
remain unchanged." (`006` §Content identity). This matches the README design
goal of inspectable reuse.

## Consequences

- An external rewrite invalidates even when its timestamp is older. A touch
  that leaves the bytes unchanged does not.
- A file rule with no declared or discovered dependency is always stale.
- Hashing costs time on every check. Later performance work caches digests
  (`docs/spec/026-runtime-performance.md`).
- Cached tasks use a separate fingerprint record ([domain/concept:Cache]).

Sources: `docs/spec/006-runtime.md` §Content identity, §When to render,
§File Rules; Git history.
