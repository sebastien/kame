Porting references: [TODO-GAPS.md](TODO-GAPS.md) (capability gaps blocking a GNU Make port) and [KAME-BUGS.md](KAME-BUGS.md) (verified defects with repros).

Improve:
- [x] Operation diagnostics name the offending operand, its kind, and the expected types (e.g. `(first 1)` reports `first` argument 1 expects list; got int). `(out 10)` accepts integer-to-text coercion.
- [x] Native `@(x/cmd)` resolution is target-scoped; missing tools in unused rules do not block planning or execution.
- [x] Native `kame do tools check TARGETS...` checks selected dependency plans without executing recipes and fails if tools are unmet.
- [x] WASM tool paths and static/pure-computed `tools check` plans match native diagnostics; unavailable tools in unused rules do not block execution.
- [x] Omit captured process output from diagnostic causes; preserve status/signal and truncation metadata without changing live recipe streams.
- [x] Host-dependent WASM tool inspection services read-only host requests and preserves native diagnostic context without executing recipes.
- `./src/**/*.km` should really be a path marked as a wildcard, there should be no need for `(wildcard ./src/**/*.km)`
- Ensure that wildcards are lazily resolved to singleton sources, and that they can stream updates -- it doesn't need to work right away, as we'll add live updates later on.
- Support capture in target names, like `aws-shell@{role-account}`
- Support arguments in target names, like `deploy {env=ENVIRONMENT}` (an argument is a standalone capture block `{name}` (required) or `{name=value}` (optional), these then become symbols available in the dependencies and rule.
- Conditional forms
- Improved templates (```...```, proc`....`)
- Rename errors to be super clear and intuitive, maybe from UPPER_CASE to PascalCaseWithExplanation

Validate:
- Streaming capabilities of standard library
- [ ] security model of process execution, hopefully uses groups, etc

Research:
- We really need to design a nice CLI experience, it's quite bare bones for now
- How do we do live updates (incremental builds as things get loaded)
- How do we manage services running/provisioning
- Using kame as a general scripting language (view of replacing shell, so that you just get kame)
- File templating is also a common use case: replacing, repeating, etc.
- Having a cli that shows number of jobs, for each job what the program is, its arguments and its running time.

Consider:
- Terminal colors easy functions
- A JavaScript API?
- A python API?
- <- for stuff that builds (as opposed to targets)
- , for sequencing in dependencies

Improve:
- Learnability
- Embrace metaprogramming, kame is made for that
- The error taxonomy (TGT_NO_RULE, TGT_AMBIG, REF_MISSING, SEL_NO_CONTEXT, …) is better than Make's, even if a few messages are cryptic.

Feedback


(llm porting sdk.mk)

What's rough (bugs aside — those are in [KAME-BUGS.md](KAME-BUGS.md); capability gaps in [TODO-GAPS.md](TODO-GAPS.md))
- The language is under-powered for meta-builds. No if, no defined?, no lambdas, single-assignment definitions, no ?=. Individually defensible; together they force "always run, no-op when empty" logic and push configuration into recipes. The port is arguably more verbose than the Make original in places.
- No dynamic definition lookup ($($(VAR))) and no generated rules ($(eval)/include-time loops). That's the real fidelity cliff: AWS_ENV_<tenancy>_<environment> and per-tool/per-dependency rule generation simply cannot be expressed. I had to hardcode every module include too, losing SDK_MODULES composition.
- Pattern ergonomics. Header-path interpolation is disallowed while recipes allow it; leading {capture} patterns don't parse; bare targets can't capture. These feel like parser gaps, not principles, and they block otherwise natural designs.
- @(x/NAME) is a trap. The idea is great; global preflight makes it unusable for anything optional, which is most of an integration SDK. I dropped it entirely.
- CLI/do expr inconsistencies. -C ignoring relative -f, capability-gated wildcard in do expr, and the concat deadlock all cost time. (The concat deadlock is now fixed — see KAME-BUGS.md KB-1.)
- Docs omit the gotchas. $$, [a b]-are-references, empty-definition parse errors, "patterns must start with a literal" — a one-page idioms/gotchas section would have saved hours.
Design decisions I'd push back on
1. Global tool preflight — should be per-target or opt-in, otherwise optional tools are impossible.
2. Single-assignment, no overrides — a config-file layering or ?=-like mechanism is needed for the "consumer overrides the framework" pattern that build frameworks depend on.
3. No conditionals/includes gating — even a minimal if/when and conditional include would restore a lot of composability.

Compared to GNU Make
Make's superpower here was metaprogramming: computed variable names, ?= overrides, $(shell) at parse, generated rules. Kame trades that away for a legible graph and correct incrementality. For a greenfield project with mostly-static configuration, I'd pick Kame. For a framework whose purpose is dynamic, project-owned configuration (this SDK), Make currently expresses more — at the cost of being much harder to debug. The honest summary: Kame made the graph better and the metaprogramming worse, and this port needed both.

- Per-target (or opt-in) tool resolution, not global preflight.
- A small conditional/when plus conditional include, and any form of definition override.
- Fix/allow: leading-capture patterns, @(…) in header paths, literal wildcard, and the (now-fixed) do expr concat hang.
- A documented "idioms and gotchas" page (the skill's current docs skip everything that bit me).
- Optional: bare-target parameters, so tf-plan@ws-style ergonomics don't require file-target workarounds.
