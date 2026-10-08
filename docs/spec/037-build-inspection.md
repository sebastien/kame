# Build inspection

## Purpose

`do plan`, `do inputs`, and `do outputs` describe the build reachable from the
selected roots, not just one rule header. They share resource identity,
read-only dependency resolution, classification, provenance, and completeness
rules. Native and WASM hosts expose the same information.

This contract refines `006-runtime.md`, `009-cli.md`, and `036-cli_ansi.md`.
Inspection describes planned work; it neither materializes artifacts nor claims
that every reachable producer will execute.

## Selection and traversal

```text
kame do plan [--depth N] [TARGET...]
kame do inputs [--depth N] [TARGET]
kame do outputs [--depth N] [TARGET]
```

All three commands default to unlimited depth (`-1`). Target selection,
configuration, captures, and target arguments follow the ordinary build rules.
`plan` accepts multiple roots in one shared graph; `inputs` and `outputs` accept
one root. Without operands, select `default` or report `TGT_NO_DEFAULT`.

- Depth `0` reports root identities only, with no dependency edges or input/output
  inventory. Depth `1` reports the selected producers' direct inputs and outputs.
- Each additional depth visits the producers of the preceding level's inputs.
  Depth `-1` follows all reachable inspectable dependencies.
- Read-only computed inputs are resolved automatically at every visited producer.
  Follow their producers as well as producers reached through literal inputs.
  A wildcard reports its concrete matches, not just its definition or pattern.
- Include generated intermediate inputs and recurse into their producing rules.
  Do not include unrelated rules or instantiate every possible pattern target.
- Resource deduplication uses canonical typed keys, not display strings. Producer
  sharing follows rule-instance identity, including captures and bound arguments.
  Sibling outputs share one producer and appear once each.
- A shared resource's shortest distance from any selected root controls depth
  inclusion; discovering it first through a longer branch must not hide its inputs.
- Preserve all consumer/provenance edges when resources or producers are shared.
  A diamond is not a cycle. A reachable dependency cycle is `DEP_CYCLE` with the
  closed dependency path, not a silently shortened report.
- A path without a producer is an external input. An unknown logical prerequisite,
  ambiguous producer, invalid expression, denied read, or failed host operation
  remains an error; traversal must not discard descendant diagnostics.

A finite depth is an explicitly truncated view. Report the requested depth and
truncation when a known dependency branch extends beyond it. Do not represent an
unvisited branch as an empty or fully resolved build.

## Resource classification

Keep kind, role, and lifetime independent. A path's spelling or directory does
not establish whether it is temporary, an intermediate, or a final product.

| Dimension | Meaning |
| --- | --- |
| Kind | Typed resource: `file`, `target` (bare task), `task` (cached task), `service`, `definition`, `glob`, `environment`, `tool`, or `operation` |
| Role | One or more of `input`, `artifact`, `intermediate`, `product`, or `configuration` in the selected graph |
| Lifetime | `persistent`, `invocation`, `generation`, `interest`, `host`, or `unknown`, when established by the resource contract |

A source file has no selected file producer. An artifact is a declared file
output of a selected producer. An artifact consumed by another file producer is
an intermediate; an explicitly requested file or a reachable artifact not
consumed by another file producer is a product. One artifact may have multiple
roles, including input and artifact, or intermediate and explicitly requested
product. Logical aggregate consumers do not turn their file products into
intermediates.

Filesystem artifacts are persistent: Kame does not delete them when interest
ends. Intermediate does not mean transient. `mem:` resources are host-scoped,
not promised to persist beyond their host. URI display and identity follow
`031-resource-protocols.md`; do not expose private host mappings.

Bare-task completion is invocation-local; definition and operation values are
generation-local. Cached-task values are also generation-local, with reusable
completion records rather than declared file artifacts; explain their cache
policy separately from value lifetime.
Services are interest-scoped and retained until final-owner shutdown and reap
as specified by `024-managed-services.md`. Neither a task name, cached record,
definition value, nor service-ready value is a file artifact.

## Inputs and outputs

