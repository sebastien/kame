# Value-producing rule bodies

This specification supersedes build-template effect behavior in specifications
005, 006, 007, 016 and 038. Standalone evaluation and direct Kash actions retain
their execution contracts.

## Producer selection

A recipe has one explicit interpretation: external-shell script, direct Kash
program, or Kame content expression. Never infer its language from body text.

```kame
./greeting.txt : ./name.txt ; [shell: kame]
	(str "Hello, " (strip (text (read @<))) "!\n")
```

`shell` selects the producer and resolves with the other recipe settings, before
prerequisite execution. The name `kame`, including a computed name, selects a
content expression. `SHELL = kame` selects it for rules without a per-rule
`shell`. The string `"kame"` remains an external executable, not this selector.
Content bodies require exactly one file output and local publication; tasks,
services, grouped outputs and remote execution are rejected.

## Content computation

The authored body is exactly one Kame expression, not a document template or Kash
program. Multiline expressions, lazy definitions, captures and rule selectors use
ordinary expression semantics. Syntax errors retain authored source spans.
The shared recipe indentation prefix is structural and is removed from
multiline string content; additional indentation remains data.

The result must be a string or raw bytes. Publish exactly those bytes, including
an empty value and every trailing newline. Do not implicitly serialize numbers,
collections, nil, callables or resources. Use `json` explicitly for structured
data. A failed computation leaves an existing artifact untouched.

Tracked reads, glob membership, environment lookups and explicit `render` calls
remain available and participate in dependency discovery and content freshness.
Discovered generated inputs complete before computation resumes. Publication uses
the existing ownership, atomic write, output verification and forwarding paths;
no shell process is needed. Dry-run computes available values but never publishes
content; unavailable generated reads remain deferred.

## Read-only expansion

Kame content computation and external-shell template expansion reject output
actions (`out!`, `err!`, `yield!`, `write!`), process execution and clock reads with
`PHASE_INVALID`. Reached helpers, callbacks and lazy definitions inherit the same
restriction; indirection cannot turn rendering into runtime execution. Unreached
branches remain lazy. Capability grants do not override this phase boundary.

Shell interpolation still computes command text; the shell itself performs its
actions. Direct Kash executes its authored program, including explicit action
statements such as `(yield! CONTENT)`, without template expansion. A Kame body
returns content rather than invoking that action.

Existing `@(yield! CONTENT)` bodies must migrate explicitly to
`[shell: kame]` with `CONTENT` as their expression, or to direct Kash if their
intent is an action program. Formatting does not perform this migration.
Preflight guards and standard-operation versions invalidate older accepted
renders, so cached outputs cannot silently preserve the removed expansion behavior.

## Verification

- `tests/T044-01-runtime-kame-rule-bodies.sh`: native/WASM publication, exact binary and
  empty output, dry-run, freshness, tracked templates and forbidden indirect effects.
- `program/test/kame_recipes.go`: result types, producer validation, multiline
  expression parsing, content reuse and failure preserving existing artifacts.
- Existing file-input and WASM publication tests exercise dynamic reads, generated
  prerequisites, content invalidation and host completion failures.
