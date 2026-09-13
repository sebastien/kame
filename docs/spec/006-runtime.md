# Program Runtime

## Purpose

The program runtime compiles scripts, selects rules, builds dynamic dependency
graphs, determines freshness, renders recipes, and executes requested targets.
It composes the engine, language evaluator, library, and host.

The runtime has explicit phases to avoid the legacy ambiguity around `make`:

```text
compile -> plan -> schedule -> render -> execute
```

`materialize` is the convenience operation that performs all phases needed to
make one target current.

## Compilation

Compilation parses one or more sources into one program. It registers:

- Lazy definitions under `definition` resource keys.
- Literal and template rules.
- Lexical functions.
- Built-in operations supplied by the caller.

Compilation reports all recoverable parse diagnostics. Any error-severity
diagnostic prevents executable program creation. Compilation performs no
filesystem access and starts no producer.

Literal phony, cached-task, and service rules share the source-level target
namespace. Defining the same literal name in more than one of those rule kinds
is `LM-TRAMB` during compilation. A definition may share that name because it
uses a distinct `definition` key; rule lookup wins and `@(NAME)` explicitly
evaluates the definition. Overlapping templates remain a selection-time error.

Imports and dynamic modules are not initially supported.

## Target Names

Requested explicit relative file targets are normalized lexically while
retaining the `./` classification in authored source. A request for `out/file`
may match authored output `./out/file`. Named targets remain unchanged.

Canonical file keys use absolute normalized paths internally. User-facing plans
and diagnostics retain authored or working-directory-relative paths.

## Rule Selection

Selection of a bare name considers phony, cached-task, and service rules as one
source namespace. Selection of a path considers file rules:

1. A literal output match wins over template matches.
2. Otherwise every matching template is collected.
3. Exactly one template match is selected.
4. Multiple template matches fail with `LM-TRAMB`.
5. No match fails with `LM-TRMRL` and may include a nearby-target suggestion.

Registration order never resolves ambiguity. Captures from the selected output
are available while rendering that rule's remaining outputs and inputs.

## Planning

A plan contains:

- Requested target and resource key.
- Selected rule and source span.
- Capture bindings.
- Flattened declared inputs.
- Rendered outputs.
- Unrendered body AST.
- Freshness `fresh`, `stale`, or `unknown`.

Planning evaluates only expressions required to determine inputs and outputs.
It does not evaluate body expressions or run commands.

Freshness is `unknown` when body evaluation may discover dependencies. A plan
never claims such a rule is fresh from declared inputs alone.

Bare input names refer to symbolic resources. `@(NAME)` evaluates and recursively
flattens the definition value into concrete inputs. Nil contributes no input.
Values other than strings, resource references, lists, or nil are invalid rule
inputs.

## Scheduling

Scheduling recursively plans inputs that have producing rules and adds graph
edges. A bare symbol with no producing rule requests its lazy definition node;
the value remains a symbolic dependency and is not flattened into path inputs.
An explicit path with no rule must exist. Missing required files fail before
dependent execution.

The engine detects cycles and exposes ready nodes. Dependencies execute before
dependents. Independent ready nodes may execute concurrently.

Multiple requested roots share dependency nodes and in-flight work.

## Rendering

Rendering evaluates recipe string templates in source order with rule inputs,
outputs, and captures in scope. It records:

- Command lines.
- `out` and `err` effects.
- `yield` content.
- Dynamic dependencies from library operations.
- Source mapping from generated shell lines to recipe body spans.

Every body line renders to command text, possibly empty after an effect
expansion. Nonempty command lines are joined with `\n` and sent as one process
request. There is no separate standalone-expression recipe syntax.

Rendering may add dynamic dependency edges. If a newly discovered dependency is
not current, rendering suspends and restarts for the latest generation after
that dependency completes. Effects are committed only by execution, so a
discarded render has no external effect. Once rendering converges, freshness is
computed from declared and discovered dependencies. Rendering always precedes
the final freshness decision in the initial implementation.

Operations that cannot be deferred, including collected `shell`, are invalid in
planning or rendering and return `LM-PHASE`.