`inputs` inventories all reachable input files, including generated inputs,
and identifies their consumers and whether they are external or produced. Show
logical prerequisites, definitions, globs, tools, and environment dependencies
in separate labeled sections, not as filenames. Include known build sources,
selected includes, and generator/configuration resources that influence the
selected graph, labeled as configuration rather than recipe file inputs.

`outputs` inventories every reachable declared artifact, including intermediate
and sibling outputs. Identify each producer and distinguish products from
intermediates. Show reached logical targets, cached tasks, services, and
definitions separately. For an aggregate such as `default : build`, traverse
through `build` to its artifacts; neither name is itself an artifact.

The inventory includes products that may be reused. A declaration is not proof
that an output exists or that a producer will run. Freshness stays `unknown`
unless accepted records and relevant observations have actually been verified.
Inspection verifies artifact status through authorized content observations and
existing accepted records; it does not substitute timestamp guesses for proof.

Each dependency identifies its consumer, origin (`declared`, `computed`, or
`configuration`), normal versus order-only purpose, and authored sequence group
when applicable. Keep configuration inputs distinct from inputs supplied by
recipe selectors. Do not dump unread environment entries or private dependency
values; identities and established relationships are sufficient.

Configuration provenance is not a new runtime scheduling dependency. In
particular, generator definition dependencies retain the compilation contract
in `025-generated-declarations.md`; inspection must not attach them as execution
prerequisites or turn source-loading observations into executable stages.

## Plan and stages

`plan` combines these inventories with the selected producer details: source
location and rule, captures, arguments, relevant configuration and tools,
declared outputs, resolved prerequisites, freshness, and known reuse policy.
Unrendered recipe text may be shown as source, not as an executed or fully
expanded command. Metadata and generator provenance retain their established
meaning.

Show numbered **stages** of dependency-ordered producer work. External file
leaves are inputs, not executable stages. Shared and multi-output producers
appear once. Identify work that may proceed in parallel, subject to existing
job limits, service readiness, and other execution constraints.

Stages preserve normal and order-only prerequisite ordering and make authored
comma-sequencing barriers explicit. Sequence groups belong to their consumer:
later groups are requested only after earlier groups are current. Shared work
already needed by an earlier group is not delayed or repeated for a later group.

Stage numbers summarize known ordering, not a global execution barrier. Work in
a later numbered stage may begin as soon as its own prerequisites and local
sequence gates permit, without waiting for unrelated work. Stages do not change
scheduling, predict durations, establish freshness, or promise an exact runtime
timeline. Dynamic discovery and reuse may change the observed work. Truncated
or deferred graphs must label their stage view as partial.

Runtime phases (`compile`, `plan`, `schedule`, `render`, `execute`) and watch
cycles remain different concepts; neither is called a build stage.

## Read-only boundary and completeness

Inspection may perform authorized reads, metadata observations, and glob or
definition resolution needed to discover input resources. It does not run
recipes, provision tools, start services or processes, render recipe bodies,
commit effects, create output directories, or publish build/cache records.
Existing grants and phase restrictions remain in force; inspection does not
broaden authority to obtain a more complete report.

Report the scope as **declared and read-only resolved**. Record unresolved
discovery explicitly, with the affected producer or resource and its reason:

- Dependencies or writes discoverable only during recipe rendering/execution.
- Computed discovery needing an unavailable generated file. Report its known
  producer but do not run it to obtain the file or guess the discovered resources.
- Outputs of opaque shell side effects, which are not declared artifacts.

Use stable deferred reasons `runtime-discovery`, `generated-input-unavailable`,
and `opaque-process-io`, respectively. These describe inspection boundaries,
not successful execution or a suppressed diagnostic. Where a producer has an
opaque process recipe, the report notes that its undeclared I/O is unknown;
known declared inputs and outputs remain in the inventory.

An absent declared external input can be listed without probing it. When an
inspection expression requires a read, propagate the read diagnostic unless the
absence is established to be an unavailable generated input. Other failures,
including capability denial and phase violations, are not completeness notes.

