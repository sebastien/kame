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

- There is no compatibility parser, legacy mode, syntax alias or migration tool.
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
     `"…"` with `${EXPR}` and `@`, raw `'…'`, verbatim `"""…"""`.
   - Placeholders: `_`, `__`, `___` and `_N`.
2. **Path atoms** (055 §3.4).
   - Prefixes: `./`, `../`, `/`, `~/`, and a leading `${…}/`.
   - Pattern groups and glob syntax are allowed inside.
   - A path atom ends at whitespace or at an unbalanced `,`, `)`, `]` or `}`.
   - A malformed group is `PARSE_ERR`; there is no fallback to literal text.
3. **Grammar** (055 §4.1).
   - Application by juxtaposition, and `f ()`.
   - Parentheses only group.
   - Lists `[a, b]` and records `{k: v, k, ...r}`, with comma or newline
     separators and an optional trailing comma.
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
2. **Stage settings.** `KEY: WORD`. Reserved keys are read by their own contract:
   `timeout: 10` is a number, and `tty: true` and `foreground: false` are
   booleans. `env:` takes a record.
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

T017-19 verifies that a returned closure can be bound in an ancestor of
its capture on native and WASM. The evaluator tests cover eager bindings,
assignment, and closures in lists and records. A scope lifetime test
checks that the last external owner releases the cycle, and a pure Kame
value block exercises the same binding behavior.

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
   - Suspension retains pending cleanup. An early stop cancels the generator
     and runs that cleanup.
   - Inside a generator, `return EXPR` is `PARSE_ERR`.
   - stdout bytes and yielded values stay separate.
5. **Async.** `$&(…)` returns an owned handle, with the same ownership as
   `CMD &`; awaiting is `$?(wait $job)`.

**Exit:** 055 §16 item 8, plus the 012, 042 and 049 acceptance tests for these
features.

Generator calls return a lazy, single-consumer stream. Kash `for` resumes the
body across yields and host work. Early break, return, and failure release the
generator and run nested cleanup. In a value pipeline, `collect!` materializes
one list in yield order and can stop after a bounded prefix. T055-01 exercises
these paths on native and WASM, including host timeouts, separate stdout and
value channels, and output ordering across generator, consumer, and cleanup.
It also verifies generator commands invoked as a standalone statement,
stdout capture, outcome capture, and async wait.
The live runtime stream handle reports suspension between pulls. A pull with
pending host work stays active, and completing or closing the stream records
its terminal state. TestGeneratorInvocationState verifies those transitions.
T055-01 verifies that completed generator captures report `:completed` and
that dry-run emits no process outcome.
Callable pipeline stages pass typed arguments and exchange bytes through bounded
host ports on native and Node. T055-02 covers stdout and stderr routing, record
loops on pipeline and invocation input, cleanup on downstream pipe closure and
sibling failure, outcome capture, and async wait. A callable that breaks out of
a borrowed stdin loop leaves unread bytes on the invocation descriptor.
Nested subprocesses inherit callable ports on native and WASM. T055-02 checks
redirection, private captures, transfers larger than a pipe buffer and
cancellation. Record reads stop at their delimiter, so subprocesses and
nested callables can take turns reading the same graph input. T055-02 verifies
both handoff directions, a record spanning multiple exchanges, and cancellation
while a FIFO writer remains open. A generator that yields without writing
more output stops and cleans up after its consumer exits successfully. Both
hosts report its stage as `:cancelled` while the graph succeeds. Function
stage errors retain their authored diagnostic. T055-02 also verifies typed
stage returns, including lexical closures, through outcome capture, command
lists, and repeated async waits. An independent stage output watch cancels
pending subprocess work and releases stage and borrowed record readers when
the consumer closes its input. T055-02 verifies cleanup and successful graph
status even when the consumer stays alive until cleanup runs. The Node host
checks the private FIFO for readers without transferring bytes, then removes
its temporary watch path when the graph joins. T048-04 verifies that deliberate
exit in a synchronous capture stops the command list, preserves output from
earlier commands and cleanup, and returns control to the caller on both hosts.
Pipeline outcomes retain stage statuses and graph telemetry when a stage
exits the capture. Outcome capture reports an ordinary unwind failure and
propagates a capability denial, including one from a later deferred action.
T048-05 checks the same boundary through an owned `with` scope. Generator
suspension remains internal. `TestGeneratorInvocationState` verifies it between
pulls, and script-visible outcomes report terminal states.

T042-11 checks that failure collection lets downstream readers drain all
worker output even when the reader starts late. The per-record stage deadline
also covers the initial wait for input. T042-09 checks merge ordering and
cancellation on both hosts.

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

- Plain path atoms and `module/dir` and `module/file` now produce `Path` values.
  Native and WASM checks cover typed equality, relative argv rendering, file
  reads, path joining, and computed rule paths.
- Path atoms distinguish `PathPattern` and `PathTemplate`. Native and WASM
  checks cover `capture`, `bind`, `regex-group`, sorted `files` results as
  paths, and discovery through a computed rule prerequisite.
- Grouped discovery supports regex, recursive and repeated named groups. Native
  and WASM watches compare the exact matched membership. Adding a nonmatching
  candidate leaves settled consumers unchanged.
- Native and WASM registration snapshot regular-file membership and refresh
  declarations on CLI watch changes, including recovery from a failed load.
  Overlap diagnostics cover concrete and patterned authored and generated
  outputs even when the outputs are absent. The portable overlap check
  respects recursive globs, regex groups, and repeated capture equality.
  Checks that exhaust the pattern budget report `PAT_LIMIT`.

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

- Verified on native and WASM. Rule bodies expose `captures` in source order,
  including anonymous captures, and `changed-inputs` shares the file recipe's
  content snapshot with imported helpers and asynchronous calls. Forced builds
  preserve the distinction between changed and unchanged content.
- The rule and script parser suites cover explicit empty prerequisites.
  T006-04 and T006-07 verify ordering-only and changed-input behavior on native
  and WASM, including computed prerequisites and recipe phase diagnostics.
- Verified on native and WASM. Declaration-time `for` over lists, block-local
  `let`, and nested `if` preserve bindings through computed targets, expression
  bodies, and Kash recipes. Captured values participate in freshness checks.
- Verified on native and WASM. Local constructors return `Rule` values that
  retain lexical bindings. `generate` accepts one rule or a list, including in
  declaration blocks. A collision reports the constructing call and registration
  site before recipes run.
- File-rule declarations and constructors accept `(EXPR)` and `${EXPR}` for
  computed outputs. T010-27 checks format, parse and materialization of
  parenthesized targets in both forms on native and WASM.
- Imported constructors register computed outputs and retain private lexical
  bindings. Registration tracks transitive module sources and missing suffix
  candidates. Collisions identify module output declarations and the call and
  registration sites.
- Declaration-local conditions and loops select includes at registration.
  Included declarations retain the caller's bindings and their own source
  locations. Selected and missing optional files participate in watch reloads.
- Source selection conditions can use tracked `files` through prior bindings.
  T004-13 checks evaluation under `-C`, source-relative include resolution,
  grant denial, output overlap, and recovery when a membership change makes
  a missing include inactive.
- Declaration-local includes share list contributions within each block.
  T004-13 checks prefix visibility in conditions, final values in earlier
  recipes, independent loop iterations, inherited bases, typed overrides
  in execution and plan JSON, cycle diagnostics, and invalid operand spans.
- Declaration discovery tracks unions and kind selection for `files`, `dirs`,
  and `wildcard`. T004-13 checks native and WASM membership, exact literal
  paths, kind-only watch changes, grants, and overlap with declared outputs.

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
   only inline form is `${EXPR}`. `@` is the only escape.
4. **Rendering.**
   - `render` decides by type: a `Path` is a template file, while `String` or
     `Bytes` is template content in the `plain` style.
   - Lists and records are invalid in interpolation unless rendered explicitly
     (for example with `join`).

**Exit:** the 016 and 030 acceptance tests pass, and 055 §16 item 15 (the
template part) passes.

- Implemented type-based render dispatch. Path-looking strings and UTF-8 bytes
  are inline content. Path values use read grants, dependency tracking, and an
  inferred style unless explicitly overridden.
- `render SOURCE PAYLOAD` accepts a record after the source. T016-02 covers
  that form and the style and payload forms defined in 016.
- Implemented whitespace-form `@include` with one postfix source operand and
  an optional payload. Relative paths resolve from the containing template.
  Normalized paths detect recursive inclusion. The formatter prints this form.
- Replaced parenthesized directive operands with the revision 2 grammar.
  Plain style recognizes directives on whole lines, comment wrappers delimit
  inline blocks, and raw blocks preserve their bodies. Positional loops use
  explicit list operands and preserve outer bindings.
- String and document interpolation require scalar values. The formatter
  canonicalizes directives and interpolation while preserving raw text and
  escaped directive lines.
- The Go suite, WASM ABI suite, and T016-01 through T016-03 passed on separate
  native and WASM artifacts. Both hosts rebuild on nested template changes and
  pass shell dollar syntax through foreign recipes with directives.
- Template AST inspection uses the document grammar. T004-05 checks the
  conditional and raw-block AST, and T010-09 compares the native and WASM
  output and checks the span of an unterminated directive.
- Tool checks and plans read the parsed foreign recipe. T016-03 checks that
  a real interpolated tool is reported while a tool-shaped raw literal is
  preserved as text.
- Foreign recipes preserve literal and interpolated empty lines. Plain
  document recipes remove the common rule indentation while preserving
  nested indentation and raw bytes. T016-03 exercises both in shell heredocs.
- Document and foreign recipe interpolation accept multiline expressions
  through the shared expression parser. T016-03 checks `match` rendering,
  formatting, comment-wrapper boundaries, and foreign shell output.

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

- Added `indexed` and `fmt`, and made `eval` a first-class standard operation.
  `eval` parses one expression, resolves bindings in its caller's scope, and
  retains the caller's phase and grants. The same generated expression can
  resume a pending observation without replaying prior output on native or
  WASM.
- `fmt` preserves supported scalar and collection kinds, including integral
  floats under 055 §13, and round-trips paths, path patterns, and path templates.
  It rejects runtime values even when they are nested in a collection.
- Discovery accepts `PathPattern` values as pattern operands and returns typed
  paths. Flat `List[Path]` operands contribute existing members, as specified
  by 007 and 038. Returned path kinds do not change host membership signatures.
  Native and WASM produce the same root-relative paths and UTF-8 byte order
  for absolute patterns inside the workspace.
- The CLI and `fmt` share the formatter. Displayed values use comma-separated
  lists, brace records, escaped strings, and kind-preserving numbers. Standalone
  byte values retain raw output.
- The registry uses the canonical comparison predicates and membership test.
  Infix comparison operators retain their standard behavior. Unknown library
  names use ordinary unresolved-name diagnostics, and lexical bindings retain
  precedence.
- The obsolete publication operation is removed. Rule expression bodies and
  Kash `return` publish the exact bytes of one value on native and WASM.
- Symbols retain their kind through evaluation, matching, equality, formatting,
  typed overrides, plugin requests and process outcome states.
- The 007 acceptance table records the named checks and inventory coverage
  still to verify.

## Phase 11: Formatter completion

**Spec:** 018.

Cover every context:

- process-graph layout, which breaks only inside a group's parentheses
- `if` and `match` layouts
- newlines for expanded list elements and trailing commas for expanded records
- rule headers
- the three `$` groups
- templates

Formatting must be idempotent over every fixture. The formatter must never
change what the code means, so add a test that parses, formats, parses again and
compares the ASTs.

- Definition continuations use tabs. Expanded value captures put the graph
  inside the capture delimiters, and process topology keeps operators on the
  opening line when a group or filter branch expands. Grouped conditional
  branches format nested operands at their actual starting columns.
- Kash `def`, `let` and `set` accept a more-indented value after `=` and wrap
  long values there in the formatter. T018-01 checks 61 goldens on native and
  WASM for idempotence, AST preservation, and execution of formatted bindings.

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
   messages for the new boundaries, for example `${` inside a Kash word, which
   should suggest `$name` or `(expr)`.
