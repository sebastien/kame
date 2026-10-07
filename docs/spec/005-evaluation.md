# Language Evaluation

## Purpose

Evaluation turns language AST values into reactive core values. It is explicit,
allocator-aware, asynchronous through engine requests, and independent of
native threads or promises.

## Evaluation Context

Every evaluation receives a context containing:

- Engine and current node identity.
- Lexical scope.
- Run allocator.
- Current rule inputs and outputs, when applicable.
- Current function arguments, when applicable.
- Operation registry.
- Granted capabilities.
- Execution phase and available host services/request queue.
- Working directory, configured process environment, timeout and capture policy.
- Execution ownership, cancellation, and request generation/attempt correlation.
- Dynamic dependency and effect channels.
- Diagnostic source and frame stack.

Operations receive this context explicitly. There is no thread-local or
async-local evaluation state.

These fields define the execution context shared by Kame effects, Kash, and
embedded `$(...)` processes (`017-kash.md`). Lexical scope governs name lookup;
it does not confer authority. Embedded processes inherit the caller's policy,
and nested configuration cannot broaden grants or relax caller limits. Lazy
definition nodes use their owning program's explicitly configured policy, never
an unrelated caller's broader grants. Unselected or unused computations perform
no host effects.

Parsing is context-independent. A demanded process substitution requires both
a phase permitting process execution and the applicable run grant. Planning or
resolution fails with `PHASE_INVALID`; absent grants fail with `CAP_DENIED`;
authorized but unavailable host services fail with `HOST_FAIL`. Redirections
and explicit environment lookups require their own grants. Capability denial
and cancellation are not recoverable through Kash fallback operators. Existing
Kame `?` continues to handle only `REF_MISSING`.

## Output Registration Context

Computed rule outputs (`004-language.md`) are demanded before target selection
in a pure context, like generated declarations. They may use lexical definitions,
pure operations/functions, and configured overrides, but cannot issue host
requests or effects, even when invocation grants would otherwise permit them.
Restrictions propagate through lazy definitions and helper calls. Unrelated
unused definitions remain lazy. Current-rule captures, target arguments, and
input/output selectors are unavailable: the target index must exist before
those bindings can be established. Output-resolution failures reject
registration before recipe effects and retain the authored output location.

## Scope

A scope is a parent-linked map of names to bindings. A binding is a value,
lexical function, operation, or lazy definition.

Lookup searches the current scope and then parents. Unknown lookup is
`REF_MISSING`. Application heads use the same lexical lookup first, then the
operation registry. A known non-callable head is `EXPR_INVALID`; an absent operation
is `OP_UNKNOWN`.

Top-level value definitions are lazy. First access starts one engine node for
the definition, and concurrent accesses share it. Definition cycles are
reported as dependency cycles. Invalidation clears the definition generation,
not the definition AST.

Program functions and their defining top-level scopes are program-owned.
Call-local scopes and lambdas are run-owned and cannot be published as node
values or stored in cacheable containers. Attempting to return such a callable
across the run boundary is `EXPR_INVALID`.

### Value-program execution

An executable `.km` program composes lazy definitions with top-level expression
statements (`004-language.md`). Register definitions before demanding selected
work so expressions may refer to later definitions. A named-entry run requests
only the selected definitions; an expression-statement run evaluates statements
in source order and returns the final value. The definitions-only `default`
convention, empty-program behavior, and CLI result presentation are defined in
`009-cli.md`.

Statement sequencing does not make all definitions eager. Every statement and
selected definition uses the owning program's argument frame, cwd, grants,
generation correlation, and dependency/effect channels. A failure stops the
selected sequence; output already committed is not rolled back. Resumption
must not replay an accepted earlier statement or consume another statement's
host completion. Named entries share one engine and lazy dependency nodes.

### Composed execution sessions

The runner can load multiple sources and inline fragments in command-line order.
Parse all inputs and register their shared top-level scope before any demanded
work executes. Named-entry and expression-statement behavior above applies to
each fragment, with definitions-only value fragments contributing bindings
without demanding a default when later work is present. Source order sequences
statements and target checkpoints, not lazy definition registration or visibility.

All fragments use one engine and owning program context. In particular, an
expression appended after a rule or Kash file can demand that file's definitions
with the same policy and host services. A language boundary does not reset cwd,
args, invocation limits, or grants; changing parser cannot broaden authority.
The first source establishes the default policy, and explicit grants configure
the session as a whole. Pure value sessions do not implicitly gain process
authority by appending a Kash file.

Compile-time failure in any fragment prevents the sequence from starting.
Runtime failure stops later fragments without rolling back earlier effects.
Resume suspended work using fragment identity and authored spans in addition to
invocation/callback correlation; identical inline text in distinct fragments is
not the same host call. Completed fragments do not replay when later work resumes.

