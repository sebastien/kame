# Plan 055: implement language revision 2

This plan is for an agent implementing the unified Kame/Kash language, revision
2. The specification is final. Implement it exactly, and do not redesign it.
Where code and spec disagree, the spec wins. Where the spec is silent or
contradictory, stop and ask; do not invent behavior.

## 0. Before you start

**Read, in order:**

1. `docs/spec/README.md`
2. `docs/spec/055-unified-kame-kash-language.md`. This is the normative
   overview; read all of it.
3. The specs refining your current phase (listed per phase below). Each starts
   with `revision: 2` front matter.

**Repository rules** (`AGENTS.md`):

- Run `memo agent wake --agent agent-kame/agent:Developer` at startup. After
  each phase, request one memo consolidation and record durable learnings.
- Do not edit the root `README.md` unless the user asks. List the changes it
  needs in your final report instead.
- Specs describe what *is*. Do not add legacy, compatibility or migration text
  anywhere: not in code comments, diagnostics, docs or specs.

**This is a clean break:**

- There is no compatibility parser, legacy mode, alias or migration tool.
- Old syntax must fail with ordinary diagnostics (`PARSE_ERR` and so on), with no
  special "did you mean the old form" machinery.
- Delete legacy code paths as you replace them. Known examples are
  `src/go/kame/lang/script/legacy_directives.go` and
  `src/go/kame/lang/eval/migration.go`, plus the `legacy-*` fixtures in
  `tests/data/cli/`.

**Work on a branch or worktree.** The repository builds itself: `Makefile`
bootstraps `kame` from source and runs `Kamefile`, which uses the old syntax.
Until the switchover in Phase 13, drive the work with the Go toolchain directly
(`go build ./...`, `go test ./...` in `src/go/kame`), not with `make`/`kame`.

## Code map (starting points; verify before relying on them)

| Area | Location |
| --- | --- |
| AST and JSON | `src/go/kame/lang/ast` |
| Expression parser, Kash command parser, operators, patterns, regex | `src/go/kame/lang/expr` |
| Script and Kash statement parsing, composition, sessions | `src/go/kame/lang/script` |
| Rule headers and environment metadata | `src/go/kame/lang/rule` |
| Templates (document and string) | `src/go/kame/lang/template` |
| Definitions and overrides (`?=`, `+=`) | `src/go/kame/lang/definition` |
| Evaluator: forms, scopes, Kash control, processes, outcomes, modules | `src/go/kame/lang/eval` |
| Formatter | `src/go/kame/lang/format`, `lang/expr/*format*`, `lang/expr/layout.go` |
| Library operations | `src/go/kame/operations` |
| Program loading, planning, declarations, generation | `src/go/kame/program` |
| CLI, REPL, WASM entry points | `src/go/kame/cmd/kame`, `cmd/kame-wasm`, `src/go/kame/cli` |
| Embedding | `src/js/kame.js`, `src/py/kame.py` |
| End-to-end tests | `tests/T*.sh`, `tests/*.py`, fixtures in `tests/data` (about 127 `.km` and 18 `.kmk` files) |
| Go test fixtures | `src/go/kame/**/test/` (about 238 files) |
| Self-build | `Kamefile`, `src/kash/*.kash`, `src/kash/templates` |
| User docs | `docs/ref-*.md`, `docs/kash-cookbook.md`, `docs/glossary.md`, `docs/idioms-and-gotchas.md`, `docs/skills/kame/**` |
| Examples | `examples/**` (about 198 files) |

## Working method

- **Phase by phase.** Each phase lists its spec references, work items and exit
  criteria. Don't start a phase until the previous one's Go unit tests pass.
- **Keep the existing AST and evaluator where you can.** Replace the surface
  parsers, then extend the AST and evaluator for new semantics. AST JSON moves to
  `schema: 2`. Spans stay zero-based byte offsets (`011`).
- **Parsing never depends on bindings, imports, `PATH`, grants or phase**
  (055 §2, conformance item 1). Enforce this structurally. The parser has no
  access to scopes.
- **Write tests from the spec text first**, then implement. Each conformance item
  in 055 §16 and in every refining spec's acceptance section must map to at
  least one test. Keep a checklist in your phase notes.