## File Rules

A file rule is fresh when:

- It has at least one output.
- Every output exists.
- Every declared and discovered file input exists.
- The oldest output modification time is not older than the newest input
  modification time.

Freshness is evaluated after dependency-discovering render. A file rule with no
declared or discovered input is always stale.

Before execution, the runtime creates parent directories for explicit relative
and absolute filesystem outputs. After successful execution, every declared
file output must exist unless the rule used `yield` for its single output.
Missing output is `LM-OUTMS`.

## Tasks

A bare task is always stale and executes whenever reached from a requested root.
It may have dependencies but no file artifact.

A `task` rule is cached according to `008-cache.md`. Until that specification is
implemented it behaves as a bare task; parsing must still retain its cached
kind.

Services parse into plans but execution is deferred. Attempting to execute a
service before service support exists returns a clear unsupported diagnostic,
`LM-UNSUP`, not ordinary task behavior.

## Rule Instances and Multiple Outputs

One rule instance is keyed by the rule declaration and complete capture map.
Every rendered output maps to that one node. Requests for sibling outputs share
execution. If two outputs of one declaration both match a requested target but
produce different captures, selection is ambiguous and returns `LM-TRAMB`.
After capture rendering, duplicate output paths are `LM-PARSE`.

## Yield and Effects

`yield` appends bytes to declarative output content. It is valid only for a file
rule with exactly one output. If the same accepted render contains any nonempty
shell command, rendering fails with `LM-OUTCF`; the runtime does not inspect
shell syntax to infer output ownership. Yielded content is written atomically
during execution.

`out` and `err` are deferred effects. Dry-run reports them but does not write
them. Normal execution commits all non-process effects in recipe source order
before starting the one shell script. This preserves one shell session without
claiming effect interleaving between its commands.

## Execution Events

Runtime execution emits:

- Target started.
- Dependency discovered.
- Effect.
- Process attempt started.
- Stdout or stderr bytes.
- Process exited.
- Target value.
- Target completed.
- Target failed.
- Target cancelled.

Events include target identity and enough source metadata for diagnostics.
Direct streams are not truncated. Retained logs follow the configured byte cap.

## Failure and Cancellation

A failed dependency prevents dependent execution. Already running independent
nodes may finish unless the caller requests fail-fast cancellation.

Cancelling a root propagates through engine interest and the POSIX process
contract. A runtime does not report completion until active process groups for
that root have terminated or remain required by another root.

## Materialization Result

Materializing a file target returns its path and status; it does not read the
file into memory. Materializing a definition returns its value. Materializing a
task returns status and retained execution metadata. Reading exact artifact
bytes is a separate operation used by `cat`.

## Acceptance Tests

- Compilation performs no filesystem or process operation.
- A literal rule wins over a matching template rule.
- Two matching templates produce `LM-TRAMB` regardless of source order.
- Captures render corresponding input and output templates.
- Bare symbolic dependencies select rules before definitions with the same
  name; explicit definition evaluation remains available through `@(...)`.
- A diamond dependency graph executes its shared node once.
- Existing fresh file outputs are skipped; older or missing outputs rebuild.
- A rule with a dynamic body dependency renders before its first freshness
  decision; plan reports `unknown` before that render.
- Independent prerequisites can execute concurrently while one recipe remains
  one shell process.
- Dynamic dependencies discovered during render are scheduled before the final
  render generation executes.
- A discarded render commits no output effect.
- Parent output directories are created before execution.
- Successful commands that omit a declared output fail with `LM-OUTMS`.
- Bare tasks run every time; cached task syntax remains distinguishable.
- Requests for two sibling outputs share one rule-instance execution.
- `yield` atomically writes one output and rejects tasks or multiple outputs.
- `yield` combined with any nonempty rendered command returns `LM-OUTCF`.
- Command failure prevents dependent execution and carries mapped source span.
- Cancelling a target leaves no process group running.
- The equivalent of `deps/littlemake-legacy/Makefile.lmk` builds and then skips
  its fresh file target.
