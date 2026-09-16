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
- Diagnostic source and frame stack.

Operations receive this context explicitly. There is no thread-local or
async-local evaluation state.

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

## Values and Truth

Evaluation uses the value kinds from `002-engine.md`. Only false and nil are
false in predicates. Zero, empty strings, empty lists, and empty records are
true.

Arguments evaluate left-to-right for deterministic diagnostics and effects.
Independent dependency nodes discovered by those arguments may execute in
parallel.

## Functions

A lexical function stores parameter names, optional rest parameter, body AST,
and defining scope. Calling it creates a child scope, binds arguments, and
evaluates body expressions in order. The final expression is returned; an empty
body returns nil.

Too few or too many arguments are `EXPR_INVALID`. A rest argument receives a list,
including an empty list.

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
the newest complete argument set.

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
The initial implementation does not load filesystem paths or complete scripts.
Those capabilities may be added with explicit host requests after the native
vertical slice.

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
containment, which is outside the initial implementation.

## Acceptance Tests

- Top-level definitions are lazy and shared by two consumers.
- Invalidating a definition causes one reevaluation on next access.
- A definition cycle reports `DEP_CYCLE` with definition frames.
- Lexical functions capture their defining scope and bind rest arguments.
- Lexical names override operation names only within their scope.
- Arguments produce deterministic left-to-right diagnostics.
- A lifted operation reruns once with the newest arguments after coalesced
  updates.
- A stale operation completion is not published.
- Empty lists and strings are true; false and nil are false.
- `?` suppresses only unknown-reference failures.
- `let` bindings see previous bindings but do not leak into their parent.
- Reference indexes, negative indexes, slices, and selections work on valid
  values and diagnose invalid bounds.
- Rule and argument selectors choose the nearest correct frame.
- A filesystem read records a dependency on its canonical file resource.
- Denied operations create no host request.
- Evaluation teardown under `mem.Tracker` leaks no scope, closure, or value.