- **Convert fixtures as you go.** When a phase changes syntax, convert the Go
  test fixtures that cover that area in the same change. End-to-end shell tests
  may stay red until Phase 13. Track which ones are red.
- **Commit at the end of each phase.** Use the repository's message style:
  `[Update] area: summary`.

## Phase 1: Lexer and expression grammar (value context)

**Specs:** 055 §3, §4; 004 §2–3; 014 §§ sections and pipes; 018 (expression
layout).

1. **Lexer.**
   - Names, including `-`, and a single trailing `?` or `!`.
   - `ENV-NAME`.
   - Qualified `ns/name`, one level only, with a quoted segment allowed.
   - Spacing rules:
     - binary `/` and `-` need whitespace on both sides;
     - an unspaced `a/b` that is not a qualified name or a path is `PARSE_ERR`;
     - `-1` is a literal;
     - `-x` is `PARSE_ERR` in expression position.
   - Keywords, from the 055 §3.3 list. `sep` is a keyword only inside a template
     `@for`.
   - `#` comments only.
   - Literals: numbers, `true`, `false`, `nil`, symbols `:x`, `:+x` and `:-x`,
     `"…"` with `@{EXPR}` and `@@`, raw `'…'`, verbatim `"""…"""`.
   - Placeholders: `_`, `__`, `___` and `_N`.
2. **Path atoms** (055 §3.4).
   - Prefixes: `./`, `../`, `/`, `~/`, and a leading `@{…}/`.
   - Pattern groups and glob syntax are allowed inside.
   - A path atom ends at whitespace or at an unbalanced `,`, `)`, `]` or `}`.
   - A malformed group is `PARSE_ERR`; there is no fallback to literal text.
3. **Grammar** (055 §4.1).
   - Application by juxtaposition, and `f ()`.
   - Parentheses only group.
   - Lists `[a, b]` and records `{k: v, k, ...r}`, with commas and an optional
     trailing comma.
   - Field access `.name`, `.0` and `."key"`.
   - Operators follow the precedence table; comparisons don't chain.
   - Also: `??`, `|>`, lambdas `(x y => …)` and `(() => …)`, and
     `if … then … elif … else …`.
   - The atoms `$(…)`, `$?(…)` and `$&(…)` delegate their contents to the
     action parser (stub it until Phase 3).
4. **Sections and pipes** (055 §4.3–4.4).
   - A paren group is a section when a placeholder appears at its top level.
   - Placeholders inside nested parens, lambdas, lists, records and the three
     `$` groups don't count toward the outer group.
   - In a `|>` stage, `_` marks where the value goes; otherwise it's appended
     last.
   - The AST keeps pipes and nested calls distinct.
5. **`match` expression** (055 §4.5).
   - Each `PATTERN => EXPR` arm ends at a newline, even inside a delimiter.
   - A lambda result must be parenthesized.
   - `else =>` is the default arm. Arm patterns are always matchers.
6. **Line continuation.** An expression continues across lines when:
   - a delimiter is open;
   - the line ends with a binary operator, `=>` or `=`;
   - the next line is more indented and starts with `|>` or a binary operator;
   - the line ends with a backslash.
7. **Expression formatter** (018). It never adds or removes parentheses or
   placeholders, never converts between pipes and calls, and is idempotent.

**Exit:** expression parse and format tests pass, including
conformance 055 §16 items 2–4, and the expression fixtures are converted.

## Phase 2: Statements, blocks and Kame program top level

**Specs:** 055 §5, §10.1 (headers), §11 (syntax only); 004 §4–9; 022 (target
arguments).

1. **Suffix dispatch.** `.km` and `.kame` are Kame programs. `.kash` and `.ksh`
   are Kash. `.kmt` is a template. Remove `.kmk` everywhere.
2. **Top-level forms.**
   - `NAME = EXPR`, `def NAME = EXPR` (a lazy value), `def NAME PARAMS = EXPR`
     and `def NAME () = EXPR`.
   - Block-bodied `def`.
   - A block-valued `NAME =` with the 055 §5.1 rule for telling a block from a
     continued expression.
   - `NAME ?= EXPR` and `NAME += EXPR`.
