# SDK migration boundaries

Use these boundaries when migrating SDK workflows: preserve Kame's value and
process semantics, and keep vendor-specific policy in the SDK. Language support
does not imply complete SDK workflow coverage.

## Truth is not emptiness (K24)

Only `:nil` and `:false` are false. Zero (integer or float), empty strings,
empty lists, empty records, and empty bytes are true. `bool`, `not`, expression
conditions, and predicate filtering share that rule. `and` and `or` short-circuit
and return an operand, not a coerced boolean. A successful process exit is a
separate Kash command-condition contract; it does not make numeric zero false.

Ask the question that the workflow means:

```kame
(nonempty VALUE) = (> (count VALUE) 0)
(if (nonempty FILES) "build" "nothing to build")
(if (!= LIMIT 0) "nonzero" "zero")
(if (!= OPTIONAL :nil) "present" "missing")
```

`count` accepts strings, bytes, lists, and records, not numbers, booleans, or nil.
For an optional collection, guard missing values explicitly:
`(and (!= OPTIONAL :nil) (nonempty OPTIONAL))`. A list containing nil and a
record with a nil-valued field are nonempty. Whitespace-only text is nonempty;
testing `(count (strip TEXT))` instead is an explicit, different policy.
Source-composition `if` uses the same truthiness as runtime `if`; use `nonempty`
when selection depends on collection membership. Legacy `when` declarations
must migrate to `if` selecting branch files, not inline declaration bodies.

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
| HTML Tidy and XSLT (`www.kmk`) | Write directly to the final output; failed or interrupted transforms can leave partial bytes or replace a previous good artifact. | Need an invocation-owned temporary path and publication policy, not mutable shell state. Do not delete the previous good output or a concurrent producer's temporary file. |
| Tool downloads (`tools.kmk`) | Download directly to the final executable path, then chmod it. Failure or interruption can leave a partial or unusable executable. | The same owned-temporary-file need; installer authentication, retries and package-manager policy remain SDK work. |
| Compilers/bundlers (`js.kmk`) | Redirections and external tools can leave partial main files or side assets. | Successful output validation is not rollback. Variable asset membership first needs the [output ownership contract](spec/039-output-ownership.md); a cleanup block alone cannot establish ownership. |
| Managed links (`opencode.kmk`) | Prep preserves user-owned files; clean explicitly refuses without a manifest. | Removing stale owned links is reconciliation, not failure cleanup. Require a validated ownership manifest and race-aware deletion; `lstat`/`readlink` alone is insufficient. |
| Release/deployment (`release.kmk`) | A tag may exist locally after push failure; deployment can fail after remote changes. | Preserve the failure and support explicit retry/reconciliation. Do not automatically delete tags or undo remote work. |
| Terraform (`tf.kmk`) | A failed provider operation may leave useful state, logs, and partial remote changes. | Preserve state and diagnostics; let the vendor tool govern recovery. Workspace synchronization/deletion is separate, explicit SDK policy. |

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
external artifacts, and remote rollback are separate contracts. Native
`write!`/`yield!` publication is not a transaction for a multi-output recipe.

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
