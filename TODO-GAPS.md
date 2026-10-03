# Kame gaps blocking a straight GNU Make port

Living document. Findings from porting the project `Makefile` to `Makefile.kmk`
(2026-10-02, Kame 0.1.0, revisions around `2ef00c6`). It records what prevented
a *literal* translation, the workaround the build settled on, and a concrete
remediation path for each gap.

Related material:

- `Makefile` — the GNU reference build.
- `Makefile.kmk` — the Kame build; inline comments call out every deviation.
- `TODO.md` — an independent port of `sdk.mk` reached many of the same
  conclusions in prose (computed variables, `$(shell)` at parse, generated
  rules, `?=` overrides, `@(x/NAME)` preflight); those are cited as *reported*.
- `KAME-BUGS.md` — verified defects split out of this document (KB-1, KB-2).
- memo learning `kame-concat-wildcards` and evidence #93 (the glob deadlock,
  since fixed; see `KAME-BUGS.md` KB-1).
- `docs/spec/007-library.md`, `009-cli.md`, `011-diagnostics.md` — the
  language/CLI surfaces these gaps touch.

## TL;DR

Three things stop a 1:1 port of this build:

1. **No parse-time evaluation.** No `$(shell ...)`, no `?=`/CLI override, no
   target-specific variables or `export` propagation.
2. **Weaker source discovery.** `wildcard` is single-pattern and has no
   alternation/extension union. (The obvious multi-glob composition *used to*
   hang; that hang is fixed — see `KAME-BUGS.md` KB-1 — but the expressiveness
   gap remains.)
3. **Header composition limits.** Rule inputs cannot interpolate a variable
   into a path, and at most one expression is allowed per header.

Everything else in this Makefile is translation (path syntax, `default`,
captures, one-shell recipes), not obstruction. Several Kame behaviours are
strictly better (dynamic glob dependencies, real file-rule freshness, cached
`task`s), so the port improves the graph while losing metaprogramming.

## Scope and method

- Read the GNU `Makefile`, classified every target (file vs phony vs task) using
  the migration guide.
- Built the Kame version target-by-target, then validated with
  `kame do fmt -n`, `kame do plan` for all 30 targets, `kame -n` dry-runs of all
  aggregate tasks, and two scratch projects for incrementality.
- Every "verify" command below was run against this tree and the observed output
  is quoted. Claims marked *reported* come from `TODO.md` and were not
  independently re-tested here.

## Severity summary

| ID | Gap | Class | Severity | In this Makefile |
| --- | --- | --- | --- | --- |
| A1 | No parse-time `$(shell ...)` / no `do expr shell` during builds | Language design | High | `WASM_SDK`/`WASM_CC`/`WASM_LD` |
| A2 | No `?=`, no build-variable override from policy/CLI | Language design | High | `WASM_CC ?=` |
| A3 | No target-specific variables or `export` propagation | Language design | High | `KAME_BUILD_MODE` |
| A4 | No conditionals or conditional include | Missing feature | Medium | not used here |
| A5 | No dynamic names (`$($(VAR))`) / `eval` / generated rules | Missing feature | Medium | not used here |
| A6 | No line continuation, no multi-line `define` | Parser gap | Low | long `KAME_INPUTS` |
| B1 | `wildcard` single-pattern; no alternation, extension union, `-type f` | Library gap | High | `WASM_SOURCES` |
| B2 | `concat` of two non-empty wildcard globs hung | Engine bug (KB-1) | Critical → fixed | `WASM_SOURCES` (workaround retained) |
| B3 | At most one expression per rule header | Parser gap | Medium | `WASM_SOURCES` + glue |
| B4 | No path interpolation in rule headers (`@(VAR)/suffix`) | Parser gap | Medium | all input paths |
| B5 | Pattern expansion must be a string; bare targets cannot capture | Parser gap | Low | not used here |
| C1 | No way to force-rebuild a file output (`.PHONY` equivalent) | Rule semantics | Low | GNU marks outputs phony |
| C2 | No order-only prerequisites | Rule semantics | Low | not used here |
| C3 | Automatic variables only partially mapped | Rule semantics | Low | `$@`/`$<`/`$^` only |
| C4 | One-shell recipe vs Make's line-per-shell | Behavioural | Info | compatible here |
| D1 | `do expr` capability-gated `wildcard` (needs `--allow-read`) | CLI/UX | Low | diagnostics only |
| D2 | `-C` ignores a relative `-f` | CLI bug (KB-2), fixed | Medium → fixed | not used here |
| D3 | No idioms/gotchas page | Docs, fixed | Medium → fixed | docs/idioms-and-gotchas.md |

