# Program Runtime

## Purpose

The program runtime compiles scripts, selects rules, builds dynamic dependency
graphs, determines freshness, renders recipes, and executes requested targets.
It composes the engine, language evaluator, library, and host.

The runtime has explicit phases:

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
is `TGT_AMBIG` during compilation. A definition may share that name because it
uses a distinct `definition` key; rule lookup wins and `@(NAME)` explicitly
evaluates the definition. Overlapping templates remain a selection-time error.

Dynamic module imports are not supported.

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
4. Multiple template matches fail with `TGT_AMBIG`.
5. No match fails with `TGT_NO_RULE` and may include a nearby-target suggestion.

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
discarded render has no external effect.

Within a generation, ordinary operation and function applications retain
completed operands when a later operand suspends. Resumption reuses those values
and their staged effects while the consumed observations, dependency edges,
lexical bindings and invocation policy remain valid. A new generation or a
changed proof discards the retained prefix. Mutable scopes, unhashable captured
values and dynamically parsed `eval` expressions use ordinary reevaluation.

A file rule is a function of its declared inputs and of the dependencies
discovered while rendering and executing it. Naming an interpreter does not
widen that set. `SHELL = kash`, `SHELL = /bin/bash`, a per-rule `shell`, and
the implicit `/bin/sh -c` use one dependency rule.

Pure/declarative rules depend only on environment names read by Kame, through
`(env "NAME")`, Kash `env.NAME`, or the same operation reached indirectly from
a definition or function. Process-executing rules additionally consume the complete
effective child environment after rule overrides. This includes `PATH`, loader
variables such as `LD_LIBRARY_PATH` and `LD_PRELOAD`, `LANG`, `LC_ALL`, `LC_*`,
`TZ`, and application-specific variables. Kash can track its own reads, but cannot
infer environment reads inside invoked binaries; opaque shell recipes are not parsed
to infer reads. Environment snapshots are persisted as digests, never plaintext.

The interpreter is an execution dependency, not a render input. Changing it
requires execution. It does not by itself require rendering, and it does not
add environment names. Rendered command text and yielded bytes are results, not
dependencies. A later invocation must not render in order to decide whether to
render.

Rendering and execution append to the dependency set. A successful run persists
that set with the outputs:

- A header input or `@(expression)` result records the resolved file path set.
- `(read PATH)`, `(render PATH)`, and a template include record that file.
- `(wildcard PATTERN)` and any glob evaluated while rendering record the pattern
  and the sorted member paths.
- An environment read records the name and the value read.
- A tool lookup records the tool name and the content digest of the resolved
  executable.
- Handing the recipe to an interpreter records that interpreter's identity:
  `kash`, or the argv used.

Order-only inputs stay out of the set unless a read or a normal edge upgrades
them. A discovered file that is itself a build output is demanded before the
result is reused. A rule with no declared or discovered dependency is always
stale.

A shell script that opens a path never named in the recipe does not introduce
a Kame file dependency. Kame does not infer filesystem reads inside opaque shell
scripts or binaries. Environment access is covered conservatively by the complete
child snapshot; Kash `env.NAME` additionally records its named read as a Kame
operation, as specified in `017-kash.md`.

## Content identity

A regular file dependency is its canonical path and the SHA-256 of its
contents. Size and modification time cannot prove that previously observed
bytes remain unchanged. Modification time is not part of content identity.

A missing path is a distinct identity. Appearance, disappearance, or replacement
by a non-regular file invalidates. Content reads follow ordinary host symlink
resolution; unreadable or non-regular content cannot prove reuse. A glob records
the pattern and sorted membership; member content is consumed only when read
or required. Existence and intentional metadata observations remain distinct.

Each successful output is recorded the same way. The next invocation reuses it
when that digest still matches. An external rewrite invalidates even when the
new timestamp is older. A timestamp update that leaves the bytes alone does not.

Engine hashing is incremental; current host transport buffers a whole file.

