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
uses a distinct `definition` key; rule lookup wins and `@{NAME}` explicitly
evaluates the definition. Unresolved template ambiguity remains a selection-time
error after applying `045-rule-precedence.md`.

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
3. Discard structurally less-specific file templates as defined in
   `045-rule-precedence.md`.
4. Select the unique remaining rule instance; competing remaining instances
   fail with `TGT_AMBIG`.
5. No match fails with `TGT_NO_RULE` and may include a nearby-target suggestion.

Registration order and source existence never resolve ambiguity. Captures from
the selected output are available while rendering that rule's remaining outputs
and inputs.

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

Bare input names refer to symbolic resources. `@{NAME}` evaluates and recursively
flattens the definition value into concrete inputs. Nil contributes no input.
Values other than strings, resource references, lists, or nil are invalid rule
inputs.

Build inspection composes the selected producer plans into the recursively
reachable resource graph defined by `037-build-inspection.md`. It resolves
authorized read-only computed prerequisites and follows their producers, retains
typed resource and rule-instance identity, and reports unresolved runtime discovery
without rendering recipes or materializing missing generated inputs. This graph
is distinct from one producer's plan and from the live execution graph.

## Scheduling

Scheduling recursively plans inputs that have producing rules and adds graph
edges. A bare symbol with no producing rule requests its lazy definition node;
the value remains a symbolic dependency and is not flattened into path inputs.
An explicit path with no rule must exist. Missing required files fail before
dependent execution.

The engine detects cycles and exposes ready nodes. Dependencies execute before
dependents. Independent ready nodes may execute concurrently.

Multiple requested roots share dependency nodes and in-flight work.

Inspection stages summarize known dependency ordering and consumer-local sequence
gates. They are not runtime phases, watch cycles, or new scheduler barriers;
independent work starts when its own prerequisites permit, regardless of unrelated
work in an earlier displayed stage. Partial graphs identify partial stage views.

## Rendering

Kash interpreters follow `038-direct-kash-and-values.md`: their recipe bodies
are parsed directly and evaluated as typed process/expression ASTs. They do not
render command text through the document-template engine. The template-rendering
contract below applies to external-shell interpreters. Both paths retain dynamic
dependencies, source provenance, publication, and suspension guarantees.

Rendering evaluates recipe string templates in source order with rule inputs,
outputs, and captures in scope. It records:

- Command lines.
- `out` and `err` effects.
- `yield` content.
- Dynamic dependencies from library operations.
- Source mapping from generated shell lines to recipe body spans.

For external-shell recipes, every body line renders to command text, possibly
empty after an effect expansion. Nonempty lines are joined with `\n` and sent
as one process request. Direct Kash recipes instead support ordinary expression
statements and structured process graphs without text generation.

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
`(env "NAME")`, `env.NAME` in Kame or Kash, or the same operation reached indirectly
from a definition or function. Process-executing rules additionally consume the complete
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

- A header input or reference/expression expansion records the resolved file
  path set.
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
result is reused. A file rule with no normal declared or discovered data
dependency is always stale. Implicit interpreter, executable, operation identity,
and whole-child-environment observations do not by themselves make an inputless
recipe reusable. Explicitly consumed definitions and named environment reads
remain data dependencies.

A shell script that opens a path never named in the recipe does not introduce
a Kame file dependency. Kame does not infer filesystem reads inside opaque shell
scripts or binaries. Environment access is covered conservatively by the complete
child snapshot; `env.NAME` additionally records its named read as a Kame
operation, as specified in `004-language.md` and `017-kash.md`.

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

Symlink outputs are an exception: publication inspects the final component without
following it and records the exact UTF-8 destination text under the link-target
observation aspect. Links to directories, dangling links, and loops are valid
outputs. Removing or retargeting a link, or replacing it with another entry kind,
invalidates reuse. The linked directory is not recursively hashed. Ordinary file
content reads still follow links and track the resolved bytes, not link identity.
Publication does not grant automatic cleanup ownership of links or their referents;
existing cleanup safety restrictions remain in force.

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

A fresh accepted record may skip execution only while the consumed inputs,
output content and execution settings still satisfy the reuse proof above.
Skipping rendering under that proof does not start a process. Unchanged rendered
command text and interpreter alone are insufficient: changed input bytes or a
missing output still require execution. Rendering may establish a new dependency
set, whose current values and output proof must be validated before reuse.
`always` and `--force` execute.

