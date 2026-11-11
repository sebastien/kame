# Kash module loader implementation plan

`docs/spec/050-kash-libraries.md` is the normative contract. This plan covers the
module loader and demand-specific freshness work; it does not migrate
`sdk.km` or introduce another include spelling.

## Current status

- Flat `.kash`/`.ksh` includes, Kash single-quoted command words, mixed-language
  fragment compilation, and definition-only library validation have been added.
  `tests/T050-01-kash-rule-includes.sh` exercises native and WASM paths.
- Literal file-backed module loading, lazy fields, retained source origins, and
  evaluator-only host request kind 42 are implemented. Ordinary operations still
  cannot submit source-load requests; unsupported hosts fail explicitly.
- Signed file-backed sources use reached-definition content observations.
  Unsigned embedding fragments retain whole-context fingerprinting. Stream
  filters conservatively observe every loaded signed source; precise callback
  demand tracking is deferred, as documented in spec 050.
- The sections below retain the implementation and verification checklist;
  verification results must distinguish executed backends from unavailable ones.

## 1. Source loading and suspension

1. In the evaluator, recognize the authored one-operand literal path form, verify
   the explicit path prefix, suffix and absence of patterns/interpolation/NUL,
   and resolve lexically from the expression's source origin. Inline, stdin and
   constructed text return `SEL_NO_CONTEXT`. Quoted strings continue through
   `(kash TEXT)`, including path-looking strings.
2. Add an evaluator-internal source-loading submission path for validated authored
   literals. It must not route through ordinary read authorization or expose a
   public arbitrary-read primitive. Preserve host authority and inheritance for
   native, Node/WASM, and custom embedders; an unsupported embedding must fail
   explicitly. Review wire kind 42, payload validation and protocol documentation.
3. Retain a per-evaluation loader state across host suspension, keyed by the
   authored source origin, operation span/call path and engine generation.
   Correlate completions, free partial state on failure, cancellation or
   invalidation, and do not repeat
   completed loads on resume. Parse bytes as Kash and apply
   `script.ValidateKashLibrary` before exposing a value or executing anything.
4. Expand required nested includes depth-first using each containing file's
   lexical directory and Kash grammar regardless of suffix. Track the active
   lexical path ancestry for cycles; reject duplicates under ordinary
   registration rules. Retain included spans, source directories and include
   frames. Preserve repeated nonrecursive inclusion behavior.

## 2. Module value and lazy fields

1. Add an opaque invocation-owned module value, not a record or callable. Define
   its representation, cloning, destruction, display (`<kash-module>`),
   publication eligibility and stable identity across engine snapshots. Its
   owner must outlive values and borrowed callables; ASTs and scopes must be
   released exactly once on shutdown, cancellation and failed loads.
2. Retain the library's parsed source and lexical scope. Register definitions
   without evaluating lazy values or running `def` bodies. Definitions from
   nested includes share that lexical scope, but record only the root file's
   own top-level names as module fields. Preserve each definition's original
   source origin for `(source-dir)` and diagnostics.
3. Route dotted field selection in `lang/eval/reference.go` and `(get KEY
   module)` through one lazy field-demand implementation. Support suspension,
   missing-field behavior and nested helper calls. Preserve `reference` stopping
   and resuming on `Waiting` after each reference part. Reject all other record
   operations with `EXPR_INVALID`, and do not make module values callable.
4. Preserve existing lexical scoping, duplicate checking, call arguments,
   failure propagation, recursion and operation-state ownership. Do not publish
   callable bodies into the persistent cache or reinterpret any argument as
   source. Test both value and function fields, including unused effectful lazy
   definitions and `get` with its existing optional-default semantics.

## 3. Freshness, dry-run and watch

1. Record content observations for the defining file when a recipe reaches an
   included definition or module field, and for nested included files whose
   definitions it actually reaches. Record canonical file identities and byte
   digests, not mtimes. Propagate observations through function and lazy-value
   calls, including across suspended work and cache restoration.
2. Separate safe whole-source preflight invalidation from semantic recipe
   identity. A preflight guard may conservatively force reevaluation, but an
   unreached library must not change the final recipe fingerprint or rebuild an
   output, except for the accepted conservative stream-filter fallback. Replace
   the Kash whole-source implementation guard only after tests show all reached
   functions, closures, predicates and dynamic observations are
   tracked; preserve safe fallback when identity cannot be proven.
3. Give retained module and `(kash TEXT)` values a stable, nonpersistent identity
   sufficient for dependency validation without serializing executable code.
   Spec 008 tag 11 remains the `"kash"` constructor identity. An unchanged
   constructed script in a definition must not make the recipe rebuild.
4. Make watch retain selected flat `.kash`/`.ksh` includes as source roots and
   reached module paths as file dependencies. Dry-run may load and parse demanded
   sources but must not execute function bodies or other effects.

## 4. Verification and completion

- Add focused evaluator tests for module identity and ownership, lazy selection,
  source-relative resolution, suspension/replay, cleanup, missing fields,
  duplicate names, cycles and malformed/invalid source. Include a test proving
  ordinary operations cannot forge source-load requests.
- Add native and Node/WASM CLI cases for the full acceptance list in spec 050:
  direct and `get` field calls; quoted text; no-context and different-cwd cases;
  nested helper visibility; invalid top-level commands with source/include
  frames; unchanged second runs; changed bytes with preserved mtime; unchanged
  `(kash """true""")`; and dry-run without body effects.
- Test an unreached nested library change does not rebuild, while changes to a
  reached library do; include watch invalidation, cancellation, denied grants,
  optional/conditional flat includes, and unsupported host behavior.
- Run focused `so test` packages, the existing Kash suites, and
  `tests/T050-01-kash-rule-includes.sh` plus new module integration tests on both
  backends. Exercise memory/lifetime checks and run `git diff --check`. Record
  actual results and any unsupported host instead of inferring support from a build.
