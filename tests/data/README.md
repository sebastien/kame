# CLI test fixtures

Fixtures consumed by the end-to-end shell suite in `tests/T*-*.sh`. They are
immutable inputs: tests copy them into their scratch directory with
`fixture_copy` and never build inside this tree.

Language fixtures live in `../lang/` and are shared with the parser tests.

## Language fixtures (`lang/`)

Fixtures under `lang/expr/`, `lang/template/`, `lang/rule/`, and `lang/script/`
feed the CLI parse matrix (T004-01), format canonicalization (T004-06), and
format/AST stability (T004-08) suites. Fixtures under `lang/invalid/` plus
`invalid/MANIFEST.tsv` feed the invalid-fixture suite (T004-02), which asserts
each fixture's first diagnostic code and severity. Spec 014 syntax is covered
by the `expr/section-*.lm`, `expr/pattern-*.lm`, `expr/placeholder-*.lm`, and
`invalid/expr-pattern-mixed-groups.lm` fixtures.

## CLI fixtures (`cli/`)

| Fixture | Used by | Contents |
| --- | --- | --- |
| `basic/` | plan, cat, targets | Definition, bare task, cached task, and a file rule with an `out` effect |
| `discovery/` | source discovery | `Makefile.lmk`, `make.lmk`, and `src/lmk/main.lmk` with distinguishable output |
| `project/` | runtime, cache, streams, meta | Real multi-rule build driven by hermetic `sh` scripts under `tools/` |
| `fresh/` | runtime | `out <- in` recipe appending to `runs.log` for freshness counts |
| `cache/` | cache | Cached task with a declared input and an environment-dependent task |
| `json/` | JSON events, dry-run, host | Text, binary, and failing recipes plus an `out`/`err` task |
| `errors/` | diagnostics, runtime | Missing output, failing recipe, yield conflict, definition cycle, ambiguity, service, timeout, bare task |
| `signals/` | signals | Recipe writing its shell and child pids, then waiting on a background sleep |
| `legacy-runtime/` | runtime | Equivalent legacy `Makefile.lmk` file build with a fresh-output skip |
| `legacy-core/` | library | Legacy core-make expression block against a small TypeScript tree |
| `plan/` | plan, graph | Captures, dynamic body, body-less static rule for freshness, multi-target task |
| `operations/lib-fs/` | library filesystem | Small text tree with a subdirectory for `read`, `stat`, and `wildcard` |
| `operations/lib-grants/` | library capabilities | Read fixture plus a target for rooted write checks |
| `host/retry.lmk` | host process | Retry recipe that fails until a marker exists |
| `fmt/` | formatter | Unformatted source and its canonical form |
| `cases/` | table-driven matrix | Reserved for the `T009-12` case runner |

## Conventions

- Fixture scripts are POSIX `sh` invoked as `sh script`; no execution bit is required.
- Recipe indentation is one tab; files use LF and UTF-8.
- No fixture writes outside its working directory, and none requires a compiler.
- Volatile values (pids, timestamps, node ids) never appear in goldens.