The unified runner is a host entry point, not a new evaluator. Single-expression
mode uses the same expression parser and evaluation context directly; removing
the CLI `do expr` command does not remove expression evaluation APIs.

## Values and Truth

Evaluation uses the value kinds from `002-engine.md`. Only false and nil are
false in predicates. Zero, empty strings, empty lists, and empty records are
true.

Arguments evaluate left-to-right for deterministic diagnostics and effects.
Independent dependency nodes discovered by those arguments may execute in
parallel.

## Value Display

When a materialized value is written to stdout — a `do run` result, a
definition value selected as a target, or a `do cat` definition value — it uses
one canonical notation that is also a valid Kame expression:

| Kind | Notation |
| --- | --- |
| nil | `:nil` |
| boolean | `:true` / `:false` |
| integer | decimal digits |
| float | shortest `%g`; negative zero is written `0` |
| string | double-quoted, with `\"`, `\\`, `\n`, `\r`, and `\t` escapes |
| pattern | double-quoted canonical pattern text |
| list | `[ITEM ...]`, items space-separated |
| record | `[KEY: VALUE ...]`, keys bare names, pairs space-separated |

The display is idempotent: evaluating the displayed text and displaying the
result again yields the same bytes. Nil, booleans, strings, patterns, lists, and
records also reproduce a value of the same kind; an integral float re-evaluates
as an integer but still displays identically. Record keys are produced from
`NAME:` syntax and are always valid names.

Bytes are written raw and resources as their name; the grammar has no literal
for either, so they are outside the notation. This notation is distinct from the
`str` operation (`007-library.md`), which encodes lists and records as JSON-like
text. `--json` emits structured values in its event schema instead of this
notation.

## Functions

A lexical function stores parameter names, optional rest parameter, body AST,
and defining scope. Calling it creates a child scope, binds arguments, and
evaluates body expressions in order. The final expression is returned; an empty
body returns nil.

Too few or too many arguments are `EXPR_INVALID`. A rest argument receives a
list, including an empty list.

Placeholder sections lower to ordinary lexical functions. A placeholder atom
evaluates by reading its indexed argument from the enclosing section call
without name lookup; user definitions named `_0` cannot shadow it. Section
arity and argument-count diagnostics follow the ordinary function rules.

Function definitions in script and `(def ...)` use the same callable
representation.

## Applications and Operations

An operation registry entry contains:

- Name.
- Named call function and opaque context.
- Minimum and maximum arity, or variadic marker.
- Capability requirements.
- Stable implementation version used by cached tasks.

The evaluator validates arity and capability before calling an operation.
Operation failures attach the current expression and call frames.

## Reactive Lifting

Operation authors consume ordinary materialized arguments. The evaluator lifts
an application over reactive inputs:

1. Request every argument node.
2. Wait until each argument has a current value or terminal state.
3. Invoke the operation once with the current argument values.
4. Publish its result.
5. Reinvoke when one or more arguments publish newer revisions, coalescing all
   changes observed before the next invocation.

One application has at most one accepted operation invocation. Each invocation
increments the node attempt number. If arguments change while it runs, its
completion no longer matches the active attempt and one invocation starts with
the newest complete argument set. During this reactive restart, old dependency
interest remains live until the replacement invocation rebinds its edges.
Rediscovered edges adopt that interest; obsolete dependencies are released at
accepted publication or termination. Cancellation and explicit invalidation
also release pending holds, and stale completions cannot restore old results.

An operation may itself return a value, complete without a value, submit a host
request, or publish a stream of values. Nested lists and records are ordinary
values; the evaluator does not recursively interpret streams hidden inside
containers.

## Special Forms

### Fallback

`(? expression...)` evaluates operands in order and returns the first that does
not fail with `REF_MISSING`. No other failure is suppressed. Nil is a successful
value. With no operands it returns nil.

### Let

`(let [name value...] body...)` requires an even binding list. Bindings evaluate
sequentially, and each later binding can reference earlier ones. The body runs
in that child scope and returns its final value.

### Def

`(def name value)` evaluates and binds a value in the current scope.
`(def name [parameters...] body...)` binds a lexical function without first
evaluating its body. Invalid names or parameter lists are `DEF_INVALID`.

### Eval

`(eval text)` parses and evaluates expression text in the current lexical scope.
`eval` does not load filesystem paths or complete scripts.

### If

`(if TEST THEN [TEST THEN]... [ELSE])` evaluates operands left to right in test
order. It evaluates the then-body of the first true test, or the trailing else,
and returns that value; with no true test and no else it returns nil. An even
operand count has no else; an odd operand count ends in else. Zero or one
operand is `EXPR_INVALID`. Exactly one branch evaluates, so a branch may
reference a name that is valid only when selected. Truth follows "Values and
Truth": only false and nil are false.

### And and Or

