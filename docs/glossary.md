# Glossary

This glossary names recurring concepts in Kame's source and specifications. It
is grouped by domain, from authored source through execution, rather than
alphabetically.

## Source and language

### Source and span

A **source** is named UTF-8 text being parsed, such as a `Makefile.km` or an
inline `-c` command. A **span** is a zero-based, half-open byte range within one
source, retained by AST nodes and diagnostics. Display lines and columns are
derived from spans when rendering diagnostics.

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
definition, operation, or named target depending on context. A **symbol** such
as `:ready` evaluates to its literal name. The boolean and nil literals are
`true`, `false`, and `nil`. An authored **path** starts with `/`, `./`, or
`../`, as in `./out/file`.

### Expression and value

An **expression** is language syntax that evaluates to a **value**. Values are
immutable tagged runtime data: nil, boolean, number, string, bytes, list, record,
callable, resource reference, pattern, or process handle. A callable is valid
while it is being evaluated, but cannot be published or cached as an ordinary
value.

### Definition

A **definition** is a top-level named value or function declaration, for example
`sources = ./src/*.c`. Definitions are lazy: the first use evaluates a
definition node, and concurrent consumers share that work. A definition has its
own resource namespace, so it can have the same spelling as a named rule.

### Scope, binding, and lexical function

A **scope** maps names to **bindings** and links to a parent scope. A binding holds
a value, operation, lazy definition, or **lexical function**. Lexical functions
retain their defining scope and evaluate in a child scope containing arguments.
Function definitions, lambdas, and placeholder sections share this callable model.

### Reference

A **reference** starts with a name and follows components such as record keys,
list indexes, slices, or selections: `project.name`, `files.0`, or
`config.{host,port}`. These references select values; they do not themselves
declare target dependencies. An **environment reference** such as `env/HOME`
performs a tracked, capability-checked read in both Kame and Kash.

### Control form

A **control form** is a special form whose operands are evaluated only when
selected. The language provides `??` for fallback, `if` for conditional values
or blocks, `and` and `or` for lazy boolean operations, and `match` for pattern
dispatch with capture bindings. Unselected branches leave dependencies and
effects untouched.

### Template and rule bindings

A **string template** is text containing literal segments and Kame expansions.
Kame strings use `${EXPR}`. Document templates and foreign-shell recipes use
`@{EXPR}`. Rule bodies bind
`inputs`, `outputs`, `changed-inputs`, and `captures`. For example,
`inputs.0` reads the first input and `outputs.0` reads the first output.

### Target template and capture

A **target template** is a rule target with capture groups, such as
`./build/{name:*}.o`. It matches a requested target and yields named **captures**
such as `name=main`. The selected captures render the rule's other outputs,
inputs, and recipe. A target template is related to, but different from, an
expression pattern: `{name}` is a target capture but an expression-pattern
reference.

### Placeholder section

A **placeholder section** is a short lexical function written around one
expression with placeholders, such as `(sub ".c" ".o" _)`. `_`, `__`,
and `_2` denote positional arguments. A placeholder only has this special meaning
inside a section; elsewhere it remains an ordinary name.

### Pattern

A **pattern** is a first-class expression value written in a path pattern or
parsed from text with `pattern`. Match groups, such as `{name:*}` and
`{**}`, capture text. Template groups such as `{name}` and `{_0}` insert
a prior capture.
Patterns power `match?` predicates and the match-and-expand form of `sub`
(`replace` is a deprecated alias). Substitution preserves nonmatches; matching
returns a boolean. Patterns do not reclassify runtime strings containing braces;
explicit regex groups use the bounded regex grammar.

## Build declarations and planning

### Script and include

A `.km` **program** contains definitions and may declare rules, tasks, and
services. A `.kash` **script** contains typed commands and control flow. Both
use the expression grammar in value context. `include PATH` composes source
in the caller's scope at the inclusion point. `include? PATH` skips a missing
file. `import` exposes a module's exports.

