# Kash output ports, command lists, and exit fidelity

## 1. Purpose and status

**Implemented contract.** This specification extends [017 Kash](017-kash.md),
[042 Kash streaming](042-kash-streaming.md), and [009 CLI](009-cli.md). Existing
behavior remains authoritative except where explicitly extended below. The
focused native and Node/WASM acceptance suite is
`tests/T047-01-kash-output-ports-and-command-lists.sh`.

Support common scripting patterns without importing Bash's descriptor model or
reparsing values as shell source:

```kash
printf "warning\n" >&err
compile && publish
probe || recover
```

An unhandled executable exit must also survive nested `kame do run` boundaries.
Process graphs remain concurrent streaming units; command lists sequence their
completed outcomes. Output ports remain distinct from named data channels.

Initial scope excludes arbitrary descriptor duplication (`2>&1`), routing a
stage's own stderr into a graph/file, subshells, heredocs, backtick substitution,
process replacement, asynchronous compound command lists, and mutable bindings.
`$(...)` remains the sole command-substitution syntax. Grants do not change.

## 2. Reserved output ports

### Syntax and boundaries

| Form | Contract |
| --- | --- |
| `command >&out` | Send stdout to the current output boundary. |
| `command >&err` | Send stdout to the current error boundary. |
| `command >&name` | Existing named-channel writer. |
| Bare `>&` | Existing conditional-branch next rejoin. |
| `>@` | Existing otherwise rejoin. |

`out` and `err` remain reserved: neither may be declared as a named channel or
used as a named reader. Destinations are authored identifiers, not computed
expressions or descriptor numbers. Reserved `in` remains unavailable.

A graph inherits output/error sinks from its execution context:

- Ordinary execution supplies the invocation's stdout/stderr presentation sinks.
- Command substitution replaces the output sink with its bounded capture sink,
  leaving the error sink unchanged.
- Nested Kash callables inherit these sinks; helpers do not bypass capture.
- Presentation may use structured host events rather than raw OS descriptors.

Consequently, `$(printf "hello" >&out)` captures `"hello"`, while
`$(printf "warning" >&err)` captures `""` and forwards `"warning"` to the error
sink. Neither form redirects the executable's own stderr.

### Graph ownership and presentation

Reserved ports are presentation destinations, not named channels. Multiple
stages may present to an enclosing sink without creating a composable merge
edge. Per-writer byte order is preserved; cross-writer order and record atomicity
are not guaranteed.

Each stdout port has exactly one disposition. These combinations fail before
launch or output truncation:

```kash
producer >&err | consumer
producer > ./result >&err
producer >&reports >&out
```

Sink-only reserved writers are allowed wherever existing sink-only named writers
are allowed. Copying stdout to both presentation sinks requires broadcast:

```kash
producer |(broadcast) (>&out, >&err)
```

Reserved writers may also terminate selected branches:

```kash
source |(frame :lines)
    |(filter (includes? "WARN" item) | cat >&err)
    | cat
```

Selected output goes to the error sink; it does not rejoin the remainder.
Framed output is encoded to payload bytes before presentation. Bounded buffering,
backpressure, cancellation, delivery failures, and invocation ownership retain
their existing contracts. No hidden capture or temporary file is introduced.

### Capture cardinality

For each captured graph, count outputs directed to its capture sink. Ordinary
terminal stdout and explicit `>&out` both count:

- One output: capture it under the existing UTF-8 and size limits.
- More than one: `KASH_AMBIGUOUS_CAPTURE`; require explicit merge or redirection.
- Zero: return an empty string after successful completion.

File redirections and `>&err` are noncapture dispositions. Named writers and
branch rejoins are internal connections, not additional terminal outputs; count
the eventual terminal destinations after resolving the topology. In particular,
a linear graph ending in `> ./file` retains its existing empty-capture behavior.

The zero-output rule includes multi-branch graphs whose outputs all have explicit
noncapture dispositions, resolving that open boundary in `042` §12. Routing two
producers to `>&out` remains ambiguous; routing both to `>&err` yields an empty
capture with independently ordered error presentation. No output is silently
discarded or concatenated to resolve ambiguity.

## 3. Sequential command lists

### Grammar and binding

Add `A && B` and `A || B`. Each operand is a synchronous graph, optionally using
existing acceptance or graph recovery. Binding order, tightest first:

