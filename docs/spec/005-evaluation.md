---
revision: 2
---

# Language Evaluation

## Purpose

Evaluation turns language AST values into reactive core values. It is explicit,
allocator-aware, asynchronous through engine requests, and independent of
native threads or promises. The surface syntax is specified in
`055-unified-kame-kash-language.md` and `004-language.md`; this specification
defines how that syntax evaluates.

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
- Working directory, environment store, timeout and capture policy.
- Execution ownership, cancellation, and request generation/attempt correlation.
- Dynamic dependency and effect channels.
- Diagnostic source and frame stack.

Operations receive this context explicitly. There is no thread-local or
async-local evaluation state.

These fields define the execution context shared by Kame values, Kash actions,
and embedded `$(…)`/`$?(…)` process graphs (`017-kash.md`). Lexical scope
governs name lookup; it does not confer authority. Embedded processes inherit
the caller's policy, and nested configuration cannot broaden grants or relax
caller limits. Lazy definition nodes use their owning program's explicitly
configured policy, never an unrelated caller's broader grants. Unselected or
unused computations perform no host effects.

The working directory and environment store belong to the current process-like
unit (055 §5.3, §8.4): the script, a rule body, a `$(…)`/`$?(…)` capture, or an
interactive entry's session. A unit inherits both from its parent when it starts.
`cd` and `set env/NAME = EXPR` change them for later statements, lookups and
processes of the same unit, including after the function that performed the
change returns, and never reach the parent unit. Per-command `:env` and `:cwd`
overrides apply to one command and leave the store unchanged. Writing the store
is not a host environment mutation and needs no environment-write grant.

Parsing is context-independent: it never depends on grants, host availability,
bindings or execution phase. Context and phase decide whether a resolved
operation may run, and a denied operation fails before its effect. A demanded
process substitution requires both a phase permitting process execution and the
applicable run grant. Declaration loading, generation, resolution and rendering
fail with `PHASE_INVALID`; absent grants fail with `CAP_DENIED`; authorized but
unavailable host services fail with `HOST_FAIL`. Redirections and environment
reads require their own grants. Capability denial and cancellation are
protected failures: neither `??` nor Kash recovery operators recover them.
`??` recovers only missing-lookup failures (see "Fallback"); operational
failures remain errors (`049-process-inspection.md`).

## Phases

Evaluation runs in one of these phases. Each phase has its own authority, and
runtime grants cannot widen it:

| Phase | Permitted work |
| --- | --- |
| Declaration loading | Pure computation, tracked read-only discovery, declaration construction and registration |
| Tracked generation | As declaration loading, within one transactional `generate` statement (`025`) |
| Target selection | Pure computation over the registered declaration index |
| Dependency resolution | Pure computation and tracked reads needed to resolve inputs |
| Action execution | Processes, writes and other granted effects |
| Output publication | Publication of rule results under `044` |

- Pure computations may be memoized by their arguments.
- Tracked reads record dependencies on the current node.
- Process execution and writes are possible only in action execution.
- Template rendering, interpolation and `$(…)` reached during planning cannot
  launch processes; they fail with `PHASE_INVALID` before any effect.

The four contexts of `055` map onto these phases. Value context computes in
whichever phase demands it. Declaration context (the top level of a Kame
program and its top-level `if`/`for` blocks) runs during declaration loading or
generation. Action context (rule, task and service bodies, block `def` bodies,
the `.kash` top level, and `$(…)`/`$?(…)`) performs effects only during action
execution. A block-valued top-level definition (`NAME =` followed by a block)
takes the context of the phase that demands it: commands in it are context
errors unless it is evaluated during action execution.

### Named execution context

The ordinary names `inputs`, `outputs`, `changed-inputs`, and `args` expose typed
lists from the current evaluation context. Lexical bindings take precedence,
preserving user definitions and parameters with these names. They do not read
files or launch processes by themselves.

| Name | Value | Frame |
| --- | --- | --- |
| `inputs` | Normal prerequisites in declaration order, excluding order-only prerequisites | Nearest rule frame |
| `outputs` | Declared output identities, including a named task's logical output name, not just file artifacts | Nearest rule frame |
| `changed-inputs` | Unique normal file inputs whose content digest differs from the recorded digest | Nearest file-rule frame, at recipe evaluation/rendering time |
| `args` | The nearest function or Kash-call argument list, or the script invocation's literal operands outside a call | Nearest call frame, else the invocation |

