# Generated Declarations

## Purpose

Generated declarations let ordinary typed Kame configuration produce a
bounded set of additional build rules before target selection. They support
data-driven target families without evaluating generated source text or
running shell commands during compilation.

## Syntax

```kame
generate NAME = EXPRESSION
```

`NAME` is a unique generator identity. The expression is evaluated in source
order after ordinary value definitions and before rule selection. It may use
pure Kame operations and functions. Process execution, writes, and host requests
are invalid during generation.

## Batch schema

The expression must return a list of records. Each record contains exactly
these required fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `kind` | string | `file`, `task`, or `cached-task` |
| `target` | non-empty string | One concrete target name or path |
| `inputs` | list of strings | Prerequisites that affect freshness |
| `order-only` | list of strings | Prerequisites that affect ordering only |
| `recipe` | list of strings | Kame recipe template lines |

Unknown fields and values of the wrong type are errors. Input and target strings
follow the ordinary rule path and resource interpretation. Recipe lines use
the same template parser and execution rules as authored recipe lines.

Generation limits are 4096 records per batch, 1 MiB of retained UTF-8 data per
batch, 64 KiB per field, 4096 combined input/order-only/recipe list items per
batch, and 256 recipe lines per rule. Limits are checked before the generated
rules become visible.

## Registration and ownership

All batches are evaluated and validated before any generated rule is
registered. A batch failure rejects compilation and leaves no executable
program. Target names must be unique within a generator and across generated
and authored rules, including literal targets obtained by computed-output
resolution. Authored outputs resolve before collision validation and visibility;
neither a failed output expansion nor a failed batch leaves executable rules.
Generated declarations participate in the same target
selection, dependency planning, freshness, caching, and execution as authored
rules.

The compiler owns the evaluated batches, lowered rules, and recipe templates.
Every success and failure path releases them exactly once. Diagnostics use the
generator declaration's source and span, and identify the emitted target when a
record is invalid.

## Dependencies and inspection

References to earlier value definitions are generator dependencies. Those
definitions are evaluated through the ordinary evaluator and their names are
retained in generator metadata. Generated file and resource prerequisites enter
the ordinary dependency graph and cache fingerprint. Watch recompilation
replaces a generator's complete target set after an input source changes.

Plan output exposes generator identity, emitted rule fields, and generator
dependencies. Rule and target order is deterministic: source order for
generators and list order within each batch.

## Portability and diagnostics

Native and WASM share parsing, evaluation, validation, lowering, registration,
diagnostics, and fingerprinting. Hosts service only ordinary build requests;
generation itself performs no host effects. Cyclic value definitions, malformed
records, duplicate targets, unknown rule kinds, and resource-limit failures use
their established diagnostic codes with authored generator locations.

Acceptance coverage must include generated tasks and file rules, pure computed
targets, definition dependencies, duplicate and malformed batches, dynamic
source replacement, native/WASM parity, deterministic planning, and allocator
cleanup on rejected batches.