1. Words, expressions, substitutions, and live operands.
2. Process groups, redirections, and writer/rejoin dispositions.
3. Pipelines and configured graph connections.
4. Acceptance `?`, then right-associative graph recovery `? ELSE`.
5. `&&` and `||`, at equal precedence, evaluated left-to-right.
6. Statement separators.

Thus `A | B && C` means `(A | B) && C`; `A || B && C` means
`(A || B) && C`; and `A ? B && C` means `(A ? B) && C`. A recovery operand ends
before a command-list operator. These parentheses explain binding; they do not
introduce authored grouping syntax. Use a Kash helper for compound control that
needs another grouping boundary.

Operators require operands and are recognized outside quoted/escaped data and
embedded Kame expressions. A newline immediately after `&&` or `||` continues
the list; a newline before an operator does not. Formatting preserves that
distinction. Existing word-composition and process-group comma boundaries remain
unchanged. A list may not appear as a process-group member, filter branch, or
live producer operand: those positions require one concurrent graph. Lists in
`$(...)` are explicitly allowed below.

### Selection and preparation

Evaluate the first operand and retain its effective outcome. For each subsequent
operator:

- `&&`: execute the right operand only after effective success.
- `||`: execute the right operand only after an eligible process-exit failure.
- A skipped operand leaves the retained outcome unchanged.
- Any other failure stops the list immediately.

A selected graph fully completes, including collection or cancellation/reaping,
before preparing its successor. Skipped operands perform no argument evaluation,
substitutions, tool resolution, runtime grant checks, file opening, or launch.
The entire source, including skipped operands, is still parsed and statically
validated. Each selected graph retains `042`'s complete prelaunch preparation;
short-circuiting does not weaken validation within that graph.

| Outcome | Eligible for `||`? |
| --- | --- |
| Ordinary nonzero executable exit | Yes |
| Process signal exit, including SIGPIPE | Yes |
| Launch failure or timeout | No |
| Framing, transport, predicate, or buffer failure | No |
| Argument/evaluation error | No |
| Capability denial or cancellation | Never |

This follows command-condition classification, not the broader recovery contract
of `? ELSE`. Existing graph recovery remains available for its documented launch,
timeout, and graph-error categories. Mixed outcomes retain the graph's existing
failure-policy precedence: a fatal graph error cannot become an eligible exit
merely because a cancelled peer also reports a signal.

An eligible failure used to decide a list does not immediately emit an
unhandled-failure diagnostic. Live stderr is unchanged. Emit that diagnostic only
if the list ultimately propagates the failure.

### Effective outcome and result

Explicit acceptance changes failure propagation, not recorded exit status:

```kash
probe? && report
```

`report` runs after an accepted exit even if the accepted result retains
`.status = 7`. Selection uses the evaluator's effective success/failure outcome,
not a numeric field in an arbitrary returned record. A helper that handles an
internal failure does not fail again because it returns that completion record.

The list returns the result and effective outcome of its last executed operand.
No implicit combined Result or unbounded operand history is introduced. Existing
events may report each executed graph independently.

| List (names indicate executable exit codes) | Executed operands | Final outcome |
| --- | --- | --- |
| `exit0 && exit7` | Both | Failure, status 7 |
| `exit7 && exit0` | First | Failure, status 7 |
| `exit7 || exit0` | Both | Success |
| `exit0 || exit7` | First | Success |
| `exit0 || exit7 && report` | First, then `report` | Outcome of `report` |

A failed list at ordinary statement position stops later statements unless an
enclosing recovery handles it. `probe && publish` therefore fails the script if
`probe` fails; use an `if` block when skipping work should allow later statements.
This is Kash failure propagation, not Bash's context-sensitive `errexit` behavior.

### Conditions, capture, async, and cleanup

Command conditions in `if`, `elif`, and `while` may contain lists. Existing
restrictions remain: no explicit acceptance/recovery or async syntax. The final
eligible process-exit failure means false; other failures propagate.

Command substitution may contain a list:

```kash
text = $(primary || fallback)
```

Executed operands write into one capture sink and share one cumulative capture
budget. Cardinality is checked per graph, not by summing sequential operands.
Output from a failed operand remains before fallback output; recovery is not
transactional. A failing final list fails the substitution. Capture overflow is
fatal and does not select `||`.