`changed-inputs` is unavailable during planning/resolution and in task/service
frames. Unavailable context reports `SEL_NO_CONTEXT`, and changed-input access
in a disallowed phase reports `PHASE_INVALID`. Empty available lists are values,
not missing context. Build invocations without operands expose an empty `args`
list. Named target parameters remain their own bindings; they are not silently
converted into `args`. Pure expression evaluators without an argument frame
report `SEL_NO_CONTEXT` for `args`.

These names support ordinary field access such as `args.0`, `inputs.-1` and
`changed-inputs.0` with normal index diagnostics: indexes are zero-based,
negative indexes count from the end, and an index outside the list is
`SEL_INDEX_INVALID`. In Kash words they are referenced as `$inputs.0`,
`$...inputs` and `$...changed-inputs`; in foreign-shell recipes as
`@{inputs.0}` and `@{outputs.0}`. Lazy scoped definitions and suspended or
async evaluations retain the same frames.

## Output Registration Context

Computed rule outputs (`004-language.md`, 055 §10) are evaluated before target
selection in declaration context, like generated declarations. They may use
lexical definitions, pure operations/functions, configured overrides, and
tracked read-only discovery (`files PATTERN`); they cannot run processes,
perform writes or untracked reads, or issue any other host effect, even when
invocation grants would otherwise permit it. Restrictions propagate through
lazy definitions and helper calls. Unrelated unused definitions remain lazy.
Current-rule captures, target arguments, `inputs` and `outputs` are unavailable:
the target index must exist before those bindings can be established.
Output-resolution failures reject registration before recipe effects and retain
the authored output location.

## Truth and emptiness

Only `nil` and `false` are false. Integer/float zero and empty strings,
bytes, lists, and records are true. `if` expressions and statements, `elif`,
`while`, `bool`/`not`, and predicate filtering use this same truth contract.
`and`/`or` short-circuit and return the deciding operand without boolean
coercion. Declaration-time `if` conditions use this same truthiness. Process
exit status has a separate contract: a condition tests a command through its
outcome, as in `if $?(test -f ./ready).ok`.

Emptiness must be tested explicitly with `count` on strings, bytes, lists, or
records; zero count does not make the original value false. Missing optional
data must be checked for `nil` before counting it. Numeric zero comparisons are
not collection emptiness checks. Whitespace, nil-valued list items, and
nil-valued record fields still count as data.

Unless an operation or expression form explicitly defers an operand, operands
evaluate left to right for deterministic diagnostics and effects. Independent
dependency nodes discovered by those operands may execute in parallel.

## Scope and Name Resolution

A scope is a parent-linked map of names to bindings. A binding is a value,
lexical function, operation, or lazy definition. Namespaces (`x/`, `env/`,
`std/`, `module/` and import aliases) live in a separate table, so a binding
named `x` does not conflict with the `x/` namespace.

**Expression position** (value, declaration and template contexts, and value
operands in action context) resolves a name in this order:

1. Lexical bindings: block bindings, parameters, module definitions and
   imported names, searching the current scope and then its parents.
2. The standard library. `std/NAME` names a library operation explicitly and
   bypasses lexical bindings.
3. For an `ENV-NAME` only (`[A-Z][A-Z0-9_]*`): the environment store of the
   current unit, as a tracked, capability-checked read. Lowercase names never
   fall back to the environment.
4. Otherwise `REF_MISSING`.

An unset variable reached through step 3 fails with `ENV_VAR_MISSING`, so typos
are caught; write `NAME ?? default` to supply a default. `env/NAME` reads the
store directly and yields `nil` when unset; an empty variable yields an empty
string. Both forms require the environment-read grant, record the same named
dependency and retain phase restrictions. Parsing, formatting, unused
definitions and unselected branches do not read the variable. A lexical
binding with an uppercase name shadows the environment within its scope.

**Command position** (the head of a command statement in action context)
resolves lexical callable bindings, then the closed set of Kash builtins, then
executables through the tool resolver using the store's `PATH` (055 §6). A
lexical binding to a non-callable at a command head is `EXPR_INVALID`; it never
falls through to an executable. The standard library is not consulted in command
position. In value and declaration contexts the executable step is never
attempted, so an unknown command fails before any effect.