`(and EXPR...)` evaluates operands left to right and returns the first false
operand, or the last operand when every operand is true; `(and)` is `:true`.
`(or EXPR...)` returns the first true operand, or the last operand when every
operand is false; `(or)` is `:false`. Evaluation stops at the deciding operand,
so later operands are never evaluated and their dependencies and effects do not
run. A single operand is returned without further evaluation.

### Match

`(match SUBJECT CLAUSE...)` evaluates `SUBJECT` once and requires a string, path,
or pattern; a `Bytes` subject is `EXPR_INVALID` and must be converted with `text`
first. Each clause `[PATTERN BODY...]` begins with a match pattern parsed at
parse time: a path or string with matcher groups matches with the anchored
semantics of `014-patterns.md`, while a path or string with no groups matches by
exact text. The first matching clause is selected, and its body evaluates in a
run-owned child scope where each named capture binds as a name, with the
enclosing scope as parent. Anonymous `{*}` and `{**}` captures match but bind no
name, and a reused capture name must match the same text. `[:else BODY...]` is
optional and terminal; when no clause matches and there is no else, the result is
nil. Only the selected body evaluates. A malformed clause is `EXPR_INVALID`;
pattern misuse, such as a clause pattern that is a pure expansion pattern, is
`PAT_INVALID`.

### With

`(with RECORD BODY...)` evaluates `RECORD`, requires a record, creates a
run-owned child scope whose parent is the caller, binds each field key as a name,
and evaluates the body in that scope, returning the body's final value. A
duplicate key re-binds, so the last field wins. An empty body returns nil.
Capture bindings and `with` bindings cannot escape the run that created them,
consistent with lexical scopes.

## Containers and References

List values evaluate their elements left-to-right. Record values evaluate
values left-to-right and retain source key order for formatting; lookup does not
depend on order.

Reference components resolve from left to right:

- Name accesses a record key.
- Integer accesses a list or string code point.
- Slice returns a list or UTF-8 string slice with half-open bounds.
- Selection returns a record containing the requested keys in request order.

Negative indexes and bounds count from the end. Missing keys and invalid type
access are `REF_MISSING`; invalid indexes and slices are `SEL_INDEX_INVALID`.

## Templates and Selectors

String template segments evaluate in source order and concatenate into an owned
string. Expression and reference values stringify according to
`004-language.md`. Selector lookup uses the nearest matching frame:

- Input/output selectors use the enclosing rule frame.
- Argument selectors use the nearest function call frame.

## Dynamic Dependencies

Filesystem and resource operations register dependencies through the evaluation
context before returning their result. Dependency registration uses the current
node and generation. The engine may suspend evaluation until the dependency is
current.

Pure text, collection, and path operations do not create resource edges.

## Capabilities

Capability names are `read`, `write`, `run`, and `env`. A grant is unrestricted
or contains allowed canonical roots/names. Authorization occurs before any host
request. Denial is `CAP_DENIED` and has no side effect.

Path authorization resolves relative paths against the evaluation working
directory and rejects traversal outside granted roots after normalization.
This check is lexical and is not a filesystem sandbox: symlinks may resolve
outside an allowed root. Untrusted build execution requires host-level
containment, which is outside the language capability model.

## Acceptance Tests

- Top-level definitions are lazy and shared by two consumers.
- Invalidating a definition causes one reevaluation on next access.
- A definition cycle reports `DEP_CYCLE` with definition frames.
- Lexical functions capture their defining scope and bind rest arguments.
- Placeholder sections read their indexed arguments without name lookup; a
  definition named `_0` does not shadow a placeholder.
- Lexical names override operation names only within their scope.
- Arguments produce deterministic left-to-right diagnostics.
- A lifted operation reruns once with the newest arguments after coalesced
  updates.
- A stale operation completion is not published.
- Empty lists and strings are true; false and nil are false.
- `?` suppresses only unknown-reference failures.
- `if` evaluates exactly one branch and returns the first true then-body, the
  trailing else, or nil; a missing operand count is `EXPR_INVALID`.
- `and` and `or` short-circuit and return the deciding operand; an unselected
  operand's failing `read` never runs.
- `match` selects the first matching clause, binds named captures in a
  run-owned child scope, returns nil on no match without else, and never
  evaluates an unselected arm.
- `with` binds record fields in a child scope, last duplicate wins, and the
  bindings do not leak into the parent.
- `let` bindings see previous bindings but do not leak into their parent.
- Reference indexes, negative indexes, slices, and selections work on valid
  values and diagnose invalid bounds.
- Rule and argument selectors choose the nearest correct frame.
- A filesystem read records a dependency on its canonical file resource.
- Denied operations create no host request.
- The value display of nil, booleans, numbers, strings, patterns, lists, and
  records re-parses as a Kame expression and displays identically; nested values
  use the same notation as top-level values.
- Evaluation teardown under `mem.Tracker` leaks no scope, closure, or value.