## When to render

Do not render when all of the following hold:

- The rule has no `always` prefix and the invocation is not `--force`.
- The recipe source that determines the dependency set is unchanged.
- Every recorded file still has its recorded content digest.
- Declared path sets and glob memberships are unchanged, and each member's
  content digest matches.
- Every recorded environment name still has the value that was read, and any
  consumed process environment snapshot is unchanged.
- Every recorded tool still has the recorded executable content digest.
- Every output exists and still has the content digest written by the successful
  run.

Otherwise render. A newly discovered dependency that is not current suspends
rendering until it completes. A successful result replaces the record. A missing
record renders once. Dry-run renders so the recipe remains visible.

## When to execute

Do not execute when the rendered recipe text is unchanged and the interpreter
dependency is unchanged. Skipping the render implies both, so a fresh record
does not start a process. If rendering ran and produced the same command text,
and the interpreter dependency is unchanged, do not execute either. `always`
and `--force` execute.

Operations that cannot be deferred, including collected `shell`, are invalid in
planning and direct build-template rendering and return `PHASE_INVALID`.
Demanded lazy values and Kash recipe expressions evaluate in their runtime
phase, where collected shell calls may execute with the target environment and
invocation run grants, as specified in 007. Dynamic prerequisites retain their
read-only phase; registration never launches these calls.

## File Rules

A file rule is fresh when the saved dependency set matches:

- It has no `always` prefix.
- It has at least one output, and every output's content digest matches the
  successful run.
- Every declared and discovered file input exists and its content digest matches.
- Declared membership and recorded globs match.

A missing record is evaluated after dependency-discovering render. A file rule
with no declared or discovered dependency is always stale.

A standalone `|` separates order-only prerequisites, for example
`./out : ./input | prepare ./directory`. Both sections must finish before the
recipe runs, and failures in either section block execution. Order-only inputs
are excluded from accepted content observations and input
selectors such as `@<*`. An explicit expression read or a normal occurrence of
the same dependency makes it a content dependency. Ordering alone does not
satisfy the file-input requirement for freshness. Plans and graph inspection
retain all prerequisites; plan JSON additionally reports `orderOnlyInputs`.
Quoted pipe characters remain literal input text. Empty and repeated order-only
sections are parse errors. Environment inheritance applies to both sections.

Whitespace-separated prerequisites form a parallel scheduling group. A comma
separates groups, for example `final : prepare index, compile, package`:
`prepare` and `index` may run independently, then `compile` is requested after
both finish, then `package` after `compile`. Comma groups are a graph-demand
barrier, not an extra order-only edge or a recipe effect sequence. Each
prerequisite retains its normal content or order-only semantics, and a failure
in one group prevents later groups and the parent recipe from running. Kash
statement sequencing remains separately defined in `017-kash.md`.

An `always` file rule, for example `always ./stamp : ./input`, bypasses
freshness every time it is reached from a new requested-root epoch. It remains
a file resource: output paths, multi-output aliases, captures, publication
checks and `do cat` work as for an ordinary file rule. It has no cache lookup.
Shared prerequisites execute once within a diamond or a concurrent root batch;
sequential requests after releasing the previous root rerun the rule. An active
root retains its generation, so overlapping roots share ongoing work. A failed
or cancelled prior generation may be retried by a new root.

Before execution, the runtime creates parent directories for explicit relative
and absolute filesystem outputs. After successful execution, every declared
file output must exist unless the rule used `yield` for its single output.
Missing output is `OUTPUT_MISSING`.

## Tasks

A bare task is always stale and executes whenever reached from a requested root.
It may have dependencies but no file artifact.

A `task` rule is cached according to `008-cache.md`. Parsing preserves its
cached kind; it must not silently execute with bare-task reuse semantics.

Services parse into plans and use the managed lifecycle contract in
`024-managed-services.md`; they are not ordinary task rules.

## Rule Instances and Multiple Outputs