Terminal async `&` retains its existing single-graph/recovery-expression contract.
`A && B &` and `A || B &` are rejected before effects in this initial extension.
Separate sequential statements such as `A &; B && C` remain valid; existing
invocation ownership and eventual async joins apply.

Direct compound lists after `defer` are also rejected, preserving its restricted
grammar. A deferred Kash helper may contain an ordinary synchronous list. Lists
introduce no scope or cleanup lifetime: reached deferred actions belong to the
enclosing Kash invocation, retaining lexical context under existing rules.

## 4. Exit-status fidelity

For explicit source execution, an unhandled process-exit failure determines the
CLI exit code:

- Ordinary executable exit: preserve its status.
- Signal exit: retain the established `128 + signal` mapping.
- Successful handled/accepted execution: exit zero, regardless of retained
  completion records.
- Language, capability, launch, timeout, and internal failures: retain their
  documented CLI mappings.

Preserve typed failure provenance through evaluation, callables, session work,
finalization, and CLI adaptation. Never infer status from diagnostic text or an
arbitrary record field. An executable exit `42` must not become `1` merely because
another `kame do run` wraps it. Process failure selected by a list or graph uses
the existing deterministic aggregate status, not whichever peer exits last.

Existing ownership and finalization ordering remain authoritative:

- Preserve an established primary failure/status if cleanup also fails.
- If work succeeded, the first unhandled cleanup failure in LIFO execution order
  becomes primary; a process-exit failure supplies its exact status, while a
  non-process failure uses its existing CLI mapping. Later cleanup failures are
  supplementary; remaining actions are still attempted under the existing budget.
- Previously handled failures must not reappear during finalization.
- Invocation interruption, cleanup budgets, and forced shutdown retain their
  established behavior.

Concurrent build-target failure aggregation is outside this extension. A nested
explicit runner preserves its exit even when its parent build command uses a
separate aggregate build-failure code. JSON summaries, human diagnostics, and
actual CLI termination must agree on the selected outcome.

## 5. Inspection, rollout, and acceptance

ASTs and formatting retain reserved versus named writers, rejoin kinds, list
operators and operand order, recovery boundaries, and authored spans. Inspection
does not evaluate operands to predict selection. Validated topology distinguishes
presentation destinations from data-channel edges. Dry-run performs no probe or
substitution to decide a short-circuit branch.

Native and Node/WASM implement the same selection, capture, status, and cleanup
contracts. Unsupported host routing fails explicitly before launch or truncation,
never falls back to a system shell. The affected clauses in `017`, `042` (including
§12), and `009` are updated; focused cross-backend coverage is registered in
`Makefile.kmk`.

Acceptance requires shared native and Node/WASM cases covering:

1. `>&err` excludes stdout from capture; `>&out` participates in capture, including
   through helpers. Executable stderr remains unchanged.
2. Reserved branch/sink-only writers, explicit broadcast, conflict preflight,
   ambiguous multi-output capture, and zero-output empty capture.
3. Large port-directed output stays bounded, drains before completion, and
   responds to delivery failure/cancellation without leaked children.
4. Skipped list operands cause no runtime effects or grant checks, while malformed
   skipped syntax still fails before execution. Selected graphs retain preflight.
5. Equal-precedence left associativity, recovery boundaries, multiline formatting,
   process-group exclusions, and literal/embedded operator boundaries.
6. Accepted nonzero exits select `&&` without losing recorded status; handled
   failures and incidental returned records do not become new failures.
7. `||` handles ordinary/signal exits but not launch, timeout, capture, capability,
   operator, or evaluation errors, including mixed-failure graphs.
8. Conditions use the final effective outcome; final unhandled list failures stop
   later statements and emit only the appropriate unhandled diagnostic.
9. Sequential capture retains failed-operand bytes, shares a cumulative limit,
   and never selects fallback after capture overflow.
10. Compound async/direct defer rejection before effects; unchanged ownership for
    separate async statements and cleanup registered in reached helpers.
11. Exit `42` survives nested explicit runners and failed helpers/substitutions;
    successful acceptance/recovery exits zero. Signals retain their mapping.
12. Primary failure survives cleanup errors; cleanup-only failure selects the
    first LIFO failure. Actual exit, JSON summary, and diagnostics agree.
13. Parse/format round trips and inspection execute nothing and preserve all
    boundaries above.