No report may print "no inputs" when input discovery is deferred. Distinguish a
known empty set from a partial inventory. Resolving a declared graph is not a
claim to know every file an opaque process could read or write. Do not infer
such I/O by parsing shell text or present a previous build's observations as
facts about the current invocation.

## Human presentation

ANSI and text contain the same identities, reference counts, stages, and
completeness information. ANSI adds styling only. Root headings
identify the direction, depth, and inspection scope; bodies separate file
inventories from logical/value/service resources.

List every discovered file. Do not silently collapse a wildcard to a pattern,
replace inputs by the name of a definition, or clip a static inventory to terminal
height. Each inventory entry occupies one line containing its display identity
and a trailing `[N]`: the number of distinct referencing producer identities in
the selected graph, including its producing target when present. Repeated edges
from one target count once; sibling outputs share one producer identity. Counts
describe the inspected graph, not omitted or deferred references.
Do not append classification metadata or nested producer/consumer details to
inventory entries. Detailed target information remains available through
`do plan TARGET`, and full classification and relationships remain in JSON.
Discovery boundaries use the same path-first layout: one line per affected
resource, followed by its distinct attributes in discovery order (for example,
`./build/app · runtime-discovery · opaque-process-io`). When no specific resource
is known, use the producer's display identity. Shared boundaries are grouped by
display identity; the JSON deferred records retain their producer provenance.
Artifact inventory entries additionally carry a status attribute:
`built` (the accepted signature proof verifies current reuse), `outdated`
(a known signature mismatch or policy requires rebuilding), or `missing`
(absence is established). Use `unknown` for denied/unavailable observations,
unverified execution context, or a source/settings guard requiring recipe
reevaluation. Existence and timestamps alone never establish `built`.
Verification may read existing proof records and resource bytes, but must not
render recipes, execute producers, or update proof/cache records. Generated
prerequisites with unresolved freshness prevent a `built` claim downstream.
Schema-2 artifact resources expose the same value in their `status` field.
Unknown lifetime or freshness in detailed plans is written as unknown, not
fabricated from naming conventions.

For a build with aggregate targets `default`, `build`, and `dist`, two native
binary products, and a generated version file consumed by both binaries, the
report separates the three logical targets from the file inventory. JSON
distinguishes the products and persistent intermediate version artifact.
The input report includes the
concrete source set and the version producer's own inputs. The plan shows the
version producer before the binaries and their parallel eligibility, without
claiming that either binary must be rebuilt.

## Structured output and compatibility

These three commands use schema-2 inspection documents under `--json` or
`--output json`. This is an intentional replacement of schema-1 single-rule
plans and untyped graph string arrays; do not silently change their meaning
under the old schema. No legacy compatibility flag is required.

Each successful invocation publishes one complete JSON document with:

| Field | Contract |
| --- | --- |
| `schema` | `2` |
| `type` | `plan`, `inputs`, or `outputs` |
| `targets` | Selected root request strings; resolved bindings are retained in producer records |
| `depth` | Requested depth, default `-1` |
| `scope` | `declared-and-read-only` |
| `truncated` | Whether known branches were omitted by depth |
| `resources` | Typed resource records with document-local `id`, canonical `key`, `display`, `roles`, `lifetime`, and artifact `status` |
| `producers` | Selected producer records with document-local `id`, `kind`, target identity, output resource IDs, and established plan/source/binding details |
| `dependencies` | Consumer producer ID, input resource ID, origin, `orderOnly`, and one-based sequence `group` where applicable |
| `stages` | Records with one-based `number` and `producers` (producer IDs), reflecting the known ordering |
| `deferred` | Discovery boundaries with a stable reason and affected producer/resource identity; source location when available |
| `items` | For `inputs`/`outputs`, resource IDs in the requested inventory; empty at depth `0`. Other reachable records retain provenance and supplementary classification. |

An output resource identifies its producer ID; all sibling outputs refer to the
same producer. Logical/value producers are identified as such, not converted to
file rules. The input inventory selects dependency resources; the output
inventory selects only declared file artifacts. Classification and relationships
remain available even when a resource is not in the requested inventory.

