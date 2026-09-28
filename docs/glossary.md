# Glossary

This glossary names recurring concepts in Kame's source and specifications. It
is grouped by domain, from authored source through execution, rather than
alphabetically.

## Source and language

### Source and span

A **source** is named UTF-8 text being parsed, such as a `Makefile.kmk` or an
inline `-c` command. A **span** is a zero-based, half-open byte range within one
source. AST nodes and diagnostics retain spans so Kame can point back to authored
text. A span is not a display line and column; those are derived when a
diagnostic is rendered.

### Token and atom

A **token** is a source-level unit recognized by a parser, such as a name, path,
number, delimiter, or expansion prefix. In the language grammar, an **atom** is
one indivisible expression spelling, such as a name, number, path, symbol, or
quoted string. This is distinct from a stream **atom**, which is a published
runtime value.

### AST

An **AST** (abstract syntax tree) is the structured representation produced by a
parser. Kame has separate ASTs and parsers for expressions, string templates,
target templates, definitions, rules, and scripts. Parsed program ASTs are
immutable and belong to the compiled program.

### Name, symbol, and path

A **name** is an identifier such as `build` or `sources`; it can refer to a
definition, operation, or named target depending on context. A **symbol** starts
with `:` and evaluates to its literal name (with `:true`, `:false`, and `:nil`
being special values). A **path** is a filesystem spelling that explicitly starts
with `/`, `./`, or `../`. In particular, `out/file` is not a path in source until
written as `./out/file`.

### Expression and value

An **expression** is language syntax that evaluates to a **value**. Values are
immutable tagged runtime data: nil, boolean, number, string, bytes, list, record,
callable, resource reference, or pattern. A callable is valid while it is being
evaluated, but cannot be published or cached as an ordinary value.

### Definition

A **definition** is a top-level named value or function declaration, for example
`sources = (wildcard ./src/*.c)`. Definitions are lazy: the first use evaluates a
definition node, and concurrent consumers share that work. A definition has its
own resource namespace, so it can have the same spelling as a named rule.

### Scope, binding, and lexical function

A **scope** is a parent-linked mapping from names to **bindings**. A binding can
hold a value, operation, lazy definition, or **lexical function**. A lexical
function retains its defining scope, then evaluates with a child scope containing
its arguments. Function definitions, lambdas, and placeholder sections use this
same callable model.

### Reference

A **reference** starts with a name and follows components such as record keys,
list indexes, slices, or selections: `project.name`, `files.0`, or
`config.{host,port}`. References operate on already evaluated values; they are
not target dependencies by themselves.

### Control form

A **control form** is a special form whose operands are evaluated only when
selected. The language provides `?` (unknown-reference fallback), `if` (ordered
test/then branches with an optional else), `and`/`or` (lazy conjunction and
disjunction), `match` (pattern dispatch with capture binding), `let`, `def`, and
`with`. A control form is not an eager operation: an unselected branch's
dependencies and effects never run.

### Template and selector

A **string template** is text containing literal segments and Kame expansions.
`@(expression)` evaluates an expression, including a reference expression. A
**selector** is an expansion whose value comes from the nearest rule or function
context: `@<` is the first rule input, `@>` the first output, and `@_` the first
function argument. Templates are rendered; they are not shell syntax.

### Target template and capture

A **target template** is a rule target with capture groups, such as
`./build/{name:*}.o`. It matches a requested target and yields named **captures**
such as `name=main`. The selected captures render the rule's other outputs,
inputs, and recipe. A target template is related to, but different from, an
expression pattern: `{name}` is a target capture but an expression-pattern
reference.

### Placeholder section

A **placeholder section** is a short lexical function written around one
expression with placeholders, such as `((replace _ ".c" ".o"))`. `_`, `__`,
and `_2` denote positional arguments. A placeholder only has this special meaning
inside a section; elsewhere it remains an ordinary name.

### Pattern

A **pattern** is a first-class expression value parsed from a path or plain
string with brace groups. Match groups, such as `{name:*}` and `{**}`, capture
text; expansion groups, such as `{name}` and `{_0}`, insert a prior capture.
Patterns power the match-and-expand form of `replace`. They are not regular
expressions and do not reclassify runtime strings containing braces.

## Build declarations and planning

### Script and include

A **script** is one composed Kame build source containing comments, definitions,
rules, and top-level expressions. `include PATH` composes file-backed scripts at
the inclusion point; included declarations share the surrounding scope and source
order.

### Rule