4. **WASM and embedding.** Keep native and WASM in parity (010). Update the JS
   and Python APIs (028).

- Native and WASM give explicit rule-bearing Kame sources the default build
  grants. A first fragment that contains only values still sets the session's
  restricted policy, and explicit grants replace defaults.
- Standalone WASM expressions resolve executables through the host and use
  the portable default capture limit. The JavaScript embedding tests cover
  command capture, process outcomes, missing tool fallback, run grants, and
  captures in a lazy definition. The Python suite covers revision 2 parsing,
  typed arguments, builds, watches, and explicit disposal.
- T010-01, T010-06, and T010-22 exercise the reactor ABI, CLI parity, and
  memory and process cleanup with revision 2 fixtures.
- `do kash` remains a language-default entry point to the unified runner.
  Specs 009, 013 and 036 agree on the alias and the `do expr` error and help.
  T017-07 verifies both command names on native and WASM.
- The native CLI and WASM language tools accept `expr`, `template`, `km`, and
  `kash`. T004-05 checks the AST goldens, T010-09 verifies byte-for-byte host
  parity, and T009-17 verifies language validation and command option parity.

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
   - `${RELEASE_BINARIES}` in inputs.
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

- The root Kamefile, release helpers, and Go test-wrapper generator use the
  unified grammar. `make -j2 build dist-wasm` builds native and WASM artifacts
  through the Makefile bootstrap. The release key environment fallback and
  explicit definition override were verified separately.
- `make test` reached the CLI harness and exposed unconverted fixtures.
  The native formatter fixture migration restored `make -j2 test-go`.
  The migrated expression definitions and evaluation suites pass independently.
  Signed distribution and mocked publication pass in T015-04 and T015-06.
- Shared CLI fixtures use `.km`, including all three Make-compatible
  discovery locations. The migrated consumers exercise scheduling, caching,
  diagnostics, source discovery, and build inspection.
- The runnable examples and their guides use the unified syntax. T013-04
  verifies composition, freshness, incremental blog generation, and cleanup.
  T013-05 verifies formatting and all ten Kash workflows on native and WASM.
- The Kame file grammar rejects obsolete source control markers through
  ordinary declaration validation. T004-13 passes 108 conditional declaration
  cases on each host.
- T040-01 uses unified rule, task, and include syntax. Automatic clean preserves
  historical artifacts when a typed `generate` declaration prevents verifying
  the current ownership context, including when the declaration returns an
  empty list. Its blocker reports the authored include source and span.
- T009-17 passes the updated command option and effect-boundary matrix on
  native and WASM, including indentation options for Kame source.
- T048-08 and T048-09 use the unified syntax and pass the regular-file and
  borrowed stdin record checks on native and WASM.
- T048-06 verifies lexical break and continue, transfer-time cleanup, async
  ownership, diagnostics, and formatting with unified syntax on both hosts.
- T048-10 verifies selective exit acceptance and preservation of raw and
  effective statuses in invocation outcomes on native and WASM.
- T048-03, T048-04, T048-05, T048-07 and T048-11 verify eager bindings,
  process-like exit boundaries, owned scopes, invocation input and callable
  recipe reuse with revision 2 syntax on native and WASM.
- T012-04 verifies WASM publication backpressure during regular builds,
  cache replay, standalone Kash, cancellation, timeout and watch.
- T012-03 exercises lossless large output, bounded cache replay and
  configured log limits on both hosts. Its shell recipe covers retained-log
  diagnostics for nonzero exits.
- T017-03 exercises pipeline capture, stage failures, capability checks, parse
  and format parity, and graph cleanup on timeout or capture overflow.
- T017-11 and T017-22 verify terminal acceptance and invocation-scoped defer
  behavior with revision 2 bindings and functions. T017-17 exercises explicit
  process forms, typed argv, host parity and capture reuse in normal builds.
- T048-01 verifies stderr routing, descriptor aliases, capture conflicts and
  tee status on native and WASM with revision 2 bindings.
- T017-19 and T017-25 exercise recipe policies, lexical function calls,
  Kash return publication, cache reuse and shared execution deadlines on
  native and WASM.
- T038-06, T006-10 and T006-11 exercise managed service teardown, warm
  watch dependencies and generated symlink outputs with revision 2 fixtures
  on both hosts. The glossary, embedding and contributing references use
  revision 2 syntax and terminology.
- T010-26 and T010-31 verify service readiness, restart, teardown, argument
  defaults in readiness probes, and argument overrides in plans. T010-27
  verifies generated rule execution and provenance on both hosts. T010-28
  through T010-30 exercise cache management, locking, and host cleanup.
- T009-18, T010-07, T010-13, T010-14, T010-19, and T010-20 exercise target
  listing, artifact reads, tool inspection, JSON event streams, cache replay,
  and include diagnostics with revision 2 fixtures. Tool inspection checks
  default Kash recipes and resolves explicit executable heads by tool name.
- T043-01 verifies revision 2 finalization callbacks, cancellation,
  backpressure deadlines, and WASM diagnostic ownership during disposal.
- T048-02, T050-01, and T050-02 verify revised Kash includes and imports
  on native and WASM, including lexical source identity, exports, authority,
  demand-based freshness, and diagnostics. T010-20 also verifies an inline
  absolute include in a Kame plan.
- T042-01 through T042-11 pass with unified topology recipes and outcomes on
  native and WASM. They verify framing, generated inputs, bounded scheduling,
  telemetry, live waits, failure collection, and worker ownership. Predicate
  overrides invalidate reuse, and invalid host telemetry fails the invocation.
- T006-03 verifies `rule always` for captured, grouped, and expression outputs
  on both hosts.
- T017-10 verifies revision 2 dry-run behavior for mixed sources, deferred
  generated reads, typed argument validation, and JSON event parity.
  T009-16 verifies exact stdout and stderr framing, typed value display,
  and literal configuration across ordered sources.
- T049-01 verifies typed byte capture, independent limits, cleanup, optional
  tool lookup, and WASM watch revalidation using unified syntax.
- T017-26 verifies canonical list rendering, lexical functions, return values,
  and cleanup on native and WASM.
- T013-07 verifies that platform overrides bypass host detection and that
  nested Make can use the prebuilt WASM gate. T009-01 exercises the migrated
  C build fixture, including incremental reuse.
- The Kame authoring skill, GNU Make migration guide, language overview,
  and program, rules, and CLI references use revision 2. Native and WASM
  CLI probes verified formatting, module imports and includes, the C build
  and digest-based reuse, foreign-shell interpolation, typed CLI configuration,
  and file publication.
- The expression and Kash references, authoring skill references, cookbook,
  and idioms guide use revision 2 syntax. Native and WASM parse 73 extracted
  examples and run representative arithmetic, matching, topology, retry,
  and recipe examples. T004-12 verifies optional includes, diagnostics,
  and watch redetection after a missing include appears on both hosts.
- T017-08, T017-09, and T017-12 through T017-15 verify include ordering,
  runner JSON events, command recovery, lookup fallback, conditional
  branches, and lexical match arms on native and WASM.
- T017-18 verifies lazy async handles, repeatable wait outcomes, shutdown,
  and handle ownership across ordered Kash and Kame sources on both hosts.
- T017-01, T017-02, and T017-04 through T017-06 verify capture limits, argv
  spread, stage policy, redirection preflight, and source formatting parity.
  T017-24 verifies bounded cleanup and both `finally!` and `defer` during
  cancellation on native and WASM.
- T004-10 verifies continued definition values and rule headers, including
  shell-owned recipe continuations. The Kame file parser regression covers a
  CRLF continuation immediately after a definition's equals sign.
- T004-19 verifies mixed computed and interpolated inputs and their order.
  T047-01 verifies capture routing, command lists, process status, and
  cancellation on native and WASM.
- T036-01, T036-03, and T036-04 verify CLI presentation and tool inspection,
  terminal reflow, raw stderr routing, watch cycles, and OSC progress on both
  hosts.
- T004-20 and T007-15 verify computed target replacement and dangling symlink
  changes during watch. T009-15 and T010-24 verify named target arguments,
  dependency scope, and the retention of overrides during watch reloads.
- T013-03, T013-09, and T015-03 verify independent build artifacts,
  incrementality, generated metadata, and deterministic per-target events.
  The determinism check normalizes absolute effect paths relative to each
  fixture copy.
- The release snapshot's Go metadata matches Linux and macOS on amd64 and
  arm64, and Windows on amd64. The Windows zip passed its integrity check and
  its five entries matched the staged files byte for byte. The Linux binary
  reported release mode and evaluated a revision 2 expression. The extracted
  package's WASM CLI evaluated the same expression through Node.
- T004-14, T004-15, and T006-08 verify positional and regex captures and
  sequenced input groups. T038-05 verifies typed paths, tracked discovery,
  capture and bind, and repeated discovery membership changes on both hosts.
- T014-03 and T014-04 verify runtime pattern construction, recursive captures,
  positional and named regex extraction, and substitution with path templates.
- T014-02 checks explicit file discovery, typed path substitutions and
  capture-scoped replacement effects, then compares the original and formatted
  Kame declarations on both hosts.
- T006-06 passes 133 scoped-environment, cache, authority, and inspection
  checks with revision 2 metadata. In JSON sessions, Kash recipe output is
  attributed to the recipe and retained for cache replay.
- T037-01 passes 127 checks for read-only inspection, dependency graphs,
  deferred discovery, artifact freshness, and downstream impact on both hosts.
- T007-14 passes 107 typed record, digest, and symlink checks on each host.
- T038-01 passes 213 checks for direct Kash recipes, explicit argv spreads,
  typed path interpolation, source coordinates, and atomic publication.
- T017-07 passes 103 shared-session checks. Rule entries selected from one
  fragment share prerequisite execution, and later fragments keep source order.
- T017-20 passes 132 checks for computed stage environments, suppression of
  secret values in diagnostics and traces, typed argv, and rule bindings.
- T009-14 and T010-23 each pass 35 watch scenarios using revision 2 source.
  Coverage includes module and include repair, generated target replacement,
  process cancellation, active graph invalidation, and subscription cleanup.
- Declaration-time generation registers typed Rule values directly. The
  generated-rule Go tests exercise atomic registration, collision diagnostics,
  cycle handling, and batch limits. T009-18 checks that typed generated targets
  appear in listing and status on both hosts.
- Inspection, recipe publication, precedence, runtime events, and plugin
  fixtures use the unified file parser. The Go suite passes with native CLI
  and formatter tests enabled. T013-04 passes all 143 checks after formatting
  the rules publication, composition, and blog examples with the current CLI.
- Planning, execution, and cache fixtures use the unified file parser. The
  separate bare-rule parser and its literal environment suffix are removed.
  Rules without an explicit shell selection default to Kash. The cache tests
  exercise explicit cached tasks, authored shell changes, and stable records.
  Expression bodies use `= EXPR`. The recipe tests verify that the former
  `shell: kame` selector is rejected.
- Kash module loading uses `import` and `from … import`. The Go module tests
  exercise nested source requests, host rejection, cancellation, and reuse.
  The native authoring-directory and module watch tests use explicit exports.
- The clean harness exposed the release signature fixture's cross-platform
  inputs. Both harness targets now declare all four platform binaries.
  T015-04 passes all seven signed-release checks with the artifacts staged.
- The WASM composition diagnostic fixture checks rich source frames and
  native/WASM parity. The opt-in WASM self-build uses the current Kamefile
  and explicit include read grants, and its repeated warm builds pass the
  heap checks. The WASM memory regression also evaluates included definitions
  twice through the same JavaScript program descriptor.
- Line-level templates use `@{…}` and `@@`, as document templates do.
  The template and fixture suites verify literal backslashes, delimiter
  boundaries, and errors for incomplete interpolation.
- Expression contexts bind `inputs`, `outputs`, `args`, and
  `changed-inputs`. T011-04 verifies rejection of positional selectors in
  expressions while preserving literal at-sign bytes in templates and Kash
  words. T006-07 verifies changed-input freshness on both hosts.
