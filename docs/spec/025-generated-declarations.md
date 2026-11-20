---
revision: 2
---

# Generated Declarations

## Purpose

Generated declarations let typed Kame configuration produce a bounded set of
build declarations before target selection. They support data-driven target
families without generating source text, reparsing it, or running processes
during declaration loading. Rules are values
(`055-unified-kame-kash-language.md` §10.4): a program computes `Rule` values
with ordinary functions and registers them with `generate`, or declares rules
directly inside top-level `for` and `if` blocks.

## Syntax

```text
GENERATE = "generate" EXPR
```

`EXPR` must evaluate to a `Rule` value or a list of `Rule` values. An empty list
registers nothing. Any other value, including `nil`, a nested list, or a record,
is `EXPR_INVALID` at the `generate` statement.

`generate` is a declaration statement. It is valid at the top level of a Kame
program, inside top-level `for`/`if` declaration blocks, and in included files.
It is not valid inside a `def` body, a lazy definition block, an expression, or
any action context.

In expression position, `rule`, `task` and `service` construct a `Rule` value
without registering it. The header and body follow the declaration grammar of
`055` §10.1, and the body closes over the lexical scope where the value is
constructed:

```kame
sources = files ./src/**/*.c

def object-path source = match source
	./src/{path:**}.c => ./build/${path}.o
	else              => fail! "not a source: ${source}"

def object-rule source =
	rule (object-path source) : source
		cc -c $inputs.0 -o $outputs.0

generate (sources |> map object-rule)

task objects : ${sources |> map object-path}
```

A constructed `Rule` that is never passed to `generate` registers nothing.
Constructing a rule performs no validation beyond parsing; validation happens
when the value is registered.

## Declaration-time control flow

Top-level `if`, `elif`, `else` and `for` blocks run in declaration context and
declare rules directly (`055` §10.3):

```kame
variants = ["debug", "release"]

for variant in variants
	let flags = if variant == "debug" then ["-g"] else ["-O2"]

	rule ./build/${variant}/{name}.o : ./src/{name}.c
		cc $...flags -c $inputs.0 -o $outputs.0

if env/CI != nil
	include ./ci.km
```

- `${variant}` is fixed when the declaration is evaluated; `{name}` stays a
  capture. Each declared rule closes over its own iteration scope.
- Inactive branches register nothing, and their includes are not read, tracked
  or cycle-checked.
- Declaration blocks can contain `let`, nested control, declarations, `include`
  and `generate`. Commands are not valid in declaration blocks.

A rule declared directly in a declaration block and a rule registered through
`generate` are equivalent once registered.

## Allowed work

Declaration evaluation, including every expression reached from a `generate`
statement or a declaration block condition or iterable, may use:

- pure computation and functions, including functions exported by imported
  modules;
- tracked read-only discovery: `files`, `dirs` and `wildcard` membership
  (`055` §9.4);
- tracked environment reads (`env/NAME` and uppercase environment fallback).

It may not run processes, capture process output, write files or outputs,
perform untracked host reads, read file contents, query a partially registered
graph, or inject declarations once a selected rule is executing. These
operations fail with `PHASE_INVALID` even when the caller grants the
corresponding capabilities. Lazy definitions, helpers and callbacks reached
during generation inherit the restriction.

Discovery during declaration evaluation returns a snapshot taken at planning
time. A change in membership invalidates the declarations that consumed it. A
declaration-time discovery pattern that overlaps any declared output, authored
or generated, is rejected with `DISCOVERY_OVERLAP`, naming both the pattern and
the overlapping output declaration. Overlap is checked once all declarations are
collected, so a discovery cannot observe outputs declared later in the same
load. As a rule input, a wildcard path keeps the dynamic membership semantics of
`023`.

## Choosing producers

Prefer a pattern rule for a target family, and discover membership in
prerequisites:

```kame
rule ./build/{name}.o : ./src/{name}.c
	cc -c $inputs.0 -o $outputs.0

task sources : ./src/*.c
```

For a compiler with known sibling outputs, declare them together:

```kame
rule ./build/{name}.js ./build/{name}.map : ./src/{name}.ts
	compiler $inputs.0 --output $outputs.0
```

One invocation owns both artifacts. Do not declare overlapping `.c` and `.cpp`
patterns for the same object output and ask Kame to choose by source existence:
both are producers, so selection fails with `TGT_AMBIG` and both locations. Use
distinct output names, or generate concrete rules from discovered sources so
that each output has exactly one producer:

