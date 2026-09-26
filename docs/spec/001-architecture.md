# Architecture

## Purpose

LittleMake is a reactive orchestration engine with a build language layered on
top. The implementation must not make the engine depend on build files, shells,
or a particular host platform.

## Package Boundaries

The initial package layout is:

```text
core/           values, graph, scheduler, updates
lang/expr/      expression AST, parser, formatter
lang/template/  template AST, parser, matcher, renderer, formatter
lang/rule/      definition and rule AST, parser, formatter
lang/program/   script composition and evaluation
operations/     standard operations
host/posix/     native filesystem and process execution
cmd/littlemake/ CLI executable
```

`host/wasm` is added only when `010-wasm.md` begins. Packages named `utils` or
`tools` must not be created without at least two concrete consumers.

## Dependency Direction

Allowed dependencies flow downward:

```text
cmd -> program -> language + operations + core
program -> host contract
host/posix -> platform and C interop
language -> core values where evaluation requires them
core -> portable Solod standard library only
```

The host never owns graph policy. The engine never invokes POSIX directly.

If package cycles appear, move the smallest shared data contract downward. Do
not introduce an interface package containing speculative abstractions.

## Portable Boundary

Portable packages must build for `wasm32-freestanding`. They may use packages
such as `mem`, `slices`, `maps`, `strings`, `path`, and token-level JSON. They
must not transitively import hosted-only packages including `os`, `conc`,
`sync`, `flag`, or native C headers.

Host interaction is represented as requests and completions. Examples include
reading a file, expanding a glob, running a process, reading a clock, and
persisting a cache record. Native code may service a request immediately;
WebAssembly may return it to JavaScript and receive a completion later.

## Extension Boundary

Operations and host jobs use a named function pointer plus an opaque context
pointer. Solod has no closures, and `any` has no runtime type information, so:

- The function that created a context is responsible for interpreting it.
- Context pointers never cross serialization or plugin boundaries.
- Public language values are tagged values, not `any`.
- Interfaces are reserved for boundaries with multiple real implementations.

## Ownership

LittleMake uses four lifetimes:

1. Instance lifetime for operation registrations and global configuration.
2. Program lifetime for source text, ASTs, top-level definitions, and lexical
   functions compiled from that source.
3. Engine lifetime for graph nodes, interned resource keys, retained latest
   values, and subscriptions.
4. Run/message lifetime for call scopes, plans, command expansion, host requests,
   completions, and streamed events.

An instance and compiled program each receive an allocator and free all retained
objects as one ownership tree. Program ASTs are immutable and program-owned.
Top-level lexical functions reference program-owned AST and scope data. Call
scopes and expression-local closures use a run arena and may not be published as
engine values or retained after the run. A queued message owns its payload.

The instance and compiled program must outlive every engine node whose producer
references them. Destruction cancels roots and waits for native process-group
cleanup. Other outstanding host requests may be abandoned by transferring their
payload ownership to the host and invalidating their engine handles; a later
completion frees its own payload without accessing the engine. The runtime then
frees subscriptions and engine nodes, the program, and finally the instance.
Destroying a program with live nodes outside this sequence is `PHASE_INVALID`.

No string or slice backed by a stack allocation may be stored in a node, map,
queue, event, or returned result. Built-in fixed-capacity maps and slices are
only for function-local data with a statically known bound.

## Glossary

- **Current:** valid for dependency readiness in the active generation.
- **Retained:** kept for an existing observer but not necessarily current.
- **Phony task:** a bare named rule that runs whenever reached.
- **Cached task:** a rule written with `task` and governed by `008-cache.md`.
- **Rule instance:** one selected rule plus capture bindings, shared by all of
  its declared output resources.
- **Host request:** owned description of work outside portable packages.
- **Attempt:** one invocation or process run within a node generation.

## Errors and Diagnostics

Solod `error` values are sentinel control results. User-facing failures use a
structured diagnostic value containing:

- Stable code such as `PARSE_ERR` or `RECIPE_FAIL`.
- Severity.
- Owned message.
- Optional source span.
- Optional notes and suggestions.
- Optional target and evaluation frames.

Packages return a result plus sentinel error where required by Solod. The
structured diagnostic is part of the result or execution event and is not
encoded into dynamically allocated error implementations.

`011-diagnostics.md` is the authoritative diagnostic model, renderer contract,
and registry for codes and severities. Other specifications must use a
registered code for every normative failure. They add diagnostic context rather
than formatting user-facing error strings at their own layer.

## Determinism

Given the same program, resource state, operation results, and requested
targets, LittleMake must produce the same:

- Rule selection.
- Dependency order.
- Revision sequence per node.
- Fingerprints.
- Canonical formatted source.

Parallel completion timing may interleave events differently across independent
nodes. Events from one node and one process stream retain their original order.

## Deferred Boundaries

The initial architecture contains no abstraction for:

- Remote execution.
- General plugin loading.
- Multiple cache backends.
- Filesystem watching.
- Windows process management.
- Arbitrary resource URI protocols.

Add one only when its specification and second implementation exist.

## Acceptance Tests

- A portable dependency audit finds no hosted imports reachable from `core`,
  `lang/expr`, `lang/template`, or `lang/rule`.
- A test using `mem.Tracker` creates and frees an engine with nodes, values, and
  queued events without leaks.
- A queued event remains valid after the function that created it returns.
- Diagnostics can be formatted after their originating parser/evaluator stack
  has unwound.
- Two executions of the same fake graph produce identical node and revision
  order.