Registered operations are callable values, including plugin operations:
`map uppercase ["a", "b"]`, `filter exists? sources`, and `predicate = exists?`
need no lambda wrapper. Lexical names shadow operation names only within their
scope. Infix comparison operators (`==`, `!=`, `<`, `>`, `<=`, `>=`) evaluate
through the canonical predicates `eq?`, `ne?`, `lt?`, `gt?`, `lte?`, and `gte?`;
the operator symbols are syntax, not names, so pass the predicate name to use
one as a value. Applying a non-callable value is `EXPR_INVALID`.

Operation callables are immutable, invocation-owned references. They may appear
in lazy definitions, lists, records, and local bindings, without capturing a
lexical scope. Resolving one does not invoke it or require capabilities. Calling
one, directly or through a collection callback, uses the caller's scope, phase,
grants and engine context, and preserves arity checks, operation-version
observations, dependency tracking, effects, diagnostics and host suspension.
They cannot be invoked through another program or serialized as persistent
cache values; ordinary closure publication restrictions remain unchanged.

Expression forms that control evaluation of their operands are syntax, not
registry operations or callable values: `if … then … else`, `match`, `and`,
`or`, `??`, `|>`, lambdas, placeholder sections, `$(…)`, `$?(…)`, `eval`, and
the block statements `let`, `set`, `def`, `return`, `yield` and `exit`. The
process forms `run!`, `pipe!` and `await` likewise consume expression syntax
(`012-streams.md`, `017-kash.md`). Registry/plugin registration rejects these
reserved operation names. Adapting one of these forms to a callable uses a
lambda or section.

## Definitions

### Top-level definitions

At the top level of a Kame program, and in the module scope of a `.kash`
source:

```kame
version = "1.0"
sources = files ./src/**/*.c
def greeting name = "Hello @{name}!"
```

`NAME = EXPR` declares a **lazy, memoized**, order-independent value. First
access starts one engine node for the definition, and concurrent accesses
share it. A definition may refer to definitions that appear later in the
source. Definition cycles are reported as `DEP_CYCLE` with definition frames.
Invalidation clears the definition generation, not the definition AST.
`NAME =` followed by an indented block computes the lazy value from a block of
`let`, `set`, control statements and `return`; the block runs at first demand,
in the context of the demanding phase.

Top-level `NAME = EXPR` is not valid inside blocks. In a `.kash` source,
top-level statements form an action block and module definitions are written
with `def`.

Program functions and their defining top-level scopes are program-owned.
Call-local scopes and lambdas are run-owned and cannot be published as node
values or stored in cacheable containers. Attempting to return such a callable
across the run boundary is `EXPR_INVALID`.

### Contributions

`NAME += EXPR` contributes a list to a top-level value binding (`004-language.md`).
Contribution diagnostics follow the same syntax/registration/demand boundaries
as ordinary definitions: malformed headers, `def`-shaped contribution headers
and `+=` inside blocks or `.kash` sources are `PARSE_ERR`; contributing to an
existing function or redeclaring a registered value fails `DEF_INVALID` during
registration. Non-list operands fail `EXPR_INVALID`, and cycles fail
`DEP_CYCLE`, only when the aggregate is demanded. Registration never evaluates
operands merely to validate their kinds.

### Block bindings

Inside a block, `let NAME = EXPR` evaluates `EXPR` **eagerly** when the
statement is reached and binds the value immutably for the rest of the block. A
later `let` of the same name shadows the earlier binding from that point on;
it does not change values already computed from it. Each block has its own
lexical scope, and so does each loop iteration; block bindings never leak into
the enclosing scope.

`set NAME = EXPR` evaluates `EXPR` and assigns a `let` binding of an enclosing
block in the same invocation. `set` cannot create a binding: assigning a name
with no enclosing `let` in the same invocation is `PARSE_ERR`, as is assigning
a binding captured by a lambda or nested `def`. `set env/NAME = EXPR` writes the
environment store; the value must be a string, number or path, and `nil` removes
the variable. A statement is a binding only when it starts with `let` or `set`.

In pure expressions, local names are introduced with a block-bodied definition
or a nested `def`.

