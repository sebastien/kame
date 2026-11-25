---
identity: 861cb8b0-c870-433e-9182-e62c8e9e5811
created: 2026-11-25T18:43:55.400033+00:00
updated: 2026-11-25T18:43:55.826740+00:00
---
# Kash

Kash is Kame's action language: the command grammar of the unified language
([domain/concept:Context] action). It passes typed values directly to
functions and executables, and composes processes into pipelines and graphs.
It "never converts values into shell text and reparses them" (`017`).

## Where it runs

- **Rule recipes.** The default body of a rule, task or service is a Kash
  action block ([domain/concept:Recipe]).
- **Kash programs** (`.kash`, alias `.ksh`). The top level is an action block.
  Run with `kame FILE.kash`, `kame do kash`, or the binary invoked as `kash`.
- **Interactive sessions.** Started with `kash` on a terminal, `kash -i`, or
  `kame do run -i` ([domain/concept:Session]).
- **Inside expressions.** `$(…)` captures stdout, `$?(…)` returns an
  [domain/concept:Outcome], and `$&(…)` starts owned async work. Each holds
  one graph or command list.

## Defining traits

- **Typed argv.** [domain/concept:Word]s never split or reparse values
  ([domain/decision:kash_typed_actions]).
- **Command-head resolution:**
  1. keywords;
  2. lexical callables (a non-callable binding is `EXPR_INVALID`);
  3. the closed builtin set (`cd`, `wait`, `kill`, `out!`, `err!`, plus
     script-only and interactive-only builtins);
  4. executables found through the unit's `PATH`.

  The standard library is never consulted: `basename $f` runs the executable.
- **Pipes stay separate.** `|` connects processes in a
  [domain/concept:ProcessGraph], while `|>` stays a value pipe. Functions can
  be stages: their stdout takes part, and their return value does not.
- **Status policy:**
  - a nonzero exit fails the statement;
  - `CMD ?` accepts the exit, and `CMD ? ELSE` recovers;
  - `&&` and `||` sequence completed outcomes;
  - `??` covers only `nil` and missing lookups;
  - capability denial and cancellation can never be recovered.
- **Lifetime:**
  - Each invocation owns its async work and its `defer` cleanup, which runs
    once in reverse order with a bounded budget.
  - Mutable state (environment store and cwd) belongs to the
    [domain/concept:ProcessUnit].
- **Parse first.** A whole body or fragment set is parsed before anything
  runs.

## Boundaries

- **Not a POSIX shell.**
  - Values are never word-split.
  - An unquoted `(` starts a value fragment, never a subshell.
  - Stderr routing is limited to `2>`, `2>>` and `2>&1`, with no numbered
    descriptors.
  - Foreign shells are explicit opt-ins (`; {shell: "bash"}` recipes,
    `source SHELL PATH as NAME`).
- **Not a separate implementation.** Kash shares Kame's values, bindings and
  evaluator. Its grammar is in `lang/expr`, composition in `lang/script` and
  evaluation in `lang/eval`. There is no `lang/kash`.

Related: [domain/architecture:kash_execution] [domain/concept:Kame]

Sources: `docs/spec/017-kash.md`,
`docs/spec/055-unified-kame-kash-language.md` §5–8,
`docs/spec/047-kash-output-ports-and-command-lists.md`,
`docs/spec/048-kash-practical-scripting.md`, `docs/ref-kash.md`.