- Target argument tests use the CLI environment override spelling
  `env/NAME=VALUE`. T009-18 exercises a generated service with a computed
  target name and bound argument, alongside generated file and task forms.
- The generated-rule Go tests check every limit at and above the bound and
  reject writes, process calls, captures, and content reads during generation.
- T007-12 and T007-16 verify first-class operations, lexical precedence,
  data-last composition, numeric comparisons, and process outcome predicates.
- T011-04 verifies actionable diagnostics and redaction. T031-01 verifies
  resource URI identity, grants, globbing, and watch on native and WASM.
- T038-03 and T041-01 verify scalar/JSON boundaries and typed predicate and
  substitution behavior. T012-02 verifies live stdout and stderr publication.
- T006-05, T008-03, and T009-19 verify effect order, cache invalidation, and
  lock ownership and recovery.
- T004-11, T004-13, and T004-16 verify wildcard invalidation, declaration
  conditions, computed output registration, formatting, and authored
  provenance. Both hosts pass 108 conditional-declaration cases and 76
  computed-output cases. T011-03 verifies tool diagnostics, operand spans,
  dependency traces, and bounded process-diagnostic metadata.
- T017-21 verifies scalar word composition, explicit list spreading, typed
  stage settings, preflight checks, and formatter round trips on both hosts.
- T017-16 and T017-23 verify scoped environment grants, exact bytes, redacted
  traces, selected exit status recovery, and retry callback order on both hosts.
- T007-11, T009-11, and T036-02 verify tool override precedence, intact
  concurrent output, and reuse explanations without disclosing values.
- T009-13 verifies literal and typed overrides, inferred target arguments,
  and lazy configuration checks. Registration propagates authored `fail!`
  diagnostics and continues to reject operations that require runtime work.
- The root README was updated with explicit approval during release review.
  - List `Kamefile`, `Makefile.km`, `make.km`, and `src/km/main.km` in discovery order.
  - Use an explicit `task default` and a file `rule` with an `= EXPR` body in the
    greeting example. Read the first prerequisite through `inputs.0`.
  - Update the syntax table to show `true`, `false`, and `nil`, comma-separated
    lists, brace records, `=>`, value pipes `|>`, and `${EXPR}`. Distinguish
    `task`, `task cached`, and `service`, and show the `inputs` and `outputs`
    selectors.
  - Describe default Kash actions, external-shell document templates selected
    by brace metadata, and expression-body publication. In the C example use
    `files`, `subst`, explicit `task` and `rule` headers, and `x/cc` with
    `$inputs.0` and `$outputs.0`. The greeting dry run, content rebuild, and C
    compilation after a header change were verified on native and WASM.
- The clean harness exposed a timing-sensitive assertion in T010-26. The
  revised test waits for the service to install its signal handler and
  measures shutdown after the consumer finishes. All five checks pass.
- Native POSIX output pipes stay open through sink backpressure after child
  exit. `TestChildOutputDrainsAfterExitUnderBackpressure` reproduces byte loss
  with the former pipe ownership and passes with the new drain. T012-02 and
  T012-03 pass with exact live output on both hosts.
- The source syntax scan found no former selectors, literals, definition
  forms, or extensions in the build file, release scripts, examples, and
  converted user guides. Rejection fixtures still exercise invalid input.
  Native and WASM also returned `PARSE_ERR` for the former definition, include,
  bracket-record, input and output selector, indirect selector, bare task,
  and colon-terminated Kash `if` forms.

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

### Spec 055 §16 evidence

The final audit also requires the refining specs' acceptance sections and a
passing clean harness. These are the direct checks for the overview.

| Item | Checks |
| --- | --- |
| 1. Grammar context | T010-09 compares application and command trees for the same source and checks that a PATH executable, a lexical binding, and changes to an imported file leave the relevant ASTs and formatting unchanged on both hosts. |
| 2. Application and precedence | `TestInfixPrecedence`, `TestInfixOperatorTokenBoundaries`, T016-03 for zero-argument calls and grouping, and `TestNumericOperatorsUseLibraryContracts` for float division and integer remainder. |
| 3. Literals | `TestBraceRecordSyntax`, `TestListSpreadCloneAndFormat`, `TestPathAtomBoundaries`, and T005-01. |
| 4. Sections and pipes | `TestPlaceholderSectionBoundaries`, `TestPlaceholderSectionsEvaluateAsLambdas`, T007-12, and T017-03. |
| 5. Name resolution | `TestNativeExpressionNamesResolveLexicalStandardAndEnvironment`, T017-20, T048-02, and `TestNativeAuthoredImportsBindOnlyExports`. |
| 6. Bindings and unit state | T048-03 and T048-05, `TestNativeKashEnvironmentWritesStayInTheirExecutionUnit`, T017-20, and T048-06. T017-16 checks executable lookup after a function changes `PATH` and when a capture changes it privately, on both hosts. |
| 7. Words | T017-21 and T038-01 check adjacency, slash boundaries, explicit list spreading, typed redirection and literal data. |
| 8. Outcomes and status | T017-17, T055-01, T055-02, T048-04, and T048-10. |
| 9. Paths | T014-02, T014-03, T038-05, `TestNativeModulePathsUseTheAuthoringFile`, and `TestNativeAuthoredImportsResolveNestedModules`. |
| 10. Discovery | `TestNativeDeclarationDiscoveryWatchRecoversFromOverlap`, `TestNativeDeclarationDiscoveryRejectsGeneratedOutput`, T004-13 for conditional includes, kind filters, unions, and watch recovery, T038-05, and T010-23. |
| 11. Declaration control flow | `TestNativeDeclarationBlocksCloseOverIterationBindings`, `TestNativeDeclarationIncludesSpliceIntoEachIteration`, and T004-13. |
| 12. Generation | `TestGeneratedBatchFailureRegistersNoExecutableProgram`, `TestGeneratedTargetCannotOverlapAnAuthoredRule`, `TestGeneratedDeclarationEnforcesBatchRuleLimit`, `TestGeneratedDeclarationEnforcesBatchListItemLimit`, `TestGeneratedDeclarationEnforcesTextLimits`, T009-14, and T010-23. T011-04 verifies that generated failures retain the constructor call, registration site and iteration, and that a collision identifies both iterations. |
| 13. Modules | `TestNativeAuthoredImportsBindOnlyExports`, `TestNativeAuthoredImportsResolveNestedModules`, `TestNativeAuthoredModuleSelectsConditionalIncludes`, and T048-02. |
| 14. Rule bodies | T044-01 checks expression bodies and Kash return publication. T016-03 checks foreign-shell interpolation and literal bytes. |
| 15. Templates, formatting and evaluation | T004-05 and T010-09 inspect the document template AST. T016-03, T018-01, and T011-04 cover formatting, evaluation, and diagnostics. |
| 16. Backend agreement | T010-09 compares ASTs, T011-04 checks diagnostic boundaries and verifies that a command argument in a rule retains its call site and both loop iterations across a scoped include, and T009-14 plus T010-23 exercise watch and reload. |

### Refining spec evidence

| Spec | Named checks |
| --- | --- |
| 004 grammar | See the detailed checks below. |
| 005 evaluation | See the detailed checks below. |
| 006 runtime | See the detailed checks below. |
| 007 library | See the detailed checks below. |
| 009 CLI | See the detailed checks below. |
| 010 WASM | See the detailed checks below. |
| 011 diagnostics | See the detailed checks below. |
| 012 streams | See the detailed checks below. |
| 013 conformance harness | See the detailed checks below. |
| 014 sections, paths and patterns | See the detailed checks below. |
| 016 templates | See the detailed checks below. |
| 017 Kash | See the detailed checks below. |
| 018 source style | See the detailed checks below. |
| 020 watch | See the detailed checks below. |
| 022 target arguments | See the detailed checks below. |
| 023 wildcard sources | See the detailed checks below. |
| 024 managed services | See the detailed checks below. |
| 025 generated declarations | See the detailed checks below. |
| 028 embedding | See the detailed checks below. |
| 029 pattern extensions | See the detailed checks below. |
| 030 template extensions | See the detailed checks below. |
| 036 presentation | See the detailed checks below. |
| 037 build inspection | See the detailed checks below. |
| 038 direct Kash and values | See the detailed checks below. |
| 041 pattern predicates and substitution | See the detailed checks below. |
| 042 Kash streaming | See the detailed checks below. |
| 043 finalization | See the detailed checks below. |
| 044 rule bodies | See the detailed checks below. |
| 045 precedence | See the detailed checks below. |
| 046 scripting and publication | See the detailed checks below. |
| 047 output ports and command lists | See the detailed checks below. |
| 048 practical scripting | See the detailed checks below. |
| 049 process inspection | See the detailed checks below. |
| 050 modules | See the detailed checks below. |
| 051 recipe authority | See the detailed checks below. |
| 052 interactive Kash | See the detailed checks below. |
| 053 Kash history | See the detailed checks below. |
| 054 Kash options | See the detailed checks below. |

#### 004 grammar acceptance

| Contract | Checks |
| --- | --- |
| Separate grammars and context | T004-01 checks the grammar matrix and T004-05 checks semantic AST fixtures. T010-09 compares action and value parsing with different process lookup environments. `TestSessionSharesLazyDefinitionsAndKeepsParserBoundaries` checks composition across fragments. |
| Names, literals, containers and precedence | T004-02 checks invalid expression and numeric fixtures. T005-01 and T005-02 exercise values, fields and indexes. `TestInfixPrecedence`, `TestInfixOperatorTokenBoundaries`, `TestListSpreadCloneAndFormat`, and `TestBraceRecordSyntax` check structural boundaries. |
| Sections, pipes and selection | T014-01 checks sections and T007-12 checks pipes, insertion and order. T005-03 and T005-06 check selected operands and outcomes. T018-01 checks round trips and retained spelling. |
| Interpolation, path atoms and command words | T011-04 checks malformed interpolation in expressions, document templates and shell recipes. T016-03 checks interpolation and its effect boundary. T014-02, T038-05 and T017-21 check paths, captures and argv. |
| Bindings, includes and imports | T004-17 and T004-18 check definitions and contributions. T048-03 checks eager bindings. T004-12, T004-13, T050-01 and T050-02 check optional and conditional includes, imports, scope and authored errors. |
| Rule headers, bodies and declaration actions | T004-09, T004-11, T004-14, T004-16 and T004-19 check target kinds, inputs and computed headers. T004-13 and T009-18 check planning-time discovery, control flow and generation. T044-01 checks rule body variants. |
| Continuation and formatting | T004-06, T004-08, T004-10 and T018-01 check canonical output, comments, continuation and parse-format-parse. |

#### 005 evaluation acceptance

| Contract | Checks |
| --- | --- |
| Lazy, eager and mutable state | `TestDefinitionIsLazyAndPublishesTemplateValue` and `TestLiftedDefinitionReevaluatesAfterDependencyInvalidation` check lazy definitions and invalidation. T048-03, T048-05 and T017-16 cover eager bindings, unit state and authorized environment removal. |
| Function and name resolution | T017-20, T017-26 and T014-01 exercise namespace order, functions and placeholder sections. `TestLexicalFunctionAndOperationShadowing` checks lexical resolution. |
| Ordered operands, reused operations and cancellation | T005-05 checks operand failures. `TestListSpreadRetainsCompletedOperands` checks suspension, `TestDependencyUpdateCancelsStaleOperationAndRestarts` checks stale results, and T007-12 checks pipe order. |
| Conditional evaluation, lookup and outcomes | `TestIfAndOrWithMatchFormsAreLazy`, T005-03 and T005-06 check short-circuit behavior, record scope and captures. T005-02 and T005-04 check selectors and argument frames. |
| Host authority and values | `TestCapabilitiesBlockOperationsBeforeInvocation`, T004-13 and T010-22 check denied or unselected reads. T041-01 and `TestPatternReplacementEffectsAreDemandedOnlyOnMatch` check scoped pattern calls. T005-01 and `TestLibraryValueSourceRoundTripsAndEvalAuthority` check value rendering and explicit evaluation. |

