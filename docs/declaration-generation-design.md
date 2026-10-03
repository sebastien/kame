# Computed configuration and declaration generation

The minimum configuration lookup requested in TODO-GAPS A5 uses records and
`(get RECORD KEY [DEFAULT])`. A key can be computed from other definitions,
arguments, or ordinary pure operations. The selected value retains its type;
a list selected by `get` can directly supply a rule's inputs. Missing keys return
nil or an explicit default. Selectors still provide concise static field access.

```kame
profiles = [debug: [flags: "-O0" inputs: [./debug.c]] release: [flags: "-O2" inputs: [./release.c]]]
profile = "release"
configuration = (get profiles profile)
./application : @(configuration.inputs)
	cc @(configuration.flags) @<* -o @>
```

This is explicit configuration data. It does not execute strings as source or
create new global names as a side effect of reading a value.

## Proposed generated declarations

General rule emission remains a design proposal. It is optional beyond the
minimum record lookup and has no currently accepted executable syntax. The
proposal is a bounded declaration batch evaluated before target selection,
under the same policy and planning phase as ordinary build configuration.

A batch would contain records with a rule kind, target template, typed input
list, recipe templates, and optional environment/ordering metadata. Targets and
input paths would use the existing pattern and canonical-path rules. Recipes
would remain authored templates; generation would construct declarations rather
than invoke an `eval` operation over arbitrary source strings.

The batch must be validated completely before registration: target shape,
pattern captures, recipe types, duplicate literal targets, overlap checks where
possible, and resource limits. A failed batch registers no partial declarations
and performs no recipe effects. Host-dependent predicates may use authorized,
dependency-tracked reads; they cannot run shell commands during planning.

Generated declarations must retain the source of the generator and each field's
span. Diagnostics would point to the generator and identify the emitted target,
so a bad target does not become an anonymous synthetic-source error. The program
owns the retained values and lowered ASTs and frees them with ordinary parsed
and materialized rules. Dependencies of a generator belong to the declaration
batch, and changing them invalidates its target set before dependent work starts.

The plan would expose the generator identity, its effective input values and
resources, and every emitted rule. Cache fingerprints must include the emitted
recipe, environment, inputs, and generator dependencies. Native and WASM must
share validation, lowering, registration, diagnostics, and fingerprinting; hosts
only service authorized reads.

Before implementing a syntax, acceptance coverage must pin all-or-nothing
registration, authored diagnostics, deterministic target selection, dynamic
membership changes, cycles, cancellation, limits, and freeing failure paths.
Rule generation cannot silently enlarge grants or allow process execution in
planning. This design is recorded so a future implementation can be reviewed
against a concrete contract instead of introducing an unrestricted parse-time
shell or string-evaluation escape hatch.