## Functions

A lexical function stores parameter names, optional rest parameter, body, and
defining scope. Calling it creates a child scope of the defining scope and binds
arguments.

- `def NAME PARAM… = EXPR` and a lambda `(PARAM… => EXPR)` evaluate their body
  expression and return its value.
- `def NAME PARAM…` with an indented block runs the block in action context.
  `return EXPR` ends the invocation with that value; `return` alone and falling
  off the end return `nil`. There is no implicit last-statement value, and
  functions never return integer statuses.
- A `def` whose body contains `yield` is a generator: calling it returns a lazy
  `Stream`, and its body runs as the consumer pulls elements (055 §8.3).

A nested `def` and a lambda close over their lexical scope. Too many arguments
are `EXPR_INVALID`. Its primary span is the function's parameter list when that
list is authored in the source being evaluated, otherwise the call. Missing
arguments are dynamic: every declared parameter without a supplied argument
binds `nil`. A rest parameter (`rest...`) receives a list, including an empty
list. A parameter name repeated in one parameter list is `DEF_INVALID`, and so
are invalid names and malformed parameter lists. Zero-parameter lambdas are
written `(() => EXPR)` and called with `f ()`.

Placeholder sections (`014-patterns.md`) lower to ordinary lexical functions. A
placeholder atom evaluates by reading its indexed argument from the enclosing
section call without name lookup; a binding named `_0` cannot shadow it. A
missing section argument reads as `nil`. Section arity and argument-count
diagnostics follow the ordinary function rules.

Top-level, nested and block-bodied `def`, lambdas and sections share one
callable representation.

## Invocation Outcomes

Every invocation, of a function or of a process, has an outcome record with the
fields `state`, `ok`, `status`, `signal`, `value`, `error` and `stages`
(055 §8.1). An application `f a b` in expression position yields the return
value and raises on failure. `$(CMD)` yields the process graph's captured stdout
and raises on failure. `$?(CMD)` yields the outcome record and never raises for
a process or function failure; protected failures (capability denial,
cancellation of the caller) still propagate. A typed return value is never
converted to stdout: `$(f x)` captures what `f` writes, not what it returns.

When a function's status is needed, as a pipeline stage, a recipe or a script
entry point, it is derived: `:completed` is 0; `:failed` is the failing
process's status when a process failure caused it, otherwise 1; `:cancelled` is
treated as a broken pipe and is not a pipeline failure. A cancelled generator
runs its `defer` cleanup. `exit N` ends the innermost process-like unit with
status `N` after running the pending cleanup of every invocation it unwinds.

## Program Execution

### Kame programs

A Kame program (`.km`, `.kame`) holds top-level definitions, imports, includes,
contributions and declarations. Register definitions before demanding selected
work, so selections may refer to later definitions. A run selects declared
targets or definitions by name (`009-cli.md`) and demands only what they reach;
unselected definitions stay lazy. The `default` convention, empty-program
behavior, and CLI result presentation are defined in `009-cli.md`.

Every selected definition, target and inline expression uses the owning
program's argument frame, cwd, environment store, grants, generation
correlation, and dependency/effect channels. A failure stops the selected
sequence; output already committed is not rolled back. Resumption must not
replay an accepted earlier selection or consume another selection's host
completion. Named entries share one engine and lazy dependency nodes.

A Kash program (`.kash`, `.ksh`) runs its top level as an action block in
statement order, with module definitions registered first.

### Declaration-time control flow

Top-level `if`/`elif`/`else` and `for` blocks in a Kame program run in
declaration context during declaration loading (055 §10.3). Their conditions
and iterated values are evaluated in a scope containing the definitions and
contributions registered earlier in source order, including those of active
earlier includes, and explicit configured overrides; environment reads are
subject to caller authority. They may use pure computation and tracked
read-only discovery, but not processes, writes or untracked reads, including
indirectly through lazy definitions or helpers. Later definitions remain
invisible to these conditions but retain normal forward lazy visibility in the
final program.

Inactive branches register nothing and are not demanded; their includes are not
read, tracked or cycle-checked. Every authored branch is still parsed and its
`include` operands and declaration shapes validated. Registration order follows
source order and iteration order, and each declaration closes over its own
iteration scope. Capability/effect failures explain the declaration-context
restriction rather than suggesting runtime grants that cannot authorize it.
Their primary location is the authored condition. Configuration frame labels
remain useful context but carry no source span until they have an authored
origin map; runtime execution's normal source mapping is unchanged.

