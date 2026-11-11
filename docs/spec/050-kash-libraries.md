# Kash libraries and module values

## Purpose

Kash definition libraries compose into value and rule programs, and can also be
loaded as module values. This specification refines `004-language.md`,
`017-kash.md`, `048-kash-practical-scripting.md`, `008-cache.md`, and
`020-watch.md`; its clauses take precedence for the behavior specified here.

| Form | Phase | Result |
| --- | --- | --- |
| `(include ./module.kash)` in `.km` or `.kmk` | Source composition | Library definitions bind flat in the caller. |
| `(kash ./module.kash)` | Expression demand | A module value whose fields demand definitions. |
| `(kash TEXT)` | Expression demand | One constructed script callable, unchanged. |

A string is never a module path: `(kash "./module.kash")` compiles that text
as a script. Values are not reparsed as source or turned into paths implicitly.
There is no new include spelling.

## Source composition

When a selected `(include PATH)` or `(include? PATH)` in a Kame value or rule
program names a `.kash` or `.ksh` file, parse that file as a Kash library.
This applies equally to a file selected by a composition `if`; only the selected
file's suffix chooses this exception. Every other suffix inherits the caller's
outer parser. In particular, including a `.kmk` from `.km` cannot import rules.
Existing literal include path syntax, selection, optional-missing behavior,
source-loading authority, and diagnostics remain unchanged.

The library grammar is the definition-only grammar of `048-kash-practical-scripting.md`
§6: comments, lazy definitions, `def`, and nested includes. Top-level commands,
expression statements, `let`, and control blocks are `PARSE_ERR`. Nested
includes remain Kash includes and parse their files as Kash regardless of suffix;
including a `.kmk` from Kash does not import rules. No optional or computed
includes are added inside Kash.

Selected includes expand depth-first and bind definitions flat into the caller,
including definitions from nested libraries. Duplicate names are registration
errors, including on repeated nonrecursive inclusion. No definition is demanded
or executed at inclusion, and grants do not widen. Included spans and include
frames survive diagnostics. `(source-dir)` in a function uses that function's
authored file, not its caller's file or cwd. Lexical active-ancestry cycle
identity and the remaining source composition rules are unchanged.

## Module expression

`(kash PATH)` is a module expression only when its operand is one literal explicit
path atom beginning `./`, `../`, or `/` and ending `.kash` or `.ksh`. Globs,
interpolation, quoted paths, and computed paths are not module operands; a string
operand uses the existing script constructor. Resolve the path from the file
containing the authored call, not cwd or `-C`. Inline, stdin, and `(kash TEXT)`
sources lack a load base and report `SEL_NO_CONTEXT` for this form. Loading uses
source-loading authority, as with include, not a general read grant.

Demanding the expression loads and parses a definition-only library under the
same grammar, diagnostics, source identity, and lexical include-cycle rules as
above. A file with top-level commands is `PARSE_ERR`; execute a script file with
`kame do run` instead. Loading does not run `def` bodies or demand unused lazy
definitions.

The result is an opaque module value displayed as `<kash-module>`, neither a
record nor a script callable. `module.std-archive!` and
`(get "std-archive!" module)` demand the named binding. Other record operations
on a module are `EXPR_INVALID`; missing fields use ordinary missing-lookup
semantics. Only definitions authored at that file's own top level are fields.
Nested includes remain visible lexically to its definitions but are not fields or
re-exported. A flat include of the same library still binds nested names.

The path form does not construct one callable from a file: a definition-only
library would otherwise produce a no-op script. Authored executable bodies belong
in `def`; generated script text continues to use `(kash TEXT)`.

## Freshness and observation

A recipe that reaches an included `def` or a module field records the defining
library file and selected nested library files actually reached as content
dependencies. Identify each by its source path and a digest of file bytes, not
mtime. An unreached definition adds no recipe dependency. Holding a constructed
`(kash TEXT)` callable or module value in a definition does not by itself make a
recipe uncacheable or force a rebuild. The callable is not published into the
cache: `008-cache.md` tag 11 remains constructor identity `"kash"`, not serialized
source or executable code. Existing cache validation and publication rules apply.

Stream filter predicates are a conservative exception: they run in a host
callback without an engine observation context, so dispatch records every signed
source currently loaded. An edit to an unreached library may therefore rebuild a
recipe using a filter. Precise predicate demand tracking is deferred; this
fallback must retain freshness rather than risk missing a reached dependency.

Dry-run may load and parse a demanded library but does not execute `def` bodies
or top-level effects. Watch treats a selected `.kash` or `.ksh` include as a
source root; a reached `(kash PATH)` is a file dependency like `(read PATH)`.
Failed loads retain attempted source paths, including missing or invalid nested
libraries, so creating or repairing those files can restart a failed watch cycle.
Unselected includes and unreached definitions retain ordinary demand semantics.

## Conformance

In addition to the library checks in `048-kash-practical-scripting.md`, require:

- A `.kmk` includes a `.kash` `def` and calls it from a `SHELL = kash` recipe;
  a `.km` including `.kmk` still cannot import rules.
- A top-level command in a library is `PARSE_ERR` with its included span and
  include frame; test both suffixes, optional and conditional selection, duplicate
  names, nested suffix independence, and unchanged authority.
- `(kash ./mod.kash)` yields a module whose field can be called, while
  `(kash "./mod.kash")` still constructs script text. Test `get`, missing fields,
  invalid record operations, malformed path operands, and lexical cycles.
- Resolve a module path relative to its authored file despite a different cwd or
  `-C`; inline, stdin, and constructed-text path forms report `SEL_NO_CONTEXT`.
- A nested helper is callable by the library, bound by flat include, and absent
  from the module's fields. Included functions retain their own `source-dir`.
- A second run reaching an unchanged library does not rebuild the output; changing
  reached file bytes invalidates it even with unchanged mtime. Unreached definitions
  add no recipe dependency, including nested helpers not reached by the recipe,
  except for the conservative stream-filter fallback above.
- A definition containing `(kash """true""")`, called by a recipe with an
  unchanged declared input, does not cause a rebuild on the second run.
- Dry-run may parse a demanded file but does not execute its `def` body.