One rule instance is keyed by the rule declaration and complete capture map.
Every rendered output maps to that one node. Requests for sibling outputs share
execution. If two outputs of one declaration both match a requested target but
produce different captures, selection is ambiguous and returns `TGT_AMBIG`.
After capture rendering, duplicate output paths are `PARSE_ERR`.

## Yield and Effects

`yield` appends bytes to declarative output content. It is valid only for a file
rule with exactly one output. If the same accepted render contains any nonempty
shell command, rendering fails with `OUTPUT_CONFLICT`; the runtime does not inspect
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

## Scoped Recipe Environments

`target : prerequisites ; env "NAME=value" ...` attaches literal child-process
environment assignments to a rule. The equivalent record surface is
`; [env: [NAME: value]]`, whose string values may be pure Kame expressions.
Record settings cannot launch processes or perform host effects during their
planning evaluation. Resolved definition dependencies are tracked by the rule.
A record may also contain `shell`, selecting that rule's interpreter as specified
in `017-kash.md`. Interpreter selection is local; environment inheritance applies
to both shell and Kash recipes. Root recipes begin with the invocation's
environment. Prerequisite recipes inherit the effective parent environment;
their own assignments override inherited values. Later assignments to the same
name win. Environments are immutable per active shared rule instance, and
canonical ordering makes equivalent assignments share the same cache identity.
A conflicting claim on a shared prerequisite returns `ENV_CONFLICT`, without
including environment values in its diagnostic. A released instance can bind a
new environment for a later root; unrelated roots retain their own environments.
Native recipe retries and forwarded WASM recipes receive the same values.

Execution reuse records include authored environment assignments as explicit
inputs and named environment reads as observations. Executing a process also
records the complete effective child environment as a digest observation.
Interpreter identity is the effective selection, independent of
whether it was selected explicitly or implicitly.
Plan and AST JSON expose authored assignments, without publishing the ambient
environment. This surface scopes shell/Kash recipes and their prerequisite
recipes. Direct `env` reads in recipe templates and structured recipes use the
effective target snapshot after checking environment grants; absent names return
nil, including in an empty snapshot. Lazy definitions and functions reached from
recipes or dynamic prerequisites evaluate against the same target environment.
A target's `KAME_NAME` assignment overrides a declared value definition `NAME`;
explicit `--define NAME=value` wins over that assignment. Overrides remain literal
strings. Definitions retain separate engine values per environment and phase,
including ordinary replay, dependency and cycle handling. Produced file reads
reached through definitions inherit the demanding target environment.
Dynamic prerequisites retain their read-only phase through lazy definitions.
Collected shell requests reached during runtime evaluation receive the same
snapshot, including an explicit empty environment. Their run grants still apply.
Declared tool lookup intentionally uses startup PATH and CLI `--tool` overrides,
as specified in 007/009; a recipe PATH assignment changes child command lookup.
Assignments do not introduce undeclared Kame variables or perform evaluation
during registration.

Inherited environment values are what the child process receives. Changing that
effective snapshot invalidates a process-executing rule, including when no Kame
expression reads the changed name. Pure/declarative rules retain selective
tracking: an unread inherited value does not invalidate them. Selecting a shell
without executing a process does not consume the snapshot. Hosts supply resource bytes
and states; the portable engine computes digests and decides freshness.
Accepted file records validate both consumed inputs and physical output bytes;
timestamps alone cannot prove reuse. Corrupt records are cache misses.
Filesystem notifications first refresh the resource. Current consumers retain
their accepted state while the engine validates dependencies bottom-up; reads
and retained-root polling wait for that validation. Equal observed signatures
preserve the consumer generation, including when an upstream recipe rebuilds
to identical output bytes. Unavailable signatures cannot prove equality.
Always/forced consumers still restart. In-flight host work uses cancellation-first
invalidation rather than accepting a completion derived from an old snapshot.
Records contain digests rather than
unread environment values.
Repository artifacts use compiler-bound modes and target-independent generated
metadata. Switching a mode alone must not rewrite shared version metadata.

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
- Two matching templates produce `TGT_AMBIG` regardless of source order.
- Captures render corresponding input and output templates.
- Bare symbolic dependencies select rules before definitions with the same
  name; explicit definition evaluation remains available through `@(...)`.