### Composed sessions

The runner can load multiple sources and inline expression fragments in
command-line order. Parse all inputs and register their shared top-level scope
before any demanded work executes. Definitions-only fragments contribute
bindings without demanding a default when later work is present. Source order
sequences inline expressions and target checkpoints, not lazy definition
registration or visibility.

All fragments use one engine and owning program context. An expression
appended after a Kame or Kash file can demand that file's definitions with the
same policy and host services. A language boundary does not reset cwd, the
environment store, args, invocation limits, or grants; changing parser cannot
broaden authority. The first source establishes the default policy, and
explicit grants configure the session as a whole. Pure value sessions do not
implicitly gain process authority by appending a Kash file.

Compile-time failure in any fragment prevents the sequence from starting.
Runtime failure stops later fragments without rolling back earlier effects.
Resume suspended work using fragment identity and authored spans in addition to
invocation/callback correlation; identical inline text in distinct fragments is
not the same host call. Completed fragments do not replay when later work resumes.

The unified runner is a host entry point, not a new evaluator. Single-expression
evaluation uses the same expression parser and evaluation context directly.

## Discovery and Path Values

Path atoms evaluate to typed values (`014-patterns.md`): a `Path`, a
`PathPattern`, or a `PathTemplate`. Evaluating a path atom never reads the
filesystem; a `PathPattern` such as `./src/**/*.c` is a value and does not
enumerate files. `@{…}` interpolations inside a path atom evaluate, left to
right, when the atom is evaluated.

`files PATTERN` is the discovery operation. It evaluates through the shared
tracked filesystem source of `wildcard`, returning a sorted, duplicate-free
`List[Path]` even for zero or one match. It uses the root directory (or `-C`),
read authorization, phase policy, and ordinary host suspension. It is forbidden
wherever host reads are forbidden. Parsing, unused definitions, and unselected
branches do not request discovery or acquire its edges.

The current node retains the pattern's membership dependency, including an
empty result. Invalidation and resumed evaluation observe the latest accepted
membership under `023-wildcard-sources.md`. Matching does not implicitly read
the returned files' contents. Strings are never reinterpreted as patterns.

In declaration context the result is a snapshot taken at planning time; a change
in membership invalidates the declarations that consumed it. A declaration-time
discovery pattern that overlaps any declared output is rejected with
`DISCOVERY_OVERLAP`, naming both the pattern and the overlapping output
declaration. As a rule input, a wildcard path keeps the dynamic membership
semantics of `023`.

## Value Display

When a materialized value is written to stdout (a run result or a definition
value selected as a target) it uses the canonical source notation of `fmt`
(055 §13). For representable values this is a valid expression:

| Kind | Notation |
| --- | --- |
| nil | `nil` |
| boolean | `true` / `false` |
| integer | decimal digits, with a leading `-` when negative |
| float | shortest `%g`; negative zero is written `0` |
| string | double-quoted, with `\"`, `\\`, `\n`, `\r`, and `\t` escapes and `@` written `@@` |
| symbol | `:name` |
| path, path pattern, path template | the canonical path atom, or `pattern "TEXT"` for rootless patterns |
| list | `[ITEM, ...]` |
| record | `{KEY: VALUE, ...}`; keys that are not names are quoted; `{}` when empty |

For representable values, evaluating the displayed text and displaying the
result again yields the same bytes, and value kinds are preserved: paths stay
paths and patterns stay patterns. An integral float re-evaluates as an integer.
Non-finite numbers have no numeric literal and are outside the round-trip
guarantee. Use `json` for JSON-compatible data or typed API results.

Bytes are written raw and resources as their name; the grammar has no literal
for either, so they are outside the notation. Runtime-only values (callables,
processes, handles, streams, rules) have no notation; `fmt` rejects them with
`EXPR_INVALID`. This notation is distinct from the `str` operation
(`007-library.md`), which concatenates only textual scalars (nil, booleans,
numbers and strings), rejecting patterns and collections. `json` explicitly
encodes JSON-compatible values, including quoted strings. Template rendering and
effect output have their own conversion contracts; neither is defined by `str`.
`--json` emits structured values in its event schema instead of this notation.
Human `do cat` definition values use a different contract (`009-cli.md`): text
and bytes stay raw while every other JSON-native value prints as plain JSON, so
other programs can parse `cat` output directly.

