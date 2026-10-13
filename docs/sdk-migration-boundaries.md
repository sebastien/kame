# SDK migration boundaries

The SDK review distinguishes existing language semantics from workflow policy.
These decisions cover K24–K27 in the SDK proposal ledger; they do not promise
complete SDK parity or authorize unrelated workflow implementations.

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
(without-final-lf TEXT) =
  (if (ends? TEXT "\n") (slice TEXT 0 -1) TEXT)
```

This preserves leading whitespace, trailing spaces/tabs, embedded newlines, and
any earlier terminal LF. An all-newline input loses only one LF. It also leaves
a preceding CR intact: remove two bytes only when the producer explicitly
promises a CRLF terminator, rather than guessing whether CR belongs to the data.
For example, a path containing a terminal LF must retain that LF after removing
the additional LF printed by a line-oriented path tool.

**Helper assessment:** no new native trim/chomp operation is required. Existing
`ends?`/`slice` express the one-terminator contract without recursion or changing
capture semantics. A future trailing-newline-only helper would need an explicit
one-versus-all and LF-versus-CRLF contract, including whitespace and newline-only
tests. It must remain opt-in; never implement it as automatic capture trimming.

## Failure cleanup is an ownership question (K26)

Concrete SDK cases, based on `src/kmk` in `sdk.km`:

| Case | Existing behavior / real need | Boundary |
| --- | --- | --- |
| HTML Tidy and XSLT (`www.kmk`) | Write an output-adjacent `.tmp`, move it on success, remove it on handled transform failure. Interruption or a failed move can leave the temporary file. | Need an invocation-owned temporary path and publication policy, not mutable shell state. Do not delete the previous good output or a concurrent producer's temporary file. |
| Tool downloads (`tools.kmk`) | Download to `.tmp`; handled download failure removes it. Failure during chmod/move or interruption can leave it behind. | The same owned-temporary-file need; installer authentication, retries and package-manager policy remain SDK work. |
| Compilers/bundlers (`js.kmk`) | Redirections and external tools can leave partial main files or side assets. | Successful output validation is not rollback. Variable asset membership first needs the [output ownership contract](spec/039-output-ownership.md); a cleanup block alone cannot establish ownership. |
| Managed links (`opencode.kmk`) | Prep preserves user-owned files; clean explicitly refuses without a manifest. | Removing stale owned links is reconciliation, not failure cleanup. Require a validated ownership manifest and race-aware deletion; `lstat`/`readlink` alone is insufficient. |
| Release/deployment (`release.kmk`) | A tag may exist locally after push failure; deployment can fail after remote changes. | Preserve the failure and support explicit retry/reconciliation. Do not automatically delete tags or undo remote work. |
| Terraform (`tf.kmk`) | A failed provider operation may leave useful state, logs, and partial remote changes. | Preserve state and diagnostics; let the vendor tool govern recovery. Workspace synchronization/deletion is separate, explicit SDK policy. |

The runtime already cancels/reaps owned process graphs and services. That is not
a promise to remove files written by external programs or reverse earlier effects.
Kash `if` and command fallback can handle recoverable process failures, but do not
run a cleanup arm on capability denial or cancellation. A separate `clean` target
is not an automatically invoked finalizer. Native `write`/`yield` publication
does not make a whole multi-output recipe or external command transactional.

**Decision:** defer general structured cleanup syntax. A proposal must first
pin down owned resources, cleanup timing on success/failure/cancellation, nested
and async lifetime, concurrent invocations, capability checks, and reporting of
cleanup failure without masking the primary error. Test failed publication and
interruption, not only a nonzero tool exit. Do not add persistent mutable Bash
sessions, sourcing, or traps merely to transliterate shell libraries. An
explicit system-shell recipe or user-launched interactive shell remains external
to Kash's language contract.

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
Documenting these boundaries completes the scope assessment, not those workflows.