Operations that cannot be deferred, including collected `sh!`, clocks, and
output actions, are invalid during planning and during shell-template expansion
or Kame content computation. They return `PHASE_INVALID`. Direct Kash recipes
evaluate in the execution phase, where those actions may run with the target
environment and invocation grants, as specified in 007. A reached lazy definition
inherits the phase of the computation that demands it. Dynamic prerequisites
retain their read-only phase; registration never launches these calls.

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
retain all prerequisites; schema-2 inspection dependency records carry their
`orderOnly` purpose as defined by `037-build-inspection.md`.
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
Every rendered sibling must select this same rule instance; shadowed or ambiguous
sibling ownership is `TGT_AMBIG` before execution (`045-rule-precedence.md`).

## Yield and Effects

`shell` selects one producer. `[shell: kame]` or `SHELL = kame` selects one Kame
expression. It returns text or bytes, and the rule atomically publishes those
bytes to its single local file output. No process is started. Tasks, services,
grouped outputs and remote execution are rejected. See
`044-value-producing-rule-bodies.md`.

`[shell: kash]` or `SHELL = kash` executes the authored body as Kash. In that
program, `(yield! ...)` appends bytes for exactly one file output. It is an
execution action, not a template expansion. `(out! ...)` and `(err! ...)` are
likewise Kash actions.

An external-shell recipe expands its body as a read-only template, then runs one
shell process. Expansion and Kame content computation reject `out!`, `err!`,
`yield!`, `write!`, process execution and clock reads with `PHASE_INVALID`,
including through reached helpers and lazy definitions. The shell performs its
own actions after expansion. Dry-run computes available Kame content but does
not publish it. A failed computation leaves an existing artifact untouched.

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
explicit CLI configuration wins over that assignment under `009-cli.md`.
`--define NAME=value` and bare assignments remain literal strings;
`--set NAME=LITERAL` preserves closed literal data without evaluation. Definitions
retain separate engine values per environment and phase, including ordinary
replay, dependency and cycle handling. Produced file reads
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

## Detected Changes and Reuse Decisions

When materialization rejects reuse or deliberately bypasses it, the runtime
reports the reason established by that decision. Explanations distinguish a
detected change from the absence of a reusable proof and from an explicit
execution policy. A target being visited, a dependency notification, or a
timestamp changing is not by itself evidence of changed consumed content.

Reasons cover:

- Missing, invalid, or incompatible saved records; observations that cannot be
  validated are reported as an unverifiable proof, not as changed content.
- Changed consumed input signatures, including content, existence, metadata
  when actually observed, selected definition values, tool or operation identity,
  and dependency membership.
- Missing or modified declared outputs.
- Changed implementation or source/context guards, resolved settings, and
  recorded environment dependencies.
- Explicit bypasses such as `--force`, `always`, and uncached tasks.

A source/context guard mismatch explains why reevaluation is needed; it does not
assert that recipe behavior or output changed. Reevaluation may establish an
unchanged result and reuse the artifact. Likewise, a changed prerequisite that
publishes the same consumed value does not imply a downstream rebuild. Reasons
identify whether the decision requires reevaluation or execution; execution and
completion remain separate lifecycle events.

Explanations use the observations and comparisons already required for the
decision. They must not evaluate additional expressions, run processes, read
extra resources, or enlarge the dependency set merely to obtain a reason. The
runtime need not enumerate every possible mismatch after finding enough evidence
to reject reuse, but must not claim unexamined dependencies changed.

For a whole-child-environment digest mismatch, the reason is `process environment
changed`. It must not name particular changed variables unless existing evidence
establishes them. A named environment observation may identify its variable name.
Neither explanation exposes values or old/new fingerprints. Aggregate evidence
does not justify guessing that a profiler variable or other incidental name was
responsible. Reporting does not exclude or normalize any environment variable,
nor change the environment dependency contract above.

Reasons are emitted once per distinct decision, reason, and affected resource in
a target generation, not repeatedly on resumed attempts. Shared prerequisites
report under their own target identity. Unchanged reuse emits no change reason.
Presentation, privacy, and JSON transport follow `027-job-presentation.md` and
`009-cli.md`.

## Inputs Changing During Production