#### 006 runtime acceptance

| Contract | Checks |
| --- | --- |
| Selection, groups and shared instances | The 045 checks below cover precedence. T006-02 checks prerequisite selection, T006-04 and T006-08 check order-only and sequenced inputs, and `TestDiamondDependencySharesPrerequisite` checks shared work. |
| Freshness, forced execution and reuse explanations | T006-01, T006-03, T006-07 and T036-02 check freshness, `always`, change selection and reuse reasons. `TestCacheLookupRejectsMutatedDependencyManifest` checks manifest validation. |
| Environment and live input validation | T006-06 and T006-10 check recipe environments, configuration and watch. T009-14 and T010-23 check changed inputs and recovery on both hosts. |
| Publication, suspension and cancellation | T006-05, T006-09, T010-21 and T044-01 check effects, suspension, prerequisites and value publication. T011-03 checks rule failures and locations, and T009-14 checks process reaping. |

#### 007 library acceptance

| Acceptance item | Checks |
| --- | --- |
| Canonical predicate names and callable forms | `TestPredicateNamingContract`, `TestPredicateAndSequenceConsistency`, T007-12, and T007-16. |
| Numeric operator contracts, overflow, and invalid divisors | `TestNumericOperatorsUseLibraryContracts` checks operator and operation parity. `TestRemediation3NumericBounds` checks finite results and integer bounds. |
| `apply` arity, typed and rest arguments, ownership, and suspension | `TestApplyNativeAndLambdaArguments`, `TestApplyFormsReleaseCallables`, and T007-12. |
| `first` and `nth 0` | `TestPredicateAndSequenceConsistency`, T007-02, and T007-16 cover list and Unicode string cases, including empty inputs. |
| Data-last operations and pipe placement | `TestSubjectLastOperationsAndPipes`, `TestCanonicalTransformationPipes`, `TestValuePipeStages`, and T007-12 cover direct and piped calls. Audit every callable in the library inventory against these cases. |
| `put` update, append, nil, and immutability | `TestCanonicalTransformationPipes` and the `items` and `put` checks in `operations/test/records_digest.go`. |
| Kind tests, `empty?`, and `blank?` | `TestPredicateAndSequenceConsistency` iterates the core kinds. `TestMembershipBlankAndRelativeJoinpath` checks blank semantics. |
| `in?`, `has-key?`, and `success?` validation | `TestPredicateAndSequenceConsistency`, T007-13, and T007-16 check structural membership, present nil, and malformed outcomes. |
| Exact integer and mixed numeric ordering | `TestExactNumericOrdering` and T007-16 cover int64 limits, values beyond 2^53, fractional floats, infinity, and NaN. |
| Operation arity, kinds, and nonmutation | `TestOperationDiagnosticsRetainOperandAndContract`, `TestEveryStandardOperationDeclaresAContract`, T007-07, and the collection and record ownership tests. |
| Pure operations keep the host idle | `TestPureOperations` executes every registered pure operation with direct host requests enabled and checks for host requests, observations, effects and publications. `TestStreamPredicatesArePureBooleanAndHostFree` and T007-04 check stream callbacks. |
| Callback order | T007-02 and the suspension and ordering cases in `operations/test/operations.go`. |
| `filter` equality shorthand | `TestStructuralEqualityAndFilterShorthand`, `TestByteEqualityAndHostValueLimits`, and T007-13. |
| Composed paths, collections, and tracked filesystem operations | T007-05, T007-09, T007-10, and T014-02. |
| Lexical path operations | T007-04 and `TestMembershipBlankAndRelativeJoinpath`. |
| `joinpath` anchors and artifact classification | `TestMembershipBlankAndRelativeJoinpath` and the computed path cases in T004-16. |
| Sorted `Path` discovery and recursive globbing | T007-05 and T007-09. |
| Filesystem kind shorthands and flag validation | T038-02 and `TestWildcardFlagsRejectInvalidCallsBeforeRequests`. Declaration-time kind options are a separate 025 acceptance item. |
| Strict typed patterns, list shape, and union dependencies | T007-09, `TestWildcardUnionResumesWithoutRevisitingEarlierPatterns`, and `TestWildcardUnionPreservesFirstPathProvenance`. |
| Path capture and binding | T014-03, T014-04, and T041-01. |
| Canonical dependencies for reads and discovery | `TestFileReadsObserveContentExistenceAndMetadataSeparately`, `TestWildcardKindMasksRetainExcludedEntryObservations`, T007-09, and T007-15. |
| Denied capabilities produce no host effects | T007-06, T010-32, and `TestBoundEnvironmentReadsRespectSnapshotAndGrants`. |
| Output effects and recipe phase boundaries | T038-04, T044-01, `TestDefinitionOutputFlushesOnlyAfterSuccessfulResume`, and `TestContractMetadataDoesNotAuthorizeNestedEffects`. |
| `sh!` status, separate bounded streams, and policy | T007-06, T010-06, and `TestShRejectsUnsupportedOptionsBeforeDryRun`. |
| `shell-template!` fragment counts and safe value interpolation | T007-06 exercises the public operation on both hosts. |
| Terminal styles and selective resets | T007-03, including WASM parity and nonstring rejection. |
| `line` and existing text/capture behavior | `TestLineRemovesOnlyOneTerminalLF`, T012-02, and T017-26. |
| Pattern `subst` callable and `resub` fallback | T014-02 and T041-01. |
| Match-local replacement and deferred effects | `TestPatternReplacementEffectsAreDemandedOnlyOnMatch`, T014-02, and T041-01. |
| Allocator cleanup | The operation suite uses the tracking allocator for returned values, including the ownership and container tests. |
| Strict equality, inequality, and boolean negation | `TestStructuralEqualityAndFilterShorthand`, `TestPredicateAndSequenceConsistency`, T007-13, and T007-16. |
| Distinct kinds and numeric equality across int and float | `TestStructuralEqualityAndFilterShorthand`, `TestExactNumericOrdering`, and T007-13. |
| Ordering operation pairs and invalid operand kinds | `TestExactNumericOrdering`, T007-07, and T007-16. |
| `fmt` and `eval` round trips through expression grammar | T007-13 checks value-kind preservation. T005-05 and T011-04 cover disallowed statement and action forms. |

#### 009 CLI acceptance

| Contract | Checks |
| --- | --- |
| Discovery, invocation and arguments | T009-02 checks discovery and T009-12 checks source composition. T009-13, T009-15 and T009-17 check typed configuration, literal target arguments and option routing. T010-06 and T010-24 exercise corresponding WASM entry points. |
| Inspection and reuse presentation | T009-05, T010-11 and T037-01 check read-only inspection. T036-02 checks reuse reasons and parity of results and effects. |
| Output, status and publication | T009-04, T009-06, T009-08 and T009-16 check JSON, exact output, dry-run and script output. T047-01 checks explicit exit status across runners. T010-15 checks interruption and process cleanup. |
| Help, formatting and policy | T009-01 and T009-17 check help and option errors. T017-07 checks the `do kash` alias and `do expr` error and help. `TestNativeFormatPreservesPermissionsAndIgnoresStagingSymlink` and T004-07 check in-place formatting. T010-03 checks execution grants. |
| Job limit | T006-08 checks that a Kash and a foreign-shell recipe serialize at `-j 1`, independent prerequisites can run together at `-j 2`, and a sole slot supports a discovered file prerequisite followed by a pipeline on native and WASM. The regression fails on both hosts with the original batch-only limit. T010-31 checks managed services and their consumers with the default job limit. |

#### 010 WASM acceptance

| Contract | Checks |
| --- | --- |
| Reactor, host forwarding and request lifetime | `make wasm-check` passed in the release snapshot. `tests/wasm/check-imports.mjs` reported only Go runtime WASI imports and 102 exported symbols. `tests/wasm/go-reactor.mjs` verified pure and hosted expressions, CLI calls, buffer bounds and stale handles. T010-02, T010-04 and T010-05 check the ABI and read/write/process forwarding. T010-16 checks cancellation. |
| Pure evaluation, parsing and formatting | T010-06, T010-09 and T010-10 check CLI modes and the shared fixtures. `TestPureOperations` verifies the registered pure operations with direct host requests enabled. |
| Allocation, memory reuse and wrappers | T010-22 checks exhaustion and repeated ABI use. T010-32 and T010-33 check embedding API behavior and disposal in JS and Python. T010-19 checks fingerprints and cache parity. |
| Materialization and presentation | T010-03, T010-11, T010-13, T010-14, T010-17 and T010-21 check materialization, plans, tools, machine/human presentation and publication. T017-16 checks rejection of required terminal handoff. |

#### 011 diagnostic acceptance

| Contract | Checks |
| --- | --- |
| Registry and public failure codes | T011-01 checks uniqueness, shape, and registration of the streaming, watch and other public codes. T011-02 checks representative usage, planning, execution and authority codes. |
| Language boundaries, spans, recovery and secrets | T011-03 checks operand spans, propagated context, and bounded cause metadata. T011-04 checks boundary diagnostics and verifies that evaluated secret values stay out of recovery output on both hosts. |
| Composition and authored origins | T004-13 checks source-selection context and authority on both hosts. T011-03 checks included recipe and dependency locations. T011-04 checks the original failure, the argument call site, and the inner and outer declaration iterations in their respective files on both hosts. `TestDiagnosticBacktraceUsesRetainedAuthoredSource` checks that source excerpts outlive parsing. |
| Presentation and structured diagnostics | T011-01 checks human and JSON parity. `TestDiagnosticPresentationEnvironmentAndJSONEarlyErrors` checks plain, colour and JSON selection. `TestFailurePresentationDoesNotRepeatDiagnosticDetails` checks diagnostic ownership and rendering. |
| Finalization and memory | T048-04 and T047-01 check body failures and cleanup failures. T010-22 checks WASM exhaustion reporting. `TestCloneOwnsStructuredDiagnosticContext` checks ownership of spans, frames, and cause data. |

#### 012 stream acceptance

| Acceptance item | Checks |
| --- | --- |
| Scalar publication and completion | `TestPublishedAtomCanWaitForDependencyWake` checks the scalar result. `TestTerminalSourceReleasesMaterializer` checks terminal release. |
| Batch and empty batch publication | `TestMaterializesBatch` and `TestMaterializesEmptyBatch` check list shapes. |
| Final batch policy | `TestEndStreamCommitsNonemptyBatch` and `TestPollCompletedDiscardsUncommittedBatch` distinguish explicit end from ordinary poll completion. |
| Nested sources and collections | `TestEngineResumesOuterSourceAfterNestedStream`, `TestNestedSourceUsesItsOwnBatch`, `TestMaterializesNestedCollections` and `TestMaterializesEmptyNestedCollection`. |
| Unbalanced controls and atoms in batches | `TestEndStreamReleasesCollectionBuilder` and `TestAtomInsideBatchReportsExprValue`. |
| Waiting source completion identity | `TestWaitingSourceConsumesMatchingCompletion` and `TestWaitingSourceRejectsWrongRequestID`. |
| Exact source release | `TestInvalidateAndCancelReleaseSources`, `TestFailureReleasesActiveSourceAndChunks` and `TestNormalCompletionReleasesSourceOnce`. |
| Subscriber ownership under coalescing | `TestSubscribersOwnCoalescedBatchLists` mutates caller-owned batch data and checks the other subscriber's copy. |
| Ready-source fairness | `TestReadyStreamDoesNotStarveDependencyOrCompletion`. |
| Repeated allocation and cancellation | The core stream tests exercise builder, payload and source cleanup. Verify the sanitizer gate on the current snapshot. |
| Generator pull, bare return and invalid return value | T055-01 checks yield order, completion and rejecting `return EXPR`. |
| Early generator cancellation and cleanup | T055-01 stops after a bounded prefix and checks deferred effects. T055-02 checks successful graph shutdown when its consumer closes. |
| Stdout and yielded values | T055-01 checks collected values and published effects separately. |
| Single consumer and materialization boundary | T055-01 rejects a second consumer and checks explicit collection. |
| Live output and sink backpressure | T012-02 verifies publication before completion. T012-03 checks exact bytes beyond the retention limit and bounds both a pause in output and total runtime. `TestChildOutputDrainsAfterExitUnderBackpressure` gates the sink past child exit before checking the last chunk. |