## Applications and Operations

`f a b` evaluates the head, then the arguments left to right, then applies.
`f ()` applies with zero arguments. `f ...xs` evaluates `xs` once at that
position, requires a list, and passes its elements as separate arguments.

An operation registry entry contains:

- Name.
- Named call function and opaque context.
- Minimum and maximum arity, or variadic marker.
- Capability requirements.
- Stable implementation version used by cached tasks.

The evaluator validates arity and capability before calling an operation.
Operation failures attach the current expression and call frames.

Pattern `subst` is an exception to eager operand evaluation (`014-patterns.md`,
`041-pattern-predicates-and-substitution.md`): evaluate the match operand,
retain the replacement expression without evaluating it, and evaluate that
expression in a capture child scope only after a successful subject match. A
two-argument call retains the expression and enclosing lexical scope in its
invocation-owned callable. Capture scopes, reached dependencies, and accepted
operands/effects follow ordinary suspension and cancellation ownership;
resumption must not replay a completed replacement or its effects.

This policy belongs to the built-in operation, whether it is reached by its
name or as `std/subst`; a user binding named `subst` follows normal lexical lookup.
`subst` is not a reserved form. Calls with already-materialized arguments use
constant or path-template values and cannot retroactively defer earlier
argument evaluation.

### Value pipelines

`VALUE |> STAGE` evaluates `VALUE` exactly once, then applies the stage. If the
stage's top level contains `_`, each `_` reads that value; otherwise the value
is appended as the last argument, so `xs |> map f` evaluates as `map f xs`.
Lowering a pipe never changes evaluation order or multiplicity, and special
forms in a stage keep their own evaluation control. `|>` never connects process
streams, and `|` never transforms values.

### Scoped finalization

[`finally!`](043-kame-finalization.md) uses ordinary callable operands but owns a
runtime execution scope. Register its cleanup before invoking the body; retain
the scope through failure, graceful cancellation, host suspension and bounded
cleanup. Scope completion and old-generation teardown precede result publication
or reactive replacement. Allocator destruction alone cannot run finalizers.
Cleanup retains the caller's authority and does not recover a body failure;
cancellation and capability denial remain protected even when the primary
diagnostic predates them. Deferred-effect-only and non-execution contexts reject
the operation before invoking callbacks. Its supported execution effect channel
must settle callback effects under 043 without changing ordinary recipe timing.

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

## Expression Forms

### Fallback

`A ?? B` evaluates `A`. It yields `A` unless `A` is `nil` or fails with a
missing-lookup failure: `REF_MISSING`, `TOOL_MISSING`, `ENV_VAR_MISSING`, or a
missing record field. In those cases it evaluates and yields `B`; otherwise `B`
is never evaluated. `??` is right-associative, so `a ?? b ?? c` tries each
operand in turn. No other failure is suppressed: protected failures
(capability denial, cancellation) and operational failures propagate. `false`
is a successful value and is not replaced. Optional tool lookup retains its
tracked resolver observations.

### If

`if C then A elif D then B else E` evaluates conditions left to right and
evaluates only the arm of the first true condition, or the `else` arm, and
yields its value. The expression form always has an `else` arm. Exactly one arm
evaluates, so an arm may reference a name that is valid only when selected.
Truth follows "Truth and emptiness".

The statement form (`if`/`elif`/`else` blocks) runs the block of the first true
condition, or the `else` block, or nothing.

### And and Or

`A and B` evaluates `A`; if it is false, the result is `A` and `B` is never
evaluated; otherwise the result is `B`. `A or B` evaluates `A`; if it is true,
the result is `A`; otherwise the result is `B`. Chains such as `a and b and c`
associate left and stop at the deciding operand, so later operands are never
evaluated and their dependencies and effects do not run.

### Match

`match SUBJECT` evaluates `SUBJECT` once and dispatches it against ordered
`PATTERN => EXPR` arms:

