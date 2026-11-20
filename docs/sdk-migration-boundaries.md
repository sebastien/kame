# SDK migration boundaries

Use these boundaries when migrating SDK workflows: preserve Kame's value and
process semantics, and keep vendor-specific policy in the SDK. Language support
does not imply complete SDK workflow coverage.

## Truth is not emptiness (K24)

Only `nil` and `false` are false. Zero (integer or float), empty strings,
empty lists, empty records, and empty bytes are true. `bool`, `not`, expression
conditions, and predicate filtering share that rule. `and` and `or` short-circuit
and return an operand, not a coerced boolean. A successful process exit is a
separate Kash command-condition contract; it does not make numeric zero false.

Use a predicate that matches the value the workflow needs to distinguish.

```kame
def nonempty value = (count value) > 0
if nonempty FILES then "build" else "nothing to build"
if LIMIT != 0 then "nonzero" else "zero"
if OPTIONAL != nil then "present" else "missing"
```

`count` accepts strings, bytes, lists, and records, not numbers, booleans, or nil.
For an optional collection, `OPTIONAL != nil and (nonempty OPTIONAL)` checks
that the collection exists before counting its elements. A list containing nil
and a record with a nil-valued field are nonempty. Whitespace-only text is
nonempty. Use `count (strip TEXT)` to count characters after trimming whitespace.
Source-composition `if` uses the same truthiness as runtime `if`; use `nonempty`
when selection depends on collection membership.

## Capture is data, not an identifier conversion (K25)

`$(COMMAND)` preserves successful valid UTF-8 stdout exactly: leading/trailing
spaces and tabs, embedded line endings, CRLF, and every trailing newline.
Stderr stays separate, and captured data is never reparsed or shell-split.
Invalid UTF-8, capture overflow, and unaccepted process failure remain errors;
streaming/redirection, not textual substitution, is the path for arbitrary bytes.

Convert only at the consumer boundary. `(strip $(git rev-parse HEAD))` is an
explicit broad-whitespace policy suitable for an identifier whose format excludes
whitespace; it is not a default for paths, templates, JSON, or other captured data.
When a producer promises one terminal LF, remove exactly that framing byte:

```kame
(line TEXT)
```

This preserves leading whitespace, trailing spaces/tabs, embedded newlines, and
any earlier terminal LF. An all-newline input loses only one LF. It also leaves
a preceding CR intact: remove two bytes only when the producer explicitly
promises a CRLF terminator, rather than guessing whether CR belongs to the data.
For example, a path containing a terminal LF must retain that LF after removing
the additional LF printed by a line-oriented path tool.

`line` removes exactly one terminal LF, not all terminators or an entire CRLF.
Whitespace and newline-only tests cover this contract. Trimming remains opt-in.

## Failure cleanup is an ownership question (K26)

SDK cases from `sdk.km/src/km/sdk`:

| Case | Existing behavior / real need | Boundary |
| --- | --- | --- |
| HTML Tidy and XSLT | Direct writes can leave partial output after an interruption or replace a previous good artifact. | Stage output in an invocation-owned temporary path, preserve the last good output, and publish only after the transform succeeds. |
| Tool downloads | An interruption before download or `chmod` completes can leave an unusable executable. | Stage downloads in an invocation-owned path. The SDK controls authentication, retries, and installer policy. |
| Compilers and bundlers | Redirections and external tools can leave partial main files or side assets. | Use the [output ownership contract](spec/039-output-ownership.md) for variable asset membership. Validation and rollback are separate operations. |
| Managed links | Preparation preserves user-owned files. Removal requires a manifest. | Validate the ownership manifest and account for races when removing stale links. Treat removal as reconciliation. |
| Release and deployment | Push failure may leave a local tag. Deployment may fail after making remote changes. | Preserve the failure and support explicit retry or reconciliation. Leave tags and remote state for explicit recovery. |
| Terraform | A failed provider operation may leave useful state, logs, and partial remote changes. | Preserve state and diagnostics for provider recovery. The SDK controls workspace synchronization and deletion. |

The runtime cancels/reaps owned process graphs and services; it does not remove
external programs' files or reverse earlier effects. Choose the right cleanup scope:

- [`finally!`](spec/043-kame-finalization.md) scopes body and cleanup callables in
  native/WASM execution contexts, with bounded cancellation, async ownership and
  primary-error preservation. It does not bracket deferred shell recipes.
- Kash [`defer COMMAND` / `defer (EXPRESSION)`](skills/kame/ref/kash.md#deferred-cleanup)
  registers invocation-scoped actions. They run LIFO after user work joins, or
  after cancellation/reaping on failure, preserving primary errors and grants.
- Kash `if` and command fallback handle recoverable failures, not cleanup on
  capability denial or cancellation. A separate `clean` target is not a finalizer.

Neither finalizer safely acquires resources on its own, proves deletion ownership,
or makes redirection/publication transactional. Fresh private workspaces remain
the author's responsibility; temporary-resource APIs, atomic publication of
external artifacts, and remote rollback are separate contracts. Native `write!` and returned rule content publish individual files.

Test failed publication and interruption, not only nonzero tool exits. Do not add
persistent mutable Bash sessions, sourcing or traps merely to transliterate shell
libraries. An explicit external-shell recipe remains an authored boundary.

## Vendor tools and SDK workflows stay outside the language (K27)

Kame composes typed argv/process graphs, scoped environments, tracked inputs and
declared outputs. Project/vendor CLIs own compilation, formatting, authentication,
package installation, infrastructure operations, and remote deployment.
CSS-producing JS and the uicss API must use an existing vendor CLI or a
project-owned CLI; neither is a request to import and execute arbitrary JS exports
inside Kame. If no suitable CLI is supplied, fail explicitly rather than silently
embedding an SDK-owned JS runtime adapter.

Keep the following in SDK workflow planning:

- **Automatic installation:** version selection, download verification, package
  manager/privilege consent, and opt-in setup; never install or authenticate during
  source loading or graph registration.
- **Release automation:** explicit version/bump/commit/tag/push/deploy policy,
  clean-tree checks, and resumable remote operations; do not auto-commit unrelated
  user changes or pretend remote actions are a transaction.
- **Terraform synchronization:** managed stale-source removal, dependency-module
  staging, persisted outputs/schemas, and credential integration; vendor CLIs
  still execute the actual operations.
- **Legacy HTML rewriting:** authored template annotations or a supplied project
  template, not implicit regex rewriting or silently removed scripts.

Escalate only a minimal language-capability reproduction: typed argument loss,
missing scoped environment support, tracked input/output ownership, unsafe
publication, or a concrete cancellation-cleanup requirement. Keep that request
separate from the vendor-specific implementation and its live integration tests.
