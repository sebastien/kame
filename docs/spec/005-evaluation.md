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

### Named execution context

The ordinary names `inputs`, `outputs`, `inputs/changed`, and `args` expose typed
lists from the current evaluation context. Lexical bindings take precedence,
preserving existing user definitions and parameters with these names. Without
a lexical binding they are aliases of `@<*`, `@>*`, `@<?`, and `@*`, respectively.
Their availability, ordering, nearest-frame selection, and errors are identical
to those selectors; they do not read files or launch processes by themselves.

`inputs` includes normal prerequisites, excluding order-only prerequisites.
`outputs` includes declared output identities, including a named task's logical
output name, not just file artifacts. Both require a rule frame. `inputs/changed`
requires a file-rule frame at recipe evaluation/rendering time; it is unavailable
during planning/resolution or in task/service frames. Unavailable context reports
`SEL_NO_CONTEXT`, and changed-input access in a disallowed phase reports
`PHASE_INVALID`. Empty available lists are values, not missing context.

`args` is the nearest function/Kash-call argument list, or the script invocation's
literal operands when outside a call. Build invocations without operands expose
an empty argument list. Named target parameters remain their existing bindings;
they are not silently converted into `args`. Pure expression evaluators without
an argument frame report `SEL_NO_CONTEXT`. Named values support ordinary list
references such as `args.0` and `inputs/changed.0` with normal index diagnostics.

Slash-separated reference names such as `inputs/changed` are not paths. Filesystem
paths still require `/`, `./`, or `../`. Lazy scoped definitions and suspended or
async evaluations retain the same frames as compact selectors.

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

## Truth and emptiness

Only nil and boolean false are false. Integer/float zero and empty strings,
bytes, lists, and records are true. Expression conditions, `bool`/`not`, and
predicate filtering use this same truth contract. `and`/`or` short-circuit and
return the deciding operand without boolean coercion. Source-composition `if`
conditions use this same truthiness; process exit status has a separate contract.

Emptiness must be tested explicitly with `count` on strings, bytes, lists, or
records; zero count does not make the original value false. Missing optional
data must be checked for nil before counting it. Numeric zero comparisons are
not collection emptiness checks. Whitespace, nil-valued list items, and
nil-valued record fields still count as data.

## Scope

A scope is a parent-linked map of names to bindings. A binding is a value,
lexical function, operation, or lazy definition.

Lookup searches the current scope and then parents, then the operation registry.
Ordinary registered operations are callable values, including plugin operations:
`(map uppercase ["a" "b"])`, `(filter exists? FILES)`, and
`predicate = exists?` need no lambda wrapper. Lexical bindings shadow operation
names. Comparison aliases (`=`, `==`, `!=`, `<`, `>`, `<=`, `>=`) also resolve to
their canonical operation callables; their existing application-head syntax is
unchanged. Unknown lookup is `REF_MISSING`; a missing application-head operation
is `OP_UNKNOWN`, and a known non-callable head is `EXPR_INVALID`.

Operation callables are immutable, invocation-owned references. They may appear
in lazy definitions, lists, records, and local bindings, without capturing a
lexical scope. Resolving one does not invoke it or require capabilities. Calling
one, directly or through a collection callback, uses the caller's scope, phase,
grants and engine context, and preserves arity checks, operation-version
observations, dependency tracking, effects, diagnostics and host suspension.
They cannot be invoked through another program or serialized as persistent
cache values; ordinary closure publication restrictions remain unchanged.

Special forms consume expression syntax, not already-evaluated arguments:
`?`, `let`, `def`, `eval`, `if`, `and`, `or`, `with`, `match`, `run!`, `pipe!`, and
`await`. They are not registry operations or callable values. Referencing an
unbound special form as a value fails `EXPR_INVALID`; use a lambda to adapt its
syntax. Registry/plugin registration rejects these reserved operation names.
The built-in `kash` constructor remains an ordinary invocation-owned callable.

Contribution diagnostics follow the same syntax/registration/demand boundaries
as ordinary definitions: malformed headers, function-shaped `+=` headers and
Kash `+=` are parse errors; contributing to an existing function or redeclaring
a registered value fails `DEF_INVALID` during registration. Non-list operands
fail `EXPR_INVALID`, and cycles fail `DEP_CYCLE`, only when the aggregate is
demanded. Registration never evaluates operands merely to validate their kinds.

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

Source-composition forms are selected before final compilation, not evaluated
as statements. Conditions use a pure scope containing only earlier selected
definitions/contributions and explicit configured overrides, with environment
configuration subject to caller authority. They cannot use host reads, globs,
processes or effects, including indirectly through lazy definitions or helpers.
Later definitions remain invisible to selection, but retain normal forward lazy
visibility in the final runtime program. Inactive conditions are not demanded;
reserved include heads and composition body shapes are nevertheless validated
in every authored branch. See `004-language.md` for literal paths and placement.
Capability/effect failures explain the pure-condition restriction rather than
suggesting runtime grants that cannot authorize selection. Their primary location
is the authored condition. Configuration frame labels remain useful context,
but carry no source/span until the synthetic prefix and isolated predicate have
an authored origin map; runtime execution's normal source mapping is unchanged.

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