3. **Blocks.**
   - Indentation prefix rules; tabs and spaces never mixed within one prefix.
   - Statements: `let`, `set`, `set env/NAME`, `if`/`elif`/`else`,
     `for … in`, `while`, statement `match`, `return`, `yield`, `break`,
     `continue`, `exit`, `defer`, `with`, `for … from`, `stream`, and
     expression statements `(…)`.
   - A line is a binding only if it starts with `let` or `set`.
4. **Declarations.**
   - `rule [always] OUTPUTS : INPUTS`, `task [cached] NAME [TARGET-ARGS]`
     and `service NAME`.
   - Inputs: parallel groups separated by commas, then order-only items after
     `|`.
   - An optional `; {metadata}` record.
   - The body is `= EXPR` or an indented block.
   - No `:` when there are no inputs.
5. **Declaration-time control and modules.**
   - Top-level `if` and `for` in Kame programs. Their blocks may contain only
     `let`, nested control, declarations, `include` and `generate`.
   - `import`, `from`, `export` and `include`/`include?`, with literal
     path-atom operands only.
6. **Errors to enforce.**
   - Top-level expression statements are invalid in Kame program files; inline
     `-c` fragments may contain them (009).
   - Top-level commands in Kame programs are invalid.
   - `return` is a parse error outside a `def`, a block-valued definition or a
     rule body.
7. **Formatter.** Blocks, headers and module statements (018). The formatter
   joins backslash continuations.

**Exit:** parse and format tests for the statement and declaration grammar pass
(055 §16 item 6 syntax, items 11 and 13 syntax), and the rule and script
fixtures are converted.

## Phase 3: Kash action grammar

**Specs:** 055 §7, §5.2; 017; 038; 042; 046–048.

1. **Command words** (055 §7.1 table).
   - `$name`, `$name.field`, `${REF}` and `${ns/name}`, `$...list`, `(EXPR)`
     fragments, `$(…)`, quoted forms, and word composition.
   - An unbraced `$` reference stops at `/`.
   - An unquoted `@{` in a word is `PARSE_ERR`; any other `@` is literal.
2. **Stage settings.** `:KEY WORD`. Reserved keys are read by their own contract:
   `:timeout 10` is a number, and `:tty true` and `:foreground false` are
   booleans. `:env` takes a record.
3. **Process syntax.** Keep `017`, `042` and `047` semantics for pipelines,
   sequencing, redirection, `>&out` and `>&err`, `CMD ?` and recovery,
   terminal `&`, channels and `stream`.
4. **Conditions are expressions**, for example `if $?(cmd).ok`.
5. **Remove** integer-status `return N` and `return N ?`, text compilation
   `(kash TEXT)`, and `case`.

**Exit:** the action-context parse and format tests pass, and parsing the same
line in action context and in value context gives two different ASTs
(055 §16 item 1).

## Phase 4: Name resolution, bindings and unit state

**Specs:** 055 §5.3, §6; 005; 051; 052 (builtins).

1. **Expression-position resolution.**
   - Order: lexical, then `std`, then the environment for `ENV-NAME`.
   - An unset `ENV-NAME` is `ENV_VAR_MISSING`, which `??` catches.