A **rule** declares outputs, inputs, and an optional indented recipe. Kame
classifies rules by their output syntax:

- A **file rule** has explicit path outputs and produces filesystem artifacts.
- A **phony task** (or bare task) has one bare-name output and runs whenever it
  is reached.
- A **cached task** is prefixed with `task` and may reuse a successful cached
  execution.
- A **service** is prefixed with `service` and represents a long-running action.

`target` is often used generically in user-facing commands, but internally the
rule kind determines whether it identifies a file, task, cached task, or service.

### Target, output, input, and artifact

A **target** is the thing requested from a program, such as `build` or
`./build/app`. A rule's declared **outputs** are the targets or paths it claims
to produce; its **inputs** are the declared prerequisites. A file **artifact** is
the resulting filesystem object, not the target declaration itself. A bare input
name is a symbolic resource; an explicit path input without a producing rule must
already exist.

### Recipe

A **recipe** is the ordered set of indented body lines belonging to a rule. Kame
renders its templates, joins its nonempty command lines into one shell script,
and sends that script to the host. Kame does not parse the shell language inside
a recipe.

### Rule selection and rule instance

**Rule selection** finds the literal or one unambiguous matching template rule
for a requested target. A **rule instance** is that selected declaration plus its
complete capture bindings. All outputs rendered by one instance map to the same
runtime node, so requests for sibling outputs share one execution.

### Plan, planning, and expansion

A **plan** records a requested target, its resource key, selected rule, captures,
rendered outputs, declared inputs, source span, and freshness status. The
**planning** phase evaluates only what is needed to establish inputs and outputs;
it neither renders recipe bodies nor executes commands. Plan expansion can resolve
eligible expression inputs for inspection without running generated rules. Its
freshness stays **unknown** when body evaluation could discover more dependencies.

### Freshness

**Freshness** is the file-rule decision to skip or execute a recipe. A file rule
is fresh only when all outputs and relevant file inputs exist and its oldest
output is at least as new as its newest input. A file rule with no inputs is
stale. Phony tasks are always stale; cached tasks use fingerprints rather than
file modification times.

### Materialize

To **materialize** a target is to perform all required phases—planning,
scheduling, rendering, and execution—until that target is current, complete, or
fails. Materializing a file target returns its path and status, rather than its
file contents.

## Reactive engine

### Resource key

A **resource key** is the internal identity of work: a resource kind plus a
canonical name. Kinds include definition, target, file, task, service, glob, and
environment. Identical canonical keys identify one node in an engine, while
different kinds can safely use the same name.

### Node

A **node** is the engine's lazy, shareable unit of computation for one resource
key. It owns graph edges, state, the latest published value, revision, generation,
attempt, and any terminal diagnostic. Nodes do not directly perform filesystem or
process work.

### Allocator, arena, ownership, and lifetime

An **allocator** supplies storage for an explicitly owned object graph. Kame
distinguishes instance, program, engine, and run/message **lifetimes** so values
cannot outlive the storage or source data they borrow. **Ownership** determines
which layer frees a value or payload; values crossing a lifetime boundary are
cloned or transferred. A WebAssembly **arena** is the fixed allocator used for an
instance and can release its logical allocations together on reset.

### Producer

A **producer** is the callback that advances a node. It uses an engine context to
publish values, request dependencies, attach a stream source, or submit host work.
It never mutates a node directly. Program runtime producers implement rule
execution; evaluator producers implement definitions and reactive expressions.

### Static and dynamic dependencies

A **dependency edge** says that one node needs another resource to be current.
**Static dependencies** are registered before a node runs. **Dynamic dependencies**
are discovered while evaluation or recipe rendering runs, for
example by `read`, `wildcard`, `env`, or a resolved tool reference. Dynamic edges
belong to one node generation and are replaced on reevaluation.

### Root and interest

A **root** represents one caller's active request for a node. A root contributes
**interest** to that node and recursively to its dependencies. The engine shares
in-flight work for nodes with multiple interested roots; when no root or
subscriber remains interested, active work can be cancelled.

### Current and retained

A node's latest value is **current** when it satisfies dependency readiness for
its active generation. A value may be **retained** for existing observers after
invalidation, but retained stale values do not satisfy new dependencies or reach
new subscribers. This distinction is why a value can remain observable without
being usable for the next build step.

### Revision, generation, and attempt

A **revision** increments each time a node publishes a value. A **generation**
identifies one run of a node and increments on invalidation or cancellation. An
**attempt** counts producer invocations within a generation. Host completions
carry all three identities so late results from a prior generation or attempt are
ignored.

