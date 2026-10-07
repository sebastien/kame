# Named arguments for build targets

## Rule declarations

A named rule may declare zero or more standalone argument blocks after its
single target name:

```kame
deploy {environment} {region=us-east} : configure @(environment) @(region)
	ship @(environment) @(region)
```

After an optional `task` prefix, the first token is the literal target name
and every following standalone brace block is an argument declaration. A
leading target-template block such as `task {name}` retains its existing
meaning as a template output.

`{name}` declares a required argument. `{name=literal}` declares an optional
argument with a literal string default. The declaration is part of the target
header, not an additional output or a target-template capture. It is valid only
on a single named task target; file targets and multi-output rules keep their
existing capture semantics. Argument names follow definition identifier rules,
are unique within the declaration, and cannot collide with target captures or
reserved selectors.

Defaults are literal text and are not evaluated as expressions. Empty defaults
are allowed; names and values cannot contain whitespace or NUL. A declaration
without arguments preserves the current rule grammar and behavior.

## Invocation

A caller supplies named assignments after the target:

```sh
kame deploy environment=production region=eu-west
kame deploy environment=production
```

Each assignment must name an argument declared by the selected rule and may
appear at most once. Missing required arguments, unknown names, duplicate
assignments and malformed assignments fail before any dependency or recipe
runs. Omitted optional arguments bind their declared literal defaults. Argument
values are text; each assignment occupies one CLI operand. Values may be empty
but cannot contain whitespace or NUL. They do not become CLI options, target
names, or program arguments.

A target assignment token is consumed as an argument only when it follows a
single explicitly selected target that declares the matching name. Existing
multiple-target invocations and targets containing `=` retain their current
behavior when no matching declaration applies. Inspection and watch commands
use the same binding rules as execution. Plan JSON exposes resolved values in
an `arguments` object. `--dry-run` validates the same bindings without running
recipes.

## Scope and identity

Bound arguments are immutable string symbols in the selected rule's scope. They
are available while resolving dependencies, rendering inputs and outputs,
selecting tools, computing cache identity and evaluating the recipe. A local
argument shadows a global definition only when the declaration explicitly uses
the same name; that collision is rejected to keep
configuration unambiguous. File-pattern captures and named target arguments
remain separate maps and are both visible in plans and diagnostics.

The complete ordered argument map, including defaults, is part of the rule
instance and cache identity. Reordering invocation assignments does not change
identity. Changing any value or default creates a distinct instance. Two
requests with equivalent bindings share prerequisites and a running task.

`do plan`, `do inputs`, `do outputs`, `do span` and `do tools check` accept the
same assignments and expose the resolved values as structured plan data without
running recipes. `--dry-run` validates and resolves arguments identically but
performs no effects. JSON diagnostics identify the target and argument name
without exposing unrelated environment values.

## Acceptance

- Required and optional arguments parse, format canonically and bind through
  dependencies, definitions, and recipe evaluation on native and WASM.
- Missing, unknown, duplicate, malformed and conflicting arguments fail before
  filesystem, process, cache-write or output-publication effects.
- Literal defaults do not evaluate Kame expressions or perform host requests.
- Assignment ordering is canonical; changed values change cache identity;
  equivalent values share one prerequisite and one task execution.
- Rule captures and target arguments remain distinct in selectors, plans,
  diagnostics, inspection and cache records.
- Multiple targets, `do run` program arguments, CLI options, and legacy literal
  targets keep their existing interpretation.
- Native and WASM execution, watch invalidation and JSON output agree.
- Native and WASM watch preserve argument values across invalidation; dry-run
  and plan JSON report or validate the same bindings without effects.