#### 013 conformance harness acceptance

| Contract | Checks |
| --- | --- |
| Catalog and fixture isolation | T013-01 checks both directions between catalog and executable files. T013-02 checks that test runs preserve fixture sources. |
| Scheduling, log isolation and worker shutdown | T013-06 and T013-08 check temporary test paths, exit status, exclusivity, lock ownership and signal cleanup. |
| Runnable examples and compiled artifacts | T013-03, T013-07, and T013-09 check compiled binary metadata, build behavior and artifact modes. T013-04 and T013-05 build and execute the shipped examples. |
| Lifetimes and release gate | `TestRunLifecycle`, `test-native-host`, `make test-sanitize`, and `make test-all` cover the current Go build and pointer checks. Supplemental Clang AddressSanitizer runs passed with `CGO_ENABLED=1 CC=clang ASAN_OPTIONS=detect_leaks=1:halt_on_error=1` and the commands `go test -p=1 -asan ./core/... ./lang/eval/... ./program/...` and `go test -p=1 -asan ./cmd/kame -run '^TestRunLifecycle$'`. In an isolated snapshot, `make test-all` exited 0 with 23 build targets complete, 212/212 ordinary suites, and 211/211 Go `checkptr` suites. The run reported `INPUT_UNSTABLE` for the Windows ZIP and the Go source glob, which includes generated version metadata. The non-generated Go and JavaScript runtime sources matched the worktree after the run. The generated version matched `VERSION`, and all five ZIP entries passed integrity and byte comparisons against the staged files. A targeted rerun of both artifact targets exited 0 without the warning. The expanded T017-16 environment checks passed separately. Spec 013 describes a Solod/Clang leak gate. |

#### 014 sections, paths and patterns acceptance

| Contract | Checks |
| --- | --- |
| Sections and value pipelines | T014-01 checks placeholder arity and nested section evaluation. `TestPlaceholderSectionBoundaries` checks parser boundaries. `TestPlaceholderSectionsEvaluateAsLambdas` checks evaluation, and `TestValuePipeCloneAndFormattingPreserveAuthoredStructure` checks insertion, ASTs and layout. |
| Path kinds, captures and safe construction | T014-02, T014-03 and T014-04 check capture, bind, computed patterns and regex patterns. `TestPathAtomBoundaries` checks authored paths and their kinds. T038-05 checks computed segments as path data. |
| Replacement scope, values and evaluation | T014-02 and T041-01 check replacement forms, nonmatches, composition and list mapping. `TestComputedPatternMatchAndReplacement` checks computed pattern values. `TestPatternReplacementEffectsAreDemandedOnlyOnMatch` checks evaluation and effect resumption. |

#### 016 template acceptance

| Acceptance item | Checks |
| --- | --- |
| References, calls, and authored interpolation spans | T016-03 checks named and qualified values, zero-argument calls and calls with arguments. T004-05 checks the document's interpolation span, and `TestStringTemplateRetainsExpansionSpans` checks the string form. |
| Missing and non-scalar interpolation | T016-03 rejects missing references, lists, records, bytes and callables. It checks the byte and callable kinds and the explicit-call tip. |
| Expression forms across interpolation contexts | T016-03 checks pipes, groups, calls, lambdas, sections, multiline matches, raw regions and escapes. T010-09 and `TestSchemaTwoSemanticFields` check AST types across contexts. |
| Conditional operand grammar | T016-03 checks value and application operands, including a zero-argument predicate. `TestDirectiveExpressionShorthandAndCanonicalFormatting` checks parsed shorthand. |
| Hash-style conditionals | `TestRenderContentTemplate` and T016-02 render the true branch. T016-03 renders the false branch. |
| Ordered elif selection and fallback | T016-03 checks elif and else selection. `TestDirectiveExpressionShorthandAndCanonicalFormatting` checks the lowered chain. |
| Foreign-shell branches and dollar bytes | T016-03 checks shell dollar syntax, whitespace-only and interpolated empty heredoc lines, and raw content with rule indentation removed. |
| Loop order and separators | T016-03 compares exact iteration bytes and separator placement. |
| Loop destructuring and explicit indices | T016-02 checks explicitly indexed lists and ordered record keys. T016-03 checks rest parameters and rejects incorrect item shapes. |
| Trim and end labels | T016-02 checks CRLF trim behavior. T016-03 rejects malformed markers and mismatched labels. |
| With bindings | T016-03 checks record field binding and rejects a non-record. T016-02 checks inline binding. |
| Let scope | T016-03 checks the binding, and `TestDocumentLetTransfersBranchConditionsAndPatterns` checks nested lowering. |
| Include scope, payload and origin | T016-03 checks relative lookup through nested includes, payload binding and invalidation. |
| Ordered match, captures and fallback | T016-02 and T016-03 exercise both outcomes and skip unselected effects. |
| Raw and escaped text | T016-03 compares exact bytes. `TestRawScannerNeverParsesMalformedLiteralContents` checks unparsed raw content. |
| Literal directive-like words and comments | T016-03 covers `@media`, `@iffy` and `@example.com`. `TestDocumentScannerKeepsExpressionContentsAndKeywordPrefixesLiteral` checks token boundaries. |
| Directive lines leave no blank lines | `TestRenderContentTemplate` checks exact output from a hash template. T016-03 compares the shell result. |
| Directive diagnostic kinds and spans | T010-09 checks an unterminated block span, T016-03 checks malformed and mismatched operands, and `TestDocumentStrayEndIsBlockError` checks stray endings. |
| Render source types and grants | T016-03 distinguishes path-looking strings, bytes and typed paths. T016-01 checks read denial and explicit grants. |
| Extension inference and explicit style | `TestDocumentAutoStyleUsesExtensionAndContent` checks representative inference and ambiguous content. T016-03 compares all explicit styles and rejects an uninferred path. |
| Template concatenation and kind checks | `TestComparisonsCatAndText` checks scalar concatenation and record rejection. T016-03 checks list and byte rejection. |
| UTF-8 decoding | T016-01 checks valid UTF-8. T016-03 rejects an invalid UTF-8 byte sequence. |
| Conditional branch laziness | T016-03 puts an effect in an unselected nested branch. |
| Tracked template dependencies | T016-03 edits an included file and changes file membership, read bytes and an environment value inside a single template. |
| Include cycles | T016-03 rejects normalized self-inclusion with `TPL_CYCLE`. |
| Verbatim round trip | T016-03 compares raw and escaped source bytes after formatting. |
| Render CLI and check mode | T016-01 verifies `--define`, exact bytes and diagnostics without output in `--check`. |
| Rendering cleanup | `TestRenderContentTemplate` exercises success, no-match and errors, releasing the program and evaluation results. Direct ownership verification remains part of the 030 audit. |

#### 017 Kash acceptance

| Contract | Checks |
| --- | --- |
| Functions, lexical names and transfers | T017-20 and T017-26 check lexical resolution and invocations. `TestNativeKashFunctionCapturesAndOutcomes` checks function captures. T048-04 and T048-06 check transfers and cleanup. `TestGeneratorInvocationState` checks internal suspension. |
| Bindings, cwd and environment | T048-03 and T048-05 check binding evaluation and execution scopes. T017-16 checks environment authority. `TestNativeKashDirectoryChangesStayInTheirExecutionUnit` checks persisted cwd and capture isolation. |
| Words, fragments and redirection | T017-21 checks typed argv and fragments, T038-01 checks composition, and T048-01 checks redirection. T018-01 checks format stability. |
| Capture, outcomes and ownership | T017-17, T047-01 and T048-10 check capture, selected status and recovery. T042-01 and T042-10 check topology and process lifetime. `TestNativeKashWaitObservesOwnedAsyncOutcomes` and `TestNativeKashKillSignalsAndCancelsOwnedWork` check async job operations. |
| Conditions, setup and backend agreement | T017-25 checks deadlines, T046-01 checks typed loops, and `TestNativeKashStatementMatchSelectsTypedAndPatternArms` checks match. T009-12 and T010-06 check source dispatch and shared run semantics. |

#### 018 source style acceptance

| Acceptance item | Checks |
| --- | --- |
| Formatting is byte-idempotent | T018-01 formats every golden twice on both hosts. |
| Equivalent AST excluding spans | T018-01 compares parsed JSON before and after formatting, including the multiline template expression. |
| Native and WASM canonical text | T018-01 compares both backends to the same expected bytes for every case. |
| Parentheses, literal kinds, pipes and comma rules | T018-01 compares structural ASTs and exact collection and graph text. `TestFloatFormattingPreservesKindAndBits` checks numeric literals. `TestValuePipeCloneAndFormattingPreserveAuthoredStructure` checks selectors and pipe structure. |
| Authored backslash continuations | T018-01 joins a continued expression and checks that the output contains no continuation marker. |
| Tabs, separators, operator spacing and block headers | T018-01 checks tabs, collection separators and a task header. `TestValueFormatterSharesFragmentGrammarAndComments` checks binary operator spacing. |
| Structural layout goldens | T018-01 covers nested applications, definitions, records, lambdas, conditionals, match arms, grouped dependencies, order-only dependencies, metadata, expression bodies and verbatim text. |
| Width boundaries | T018-01 checks exact 80 and 81 column expressions, definitions and rule prefixes and checks Unicode column counts. |
| Inputs with no legal break point | T018-01 keeps a long atom and an ungrouped application intact and compares their ASTs. |
| Order, comments, blank lines and literal text | T018-01 checks the AST, literal bytes, comments, blank lines, and execution of formatted definitions and statements. `TestValueFormatterSharesFragmentGrammarAndComments` checks comment positions. |
| Identifiers retain their spelling | T018-01 checks mixed case and underscores in a definition and its operand. |
| Formatting has no effects or name resolution | T018-01 formats a declaration with an effect and an absent include. T010-09 compares formatting with and without a matching PATH executable. |
| Value pipes and compact contexts | T018-01 and `TestValuePipeCloneAndFormattingPreserveAuthoredStructure` cover inline and expanded pipelines, nested calls, repeated placeholders, strings, templates and Kash words. T018-01 compares an expanded rule header containing a metadata pipe and retained comments across both hosts. |
| Kash graph expansion and preservation | T018-01 compares graph ASTs and expected text for nested fan-out destinations, filter branches and implicit stages. `TestKashGraphFormattingCanonicalizesOperatorStages` checks idempotence and operator spelling. |

#### 020 watch acceptance

| Contract | Checks |
| --- | --- |
| Change detection, cancellation, and recovery | T009-14 and T010-23 run `watch-runtime.py` on both hosts, checking consolidated warnings, changed inputs, source reload and repair, quiet-period coalescing, and reaping before cancelled work restarts. |
| Shared roots and membership | T009-14 and T010-23 check shared prerequisites, idle behavior, recursive glob directories and missing sources. T004-13 checks declaration snapshots. T004-20 checks computed outputs. |
| Freshness, policy and observers | T009-14 and T010-23 check preserved-mtime changes, source observation and bounded output. T007-15 checks symlinks, and T006-10 checks environment state. `watch-runtime.py` checks Linux read counts and polling fallback. |
| Portable instances and ABI | T010-25 checks retained snapshots, invalid resource requests, isolation and disposal. `TestModuleWatchStartsFromAcceptedSourceBytes` checks retained source bytes in the program API. |

#### 022 target argument acceptance