### Invalidation and completion

**Invalidation** marks a node's current result stale, clears generation-owned
dynamic edges, and starts a replacement generation when demand remains. A
**completion** is the host result for submitted work; it is accepted only if its
node ID, generation, attempt, and request ID match the active invocation.

### Subscription and coalescing

A **subscription** observes a node's latest value and future updates. It is not a
replay of every historical value. When a bounded subscription queue fills, the
engine **coalesces** intermediate updates while preserving an oldest pending
value, a newest value, and terminal state.

## Streams and execution

### Source and materializer

In the stream protocol, a **source** is a resumable producer of protocol atoms;
it is unrelated to authored source text. A **materializer** owns a source while it
converts the protocol into published immutable values. A source can wait on a
dependency or host completion without blocking the whole engine.

### Stream atom, chunk, batch, and collection

A stream **atom** immediately publishes one materialized value. A **chunk** is
one item accumulated into the current **batch**; ending a batch publishes one
immutable list, including an empty list. A **collection** is a nested list being
assembled inside a batch. Raw stream atoms are internal protocol events and are
never sent to node subscribers.

### Phase

The program runtime uses the phases **compile → plan → schedule → render →
execute**. An operation may be valid only in particular phases: planning cannot
perform effects, rendering records effects without committing them, and execution
commits the accepted render's effects.

### Effect and yield

An **effect** is a deferred action recorded while rendering: `out` and `err`
write to the corresponding runtime streams, and `write` can write a file.
**Yield** is the declarative effect that appends content for exactly one file-rule
output. Yield cannot share an accepted render with a nonempty shell command,
because Kame must have one unambiguous owner for that output.

### Host request

A **host request** is the portable description of work that only an embedding
environment can do, such as filesystem access, environment lookup, process
execution, clock reads, or cache persistence. The host returns a completion; the
engine, rather than the host, retains graph policy and validates it.

### Capability and grant

A **capability** is permission to perform host-backed work: `read`, `write`,
`run`, or `env`. A **grant** is the configured unrestricted permission or the set
of allowed canonical roots/names for a capability. Authorization is checked before
the host request, so denial (`CAP_DENIED`) has no side effect.

### Tool reference

A **tool reference** is `@(x/NAME)` in a recipe. Kame resolves the named
executable from the build driver's startup `PATH` before execution, renders its
resolved path, and records that executable file as a dynamic dependency. An
ordinary shell command name is not a tool reference and remains shell-resolved.

## Cache and portable ABI

### Cache identity, fingerprint, and manifest

A cached task's **identity** consists of its canonical target, selected captures,
and rule declaration. Its **fingerprint** is the canonical hashable encoding of
that identity plus declared and dynamic dependencies, rule text, operation
versions, resolved tools, and execution settings. The **manifest** is the
recorded dependency data used to recompute the fingerprint. A successful matching
fingerprint is a cache hit; a malformed or uncacheable record is a miss, normally
with `CACHE_UNUSABLE` as a warning.

### Handle and slot

In the WebAssembly ABI, a **handle** is an opaque integer for an instance,
request, root, or other exported object. It encodes a handle-table **slot** and a
generation. Reusing a freed slot changes its generation, so stale or foreign
handles can be rejected without exposing native pointers.

### ABI event and payload pinning

An **ABI event** is the versioned WebAssembly-facing envelope for runtime
activity, carrying identifiers such as root, node, request, generation, and
revision. Querying an event's payload length **pins** that queued event until its
payload is copied or discarded, preventing a later step from replacing the
caller-visible payload.

## Diagnostics

### Diagnostic, code, frame, and cause

A **diagnostic** is Kame's structured user-facing failure or warning. Its stable
**code** identifies the category (for example `TGT_NO_RULE` or `CAP_DENIED`), and
its primary span identifies where the failure occurred. A **frame** records an
evaluation, definition, rule, or script boundary crossed while the diagnostic
propagated; it is context, not a second error location. A **cause** preserves the
bounded underlying host or process outcome without replacing Kame's diagnosis.

For normative detail, see the specifications in `docs/spec/`, especially
`001-architecture.md`, `002-engine.md`, `004-language.md`, `005-evaluation.md`,
`006-runtime.md`, `007-library.md`, `008-cache.md`, `010-wasm.md`,
`011-diagnostics.md`, `012-streams.md`, and `014-patterns.md`.