### Rule

A **rule** declares outputs, inputs, and an optional indented recipe. Kame
classifies declarations by their leading keyword.

- A **file rule** uses `rule` and produces one or more filesystem artifacts.
- A **phony task** uses `task`, has a named target, and runs
  whenever it is reached.
- A **cached task** uses `task cached` and may reuse a successful cached
  execution.
- A **service** is prefixed with `service` and represents a long-running action.

`target` is often used generically in user-facing commands, but internally the
rule kind determines whether it identifies a file, task, cached task, or service.

### Target, output, input, and artifact

A **target** is a requested result, such as `build` or `./build/app`. A rule's
**outputs** are the targets or paths it claims to produce; its **inputs** are
declared prerequisites. A file **artifact** is the resulting filesystem object,
not its declaration. A bare input name is a symbolic resource; an explicit path
input without a producing rule must already exist.

### Recipe

A **recipe** is the ordered body of a rule. A default recipe contains Kash
statements and process graphs. When metadata or the build-wide `SHELL`
setting selects an external shell, Kame renders the body as a document
template and sends the result to that shell.

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

**Freshness** is the file-rule decision to reuse or execute a recipe. Reuse needs
an accepted successful-run proof matching the current consumed dependencies,
output content signatures, rule/operation identity, and execution settings.
File content is compared by digest, not oldest-output/newest-input timestamps;
metadata observations are tracked separately. Missing outputs or unverifiable
proofs prevent reuse, as does having no reusable material input. Phony tasks
always run; cached tasks use their matching successful fingerprint/manifest.
See the [runtime](spec/006-runtime.md) and [cache](spec/008-cache.md) contracts.

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

An **allocator** supplies storage; **ownership** determines who must release an
object or payload. A **lifetime** bounds validity, including borrowed source data.
Kame distinguishes instance, program, engine, and run/message lifetimes; these
logical owners can share an allocator. Boundary crossings require explicit
cloning or transfer. An **arena** is an allocation strategy, commonly releasing
storage together on reset, not a synonym for ownership. WASM instances use a
fixed backing buffer managed by a coalescing **reclaiming heap**, which can free
individual blocks while the compiled program remains alive.

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

### Process graph, channel, and framing

A **process graph** connects concurrently running commands and stream operators.
A **channel** carries bytes or explicitly framed records between graph ports;
it is not an engine dependency edge. **Framing** marks record boundaries, such as
LF/CRLF-delimited lines, while retaining exact payload bytes. These process
streams are distinct from the engine's value-stream protocol below.

### Process handle

A **process handle** is an opaque value for asynchronous process work owned by
the current invocation. `$&(COMMAND)` returns a handle and
`$?(wait $handle)` reads its outcome. The owning invocation joins or cancels
its asynchronous work before it ends.

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
perform effects, rule-body computation and shell-template expansion are read-only,
and execution runs the selected producer and publishes its accepted output.

### Effects and file publication

An **effect** is an explicit runtime action. `out!` and `err!` write to the
corresponding streams, and `write!` writes a file. A **value-producing rule
body** supplies text or bytes for exactly one file output. An expression body
supplies its result, and a Kash body can supply it with `return VALUE`. Rule
expression bodies and shell-template expansion reject effects.

Standalone evaluation flushes successful `out!` and `err!` effects to the
corresponding streams after evaluation completes. A host-request retry does
not duplicate that output.

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

A **tool reference** obtains a resolved, tracked executable through `x/NAME` or
`(tool NAME)`, rendered as `@{x/NAME}` in external-shell recipes or referenced
as `$x/NAME` in Kash. Named lookup uses the driver's startup `PATH` and `--tool`
overrides. Every reached Kash command head also acquires an executable dependency
automatically; explicit executable paths resolve from stage cwd. Ordinary command
names in external-shell text remain shell-resolved and are not tracked this way.

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

For normative definitions, use the [specification index](spec/README.md).