| Acceptance item | Checks |
| --- | --- |
| Required and optional arguments through parse, format, dependencies and recipes | T009-15 and T010-24 check both backends. `TestNamedTaskArgumentsParseFormatAndValidate` verifies canonical declarations. T009-13 checks configuration precedence, and T009-18 checks generated service arguments. |
| Invalid operands and binding conflicts | T009-15 and T010-24 check missing, unknown, duplicate and malformed assignments plus the diagnostic name. They repeat rejections during execution and check the absence of process and effect events and recipe outputs. `TestNamedTargetArgumentCannotShadowDefinition` checks a conflict with a definition. |
| Literal defaults | T009-15 and T010-24 assert that an environment-shaped default remains literal in the plan and the recipe. `TestNamedTargetArgumentDefaultsDoNotRequestHost` inspects environment- and definition-shaped defaults with host forwarding enabled. |
| Canonical order and value-based cache identity | T009-15 and T010-24 run the same assignments in different orders and compare execution counts across equivalent and changed values. |
| Captures and arguments remain distinct | T009-15 and T010-24 check distinct producer fields and runtime bindings when a capture and an argument share a name. They check argument-based cache keys. `TestCachedTaskCapturesHaveDistinctIdentities` checks capture-based keys. |
| Other CLI operand classes | T009-17 checks option routing. T009-15 and T010-24 run multiple targets with named values, select a literal path containing `=` after the build delimiter, and verify that the runner's program arguments retain assignment- and option-shaped text. |
| Watch and dry-run agreement | T009-15 and T010-24 check retained values across invalidation and source reload, and compare plans and dry-run behavior on both hosts. |

#### 023 wildcard source acceptance

| Acceptance item | Checks |
| --- | --- |
| Shared lazy recursive and empty discovery | T007-09 checks recursive unions, deduplication and empty members. `TestGlobRevalidationSharedMembershipLocalAndForwarded` checks sharing during invalidation. |
| Discovery contexts and list shape | T038-05 checks definitions, application arguments, lists and records with empty and singleton results. T004-11 checks rule inputs. T004-13 checks declarations with tracked kind filters and unions. |
| Captures remain path patterns | T007-09 rejects a capture pattern as a wildcard operand. T014-03 checks explicit construction and use as a matcher. |
| Literal wildcard bytes and effect-free syntax inspection | T004-11 checks quoted literal input and preserved formatting. T038-05 checks computed wildcard, brace, bracket and separator bytes. T010-22 checks parse and format with a counting filesystem host. |
| Lists of already discovered paths | T007-09 unions typed Path lists. `TestWildcardUnionPreservesFirstPathProvenance` verifies origin, and T004-13 checks an exact path containing a literal asterisk. |
| Lazy branches and grant checks | T004-13 checks declaration-time denial. T010-22 asserts that unused, unselected and denied expressions leave the callback idle. `TestWildcardDeniedOrPurePredicateLeavesHostIdle` checks that all three discovery operations leave the host queue empty for a denied root or a pure predicate. |
| Declared output overlap | T004-13 checks overlap against an absent output, and `TestNativeDeclarationDiscoveryRejectsGeneratedOutput` covers a generated output. |
| Watch membership | T004-13 checks kind-only invalidation. T009-14 and T010-23 exercise watched membership changes and recovery. |

#### 024 service acceptance

| Contract | Checks |
| --- | --- |
| Authored configuration before effects | T010-31 checks named service arguments and rejects invalid readiness settings at their authored location before starting the service or its prerequisite on both hosts. |
| Readiness, retries and health | T010-26 checks successful readiness, timeout and cleanup for forwarded probes. T010-31 checks a failed start followed by a successful restart. `TestServiceRestartsAfterFailedStartup`, `TestServiceReadinessProbeGatesDependents`, `TestServiceReadinessTimeoutCancelsService`, and `TestServiceHealthFailureRestartsAndResumesConsumer` exercise the native policy. |
| Shared lifetimes, cancellation and host parity | `TestServicePublishesOnSpawnAndKeepsPrerequisiteAlive`, `TestServiceRootCanWaitForReadinessAndRelease`, `TestServiceStopUsesConfiguredGracePeriod`, and `TestNativeMaterializeSignalsDuringServiceTeardown` cover native ownership and release. T010-31 compares native and WASM lifecycle events. `TestRuntimeEmbeddingServiceLifecycleAndCleanupEvents` checks the forwarding boundary. T010-32 checks shared consumers, service process groups, and reaping after watch invalidation. |
| Retained output | `TestForwardedServiceUsesConfiguredLogRetention` checks per-stream retention. T010-31 checks that lifecycle events exclude captured output and environment. |

#### 025 generated declaration acceptance

| Acceptance item | Checks |
| --- | --- |
| Local and imported constructors for all four rule kinds | T009-18 plans and lists file rules, phony tasks, cached tasks and services from each constructor. It checks generator attribution, service argument defaults and overrides, and cached reuse policy. |
| Top-level loops and conditionals | `TestNativeDeclarationBlocksCloseOverIterationBindings` checks closures. `TestNativeDeclarationIncludesSpliceIntoEachIteration` checks include scoping, and T004-13 checks inactive branches and source loading. |
| Pattern and computed headers, and definition dependencies | `TestGeneratedAndAuthoredPatternsReportAmbiguousSelection` checks pattern registration and selection. `TestGeneratedTargetsPlanAndMaterializeWithDefinitionDependencies` checks computed headers and generator dependency attribution. |
| Batch rejection and provenance | `TestGeneratedBatchFailureRegistersNoExecutableProgram`, `TestGeneratedTargetCannotOverlapAnAuthoredRule`, and `TestGeneratedDeclarationRejectsDefinitionCycles` cover invalid values, duplicates, cycles, and atomic rejection under the tracking allocator. |
| Discovery membership and overlap | T004-13 checks tracked kind filters and unions, including a file-to-directory watch change with the same name. `TestNativeDeclarationDiscoveryWatchRecoversFromOverlap`, `TestNativeDeclarationDiscoveryRejectsGeneratedOutput`, T009-14 and T010-23 cover watched membership and conflicts with authored and generated outputs. |
| Disallowed generation effects | `TestGeneratedDeclarationsRejectBuildEffects` checks processes, writes, captures and content reads with all corresponding execution grants supplied. |
| Boundary limits | `TestGeneratedDeclarationEnforcesBatchRuleLimit`, `TestGeneratedDeclarationEnforcesBatchListItemLimit` and `TestGeneratedDeclarationEnforcesTextLimits` check exact and over-limit counts, per-field lengths, and aggregate bytes. |
| Replacement and deterministic plans | `TestGeneratedTargetSetIsReplacedWhenSourceChanges`, T009-14, T010-23 and T010-27 exercise replacement, discovery, and generated plan output. T009-18 asserts the same plans and ordering on both hosts. |

#### 028 embedding acceptance

| Acceptance item | Checks |
| --- | --- |
| Import without dispatching the CLI | T010-32 imports `Kame` and `KameProgram` in a Node ESM process and makes API calls. |
| Parse diagnostics, pure values, and copied buffers | T010-32 exercises the JavaScript parser and evaluator and verifies prior values survive later calls. T010-09 compares native and WASM ASTs. T011-04 compares their diagnostics. T010-22 runs `tests/wasm/test-memory-reuse.mjs`, including file-backed includes, imports, and exports through the JavaScript API. |
| Async callback identity across independent instances | T010-32 interleaves two JavaScript owners and checks their request identity and completion. |
| Denied grants invoke no host callback | T010-32 checks denied execution grants. T010-22 checks that a source path outside the compilation grant reaches no callback. |
| Copied build results and ordered events, watch invalidation and close | T010-32 checks returned results and events, watch refresh, cancellation, and watch ownership after disposal. |
| Abort, disposal, child reaping, and late or unresolved callbacks | T010-32 exercises evaluation, building, and watching with cancellation and checks child process IDs and owner state. |
| Python operations, grants, copying, and managed cleanup | `tests/py/test_kame.py` covers expression and build values, parse and format, watches, grants, subprocess cancellation, and context-managed disposal. |
| At least 100 repeated lifecycle cycles per language | T010-32 repeats 100 JavaScript compilation, evaluation, and disposal cycles. `test_repeated_compile_and_disposal` repeats 100 Python cycles. |

#### 029 pattern extension acceptance

| Acceptance item | Checks |
| --- | --- |
| Anonymous recursive and segment captures | `TestTargetAnonymousCapturesKeepPositionalOrder` and T014-01 check path-order capture values. T004-14 checks the actual recursive and segment target against a file dependency. |
| Independent anonymous captures and repeated names | `TestTargetMatchesLeftmostShortestCapture` and `TestTargetAnonymousCapturesKeepPositionalOrder` check matching order. T038-05 checks a repeated named capture. |
| Positional rule inputs on both backends | T004-14 materializes `./src/demo.c` from `./src/demo.h`. |
| Position counts in named and repeated groups | T004-14 materializes a rule whose input uses a positional reference for each occurrence of a repeated named group. |
| Anonymous capture selectors in rule bodies | T004-14 checks `captures.0` in a Kash word, an expression body and foreign-shell interpolation on both hosts. |
| Existing named captures | T014-02 checks matching, formatting and selection. `TestCachedTaskCapturesHaveDistinctIdentities` checks cache identity. |
| Runtime pattern construction | T014-03 constructs typed patterns, renders substitutions and diagnoses an invalid combination. |
| Regex capture spelling and order | `TestTargetRegexCaptureFormatsAndMatches`, `TestSchemaTwoSemanticFields` and T014-04 cover authored spelling, capture fields and regex diagnostics. |
| Regex operations and limits | T014-04 compares named and positional captures, substitution and invalid input. Both hosts preserve `PAT_LIMIT` through `regex-match`, `regex-group` and `resub`. |

#### 030 template extension acceptance

| Acceptance item | Checks |
| --- | --- |
| Matching and mismatched block labels | `TestDocumentBlockEndLabelsAreChecked` checks matching labels and rejects a mismatch. `TestDocumentLetRetainsEndLabelDiagnostic` checks the closing-token span after a nested binding. |
| Match cases, captures, fallback and lazy branches | T016-02 verifies path captures and inline fallback. T016-03 checks first-match ordering, skipped effects and empty no-match output against the expression form. |
| Comment-style inline blocks, plain whole lines and diagnostics | T016-02 covers HTML, C, PowerShell and plain rendering. T010-09 compares the authored opening span of an unterminated directive on both hosts. |
| Trim markers and malformed marker placement | T016-02 checks CRLF and trimming at an else boundary. T016-03 rejects `@else-` and exercises trailing trims. |
| Malformed case patterns and invalid capture groups | T016-03 checks `TPL_PARSE` and `PAT_INVALID` separately. |
| Automatic style selection and explicit override | `TestDocumentAutoStyleUsesExtensionAndContent` checks extension inference, content inference and ambiguity. T016-02 checks plain inline rendering, and T016-01 checks an explicit style for a file with no extension. |
| PowerShell and batch comment styles | T016-02 renders both styles, and T016-03 checks canonical formatting and idempotence. `TestDocumentPowerShellAndBatchCommentStyles` covers recognition and literal comment preservation. |
| Positional loops and explicit indices and keys | T016-02 renders explicitly indexed lists, record entries in stored order, and nested scopes. T016-03 checks rest parameters and rejects destructuring a non-list item. |
| Formatting canonicalizes tokens and preserves literal bytes | T016-03 checks byte-exact raw and escaped regions and formats each comment style twice on both hosts. T016-02 checks canonical directives and the status of `fmt --check`. |
| Ownership during recovery, execution, and cancellation | `TestDocumentRecoveryFreesDiscardedTrees` and `TestRenderContentTemplate` cover recovery, success, an empty match and rendering errors. T010-22 checks bounded heap use after repeated formatter failures and rejects a late completion after cancelling a suspended render. |

#### 036 presentation acceptance

