# Diagnostic Registry

## Purpose

This file is the authoritative registry of stable LittleMake diagnostic codes.
Codes use `LM-` followed by exactly five uppercase ASCII letters. A code's
meaning must not change after release.

## Severities

- `warning`: execution may continue and the result remains usable.
- `error`: the current parse, evaluation, target, or command cannot complete.
- `fatal`: the instance cannot safely continue, normally due to exhausted or
  corrupted host state.

## Codes

| Code | Default severity | Meaning |
| --- | --- | --- |
| `LM-PARSE` | error | Invalid syntax; recovery may lower it to warning |
| `LM-REFNO` | error | Unknown reference or missing record component |
| `LM-SELCT` | error | Selector used without a matching context |
| `LM-SELIX` | error | Invalid index or slice |
| `LM-OPUNK` | error | Unknown operation |
| `LM-EXPRV` | error | Invalid expression value, type, or arity |
| `LM-DEFIV` | error | Invalid definition or binding form |
| `LM-CAPDN` | error | Capability denied |
| `LM-PHASE` | error | Operation is not valid in the current phase |
| `LM-TRMRL` | error | No rule or source for target |
| `LM-TRAMB` | error | More than one rule instance matches |
| `LM-TRDCY` | error | Dependency cycle |
| `LM-CMDFL` | error | Recipe process exited unsuccessfully |
| `LM-CMDTO` | error | Recipe process timed out |
| `LM-CMDCN` | error | Execution was cancelled |
| `LM-HOSTF` | error | Host request, spawn, pipe, wait, or signal failure |
| `LM-FSERR` | error | Filesystem read, write, stat, or glob failure |
| `LM-OUTMS` | error | Successful recipe omitted a declared output |
| `LM-OUTCF` | error | Declarative yield conflicts with a shell command |
| `LM-CACHE` | warning | Cache record or manifest cannot be used |
| `LM-UNSUP` | error | Valid but unsupported feature on this target |
| `LM-NOMEM` | fatal | Allocator or fixed arena exhausted |
| `LM-HNDLE` | error | Invalid, stale, or foreign ABI handle |
| `LM-CLIUC` | error | Unknown CLI command |
| `LM-CLIUO` | error | Unknown CLI option |
| `LM-CLIMV` | error | Missing CLI option value |
| `LM-CLICF` | error | Conflicting CLI options |
| `LM-CLIIV` | error | Invalid CLI option value |
| `LM-CLINS` | error | No build source was found |
| `LM-NOART` | error | Requested target has no readable artifact |

## Rules

- A lower package returns the most specific code it can establish.
- Wrapping adds frames and notes but retains the original code.
- Command exit uses `LM-CMDFL`; inability to spawn or reap uses `LM-HOSTF`.
- A missing requested source file uses `LM-FSERR`; a file target with no rule and
  no existing file uses `LM-TRMRL`.
- Malformed cache data is a miss and `LM-CACHE` warning, never a fatal error.
- Unsupported service execution uses `LM-UNSUP`.
- OOM formatting must not allocate.

## Acceptance Tests

- Every normative failure named in `docs/spec` references a registered code.
- Every code matches `^LM-[A-Z]{5}$` and appears once in this registry.
- Wrapping a diagnostic preserves code, severity, and original source span.
- Human and JSON formatting represent the same code and notes.
- Static `LM-NOMEM` formatting succeeds with a failing allocator.
