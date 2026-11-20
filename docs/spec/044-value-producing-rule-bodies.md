---
revision: 2
---

# Value-producing rule bodies

This specification defines how a rule body produces its output's content as a
value instead of writing it through a process. It refines the rule body forms
of `055-unified-kame-kash-language.md` §10.1 and governs build-time content
publication in `005`, `006`, `007`, `016` and `038`. Standalone evaluation and
Kash actions keep their execution contracts.

## Producer selection

A body has exactly one interpretation, selected syntactically and by the
resolved `shell` setting, never by inspecting body text:

| Body | Interpretation |
| --- | --- |
| `= EXPR` in the header | **Expression body**: `EXPR` is the output content |
| Indented block, `shell` unset or naming Kash | **Kash body**: an action block; it may publish with `return VALUE` |
| Indented block, `shell` naming an external shell | **Foreign-shell body**: a document template rendered to one script (`016`) |

```kame
rule ./greeting.txt : ./name.txt = str "Hello, " (strip (text (read inputs.0))) "!\n"

rule ./build/config.json : ./config.km = json config

rule ./build/version.txt : ./VERSION
	let version = strip (text (read inputs.0))
	let revision = $(git rev-parse --short HEAD)
	return "${version}+${revision}\n"
```

`shell` resolves with the other rule settings, before prerequisite execution;
it may be per-rule metadata (`; {shell: "bash"}`) or a build-wide `SHELL`
definition. `shell` applies only to block bodies. An expression body whose
per-rule `shell` names an external shell is rejected when the declaration is
loaded; a build-wide `SHELL` does not apply to expression bodies.

Publishing bodies require exactly one file output and local publication. An
expression body on a task, service, grouped-output rule, or remotely executed
rule is rejected when the declaration is loaded. A Kash body on such a rule may
use `return` without a value, or `return nil`; returning any other value fails
the invocation at the `return` statement.

## Expression bodies

The body is exactly one expression (`055` §4), not a document template or a
Kash block. It may continue onto following lines under the expression
continuation rules; lazy definitions, captures and the rule context (`inputs`,
`outputs`, named captures) use ordinary expression semantics. Syntax errors
retain authored source spans. Multiline string content keeps its authored bytes;
the rule's structural indentation is not part of the expression.

The expression is evaluated in a read-only phase (see below). It closes over
its lexical scope like any rule body.

## Kash `return` publication

A Kash body runs as an action block with the rule's ordinary process, write and
capability authority. When its invocation completes through `return VALUE` and
`VALUE` is not `nil`, that value is the output content and is published under
this specification. `return` may appear anywhere a block statement may; the
reached `return` decides. A body that completes without a `return`, or with
`return nil`, publishes nothing: its output must have been written by the body
itself under the ordinary output contract.

When a value is published, the body's own writes to the output path are not the
result: the published content replaces the output through the atomic publication
path after the body completes. A body that fails, is cancelled, or ends with
`exit` publishes nothing; `exit 0` ends the body without a return value.

## Content

The published value must be a `String` or `Bytes`. Publish exactly those bytes,
including an empty value and every trailing newline. Numbers, collections,
paths, callables and resources are not serialized implicitly and are
`EXPR_INVALID`; use `json`, `fmt`, `str` or `render` explicitly. A failed
computation leaves an existing artifact untouched.

Tracked reads, discovery (`files`, `dirs`, `wildcard`), environment lookups and
explicit `render` calls remain available and participate in dependency
discovery and content freshness. Discovered generated inputs complete before
computation resumes. Publication uses the existing ownership, atomic write,
output verification and forwarding paths; an expression body needs no process.
Dry-run computes available expression values but never publishes content and
never runs a Kash body; unavailable generated reads remain deferred.

Rule identity includes the producer kind, so changing a body between expression,
Kash and foreign-shell forms invalidates previously accepted outputs.

## Read-only evaluation

Expression bodies and foreign-shell template rendering reject output actions
(`out!`, `err!`, `write!`), process execution (including `$(…)` and
`$?(…)`) and clock reads with `PHASE_INVALID`. Reached helpers, callbacks and
lazy definitions inherit the same restriction; indirection cannot turn
evaluation into runtime execution. Unreached branches remain lazy. Capability
grants do not override this phase boundary.

Foreign-shell interpolation computes script text; the shell itself performs its
actions. A Kash body executes its authored program, including explicit action
statements, without template expansion, and publishes only through
`return VALUE` or its own writes. An expression body returns content and never
performs actions.

## Verification

- `tests/T044-01-runtime-kame-rule-bodies.sh`: native/WASM publication, exact
  binary and empty output, dry-run, freshness, tracked templates and forbidden
  indirect effects.
- `program/test/kame_recipes.go`: result types, producer validation, multiline
  expression parsing, content reuse and failure preserving existing artifacts.
- `tests/T044-01-runtime-kame-rule-bodies.sh` also covers Kash `return`
  publication: string and bytes results, `return nil` and bare
  `return`, non-nil returns from ineligible rules, `exit` and failure before
  publication, and replacement of body-written output.
- Existing file-input and WASM publication tests exercise dynamic reads,
  generated prerequisites, content invalidation and host completion failures.