```kame
def object-path source = match source
	./src/{path:**}.c => ./build/@{path}.o
	./gen/{name}.c    => ./build/gen/@{name}.o
	else              => fail "not a source: @{source}"
```

The subject must be a string, a `Path`, or a literal value (number, boolean,
symbol, `nil`); a `Bytes` subject is `EXPR_INVALID` and must be converted with
`text` first. Arm patterns are parsed at parse time and take the matcher role:

- A path atom with groups or glob syntax matches the subject's text with the
  anchored semantics of `014-patterns.md`; `{name}` captures as `{name:*}`.
- A path atom without groups or glob syntax, a string, or another literal
  matches by equality of kind and text or value.

The first matching arm is selected, and only its expression evaluates, in a
run-owned child scope where each named capture binds as a string, with the
enclosing scope as parent. Anonymous `{*}`, `{**}` and glob wildcards match but
bind no name, and a reused capture name must match the same text. `else => EXPR`
is optional and must be the last arm; when no arm matches and there is no
`else`, the result is `nil`. A malformed arm is `PARSE_ERR`; pattern misuse,
such as an arm containing a positional reference `{_N}`, is `PAT_INVALID`.

The statement form `match EXPR` in a block uses `PATTERN =>` arms followed by
indented blocks, with the same selection and capture scoping.

### Eval

`eval TEXT` parses `TEXT` as one expression and evaluates it in value context in
the current lexical scope and phase. It does not parse statements, declarations
or commands, load filesystem paths, or widen authority. No other operation parses
generated text as Kame or Kash source.

### Record scopes

Template `@with RECORD` (`016-templates.md`) evaluates `RECORD`, requires a
record, creates a run-owned child scope whose parent is the caller, binds each
field key as a name, and renders its block in that scope. A duplicate key
re-binds, so the last field wins. An empty block renders nothing. Capture
bindings and record-scope bindings cannot escape the run that created them,
consistent with lexical scopes.

## Containers and References

List values evaluate their elements left to right. Record values evaluate
field values left to right and retain source key order for formatting; lookup
does not depend on order. A shorthand field `name` evaluates the name `name`.

For a list spread `[a, ...xs]`, evaluate its operand once at that entry's
position, require a list, and append its elements without recursively
flattening them. Empty lists append nothing; nil, strings, bytes, records, and
other non-lists fail `EXPR_INVALID` at the spread operand. Nil and container
elements inside the spread list remain ordinary elements. A record spread
`{...r, k: v}` evaluates its operand once, requires a record, and inserts its
fields at that position; a later field with the same key replaces the earlier
value. A failing entry prevents later entries from being evaluated.

Spread uses the ordinary scope, authority, phase, and dependency channels. It
does not reinterpret elements as source, paths, patterns, or argv. Unused
definitions and unselected branches remain lazy. Across suspension, retain
completed entries and operand evaluation state so a spread never repeats an
accepted effect or duplicates inserted elements. Result ownership follows
ordinary values; spreading does not invalidate or mutate the operand.

Field access components resolve from left to right:

- `r.name` and `r."key"` access a record key.
- `xs.N` accesses a list element or string code point; negative indexes count
  from the end.

Slices use the `slice` operation and computed keys use `get` (`007-library.md`).
Missing keys and invalid type access are `REF_MISSING`, a missing-lookup failure
that `??` recovers; invalid indexes are `SEL_INDEX_INVALID`.

## Interpolation

`@{EXPR}` segments in strings, path atoms, templates and foreign-shell recipes
evaluate in source order, in value context, and concatenate into an owned
string or path. Scalars and paths render as text. Lists, records and other
non-scalars are `EXPR_INVALID` unless an explicit operation (`join`, `json`,
`fmt`) renders them. `nil` renders as an empty string. Interpolation never
applies escaping or quoting implicitly. Rule-context names resolve against the
nearest rule frame and `args` against the nearest call frame.

## Dynamic Dependencies

Filesystem and resource operations register dependencies through the evaluation
context before returning their result. Dependency registration uses the current
node and generation. The engine may suspend evaluation until the dependency is
current. Environment reads through `env/NAME` or an uppercase name register a
named environment dependency.

Pure text, collection, and path operations, including pattern matching,
`capture` and `bind`, do not create resource edges.

## Capabilities