Resource `key` records contain `kind` and canonical `name`. Producer records use
`target` for the request identity, `resource` for their representative resource
ID, and `outputs` for declared file-output IDs (empty for logical/value producers).
Their `kind` is `file`, `target`, `task`, `service`, or `definition`. Retain
`source`, `rule`, `captures`, `arguments`, `configuration`, `tools`, `metadata`,
`environment`, `generator`, and `always` when established, with the existing
plan-data meanings. File producers include `freshness`, using `unknown` when
unverified. A resource's optional `producer` field references its producer ID.

Dependency records use `producer`, `resource`, `origin`, `orderOnly`, and optional
`group`. Configuration provenance has no sequence group and does not participate
in stage scheduling. Deferred records use `reason` and the affected `producer`
or `resource`, with optional source location. Empty collections are explicit;
unavailable optional details are omitted. `plan` omits `items`.

Document-local IDs are deterministic references, not heap pointers, scheduler
node IDs, or persistent cache identities. Emit stable discovery order following
root order, authored dependency order, and canonical glob order; preserve
sequence groups rather than alphabetically sorting them away. Host yields and
repeated ABI size/copy queries must not duplicate records or change IDs.

On failure, emit established diagnostic JSON with its own schema, never a partial
inspection document. Preserve all available source spans and dependency context.
JSON contains no presenter-generated ANSI and exposes no more private values
than the corresponding human report. Execution events, diagnostics, `span`,
tool lists, cache lists, and AST output retain their existing schemas/framing.

`span` remains an authored-static versus evaluation-dependent resource view with
its existing depth and explicit `--expand` behavior. It is not the implementation
of the new file inventories or a prerequisite command the user must run.

The WASM `kame_wasm_plan` query returns the same recursive schema-2 document;
its retained `expand` argument does not disable automatic discovery. The graph
query uses kind `0` for inputs, `1` for outputs, `2` for unchanged schema-1 span,
and `3` for plan. Kind `3` receives a JSON string array of roots in the target
buffer so multiple roots share one graph. Inspection queries may return
`HOST_NEEDED`; service only the authorized read-only request and retry. A size
probe or buffer-too-small retry is not a new inspection or permission to execute.

## Acceptance criteria

- Default recursive reports cross multiple aggregate targets and enumerate the
  concrete computed source set, generated intermediates, and their producer inputs.
- Literal, wildcard, computed, generated, pattern, URI, order-only, and sequenced
  dependencies preserve classification and provenance on both hosts.
- Multi-output producers and diamonds appear once per identity, retain all
  consumers, and preserve different argument-bound instances.
- An intermediate artifact is labeled persistent where appropriate. Bare targets,
  cached tasks, definitions, and services are never counted as file outputs.
- Stages show dependency ordering, parallel eligibility, and consumer-local
  sequencing without introducing global barriers or predicting actual execution.
- Depth `0`, `1`, finite recursive depths, and `-1` have the specified inventories
  and truncation labels. Unrelated rules and uninstantiated patterns stay absent.
- Missing generated discovery, runtime-only reads, and opaque writes yield explicit
  boundaries; empty, partial, and unknown remain distinct.
- Human boundaries group distinct attributes on one line per affected display
  identity. Artifacts report verified `built`, `outdated`, `missing`, or `unknown`
  status; preserved-metadata input changes and output tampering invalidate reuse,
  while source/context changes requiring reevaluation remain unknown. Status
  observation never rebuilds, repairs, or writes accepted records.
- Unknown logical prerequisites, ambiguous producers, input-resolution failures,
  and direct/indirect cycles preserve diagnostics and closed dependency paths.
- Inspection creates no artifacts, directories, cache records, process/service
  launches, or committed effects, including when a generated input is unavailable.
- Human inventories use one identity and distinct-target count per line without
  nested target details; selected-target plans retain details and JSON retains
  full classification and relationships. Multiple roots share
  one plan document; schema-1 execution/diagnostic/span framing remains unchanged.
- Native and WASM agree on normalized documents, grants, source locations, errors,
  asynchronous resolution, and repeated queries. Interrupted inspection releases
  retained roots and host requests without leaking or publishing partial results.
