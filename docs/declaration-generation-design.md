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

## Generated declarations

`generate NAME = EXPR` is a top-level declaration. `EXPR` evaluates to a list
of records before target selection. It can use ordinary pure Kame operations,
functions, and earlier value definitions; it cannot run processes, write files,
or issue host requests. A generated batch is data, not source text: Kame never
parses a string returned by a generator as a declaration.

Each record has these fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `kind` | string | `file`, `task`, or `cached-task` |
| `target` | string | One concrete target name or path |
| `inputs` | list of strings | Required targets or paths, in declaration order |
| `order-only` | list of strings | Ordering prerequisites that do not affect freshness |
| `recipe` | list of strings | Authored recipe template lines |

`kind`, `target`, `inputs`, and `recipe` are required. Unknown fields, invalid
types, empty targets, NUL bytes, invalid target forms, and unsupported rule
kinds reject the complete batch. A generator name is unique within one source
program. Generated targets retain the ordinary target selection and execution
semantics; generated recipes use the normal Kame template syntax.

For example, a pure `map` can produce one task per configured module:

```kame
MODULES = ["core" "cli"]
generate module-checks = (map ([module] [kind: "task" target: (join (list "check-" module) "") inputs: [] order-only: [] recipe: ["go test ./..."]) MODULES)
```

The runtime evaluates and validates every batch before adding any generated
rule to target selection. It checks duplicate targets both within a batch and
against ordinary rules and other batches. It enforces bounded record counts,
field sizes, and total retained batch bytes. If any batch fails, compilation
returns an authored diagnostic and exposes no executable program.

Generator identity, the referenced definitions, and each emitted rule's full
kind, target, inputs, ordering inputs, and recipe are retained for inspection
and cache fingerprinting. File prerequisites are ordinary tracked build
dependencies. When a referenced source definition changes during watch, the
source program is recompiled and the generated target set is replaced as one
unit. Native and WASM share parsing, evaluation, validation, lowering,
registration, diagnostics, and fingerprinting.

Diagnostics identify the generator declaration and name the emitted target
when a record fails validation. The program owns generated records, lowered
rules, and recipe templates, and releases them on every compile failure and
program disposal path. No partial batch is observable after failure.

Required acceptance coverage includes all-or-nothing registration, authored
diagnostics, deterministic target selection, changed definition inputs and
watch replacement, duplicate targets, cycles, limits, and allocation cleanup.
Generation cannot enlarge grants or perform effects during compilation.