2. **Command-position resolution.**
   - Order: lexical callable, then builtins, then executables (using the
     store's `PATH`). The standard library is never consulted here.
   - A lexical non-callable is `EXPR_INVALID`.
   - Value and declaration contexts never launch an unknown command.
3. **Builtins.**
   - Available everywhere: `cd`, `wait`, `kill`, `out!`, `err!`.
   - Interactive only (`PARSE_ERR` elsewhere): `jobs`, `fg`, `bg`, `alias`,
     `unalias`, `history`, `option`, `explain`.
4. **Namespaces.** `x/`, `env/`, `std/`, `module/dir` and `module/file`, plus
   import aliases. Reserved names can't be used as aliases.
5. **Bindings.**
   - Lazy, memoized top-level definitions.
   - Eager `let`, with shadowing.
   - `set` may only target an existing `let` in the same invocation that no
     closure captures; anything else is `PARSE_ERR`.
6. **Process-like unit state.**
   - Units: the script, a rule body, a capture, an interactive session, and one
     `kame do run` invocation.
   - Each unit owns an environment store and a working directory. Writes are
     visible after the writing function returns but never reach the parent.
   - `set env/NAME = nil` unsets, following 051 authority.
   - `with` overlays don't write the store.

**Exit:** 055 §16 items 5–6, plus the 005, 051 and 052 acceptance tests that
belong to this area.

## Phase 5: Invocations, outcomes, generators, async

**Specs:** 055 §7.2–7.3, §8; 017; 012; 042; 049.

1. **Functions as commands.** Words become arguments, and the function's
   stdout/stderr take part in pipes and redirection.
2. **Outcome record** (055 §8.1, plus 049's fields).
   - `$?(…)` captures stdout into `value`.
   - It doesn't raise for exits, launch failures, timeouts or function failures.
   - It does raise for capability denial, phase errors, parse errors and
     cancellation.
   - An accepted exit gives `state :completed` with `ok false`.
3. **Status and `exit`.**
   - Function status is derived (§8.2).
   - A cancelled function or generator stage doesn't fail the graph, but a
     process killed by SIGPIPE keeps 042's rules.
   - `exit` ends the innermost unit and runs cleanup.
4. **Generators.**
   - A `def` containing `yield` returns a lazy `Stream`.
   - The suspended and cancelled states run `defer` cleanup.
   - Inside a generator, `return EXPR` is `PARSE_ERR`.
   - stdout bytes and yielded values stay separate.
5. **Async.** `$&(…)` returns an owned handle, with the same ownership as
   `CMD &`; awaiting is `$?(wait $job)`.

**Exit:** 055 §16 item 8, plus the 012, 042 and 049 acceptance tests for these
features.

## Phase 6: Paths, patterns, discovery

**Specs:** 055 §9; 014; 023; 029; 041.

1. **Path kinds.** `Path`, `PathPattern` and `PathTemplate` are classified when
   the atom is parsed or evaluated, and never convert implicitly.
2. **Operations.**
   - `capture PATTERN SUBJECT` and `bind TEMPLATE CAPTURES`.
   - The regex accessor is `regex-group`.
   - `pattern TEXT` is the explicit way to turn text into a pattern.
   - There are no string-pattern literals outside `match` arms.
3. **Root directory.**
   - Kame programs: the entry file's directory. Kash programs: the invocation
     directory. `-C` overrides both.
   - `include` and `import` paths are relative to the current file.
   - Relative path values keep their authored form.
   - `module/dir` gives a module its own directory.
4. **Discovery.**
   - `files PATTERN` returns a sorted list and registers a tracked membership
     dependency.
   - In declaration context it's a snapshot, invalidated when membership
     changes.
   - A discovery pattern that overlaps a declared output is
     `DISCOVERY_OVERLAP`, naming both locations.
   - A wildcard rule input keeps 023's dynamic semantics.

**Exit:** 055 §16 items 9–10, plus the 014, 023, 029 and 041 acceptance tests.

## Phase 7: Rules, generation, rule bodies

**Specs:** 055 §10; 004 §8; 006; 022; 025; 038; 044; 045.

1. **Rule kinds and inputs.**
   - `task` is phony, `task cached` is cached, and `rule always` is always
     stale.
   - Output atoms are matchers. Input atoms are templates, and `{_N}` refers to
     positional captures.
2. **Body bindings:** `inputs`, `outputs`, `changed-inputs`, `captures`, named
   captures and target arguments.
3. **Publication.**
   - An expression body `= EXPR`, or a Kash body's `return VALUE` that isn't
     `nil`, publishes to the single output under 044.
   - Remove `shell: kame` and `yield!`.
4. **Foreign-shell recipes.** The body is a template using `@{…}`; `$` passes
   through untouched.
5. **Rule values and generation.**
   - `rule`, `task` and `service` in expression position build `Rule` values.
   - `generate EXPR` registers them transactionally.
   - Declaration-time `for` and `if` give each iteration its own closure.
   - Inactive branches are neither evaluated nor read.
   - 025's limits apply per `generate`.
   - Declarations record provenance.
6. **Declaration-time work.** Allowed: pure computation, tracked discovery and
   tracked environment reads. Forbidden: reading file contents, processes and
   writes.

**Exit:** 055 §16 items 11, 12 and 14, plus the 025, 044 and 045 acceptance tests.

## Phase 8: Modules

**Specs:** 055 §11; 050; 004 §7.

1. **Loading.** Each module loads once, keyed by normalized source identity.
   Importing evaluates nothing eagerly and registers nothing.
2. **Path resolution.**
   - An operand without one of the four module suffixes tries `.km`, `.kame`,
     `.kash`, then `.ksh`.
   - More than one match is `RES_INVALID`; no match is `FS_ERR`.
   - Importing a `.kmt` is `RES_INVALID`.
3. **Exports.**
   - `export` is required; a module with no `export` exports nothing.
   - Re-exports aren't allowed.
   - Exporting the same name twice is fine.
   - Unknown or imported export names, namespace collisions and invalid stems
     without `as` are `DEF_INVALID`.
   - Importing a name that isn't exported is `REF_MISSING`.
4. **Cycles.** Import cycles and active include cycles are `DEP_CYCLE`, naming
   the chain.
5. **Kash modules.**
   - A Kash module's top level is checked when it's loaded; anything other than
     definitions, imports, exports and plain includes is `DEF_INVALID`.
   - Kash sources include only Kash sources; anything else is `RES_INVALID`.
   - A Kash source has no conditional include.
6. **Freshness and watch.** Follow 050.

**Exit:** 055 §16 item 13, plus the 050 acceptance tests.

## Phase 9: Templates

**Specs:** 055 §12; 016; 030.

1. **Directive grammar.**
   - `"@" "-"? keyword operands? (WS "-")?`, with no parentheses around
     operands.
   - The keyword ends at whitespace, at `-`, or at the end of the directive.
   - The optional label on `@end` must match the block it closes.
2. **Directives:**
   - `@if` / `@elif` / `@else`
   - `@for NAMES in EXPR [sep EXPR]`, which destructures each item positionally
   - `@with`
   - `@match` / `@case`
   - `@let NAME = EXPR`
   - `@include PATH [PAYLOAD]`, where `PATH` is a single operand resolved
     against the template's directory
   - `@raw`
3. **Inline directives.** Allowed only in comment styles; the `plain` style's
   only inline form is `@{EXPR}`. `@@` is the only escape.
4. **Rendering.**
   - `render` decides by type: a `Path` is a template file, while `String` or
     `Bytes` is template content in the `plain` style.
   - Lists and records are invalid in interpolation unless rendered explicitly
     (for example with `join`).

**Exit:** the 016 and 030 acceptance tests pass, and 055 §16 item 15 (the
template part) passes.

## Phase 10: Library

**Specs:** 007; 055 §4.6, §13.

1. Signatures are data-last.
2. Renames and removals:
   - `set` becomes `put`.
   - The regex `capture` becomes `regex-group`.
   - `yield!` is removed.
   - Remove every compatibility alias (`eq`, `is`, `has?`, `replace`, `=` as
     equality and so on).
3. Add `indexed`, `neg` and `fmt`, and restrict `eval` to a single expression.
4. `files`, `dirs` and `wildcard` take `PathPattern` operands only.
5. Library names are lower-kebab-case.

**Exit:** the 007 acceptance tests pass, and `fmt` output round-trips through
`eval` (055 §16 item 15).

## Phase 11: Formatter completion

**Spec:** 018.

Cover every context:

- process-graph layout, which breaks only inside a group's parentheses
- `if` and `match` layouts
- trailing commas, present exactly when a collection is expanded
- rule headers
- the three `$` groups
- templates

Formatting must be idempotent over every fixture. The formatter must never
change what the code means, so add a test that parses, formats, parses again and
compares the ASTs.

**Exit:** the 018 acceptance tests pass, and formatting is idempotent across all
converted fixtures.

## Phase 12: CLI, REPL, diagnostics, WASM, embedding

**Specs:** 009; 010; 011; 013; 028; 036; 052–054.

1. **CLI** (009).
   - Default entry files: `Kamefile`, `Makefile.km`, `make.km`,
     `src/km/main.km`.
   - Suffix dispatch.
   - Selecting a target or definition by name.
   - `-c` fragments: inline Kame fragments may contain expression statements.
   - `--lang` and parse modes `expr`, `template`, `km` and `kash`.
   - Default grants.
2. **REPL** (052).
   - Builtins.
   - `set env/` replaces `export` and `unset`.
   - `x/kill` runs the external `kill`.
   - `exit` ends the session.
   - Completion and highlighting know the new keywords, namespaces and `$`
     groups.
3. **Diagnostics** (011). Add `ENV_VAR_MISSING` and `DISCOVERY_OVERLAP`. Write
   messages for the new boundaries, for example `@{` inside a Kash word, which
   should suggest `$name` or `(expr)`.
4. **WASM and embedding.** Keep native and WASM in parity (010). Update the JS
   and Python APIs (028).

**Exit:** the 009, 011 and 052–054 acceptance tests pass, and the native and WASM
parity suites pass.

## Phase 13: Switchover and repository conversion

Do this phase as a single change; the repository has to build itself afterwards.

1. **Remove the old front end.** Delete any remaining old parsers and the legacy
   files listed in §0.
2. **Self-build.** Convert `Kamefile`, `src/kash/*.kash` and
   `src/kash/templates`. Watch for:
   - upper-case configuration such as `KAME_RELEASE_PUBLIC_KEY ?= ""`, which
     now has the environment as a fallback (that's intended, but check each
     use);
   - `(include …)`, which becomes `include ./…`;
   - `SHELL = kash`;
   - bare tasks, which become `task …`;
   - `@{RELEASE_BINARIES}` in inputs.
3. **Tests.** Convert `tests/data` (rename `.kmk` to `.km`, including the
   default-entry fixtures under `discovery/`), the snippets embedded in
   `tests/T*.sh` and `tests/*.py`, `tests/data/README.md`, the catalog
   (`tests/CATALOG.tsv`), and every Go test fixture.
4. **Examples.** Convert all of `examples/**`.
5. **Docs and skills.** Convert `docs/ref-*.md`, `docs/kash-cookbook.md`,
   `docs/glossary.md`, `docs/idioms-and-gotchas.md` and `docs/skills/kame/**`.
   Rewrite `docs/skills/kame/ref/make-migration.md` as a Make-to-Kame guide;
   that's about GNU Make, not the old Kame syntax, so it stays.
6. **Root `README.md`.** Don't edit it. List the changes it needs in your
   report.
7. **Full suite.** Run `make test`, then `make test-all` if sanitizers are
   available. Everything must pass.

**Exit:** `make test` passes. `rg` over the repository (excluding `deps/`,
`build/`, `dist/` and `.git/`) finds none of these:

- `.kmk`
- `@<`, `@>`, `@(`
- `:nil`, `:true`, `:false`
- `(def `, `(include`
- `yield!`
- `env\.[A-Z]`
- `[a-z]: ` inside `[...]` record literals

## Phase 14: Conformance audit

1. Walk through 055 §16 and every refining spec's acceptance section. Check off
   each item against named tests.
2. Add tests for any item without one.
3. Write the checklist into the final report, one line per item with its test.
4. Run `memo` consolidation.

## Definition of done

- Every phase's exit criteria are met, and `make test` passes on a clean
  checkout.
- No old syntax is accepted anywhere: parser, formatter, REPL, WASM or fixtures.
- No legacy, compatibility or migration code or text remains.
- Native and WASM agree on parse trees, diagnostics, phase boundaries and
  results.
- The final report contains:
  - the conformance checklist;
  - the list of changes the root `README.md` needs;
  - any spec ambiguity you hit, with the question you'd ask (don't guess);
  - deviations from this plan, with the reason for each.

## Known risk areas

- **Section boundaries versus `|>` stages versus nested groups** (055 §4.3–4.4).
  Test the edge cases from 014.
- **Telling a block-valued `NAME =` from a continued expression** (055 §5.1).
  The formatter must never produce a layout that changes the meaning.
- **`$dir/file` versus `${ns/name}`** in words, and `@{` in words.
- **Unit state persisting across function returns** (055 §5.3) while captures
  and rule bodies stay isolated.
- **Generator cancellation and `defer` ordering** relative to process cleanup and
  the shared unwind deadline (043).
- **Declaration-time tracked discovery and `DISCOVERY_OVERLAP`** under watch
  reloads (020).
- **Upper-case configuration names in `Kamefile`** now read the environment as a
  fallback. Check that release-key handling still behaves as intended.