Capability names are `read`, `write`, `run`, and `env`. A grant is unrestricted
or contains allowed canonical roots/names. Authorization occurs before any host
request. Denial is `CAP_DENIED` and has no side effect.

Path authorization resolves relative paths against the root directory, or the
unit's working directory for process-relative operations, and rejects traversal
outside granted roots after normalization. This check is lexical and is not a
filesystem sandbox: symlinks may resolve outside an allowed root. Untrusted
build execution requires host-level containment, which is outside the language
capability model.

## Acceptance Tests

- Top-level definitions are lazy, order-independent and shared by two consumers.
- `let` evaluates eagerly when reached; shadowing does not change earlier
  derived values; bindings do not leak into the parent block.
- `set` assigns an existing, uncaptured `let` in the same invocation; assigning
  an unknown or captured binding is `PARSE_ERR`.
- `set env/NAME` and `cd` are visible to later statements, lookups and
  processes of the same unit, including after the setting function returns, and
  never reach the parent unit; `set env/NAME = nil` unsets.
- Declaration-time conditions share `if` truthiness, see only earlier
  registered bindings, preserve override/environment authority, allow tracked
  discovery and forbid processes and writes; inactive branches read no includes;
  final runtime forward references remain lazy and valid.
- Invalidating a definition causes one reevaluation on next access.
- A definition cycle reports `DEP_CYCLE` with definition frames.
- Lexical functions capture their defining scope and bind rest arguments;
  block-bodied functions return `nil` when falling off the end.
- Placeholder sections read their indexed arguments without name lookup; a
  definition named `_0` does not shadow a placeholder.
- Expression position resolves lexical, then standard library, then uppercase
  environment names; lowercase names never read the environment; an unset bare
  `ENV-NAME` is `ENV_VAR_MISSING`, and `env/NAME` is `nil` when unset.
- Lexical names override operation names only within their scope; `std/NAME`
  bypasses them.
- Value and declaration contexts never launch an unknown command.
- Arguments produce deterministic left-to-right diagnostics.
- List and record spreads preserve entry order, empty lists, nil members, and
  nested containers without mutating their operands. Wrong-kind operands fail
  only when demanded, stop later entries, and preserve ordinary
  phase/capability checks. Suspended spread operands resume without repeated
  effects or duplicated items.
- Path atoms never enumerate files; `files` retains read/phase checks, empty
  membership dependencies and lazy selection; multiple consumers share their
  source on native and WASM; declaration-time discovery overlapping a declared
  output is `DISCOVERY_OVERLAP`.
- Pattern `subst` binds captures only while evaluating its selected replacement,
  including calls through `std/subst`, and retains scope and once-only effects
  across suspension without leaking bindings or replaying completed list items.
- `xs |> f a` evaluates `xs` once and equals `f a xs`; a stage with `_` places
  the value there.
- A lifted operation reruns once with the newest arguments after coalesced
  updates.
- A stale operation completion is not published.
- Empty lists and strings are true; `false` and `nil` are false.
- `??` recovers `nil` and missing-lookup failures only, preserves successful
  values including `false`, never evaluates its right operand otherwise, and
  propagates capability denial and cancellation.
- `if` evaluates exactly one arm.
- `and` and `or` short-circuit and return the deciding operand; an unselected
  operand's failing `read` never runs.
- `match` selects the first matching arm, binds named captures in a run-owned
  child scope, returns `nil` on no match without `else`, and never evaluates an
  unselected arm.
- `@with` binds record fields in a child scope, last duplicate wins, and the
  bindings do not leak into the parent.
- `$(…)` raises on failure; `$?(…)` returns an outcome with the same fields for
  functions and processes; derived function statuses apply in pipelines.
- Field access, negative indexes, and missing fields behave as specified, and
  invalid indexes are diagnosed.
- Rule-context names and `args` choose the nearest correct frame.
- A filesystem read records a dependency on its canonical file resource.
- Denied operations create no host request.
- The value display of nil, booleans, numbers, strings, symbols, paths,
  patterns, lists, and records re-parses as an expression, displays identically
  and preserves kinds; nested values use the same notation as top-level values.
- `eval` evaluates one expression and rejects statements and unauthorized
  effects.
- Evaluation teardown under `mem.Tracker` leaks no scope, closure, or value.