Severity is relative to "making a real project build faithfully on Kame", not
to Kame's own roadmap. B2 was called Critical because it was a hang, not because
it is the largest conceptual gap; it is now fixed.

---

## A. Evaluation model

### A1 — No parse-time shell

**Symptom.** GNU computes tool paths once, while reading the file:

```make
WASM_SDK ?= $(shell mise where asdf:mise-plugins/mise-wasi-sdk 2>/dev/null)/wasi-sdk
WASM_CC  ?= $(WASM_SDK)/bin/clang
WASM_LD  ?= $(WASM_SDK)/bin/wasm-ld
```

Kame's `shell` op is valid only in standalone `do expr`; it is rejected during
planning and recipe rendering (see `docs/spec/007-library.md`, `shell` row).

**Impact.** The variable's value cannot be known before execution, so it cannot
be used in a rule header, in another parse-time computation, or in a `default`
decision. The build instead embeds literal `$(mise where ...)` text in a
definition and lets the recipe shell expand it:

```kame
WASM_SDK = "$(mise where asdf:mise-plugins/mise-wasi-sdk)/wasi-sdk"
WASM_CC  = "@(WASM_SDK)/bin/clang"
```

This works because `@()`/nested definitions render the `$(...)` through to the
shell, but `mise where` now runs 4× per wasm link instead of once.

**Remediation.**

- Short term: document the "definition holding shell text" idiom (it is not in
  the skill today).
- Medium term: a hermetic, dependency-tracked tool-path operation for the
  common case (resolve an executable once, cache it for the build). It must not
  reintroduce arbitrary parse-time execution.
- Do **not** add a general parse-time `shell`; that would break Kame's
  determinism story. A `tool NAME` resolver plus `--tool` overrides is the
  better shape.

**Acceptance.** A build can define `WASM_CC` from a tool resolver and use it in
`command -v` and the link line without re-resolving per use, with the resolver
result recorded in the plan.

### A2 — No `?=` and no variable overrides

**Symptom.** GNU lets the environment or the command line override a default:
`make WASM_SDK=/opt/wasi-sdk`. `?=` is the standard "framework default,
consumer overrides" mechanism. Kame definitions are single-assignment; there is
no `?=`, no policy/CLI override, and `--env` only changes the *recipe's*
environment, not the build language's definitions.

**Impact.** CI or a downstream consumer cannot point the build at a different
wasi-sdk (or any other tool) without editing the file. `TODO.md` calls this out
for tools too ("`?=` overrides" and "single-assignment, no overrides").

**Remediation.**

- Add a definition-override surface, e.g. `--define NAME=VALUE` (repeatable) and
  a documented `KAME_<NAME>` environment convention, resolved before planning.
- Add `?=` (define only if not already provided), which becomes meaningless
  until D2/overrides exist.
- Keep overrides visible in the plan so a build is reproducible.

**Acceptance.** `kame --define WASM_SDK=/opt/wasi-sdk ./build/wasm/kame.wasm`
links against the override and `kame do plan` shows the effective value.

### A3 — No target-specific variables / no export propagation

**Symptom.** GNU scopes a variable to a target *and its prerequisites*:

```make
build/kame.debug: KAME_BUILD_MODE=debug
build/kame.debug: $(KAME_BUILD_INPUTS)
```

`generate-version.sh` runs as a prerequisite and reads `KAME_BUILD_MODE`, so
the generated `buildMode` matches the binary being built.

**Impact.** Kame has no construct that attaches a value to a target and
propagates it to the commands reached beneath it. The generated
`version_generated.go` therefore always reports the default `development` mode,
and `kame --version` cannot distinguish a debug/sanitize/release artifact. This
is documented inline in `Makefile.kmk` as a known non-equivalence.