Unless an operation contract explicitly defers an operand, arguments evaluate
left-to-right for deterministic diagnostics and effects.
Independent dependency nodes discovered by those arguments may execute in
parallel.

### Glob expressions

A demanded unquoted path glob (`004-language.md`) evaluates through the same
tracked filesystem source as `wildcard`, producing a list even for zero or one
match. It uses evaluation cwd, read authorization, phase policy, and ordinary
host suspension. It is forbidden wherever host reads are forbidden, including
pure output registration and source-selection predicates. Parsing, unused
definitions, and unselected branches do not request a glob or acquire its edges.

The current node retains the pattern's membership dependency, including an
empty result. Invalidation and resumed evaluation observe the latest accepted
membership under `023-wildcard-sources.md`. Matching does not implicitly read
the returned files' contents. Strings, including computed strings and literal
interpolation values, are never retrospectively treated as glob expressions.

## Value Display

When a materialized value is written to stdout — a `do run` result or a
definition value selected as a target — it uses
one canonical display notation. For the representable subset described below,
this is also a valid Kame expression:

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

For representable values, evaluating the displayed text and displaying the
result again yields the same bytes. Kind preservation is a separate property:
an integral float re-evaluates as an integer, and a pattern-looking string may
re-evaluate as a pattern. Strings containing interpolation openers are also
outside the inert-string round-trip guarantee. Non-finite numbers have no
ordinary numeric literal. Record display writes keys as bare names, but runtime
records can have arbitrary string keys, including empty keys; those keys are
not necessarily expressible with `NAME:` syntax. Empty records likewise have no
distinct literal (`[]` is a list). The round-trip guarantee therefore applies
only to values whose displayed atoms and nested containers are representable;
it is not a general serialization contract. Use `json` for JSON-compatible
arbitrary-key data or typed API results to preserve runtime kinds.

Bytes are written raw and resources as their name; the grammar has no literal
for either, so they are outside the notation. This notation is distinct from the
`str` operation (`007-library.md`), which concatenates only textual scalars
(nil, booleans, numbers and strings), rejecting patterns and collections.
`json` explicitly encodes JSON-compatible values, including quoted strings.
Template rendering and effect output have their own conversion contracts;
neither is defined by `str`. `--json` emits structured values in its event schema instead of this
notation. Human `do cat` definition values use a different contract
(`009-cli.md`): text and bytes stay raw while every other JSON-native value
prints as plain JSON, so other programs can parse `cat` output directly.

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

Pattern `sub`/`replace` is an exception to eager operand evaluation
(`014-patterns.md`): evaluate
the match operand, retain the replacement expression without evaluating it,
and evaluate that expression in a capture child scope only after a successful
subject match. A two-argument call retains the expression and enclosing lexical
scope in its invocation-owned callable. Capture scopes, reached dependencies,
and accepted operands/effects follow ordinary suspension and cancellation
ownership; resumption must not replay a completed replacement or its effects.

This policy belongs to the selected built-in operation, including expression
calls through an operation alias; a user binding named `replace` still follows
normal lexical lookup. `replace` is not a reserved special form. Calls with
already-materialized arguments use constant or expansion-pattern values and
cannot retroactively defer earlier argument evaluation.

## Reactive Lifting

Ordinary operation authors consume materialized arguments. The evaluator lifts
an application over reactive inputs; explicitly deferred operands, such as a
pattern replacement expression, are demanded only when their contract selects
them:

1. Request every currently demanded argument node.
2. Wait until each demanded argument has a current value or terminal state.
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
- Composition conditions share `if` truthiness, see only earlier selected
  bindings, preserve override/environment authority and forbid indirect host
  effects; final runtime forward references remain lazy and valid.
- Invalidating a definition causes one reevaluation on next access.
- A definition cycle reports `DEP_CYCLE` with definition frames.
- Lexical functions capture their defining scope and bind rest arguments.
- Placeholder sections read their indexed arguments without name lookup; a
  definition named `_0` does not shadow a placeholder.
- Lexical names override operation names only within their scope.
- Arguments produce deterministic left-to-right diagnostics.
- Glob expressions retain read/phase checks, empty membership dependencies and
  lazy selection; multiple consumers share their source on native and WASM.
- Pattern replacement binds captures only while evaluating its selected
  replacement, including aliased calls, and retains scope and once-only effects
  across suspension without leaking bindings or replaying completed list items.
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