| Contract | Checks |
| --- | --- |
| Invocation options, exact bytes, and schemas | T036-01 selects presentation modes on native and WASM and checks command bytes, JSON records, and early failures. T009-17 checks option routing and `TestDiagnosticPresentationEnvironmentAndJSONEarlyErrors` checks explicit diagnostic overrides. |
| Reason and diagnostic fidelity | T036-02 checks reason identity, deduplication and privacy. T011-01 and T011-03 check presentation and bounded diagnostic causes. `TestFailurePresentationDoesNotRepeatDiagnosticDetails` checks single-headline reporting. |
| Terminal lifecycle and live output | T036-03 checks terminal reflow, cursor ownership, watch cycles and the shared presentation ABI. T036-04 checks OSC progress and terminal restoration. T009-14 and T010-23 exercise watch updates and bounded public output. |

#### 037 build inspection acceptance

| Contract | Checks |
| --- | --- |
| Producer, artifact and consumer identities | T037-01 checks concrete inputs, generated products, compound producer selection, persistent intermediates, and downstream patterns and missing glob members. T009-15 and T010-24 check distinct argument-bound instances. |
| Ordering, boundaries and diagnostics | T037-01 checks shared roots, depth, consumer-local sequence groups, unavailable generated discovery, missing graph members, and cycles. Overlapping producers produce only `TGT_AMBIG` in plan and inputs and leave the destination absent. |
| Read-only inspection and backend parity | T037-01 checks preserved-metadata changes, artifacts and freshness without execution, and normalized documents on both hosts. `TestExpandPlanDoesNotMaterializeRuleDependency` checks execution isolation. |

#### 038 direct Kash and values acceptance

| Contract | Checks |
| --- | --- |
| Typed execution, fragments and stable formatting | T038-01 checks direct recipes, typed arguments, expression fragments and canonical ASTs on both hosts. T017-21 checks composition of words and redirection operands. T009-18 checks generated and authored declarations. |
| Paths, wildcards and values | T038-02 checks filtered membership, symlinks and shared watch invalidation. T038-03 checks `str` and `json`, and T038-05 checks computed paths and literal glob bytes. |
| Actions, tool identity and recipe lifetime | T038-04 checks action names, effects and ordinary capability preflight. T038-01 and T038-06 check reached tool fingerprints, executable changes, scoped recipe effects, and managed services. |

#### 041 pattern predicates and substitution acceptance

| Contract | Checks |
| --- | --- |
| Direct and callable predicates | T041-01 checks anchors, nonempty and repeated captures, recursive and regex patterns, String and Path subjects, and invalid patterns or subjects. |
| Substitution and composition | T041-01 checks passthrough by value kind, positional list mapping, literal replacement, empty and nil cases, separate `resub`, and the formatted composition on both hosts. |
| Capture scope and failure | `TestPatternReplacementEffectsAreDemandedOnlyOnMatch` checks skipped and selected replacement effects. `TestComputedPatternMatchAndReplacement` and T014-02 check runtime construction and capture-scoped replacement. `TestSubstitutionOperationMetadata` checks operation versions. |

#### 042 Kash streaming acceptance

| Contract | Checks |
| --- | --- |
| Topology, framing and channels | T042-01 checks graph shape and validation before effects. T042-02 checks framing, filter branches and predicate-dependent reuse, `TestStreamPredicatesArePureBooleanAndHostFree` checks predicate purity, and T042-03 checks channel scopes and endpoints. |
| Tee and positional operands | T048-01 checks tee copies and status. T042-04 checks operand indexing, concurrent producers, recovery and cleanup. |
| Scheduling and memory bounds | T042-05, T042-06 and T042-09 check pressure, lossy buffer limits, fairness and strict merge ordering. T012-03 checks large streams with a bounded final capture. |
| Ownership, failure, and workers | T042-01 and T042-10 check graph failure modes, siblings and process cleanup. T042-11 checks record workers, failure collection and downstream completion. |
| Inspection and telemetry | T042-07 and T042-08 check topology and metrics. `TestProcessSnapshotsCoalesceAndRejectStaleRequests` checks request identity and snapshot retirement. |

#### 043 finalization acceptance

| Contract | Checks |
| --- | --- |
| Callback validation, returned values and effect ordering | T043-01 checks preflight, callback results, ordered effects, and body and cleanup failures on both hosts. T017-22 checks deferred operands and retained lexical values. |
| Deadlines, ownership and protected outcomes | T043-01 checks cancellation, reaping and deadline propagation. T048-04, T048-05 and T048-06 check nested boundaries, transfers, joins and cleanup. `TestNativeRecipeTerminalRestoresForegroundGroup` holds cleanup while checking that the foreground child was reaped. |
| Forwarded lifetime and dry-run | T043-01 checks dry-run, repeated and disposed calls, and host forwarding. T010-16 checks late completions; T010-32 checks disposal under process load. |

#### 044 rule body verification

| Verification item | Checks |
| --- | --- |
| Exact publication, dry-run, freshness and effects | T044-01 compares text, binary, empty and rendered bytes on both hosts, verifies reuse and input change invalidation, and rejects direct and indirect effects. |
| Expression types, parsing, producer validation and atomicity | `TestKameBodyPublishesExactContentAndReusesTrackedInputs` and `TestKameBodiesRejectEffectsInvalidValuesAndAmbiguousProducers` exercise the expression path under the tracking allocator. |
| Kash return and ordinary completion | T044-01 checks string and byte results, nil and bare returns, ineligible rules, exit, failure, and returned content replacing a buffered write. |
| Dynamic prerequisites and host publication | `TestKashReturnedArtifactTracksAdditionalReads` checks dependency invalidation for a returned artifact. T010-21 builds a computed prerequisite, reads it through a Kash return, and checks real host publication failure on both hosts. |

#### 045 rule precedence verification

| Verification item | Checks |
| --- | --- |
| Prefix and suffix specializations, chains, and multiple overlaps | `TestRulePrecedenceSelection` reverses declaration order. `TestRulePrecedenceCollectsAllMatches` enumerates permutations for both a chain and a dominating candidate. |
| Capture cardinality, renaming, shorthand and ambiguity | `TestRulePrecedenceSelection` covers segment and recursive captures, equivalent names, anonymous captures, regex, multi-capture, incomparable paths, and task names. |
| Literal rule priority | `TestRulePrecedenceCollectsAllMatches` plans the literal winner in each permutation. T014-02 executes an exact-match winner. |
| Failure of a specialized prerequisite | `TestRulePrecedenceExecutionDoesNotFallback` leaves the broad input available and requires the selected rule to fail. |
| Planning, inspection and execution choose the same producer | `TestRulePrecedenceExecutionDoesNotFallback` compares plan and inspection inputs with the resulting artifact. |
| Group producer ownership | `TestRulePrecedenceGroupedOwnership` rejects the shadowed group in both request orders. `TestRulePrecedenceGroupedConflictLocatesMatchedOutput` checks competing spans. `TestRulePrecedenceSelection` checks an unshadowed sibling plan. |
| Portable selection | T014-02 uses both CLI backends for exact priority, capture precedence and ambiguity. |

#### 046 scripting and publication verification

| Contract | Checks |
| --- | --- |
| Loop values, scope, timing and formatting | T046-01 checks typed iteration, fresh scope, ordered effects, timeouts, dry-run and parse-format-parse. T048-06 checks transfers and loop-driven generator cleanup. `TestNativeKashLoopsEvaluateValueHeaders` exercises value headers through the compiled CLI. |
| Pure option parsing and token boundaries | T046-01 exercises alias handling, values, operand boundaries and Unicode replacement on both hosts. `TestSubToken` and `TestTokenUnicodeTable` check substitution and Unicode tables in Go. |
| No-clobber publication | T046-01 exercises existing destinations, two synchronized publishers, and embedding outcome and grant handling. `TestMemoryResourcesReadListPublishAndRemove` checks the in-memory resource path. |

#### 047 output ports and command lists verification

| Contract | Checks |
| --- | --- |
| Reserved ports, pipeline behavior and capture | T047-01 compares stdout and stderr routing, branch and capture semantics, and capture limits on native and WASM. T012-03 stresses large-output capture and streams. |
| List selection and command outcomes | T047-01 checks selection, effective outcomes, failure recovery, signals, cumulative capture and explicit exit status. T048-10 exercises selected exit policies. |
| Failure and cleanup order | T047-01 checks primary failure, cleanup failures and the JSON summary. T048-04 checks intentional exit and cleanup. |

#### 048 practical scripting verification

| Contract | Checks |
| --- | --- |
| Input ownership and records | T048-07 covers invocation input, T048-08 covers record behavior, T048-09 covers stdin record boundaries, and T048-11 covers loop reuse. |
| Stderr routing | T048-01 exercises file replacement and append, alias ordering, ambiguity, and tee status. |
| Eager bindings and transfers | T048-03 checks binding evaluation, T048-04 checks `exit`, and T048-06 checks `break`, `continue`, and `return`. `TestKashLoopTransfersSkipStatementsAndRetainCleanupProgress` checks resumed evaluator state. |
| Libraries and execution scopes | T048-02 checks authored library references, source context and authority. T048-05 checks scoped setup, direct builtin output, owned-work join and cleanup with a readiness marker. T050-01 and T050-02 check modules and source identity. |
| Accepted exits | T048-10 checks stage selection and outcome status; T047-01 checks selection and failure recovery in command lists. |

#### 049 process inspection verification

| Verification item | Checks |
| --- | --- |
| Exact and empty typed byte captures | T049-01 checks empty byte values, non-UTF-8 output, trailing newlines, a large pair of simultaneous streams, and independently selected capture ports. |
| Independent limits and live output | T049-01 checks exact bounds and overflow per stream. T012-03 checks continuous publication beyond the log retention cap. |
| Signals, unaccepted exits, deadlines and cancellation | T049-01 covers nonzero exit, signal, timeout, explicit cancellation and deferred cleanup. |
| Closed input, once-only evaluation and preflight | T049-01 preserves invocation input for the later command, checks one argument effect, and rejects invalid policies before a marker can be written. |
| Optional tool values and resolver changes | T049-01 checks absence, override, availability changes, unchanged absent revisions and protected host errors. |
| Outcome fields and function stages | T017-17 checks process status and stage vectors. T055-02 checks typed returns and errors in callable stages across synchronous capture and async wait. |
| Invocation failure versus enclosing failure | T048-04 and T048-05 check ordinary unwind errors and propagating capability denial. T055-02 checks authored stage diagnostics. |
| Selected nonzero status | T048-10 checks `:completed`, a false `ok`, the raw exit status, and `effectiveStatus` 0. |
| Early cancellation of a generator stage | T055-02 checks cleanup, a cancelled stage, and a successful enclosing graph. |
| Invocation suspension | `TestGeneratorInvocationState` checks internal suspension between pulls and the completed, failed and cancelled transitions. |
| Host ABI and unsupported capabilities | T049-01 verifies host completion with copied byte buffers and injects an unavailable capture ABI, checking rejection before a marker is written. `TestTypedCaptureRepeatedCompletionOwnsBothStreams` checks independent ownership across repeated completions. |

#### 050 module acceptance

| Acceptance item | Checks |
| --- | --- |
| Kame calls a Kash module through a namespace and through `from` | T050-02 invokes both calls from Kash rule recipes. |
| Kash calls a Kame module without registering its rules | T050-02 runs the exported Kame function in a Kash source and checks the importing rule catalogue. |
| Exports, private definitions, and facades | T050-02 checks private members, duplicate exports, and unknown exports. `TestNativeAuthoredImportsResolveNestedModules` checks inherited private access, re-export through a local definition, and rejection of a directly re-exported import. |
| Namespace and selective-binding conflicts | T050-02 checks duplicate aliases, reserved stems with and without `as`, and both kinds of selective-binding collision. |
| Kash module validation, optional and conditional includes, and grants | T050-01 checks optional and conditional includes and an invalid library. T050-02 checks diagnostics with a nested include frame and verifies that module loading preserves effect authority. `TestNativeAuthoredModuleSelectsConditionalIncludes` checks conditional includes inside an imported Kame module. |
| Kash includes cannot load Kame sources | T050-02 checks the forbidden include and the valid import of a Kame module. |
| Cycles and source-load deduplication | T050-02 checks the nested import cycle. `TestNativeAuthoredModuleSelectsConditionalIncludes` checks an active include cycle. `TestAuthoredImportsLoadEachNormalizedSourceOnce` counts the host requests. |
| Missing, ambiguous, and template module paths | T050-02 checks missing candidates, ambiguous candidates, and rejection of `.kmt` imports. |
| Authoring-relative paths and literal operands | T050-02 checks lookup across `-C` and rejects computed and quoted paths. T050-01 checks the inline relative-path diagnostic. T010-22 exercises authored module import and origin in the JS embedding API. |
| `module/dir` follows the file that owns the function | T050-01 checks an included function. T050-02 checks an imported function. |
| Reached source changes invalidate outputs even with unchanged mtime | T050-01 and T050-02 exercise flat includes, nested imports, unchanged inputs, reached and unreached helpers, and stream filter provenance. |
| Dry-run does not execute demanded module bodies | T050-01 and T050-02 check that dry-run leaves the authored output and helper effects absent. |

