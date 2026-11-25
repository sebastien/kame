---
identity: b6ea33f6-1c3e-457c-a99e-f2330fb9eeb2
created: 2026-11-25T18:43:52.315578+00:00
updated: 2026-11-25T18:43:52.740111+00:00
---
# Diagnostic

A diagnostic is Kame's structured, user-facing failure or warning. It has a
stable **code** (for example `TGT_NO_RULE`), a severity (warning, error or
fatal), an owned message and an optional primary source span. It may also
carry notes, related locations, tips, a target and target stack,
**frames** and a **cause**.

- **Frames** record evaluation, definition, rule or script boundaries crossed
  during propagation. They are context, not a second error location. The
  human trace is numbered from `#0` in propagation order. It is an evaluation
  trace, not a native stack.
- **A cause** keeps the bounded underlying host or process outcome without
  replacing Kame's diagnosis. It never includes captured stdout or stderr,
  environment values or secrets.
- **Code families** include `PARSE_ERR`, `REF_MISSING`, `EXPR_INVALID`, `DEF_*`,
  `CAP_DENIED`, `PHASE_INVALID`, `TGT_*`, `DEP_CYCLE`, `RECIPE_*`, `OUTPUT_*`,
  `KASH_*`, `SERVICE_*`, `HOST_FAIL`, `FS_*`, `CACHE_UNUSABLE`, `CLEAN_*`,
  `OPT_*`, `FEATURE_UNSUP` and `HANDLE_INVALID`.

## Rules

- The lowest layer that detects a failure sets the code and primary span.
  Outer layers only add frames, notes, tips, target and cause.
- Locations are never fabricated, and spans stay on authored source across
  context boundaries (for example, a value fragment in a command word in a
  rule in a declaration loop).
- Human, plain and JSON renderings carry the same data.
- A code's meaning never changes after release, and every normative failure
  uses a registered code.
- Internal Go errors are translated into diagnostics. They never replace the
  contract.

## Where it lives

- Model: `src/go/kame/diagnostic`.
- Registry: the Codes table in `docs/spec/011-diagnostics.md`, checked by the
  `T011-*` tests. There is no Go code table; codes are literals at call sites.
- Rendering: `cmd/kame/diagnostics.go` (native), a separate copy in
  `src/js/kame.js` (WASM CLI), and `program/event_json.go` (JSON).

Related: [domain/component:Cli] [domain/architecture:kame_system]

Sources: `docs/spec/011-diagnostics.md`; `docs/spec/001-architecture.md`
§Errors and Diagnostics; `docs/glossary.md` (Diagnostics).