A build must report detected changes to tracked file inputs while it is producing
outputs. This applies to one-shot builds as well as watch cycles. The purpose is
to distinguish successful execution from confidence that its outputs correspond
to stable inputs; it does not provide an immutable filesystem snapshot.

### Observations and scope

An input observation associates a canonical file resource and its consumed aspect
with the signature used by the build. The build retains the relationship between
that observation and its consumers, including shared and dynamically discovered
dependencies. Identity follows [Content identity](#content-identity), not output
timestamps or notification arrival alone.

Known inputs have baseline observations before their dependent production begins,
after required prerequisite production finishes. A dynamically discovered input
joins validation when it is first observed; no claim is made about changes before
that observation. Generated prerequisites are inputs to their consumers, but their
expected production is not itself an input-change warning. Order-only inputs are
excluded unless upgraded to consumed dependencies by a normal edge or read.

Before reporting successful build completion, the runtime revalidates these
observations against the host. A changed signature, disappearance, or appearance
of a previously observed missing path establishes an input change. Unchanged
content with a changed timestamp does not. An unreadable input or otherwise
unavailable signature is reported as unverifiable, not asserted to have changed.
This validation may perform additional reads of tracked inputs; the restriction
on reads solely to explain reuse decisions does not prohibit it.

The scope is tracked file observations, not arbitrary paths opened inside opaque
processes. Validation does not discover additional dependencies or widen grants.
Before/after observations need not detect an edit that is reverted between them,
and no guarantee covers edits after the final observation. Independently detected
changes during production remain evidence even if the final signature matches
the baseline. Validation is not filesystem locking or transactional isolation.

### Outcome and recovery

When changes are detected, the build emits one consolidated warning listing the
affected file paths without duplicates, using authored or working-directory-relative
paths. It states that outputs may be inconsistent and, for a one-shot build,
advises running the build again. Unverifiable inputs are identified separately
from confirmed changes. Unchanged, verifiable inputs produce no warning. Ordinary
diagnostic presentation and privacy follow `027-job-presentation.md` and `009-cli.md`.

The warning alone does not change the exit status, convert successful recipes to
failures, or trigger an automatic retry in a one-shot build. Existing execution
failures retain their ordinary status. Already written files and external effects
are not rolled back.

A result affected by a changed or unverifiable input must not become an accepted
freshness or cache proof. This also applies to results derived from it; unrelated
results retain ordinary reuse eligibility. Any provisional acceptance must be
invalidated before completion is reported. The next build must reevaluate affected
work rather than accept the suspect outputs because their timestamps are newer,
their bytes happen to match, or a record was written before validation. A later
successful production with validated inputs may establish a new accepted record.
Watch scheduling and cancellation follow [020 Watch](020-watch.md#input-change-reporting).

## Materialization Result

Materializing a file target returns its path and status; it does not read the
file into memory. Materializing a definition returns its value. Materializing a
task returns status and retained execution metadata. Reading exact artifact
bytes is a separate operation used by `cat`.

## Acceptance Tests

- Reuse rejection reports its established reason for changed inputs, membership,
  outputs, implementation/guards, settings, and environment observations;
  unavailable proofs and explicit policy bypasses are distinguished from changes.
- Guard-triggered reevaluation with an unchanged result does not claim execution;
  equal-result dependency revalidation and successful no-op reuse emit no false
  downstream change reasons.
- Resumption does not duplicate a reason within a target generation; later
  generations can report the same reason again.
- Explanations preserve dependency sets, freshness results, process counts, and
  output timestamps, and perform no additional host reads or evaluation.
- Native and WASM report equivalent reasons and omit environment values, source
  contents, captured process data, and old/new fingerprints.

- Compilation performs no filesystem or process operation.
- A literal rule wins over a matching template rule.
- A structurally more-specific file template wins regardless of source order;
  equal or incomparable remaining matches produce `TGT_AMBIG`.
- Captures render corresponding input and output templates.
- Bare symbolic dependencies select rules before definitions with the same
  name; explicit definition evaluation remains available through `@{NAME}`.
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
- `[shell: kame]` publishes exact text or bytes for one local file output and does not start a process.
- Shell-template expansion and Kame content computation reject output actions, processes and clocks with `PHASE_INVALID`.
- Direct Kash `yield!` writes one file output and rejects tasks or multiple outputs.
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
`./build/{stem}.o` and `@{stem}` in its recipe. Output and first-input directories
use `@(dirname @>)` and `@(dirname @<)`.
