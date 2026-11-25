---
identity: 80a5b763-e038-4816-91c2-fcf92796fc14
created: 2026-11-25T18:43:49.920303+00:00
updated: 2026-11-25T18:43:50.467653+00:00
---
# Capability

A capability is permission for host-backed work. There are four: `read`,
`write`, `run` and `env`. A **grant** gives a capability either
unrestricted, or limited to canonical roots (paths) or names (environment).
Authorization is checked before a [domain/concept:HostRequest] is submitted,
so a denial (`CAP_DENIED`) has no side effect.

## Where checks happen

- The evaluator checks arity and capability before calling an operation.
- Resource URIs are checked after canonicalization.
- Plugins check both their declaration and the caller's grants.
- The JavaScript API checks before invoking host callbacks.
- Remote execution checks before staging inputs.

## Defaults (CLI)

| Session starts with | Default grants |
| --- | --- |
| `expr`, or a `.km` program without rules | none |
| A `.km` program that declares rules | read/write within cwd, plus run |
| Kash | read/write within cwd, run, unrestricted env reads |

- An explicit `--allow-*` replaces only that capability's default.
  `--no-default-grants` denies everything not explicitly granted.
- The first fragment fixes the session policy, and later fragments cannot
  broaden it.
- The JavaScript API denies everything unless granted. Python follows the CLI.

## Boundaries

- **Not a sandbox.** Path checks are lexical, so symlinks can escape them,
  and a run grant does not contain the child process.
- **Not a phase.** A grant never authorizes an effect that the current
  [domain/concept:Phase] forbids.
- **Configuration is not an env grant.** These do not grant environment
  reads: `--env`, `NAME=V`, `KAME_NAME`, `--set`, `set env/…` and rule `env:`
  metadata. A rule's `authority` metadata adds named read/unset rights for
  that recipe only.
- **Internal authority is separate.** Source loading, build-metadata checks
  and tool resolution use internal authority, not user read grants.
- **Denial cannot be recovered.** `CAP_DENIED` and cancellation propagate
  through `??`, Kash recovery and `$?(…)`.

Related: [domain/concept:ProcessUnit] [domain/component:Evaluator]

Sources: `docs/spec/005-evaluation.md` §Capabilities; `docs/spec/009-cli.md`
§Execution Capabilities; `docs/spec/028-embedding-apis.md`;
`docs/spec/051-kash-session-authority.md`.
