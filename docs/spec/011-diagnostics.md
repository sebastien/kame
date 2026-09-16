# Diagnostic Registry

## Purpose

This file is the authoritative registry of stable LittleMake diagnostic codes.
Codes use uppercase ASCII words separated by underscores. A code's meaning
must not change after release.

## Severities

- `warning`: execution may continue and the result remains usable.
- `error`: the current parse, evaluation, target, or command cannot complete.
- `fatal`: the instance cannot safely continue, normally due to exhausted or
  corrupted host state.

## Codes

| Code | Default severity | Meaning |
| --- | --- | --- |
| `PARSE_ERR` | error | Invalid syntax; recovery may lower it to warning |
| `REF_MISSING` | error | Unknown reference or missing record component |
| `SEL_NO_CONTEXT` | error | Selector used without a matching context |
| `SEL_INDEX_INVALID` | error | Invalid index or slice |
| `OP_UNKNOWN` | error | Unknown operation |
| `EXPR_INVALID` | error | Invalid expression value, type, or arity |
| `DEF_INVALID` | error | Invalid definition or binding form |
| `CAP_DENIED` | error | Capability denied |
| `PHASE_INVALID` | error | Operation is not valid in the current phase |
| `TGT_NO_RULE` | error | No rule or source for target |
| `TGT_AMBIG` | error | More than one rule instance matches |
| `DEP_CYCLE` | error | Dependency cycle |
| `RECIPE_FAIL` | error | Recipe process exited unsuccessfully |
| `RECIPE_TIMEOUT` | error | Recipe process timed out |
| `EXEC_CANCELLED` | error | Execution was cancelled |
| `HOST_FAIL` | error | Host request, spawn, pipe, wait, or signal failure |
| `FS_ERR` | error | Filesystem read, write, stat, or glob failure |
| `OUTPUT_MISSING` | error | Successful recipe omitted a declared output |
| `OUTPUT_CONFLICT` | error | Declarative yield conflicts with a shell command |
| `CACHE_UNUSABLE` | warning | Cache record or manifest cannot be used |
| `FEATURE_UNSUP` | error | Valid but unsupported feature on this target |
| `NO_MEMORY` | fatal | Allocator or fixed arena exhausted |
| `HANDLE_INVALID` | error | Invalid, stale, or foreign ABI handle |
| `CMD_UNKNOWN` | error | Unknown CLI command |
| `OPT_UNKNOWN` | error | Unknown CLI option |
| `OPT_NO_VALUE` | error | Missing CLI option value |
| `OPT_CONFLICT` | error | Conflicting CLI options |
| `OPT_VALUE_INVALID` | error | Invalid CLI option value |
| `BUILD_NO_SOURCE` | error | No build source was found |
| `NO_ARTIFACT` | error | Requested target has no readable artifact |

## Rules

- A lower package returns the most specific code it can establish.
- Wrapping adds frames and notes but retains the original code.
- Command exit uses `RECIPE_FAIL`; inability to spawn or reap uses `HOST_FAIL`.
- A missing requested source file uses `FS_ERR`; a file target with no rule and
  no existing file uses `TGT_NO_RULE`.
- Malformed cache data is a miss and `CACHE_UNUSABLE` warning, never a fatal error.
- Unsupported service execution uses `FEATURE_UNSUP`.
- OOM formatting must not allocate.

## Acceptance Tests

- Every normative failure named in `docs/spec` references a registered code.
- Every code matches `^[A-Z]+(?:_[A-Z]+)*$` and appears once in this registry.
- Wrapping a diagnostic preserves code, severity, and original source span.
- Human and JSON formatting represent the same code and notes.
- Static `NO_MEMORY` formatting succeeds with a failing allocator.