#### 051 recipe authority conformance

| Contract | Checks |
| --- | --- |
| Policy shape and grants | `TestNativeKashRecipeEnvironmentRemovalRequiresScopedAuthority` checks malformed authority, a denied rule and no prerequisite effects. T017-16 checks both hosts for duplicate policy names, scoped removal, and a separate read grant that does not authorize removal. `TestNativeRecipeTerminalHandoff` checks foreground without `tty` and unauthorized stage attachment. |
| Environment overlay | T017-16 checks computed writes, empty values and removal without a read grant on both hosts. `TestNativeKashEnvironmentWritesStayInTheirExecutionUnit` checks function and capture scope, stage overrides, invalid scalar types and dry-run. `TestNativeInteractiveKashSessionDirectoryAndEnvironment` checks the same capture isolation between interactive entries. |
| Executable lookup | `TestNativeKashWrittenPathSelectsExecutable` checks written and removed `PATH`, a stage-local override, explicit tools and scoped run grants. `TestNativeInteractiveKashSessionPath` checks live changes and directory-relative lookup. |
| Terminal handoff | `TestNativeRecipeTerminalHandoff` checks attachment selection, foreground ownership, capture isolation, authorization and missing terminal. `TestNativeRecipeTerminalRestoresForegroundGroup` checks restoration and reaping on success, failure, timeout, and cancellation. T017-16 checks no spawn under Node/WASM and rejects grants on foreign and expression bodies before prerequisite effects on both hosts. |
| Freshness | `TestNativeKashFreshnessValidatesConsumedChildEnvironment` checks invocation values, a self-written value, and removal against a cache record and subsequent executions. |

#### 052 interactive Kash conformance

| Contract | Checks |
| --- | --- |
| Invocation and entries | `TestNativeInteractiveKashSession` exercises preloading and continuation. `TestNativeInteractiveKashEntryContracts` exercises no preload, stdin conflict, and partial EOF. `TestNativeInteractiveKashPromptInterrupt` starts bare `kash` on a pseudo-terminal. `TestNativeInteractiveKashHighlightingAndBlocks` submits a Tab-indented block. |
| Shared session, rebindings, rejection and diagnostics | `TestNativeInteractiveKashSession`, `TestNativeInteractiveKashEntryContracts`, and `TestNativeInteractiveKashIndependentSourceDiagnostics` check value visibility, callable redefinition, registration failure without effects, and source positions for delayed diagnostics. |
| Termination and terminal handoff | `TestNativeInteractiveKashSessionInterrupt`, `TestNativeInteractiveKashForcedInterrupt`, and `TestNativeInteractiveKashPromptInterrupt` check cancellation and status. `TestNativeInteractiveKashTerminalHandoff`, `TestNativeInteractiveKashResetsInputModes`, and `TestNativeInteractiveKashKeysTypedDuringCursorProbe` check terminal ownership, capture pipes, Ctrl-C, modes, and typed-ahead input. |
| Directory and environment | `TestNativeInteractiveKashSessionDirectoryAndEnvironment` checks home, relative and previous-directory navigation, capture isolation and invalid writes. `TestNativeInteractiveKashExplicitGrants`, `TestNativeInteractiveKashEnvironmentPersistsAcrossEntries`, and `TestNativeInteractiveKashSessionPath` cover grant transitions and environment changes between entries. Removal follows the rule authority in 051, which a direct interactive session lacks. |
| Startup | `TestNativeInteractiveKashStartupFile` checks startup definitions, environment, aliases, directory state, diagnostics and loading controls. |
| Stop, foreground and background | `TestNativeInteractiveKashStopAndForeground`, `TestNativeInteractiveKashStopDuringEvaluation`, `TestNativeInteractiveKashBackground`, `TestNativeInteractiveKashBackgroundEntryReadsTerminal`, and `TestNativeInteractiveKashJobTerminalModes` exercise process groups, resumed entries, background completion, terminal reads, and modes. |
| Job commands and graph ownership | `TestNativeInteractiveKashKill`, `TestNativeInteractiveKashEndWithStoppedJob`, `TestNativeInteractiveKashJobCommandsAndPrompt`, `TestNativeInteractiveKashAsyncGraphTerminal`, `TestNativeInteractiveKashWaitAndJobSelectors`, and `TestNativeInteractiveKashTimeoutCancelsJobs` check job operands, external `x/kill`, stopped jobs at EOF, async graph ownership, selectors, and timeout. |
| Prompt and line editing | `TestNativeInteractiveKashPromptFunction`, `TestNativeInteractiveKashRightPrompt`, `TestNativeInteractiveKashRightPromptUnicodeEditing`, `TestNativeInteractiveKashBracketedPaste`, and `TestNativeInteractiveKashKeystrokeLatency` exercise prompt fallback, status, display columns, paste, and latency measurement. `TestNativeInteractiveKashPersistentLatency` measures a live session after 1,000 warm-up entries, with an output marker after each batch. |
| Completion and highlighting | `TestCompletionWordStart`, `TestCompletionCommandPosition`, `TestCompletionsAt`, `TestPathCompletionKinds`, `TestReplHighlight`, and `TestReplHighlightPathCache` test extraction, groups, path kinds, lexer, and cache invalidation. `TestNativeInteractiveKashCompletion`, `TestNativeInteractiveKashCompletionSeparator`, and `TestNativeInteractiveKashHighlightingFollowsSession` exercise editor integration. |
| History | See the 053 acceptance checks. |

#### 053 Kash history acceptance

| Acceptance item | Checks |
| --- | --- |
| Whole-entry credential filtering and ordinary text | `TestHistorySecrets` covers positive and negative forms. `TestHistoryRoundTripAndPrivacy` and `TestNativeInteractiveHistoryScopesAndPrivacy` check storage and recall, including leading space. |
| Rescan, expiration, and malformed records | `TestHistoryExpiryAndRescan` checks persisted filtering, exact deadline, idle recall, and future timestamps. `TestHistoryMalformedRecords` checks invalid records and recovery. |
| Independent quotas and encoded size | `TestHistoryQuotaIndependentScopes`, `TestHistoryScopeAndQuota`, and `TestHistoryRoundTripAndPrivacy` cover scope budgets, oversized records, and non-ASCII multiline round trips. |
| Scope discovery, submission directory, and recall | `TestHistoryScopeAndQuota` checks worktree, nested and co-located metadata, canonical CWD, and original submission scope. `TestNativeInteractiveHistoryScopesAndPrivacy` checks scoped and global terminal recall. |
| Concurrent writers, clear and deduplication | `TestHistoryConcurrentAndClear`, `TestHistoryLocalClearAndDeduplication`, and `TestHistoryLockAndConfiguration` cover independent writers, identity, stale sessions, and local and global clear. |
| Storage, locking and configuration faults | `TestHistoryPrivateStorageAndFailedReplacement`, `TestHistoryLockAndConfiguration`, and `TestHistoryMigrationAndUnsafeStorage` cover permissions, failed replacement, unavailable locks, disabling, and file versions. |
| Only eligible submitted entries persist | `TestNativeInteractiveHistoryRecordsOnlySubmittedEntries` exercises startup, a prompt function, a history control, and an interrupted unsubmitted line in a persistent terminal session. |

#### 054 Kash options verification

| Contract | Checks |
| --- | --- |
| Interactive builtin and scope precedence | `TestNativeInteractiveOptions` checks startup, effect-free script rejection, and command status. `TestOptionsScopesAndPersistence` checks local, saved and environment precedence, global inspection, and reload behavior. |
| Validation and completion | `TestOptionsValidationIsAtomic` checks invalid names, values, and combinations of scope flags. `TestOptionCompletions` checks scope flags, names, and values. |
| History integration | `TestOptionsHistoryUsesSharedState` and `TestNativeInteractiveOptionsHistory` check the shared session setting, immediate recall changes, and exclusion of option-management input. |
| Global storage | `TestOptionsMalformedFilesPreserved`, `TestOptionsConcurrentWriters`, and `TestOptionsUnsafeFilesAndLockFailure` check malformed stores, read-modify-write, file permissions, unsafe paths, and bounded lock acquisition. |

## Implementation report

The conformance checklist is in Phase 14 and the root README revisions are
recorded in Phase 13. In the isolated release snapshot, `make test-all` completed 23
build targets, 212 standard suites and 211 pointer-check suites. A targeted
rerun of the two artifacts mentioned in the input-stability warning completed
without the warning. `make wasm-check` passed on the same snapshot.

The implementation follows the existing Go runtime and build targets. The
release check uses Go `checkptr`, and the report records the additional Clang
AddressSanitizer runs for runtime packages and native CLI lifecycle separately
from the Solod/Clang gate described in spec 013. The approved arithmetic and
service-argument decisions are reflected in specs 007 and 022 and their tests.
Specs 009, 013 and 036 specify `do kash` as shorthand for `do run --lang kash`.
Specs 049 and 055 define `:suspended` as internal to the generator. Specs 004
and 055 allow `(EXPR)` as a computed file-rule output in declarations and
constructors. T010-27 passed with those forms on both hosts. The rules
publication example uses a parenthesized output. It passed the layered example
suite and separate format and build checks on native and WASM. The Kash and
CLI references document the alias and terminal outcome semantics. A newline
can separate collection items and fields. Expanded lists use newline separators
in canonical format. Wrapped calls use an explicit
continuation when the formatter splits their arguments at collection depth.
Native and WASM parsed, evaluated and formatted the new syntax consistently.
Kash command stages and `with` headers use `KEY: VALUE` setup pairs. The
expression operations `run!` and `capture!` use symbol arguments for setup.
The first full run with these changes found a stale `:KEY` spelling in an
invalid-scalar diagnostic and stale expanded-list formatter expectations. The
corrections passed all 68 T017-21 checks and all 64 T018-01 formatter goldens
on native and WASM. The full release rerun is in progress, followed by the
curated `make test-wasm` gate. The updated root README examples passed on both
hosts, including greeting reuse and C header invalidation. The installation,
contributor and packaging guides now describe the Go release artifacts and
checks. The package-manifest command produced both definitions in a scratch
directory, and the WASM import and reactor checks passed on the release build.
Kame strings, paths and rule headers and Kash command words now share
`${EXPR}` substitution. In a string or composed Kash word, it renders atoms
as text and joins list items with spaces. Document templates and foreign-shell
recipes use `@{EXPR}`. The full Go suite, native/WASM word tests and template
boundary checks passed. The first release-wide rerun exposed stale fixtures
and a native/WASM difference in the display of parsed substitution forms.
A new release-wide run includes those corrections and the string conversion.

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
- **`$dir/file` versus `${ns/name}`** in words, and `${` in words.
- **Unit state persisting across function returns** (055 §5.3) while captures
  and rule bodies stay isolated.
- **Generator cancellation and `defer` ordering** relative to process cleanup and
  the shared unwind deadline (043).
- **Declaration-time tracked discovery and `DISCOVERY_OVERLAP`** under watch
  reloads (020).
- **Upper-case configuration names in `Kamefile`** now read the environment as a
  fallback. Check that release-key handling still behaves as intended.