```kame
def object-for source =
	rule ./build/${(splitext (basename source)).0}.o : source
		cc -c $inputs.0 -o $outputs.0

generate (files ./src/*.c ./src/*.cpp |> map object-for)
```

A source present as both `x.c` and `x.cpp` still yields two producers of
`./build/x.o` and fails with `TGT_AMBIG`. See
[output ownership](039-output-ownership.md) for variable membership limits.

## Registration

All declarations, authored or generated, go through the same validation,
collision detection, target identity, freshness, ownership and scheduling.

- **Transactional.** Every `generate` statement and declaration block in a load
  is evaluated and validated before any declaration becomes visible. A failure
  in any of them registers nothing and leaves no executable program.
- **Validation.** Each registered `Rule` is checked as an authored declaration
  would be: output and target kinds, input groups, metadata keys, body form and
  producer selection (`044`).
- **Collisions.** A target or output may have one producer across authored and
  generated declarations, including concrete outputs obtained by resolving
  computed headers. A collision is `TGT_AMBIG` and reports the provenance of each
  declaration.
- **Order.** Registration follows source order, then iteration order, then list
  order within a `generate` value. Plans and rule order are deterministic.
- **Re-evaluation.** A generator is a `generate` statement at its site in its
  include chain. Re-evaluating the program replaces each generator's complete
  declaration set, and the declarations of each declaration block, as a whole.

Generated declarations participate in the same target selection, dependency
planning, freshness, caching, ownership and execution as authored rules.

## Limits

The following limits apply per `generate` statement, summed over every
evaluation of that statement in a load, including each enclosing loop
iteration:

| Limit | Value |
| --- | --- |
| Registered `Rule` values | 4096 |
| Retained declaration data (output, target, input and metadata text, UTF-8) | 1 MiB |
| One output, target, input or metadata string | 64 KiB |
| Combined input and order-only items | 4096 |

Rule bodies are authored source shared by every rule constructed from them; they
are not counted as generated data. Limits are checked before any generated rule
becomes visible, and exceeding one rejects the load with its established
resource-limit diagnostic at the `generate` statement.

## Provenance

Every declaration retains its origin:

- the source file and span of the declaration header that constructed it;
- the enclosing loop iterations and their bindings;
- the constructing call chain, including calls into imported modules;
- the `generate` site that registered it, if any, and its include chain.

Module paths follow `055` §9.2, so a rule constructed by an imported function
resolves its outputs and inputs against the importer's root. Diagnostics for
an invalid or colliding declaration identify the declaration header, the
`generate` site, and the iteration that produced it.

## Ownership

The loader owns evaluated `Rule` values, lowered declarations and their body
closures. Every success and failure path releases them exactly once, including
rejected loads, limit failures, and generator replacement during watch.

## Dependencies and inspection

References to value definitions, imported functions, discovered memberships and
environment reads made during declaration evaluation are generator
dependencies. Definitions are evaluated through the ordinary evaluator and their
names are retained in generator metadata. Generated file and resource
prerequisites enter the ordinary dependency graph and cache fingerprint. Watch
re-evaluation replaces generator declaration sets after a source, discovered
membership, or environment dependency changes.

Plan output exposes each generator's site, the fields of each emitted rule
(kind, outputs or target, inputs, order-only inputs, metadata, body span),
provenance, and generator dependencies.

## Portability and diagnostics

Native and WASM share parsing, evaluation, validation, lowering, registration,
diagnostics, and fingerprinting. During generation hosts service only tracked
discovery and environment requests; generation performs no other host effects.
Cyclic value definitions, non-`Rule` values, invalid declarations, collisions,
discovery overlap, phase violations and resource-limit failures use their
established diagnostic codes (`EXPR_INVALID`, `TGT_AMBIG`, `DISCOVERY_OVERLAP`,
`PHASE_INVALID` and the others in `011`) with authored locations and
provenance.

## Acceptance

Coverage must include:

- generated file rules, phony and cached tasks, and services, constructed by
  local and imported functions;
- top-level `for` and `if` declarations with per-iteration closures, and
  inactive branches that neither evaluate values nor read includes;
- pattern and computed headers, and definition dependencies;
- non-`Rule` values, invalid declarations and collisions, reported with
  provenance, and rejecting the whole load;
- tracked discovery: membership snapshots, invalidation on membership change,
  and `DISCOVERY_OVERLAP` against authored and generated outputs;
- process execution, writes, captures and file content reads during generation
  failing with `PHASE_INVALID`;
- each limit at and beyond its bound;
- generator replacement on re-evaluation, native/WASM parity, deterministic
  planning, and allocator cleanup on rejected loads.