**Remediation.**

- Add a target-scoped environment, e.g. `./build/kame.debug : env KAME_BUILD_MODE=debug`
  or a `with-env NAME=VALUE TARGET` form, inherited by prerequisite/process
  execution and included in the cache fingerprint.
- In the meantime, the only clean option is to make the generated metadata
  target-independent (drop `buildMode` from `generate-version.sh`) or generate
  it inside each build recipe — the latter reintroduces rebuild churn, so avoid.

**Acceptance.** Building `build/kame.debug` and `dist/kame` consecutively yields
`buildMode=debug` and `buildMode=release` respectively, without forcing a
spurious relink when the mode is unchanged.

### A4 — No conditionals / conditional include

**Symptom.** No `ifeq`/`if`/`ifdef`, no `-include`. `include` is depth-first and
rejects repeated or cyclic inclusion, so it is not an idempotent `-include`.

**Impact.** Optional configuration and platform branches must be pushed into
recipes or made unconditional. Not exercised by this Makefile, but it is the
next thing a real project hits.

**Remediation.** A small `when`/`if` form over definition predicates plus a
conditional include. `TODO.md` asks for "a minimal if/when and conditional
include".

### A5 — No dynamic definition lookup / `eval`

**Symptom.** `$($(VAR))` and `$(eval ...)` have no equivalent. `TODO.md`
reports this as "the real fidelity cliff" for `sdk.mk`, where it had to hardcode
every module include and per-tenant/per-environment rule.

**Remediation.** At minimum, indexed/record definitions that can be looked up by
computed key; ideally a programmatic rule-emission form. Design before
implementation — this is the largest language change on the list.

### A6 — No line continuation / multi-line `define`

**Symptom.** A backslash continuation is a parse error:

```text
A = one \
two \
three
# test.kmk:2:1: PARSE_ERR: indented text has no preceding rule
# test.kmk:2:4: PARSE_ERR: unexpected top-level input
```

**Impact.** Long expressions (`KAME_INPUTS`, the wasm header) must be a single
physical line, which hurts reviewability. Multi-line `define` bodies are
impossible.

**Remediation.** Support `\`-newline continuation at top level (joining into the
same definition/rule line). Low risk, high readability payoff.

---

## B. Source discovery and expression composition

### B1 — `wildcard` is single-pattern and cannot express `find`

**Symptom.** The GNU side discovers sources with:

```make
WASM_SOURCES := $(shell find $(KAME_DIR)/cli ... $(KAME_DIR)/program tools/wasm \
  -type f \( -name '*.go' -o -name '*.c' -o -name '*.h' \))