- A diamond dependency graph executes its shared node once.
- Existing fresh file outputs are skipped; missing outputs and changed content
  rebuild. A modification-time update that leaves the bytes alone does not.
- A later invocation does not render or execute a fresh file rule when the saved
  dependency set is unchanged, whether the interpreter is Kash or a shell.
- Changes to the effective child environment invalidate process-executing rules
  on native and WASM, including shell and Kash recipes. Pure/declarative rules
  ignore unread names; direct and indirect named reads invalidate when their
  observed value changes. Equivalent environment assignment order preserves reuse.
- Changing file bytes without changing the modification time renders and
  executes. Replacing an output's bytes invalidates even when the new timestamp
  is older.
- A changed discovered input, a changed declared-input membership, a removed
  glob member, or a changed recipe source renders again. Changing only the
  interpreter executes again and does not add environment dependencies.
  `always` and `--force` render and execute.
- `always` file outputs rerun on sequential roots and invocations, while shared
  diamond dependencies execute once per root; artifact/output verification remains.
- A rule with a dynamic body dependency renders before its first freshness
  decision; plan reports `unknown` before that render. A later invocation uses
  the saved dependency set and does not render when those inputs are unchanged.
- Independent prerequisites can execute concurrently while one recipe remains
  one shell process.
- Dynamic dependencies discovered during render are scheduled before the final
  render generation executes.
- A discarded render commits no output effect.
- Parent output directories are created before execution.
- Successful commands that omit a declared output fail with `OUTPUT_MISSING`.
- Scoped recipe environments inherit through prerequisites, apply local/last
  overrides, isolate roots, and reject conflicting active shared contexts.
- Equivalent assignment order shares a prerequisite. Changing a recorded
  environment read or consumed child snapshot invalidates a cached task.
  Unchanged recorded values reuse the record.
- Bare tasks run every time; cached task syntax remains distinguishable.
- Whitespace prerequisite groups schedule independently; comma-separated
  groups wait for all prior prerequisites, preserve failure blocking, and retain
  normal/order-only input semantics on native and WASM.
- Requests for two sibling outputs share one rule-instance execution.
- `yield` atomically writes one output and rejects tasks or multiple outputs.
- `yield` combined with any nonempty rendered command returns `OUTPUT_CONFLICT`.
- Command failure prevents dependent execution and carries mapped source span.
- Cancelling a target leaves no process group running.
- A representative project builds and then skips its unchanged file target on
  the next invocation.

## Newer-input selection

In a file recipe, `@<?` is the list of unique normal declared file inputs whose
content digest differs from the digest recorded for the successful run. If any
output is absent, or no record exists, it selects all normal file inputs. It
preserves input order and authored path spelling; order-only prerequisites and
named tasks are omitted. A recipe with no normal file inputs gets an empty list.
Forced execution does not invent changed inputs. Input planning rejects this
selector with `PHASE_INVALID`; a task recipe or evaluation without a file-rule
frame gets `SEL_NO_CONTEXT`.

The snapshot is taken after prerequisites finish and before rendering; repeated
references, including references through definitions, share it. Identity is the
content digest from Content identity, not modification time. A successful build
may skip its next invocation. Changing the recipe source renders again so a new
read can be discovered. An unread environment value only invalidates when the
rule consumes a process environment snapshot.

Make's `$?` maps to `@<?`. Pattern stems use explicit captures, for example
`./build/{stem}.o` and `@(stem)` in its recipe. Output and first-input directories
use `@(dirname @>)` and `@(dirname @<)`.