```

Kame's `wildcard`:

- accepts exactly one pattern — two args error:
  `EXPR_INVALID: wildcard expects 1 argument; got 2`;
- has no brace/alternation — `(wildcard ./src/go/kame/{cli,core}/**/*.go)`
  returned `[]`;
- has no extension union and no `-type f`.

**Impact.** The set "these 7 packages, these 3 extensions, excluding `cmd/`"
cannot be stated directly. Note that wildcard *does* match zero directory levels
(`./tools/wasm/**/*.c` matched a file directly in `tools/wasm`) and does record
a dynamic dependency, so it is better than `$(wildcard)` per file — just not
expressive enough.

**Remediation.**

- Allow `wildcard` to take multiple patterns (union), or add a `find`-style op
  with include/exclude globs; or
- support alternation in patterns, e.g. `{go,c,h}` / `{cli,core,...}`.
- Keep the dynamic dependency semantics for every union member.

**Acceptance.** One expression yields the same set as the GNU `find`; adding a
file under `lang/` invalidates consumers, and touching `cmd/` does not.

### B2 — `concat` of two non-empty wildcard globs hung (bug, fixed)

**Tracked as [KAME-BUGS.md](KAME-BUGS.md) KB-1** — was a critical hang, now
fixed in the current working-copy revision. The reproduction, root cause (a
replayed read-only host request that never converged; fixed by operation-state
caching in `operations/host.go`), and verified matrix live there.

**Historical impact on this port.** While it hung, `WASM_SOURCES` had to use a
single wildcard narrowed with `filter-out` instead of concatenating
per-directory globs, which in turn forced the four non-Go glue inputs into a
literal list in the rule header (see B3/B4). `concat` of per-directory globs is
now viable and the workaround can be retired.

### B3 — At most one expression per rule header

**Symptom.** Two expressions in one header are rejected:

```text
./out : @(wildcard ./src/go/kame/cli/**/*.go) @(wildcard ./src/go/kame/core/**/*.go)
# EXPR_INVALID: rule input expression must produce strings, resources, lists, or nil
```

Mixing literal paths with a single `@(...)` works and is what `Makefile.kmk`
uses.

**Impact.** A source set plus a separate literal glue set must live in one
expression (hence the literal `.c/.h` list) or be merged via `concat`, which is
now viable again (B2/KB-1 fixed).

**Remediation.** Allow any number of expression and literal tokens in an input
list, evaluated and flattened left to right. Confirm that each contributes its
dependency edges.

**Acceptance.** A header may mix several wildcards, definitions, and literal
paths, and the plan reports the union of inputs.

### B4 — No path interpolation in rule headers

**Symptom.** `@(VAR)` works in a recipe, including with a suffix, but not in an
input slot:

```text
./out : @(KAME_DIR)/../VERSION
# PARSE_ERR: invalid rule input
```

In a recipe, `cd @(KAME_DIR) && ...` and `-I @(KAME_DIR)/host/wasm` render fine.
So values are available; only header parsing refuses to compose them.

**Impact.** Every input path in a header must be a literal or a whole
expression. `KAME_DIR`/`EXAMPLES_DIR` cannot be used to build input paths, so
the file mixes `@(KAME_DIR)` in recipes with hardcoded `./src/go/kame/...` in
headers. `TODO.md` flags the same asymmetry ("Header-path interpolation is
disallowed while recipes allow it").

**Remediation.** Parse header input tokens as the same string templates recipes
use, then classify the rendered value. Keep rejecting genuinely dynamic
(non-string/list) results with today's `EXPR_INVALID`.

**Acceptance.** `./out : @(KAME_DIR)/cmd/kame/version_generated.go` resolves to
`./src/go/kame/cmd/kame/version_generated.go` in `do plan`.

### B5 — Pattern ergonomics

**Reported, not re-verified here:** leading-capture patterns do not parse, and
bare targets cannot capture (`TODO.md`). Separately verified: a pattern
replacement *expansion* must be a quoted string or expansion pattern — `1`
failed with `PAT_INVALID: expansion must be a string or an expansion pattern`
while `"1"` worked. This makes `replace` predicates awkward to write and is easy
to trip over.

**Remediation.** Accept scalar integer/bool expansions (coerce to text);
support leading captures; allow capture on bare targets for the parameter idiom
in `TODO.md`.

---

## C. Rule semantics

### C1 — No force-rebuild for a file output

GNU's `.PHONY` naming a real output makes it rebuild every run. Kame has no
equivalent: a bare task always runs, but you cannot give it the same name as an
artifact. In this Makefile the GNU `.PHONY` list names `build/kame.debug`,
`build/kame.sanitize`, `dist/kame`, and `dist/kame.com`, which contradicts its
own `KAME_BUILD_INPUTS`; `Makefile.kmk` intentionally treats them as file rules.
This is arguably a Makefile bug, not a Kame gap, but the absence of an explicit
"always run this file target" escape hatch should be documented.

### C2 — No order-only prerequisites

No `|` equivalent. Redesign so order-only dependencies are ordinary inputs or
separate tasks. Not used here.

### C3 — Partial automatic variables

Only `$@`→`@>`, `$<`→`@<`, `$^`→`@<*` map cleanly. `$?` (newer inputs), `$*`
(stem), `$(@D)`, `$(<D)` have no general equivalent. Use explicit captures and
selectors (`@<N`, `@<A..B`). Not used here.

### C4 — One shell per recipe, not per line

Kame renders a rule body as one shell script, so `cd`, variables, and pipelines
persist; Make starts a shell per line. This is usually an improvement, but a
port that relied on line isolation (variables not leaking, a failed line not
aborting the next) can change behaviour. This Makefile already uses
`cd ... && ...` per line, so it is compatible. Flag it in the gotchas page.

---

## D. Tooling and docs

### D1 — Capability-gated `wildcard` in `do expr`

`kame do expr -c '(wildcard ...)'` needs `--allow-read`; without it the read is
denied. Correct by design, but it surprised the port; the error should name the
grant to add (it is close today).

### D2 — CLI inconsistencies

**`-C` ignores a relative `-f`** is fixed, with native/WASM regression tests in
T009-02 and T010-06. The historical reproduction is tracked as
[KAME-BUGS.md](KAME-BUGS.md) KB-2: `kame -C sub -f Makefile.kmk` fails with
`FS_ERR: cannot read source: Makefile.kmk`, while `-f sub/Makefile.kmk -C sub`
works. Capability-gated `wildcard` in `do expr` (D1) is intentional; the concat
deadlock was KB-1 and is now fixed.

### D3 — Docs omit the gotchas (fixed)

Added [docs/idioms-and-gotchas.md](docs/idioms-and-gotchas.md), with executed
expression examples and explicit remaining syntax boundaries. Historical finding:

The skill/reference docs cover the happy path but not: shell-text definitions
(A1), header interpolation limits (B4), `$$`/literal-`$`
rules, `[a b]` list-vs-reference, empty-definition parse errors, and pattern
expansion quoting (B5). A one-page **idioms and gotchas** section would have
saved most of the discovery time and belongs next to the migration guide.

---

## Corroborating reports

`TODO.md` contains an independent port of `sdk.mk` that reached compatible
conclusions: no `if`/`defined?`/lambdas/`?=`, no dynamic definition lookup or
generated rules, header-vs-recipe pattern asymmetry, global tool preflight, and
the (since-fixed) `concat` deadlock. Treat that section as supporting evidence
for A2, A4, A5, B4, and D2; this document adds exact repros and acceptance
criteria so the items can be closed.

## Prioritized remediation checklist

Ordered by value-to-effort for porting real projects:

- [x] **KB-1** (was B2) Fixed the `concat`-of-wildcards hang with per-operation
      read-request state; regression coverage in `T011-03` and `T010-13`.
- [ ] **KB-2** Fix `-C` + relative `-f`; add CLI regression tests.
- [ ] **B3** Allow multiple expressions/literals per rule header, flattening
      dependency edges.
- [ ] **B4** Allow `@(VAR)/suffix` and other path interpolation in headers by
      parsing inputs as string templates.
- [ ] **A2** Add `--define NAME=VALUE` + a documented env convention, then `?=`.
- [ ] **A3** Add target-scoped environment inherited by prerequisites, included
      in cache fingerprints; restore `KAME_BUILD_MODE`.
- [ ] **A1** Add a hermetic tool resolver (not general parse-time shell) and a
      `--tool` override.
- [ ] **B1** Multiple patterns / alternation for `wildcard`, or a `find`-style
      op with include/exclude.
- [ ] **B5** Accept scalar pattern expansions; leading captures; captures on
      bare targets.
- [ ] **A6** Backslash line continuation in definitions.
- [ ] **A5** Design computed-key definitions and rule emission (largest change).
- [ ] **A4** Minimal `when`/`if` plus conditional include.
- [ ] **D3** Add an "Idioms and gotchas" page to the skill/reference docs.

## What is *not* a gap

Listed so the gaps above are not read as "Kame is worse":

- `default`, bare targets, and file rules map cleanly; `./` makes a path a file
  resource.
- `@<`, `@<*`, `@>`, and index/slice selectors cover the automatic variables
  this build uses.
- `{name}` target templates replace `%` pattern rules for the common case.
- Real file-rule freshness, dynamic glob dependencies, cached `task`s,
  multi-output file rules, and one-shell recipes are improvements over Make.
- Kame's richer error taxonomy (`TGT_NO_RULE`, `EXPR_INVALID`, `PAT_INVALID`, …)
  made most failures self-explanatory.

The honest summary matches `TODO.md`: Kame makes the graph better and the
metaprogramming worse. For this Makefile the missing metaprogramming cost three
targeted workarounds; closing B1–B4 and A1–A3 would make the port literal.
