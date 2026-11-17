---
revision: 2
---

# Modules: import, export and include

## Purpose

Kame programs (`.km`, `.kame`) and Kash programs (`.kash`, `.ksh`) compose
through one module system. This specification refines `055` §11 together with
`004-language.md`, `017-kash.md`, `048-kash-practical-scripting.md`,
`008-cache.md` and `020-watch.md`; its clauses take precedence for the behavior
specified here.

```kame
import ./lib/c                          # namespace c/…
import ./lib/archive.kash as archive    # namespace archive/…
from ./lib/c import compile, object-path
from ./lib/checks.kash import check-config as check
export compile, object-path
include ./rules/common.km
include? ./local.km
```

| Statement | Phase | Result |
| --- | --- | --- |
| `import PATH` / `import PATH as NAME` | Source loading | Binds the module's exports as a namespace. Registers nothing. |
| `from PATH import NAME (as ALIAS)?, …` | Source loading | Binds selected exports directly as lexical names. Registers nothing. |
| `export NAME, …` | Source loading | Declares the module's public interface. |
| `include PATH` / `include? PATH` | Source composition | Splices the file's definitions, and in Kame programs its declarations, into the caller. |

The same statements and rules apply in both languages. Imports work in both
directions: a `.km` file may import a `.kash` module and a `.kash` file may
import a `.km` module. A `.km` file may include a `.kash` file, but a Kash
source may include only Kash sources; it reaches a `.km` file through `import`.
Each loaded file is parsed with the grammar selected by its own suffix (`055`
§2), never by the suffix of the file that loads it.

## Module paths and identity

`import`, `from`, `include` and `include?` operands are literal path atoms
beginning with `./`, `../`, `/` or `~/`. Globs, capture groups, `@{…}`
interpolation, quoted strings and computed values are `PARSE_ERR`; a string is
never a module path, and values are never turned into paths or reparsed as
source. A relative operand resolves against the directory of the file
containing the statement, not the root, the working directory or `-C` (`055`
§9.2). Inline, stdin and `eval` sources are not file-backed and have no load
base; a relative module path there is `RES_INVALID`.

An `import` or `from` operand is without a suffix when it does not end in `.km`,
`.kame`, `.kash` or `.ksh`. Such an operand tries `PATH.km`, `PATH.kame`,
`PATH.kash` and `PATH.ksh`. More than one existing candidate is `RES_INVALID`
naming each; none is `FS_ERR` listing every attempted path. Importing a `.kmt`
document template is `RES_INVALID`. Include operands name their file exactly.

A module is loaded once per normalized source identity, however many `import`
or `from` statements name it. Loading uses source-loading authority, as with
`include`, not a general read grant, and never widens grants.

## Import

`import PATH` binds a namespace whose names are the module's exports. The
default namespace name is the file stem (`./lib/archive.kash` gives
`archive/…`); `as NAME` overrides it. A stem that is not a valid `NAME`, or that
names a reserved namespace (`x`, `env`, `std`, `module`), requires `as` with a
valid, unreserved alias; otherwise the statement is `DEF_INVALID`. Two imports
binding the same namespace name in one file are `DEF_INVALID`.

`from PATH import NAME, …` binds each listed export directly in the importing
file's scope, optionally renamed with `as`. Naming a name the module does not
export is `REF_MISSING` at the authored name. A `from` binding that collides
with a definition of the importing module or with another `from` name is
`DEF_INVALID`.

Imported names are lexical bindings. In expression position they resolve in
step 1 of `055` §6; in command position they resolve before builtins and
executables, without changing effect authority:

```kash
import ./lib/archive.kash

def release target
	archive/create! ./dist/${target}.tgz ./build/$target
```

Importing is not execution:

- Loading parses the module and binds its exports. No module definition is
  demanded, no `def` body runs and no command executes.
- No declaration of the imported module is registered. A module that offers
  rules exports functions returning `Rule` values, which the importer registers
  with `generate` (`055` §10.4). A rule built by an imported function resolves
  its outputs and inputs against the importer's root.
- Import cycles, direct or indirect, are rejected with `DEP_CYCLE`, naming each
  module on the cycle.

## Export

`export NAME, …` is a top-level statement. Each name must be a definition of the
module's own scope, which includes definitions spliced in by `include`. An
unknown name, or a name bound by `import` or `from`, is `DEF_INVALID`: `export`
cannot re-export imported names. A facade module defines an alias such as
`def f = lib/f` and exports it. A module without an `export` statement exports
nothing. Several `export` statements accumulate; exporting the same name twice
is allowed and has no further effect.

Only exported names are visible to importers. Unexported definitions remain
visible lexically to the module's own definitions, so a public function may call
private helpers.

## Include

`include PATH` splices the included file's top-level definitions into the
caller's scope at that point. `include?` skips a missing file only; a present
file with a parse error still fails. Include operands are literal (above). In a
Kame program, a conditional include is a declaration-time `if` containing
`include` (`055` §10.3); inactive branches do not read, track or cycle-check
their includes. Kash sources have no conditional include: `include` and
`include?` appear only unconditionally at the `.kash` top level.

- Includes expand depth-first. Definitions bind flat into the caller, including
  definitions from nested includes. Duplicate names are registration errors,
  including on repeated nonrecursive inclusion.
- No definition is demanded or executed at inclusion.
- Active include cycles are `DEP_CYCLE`, naming the chain; repeated includes
  register again under ordinary collision rules. Inclusion is not include-once.
- In a Kame program, the included file's declarations join the caller's
  declaration transaction. A Kash source has no declaration transaction and may
  include only Kash sources; including any other file from a Kash source is
  `RES_INVALID`. A declaration in a `.kash` file loaded as a module
  is `DEF_INVALID` (below).
- An included `.km` file may hold value definitions written `NAME = EXPR`;
  they are lazy definitions of the including Kame program.
- Included spans and include frames survive diagnostics.

`module/dir` and `module/file` in a definition denote the file that authored
that definition, not its caller's file, the root or the working directory. An
included or imported function therefore resolves assets beside its own source:

```kash
def check-config path
	let schema = joinpath module/dir "schema.json"
	validate $schema $path
```

## Kash modules

A `.kash` source always parses as an action block. When it is loaded as a
module, through `import` or `from` or as the target of an `include` from a
module, the loader checks that its top level contains only comments, `def`
definitions, `import`, `from`, `export` and unconditional `include` and
`include?`. Anything else, including top-level commands, `let`, `set`, control
blocks, `return`, `yield`, `exit` and declarations (`rule`, `task`, `service`,
`generate`), is `DEF_INVALID` at the offending statement's span with the load
frame; run a script file with `kame do run` instead. Bodies of `def`
definitions retain the full action-block grammar.

The same file may also be run as a Kash program; the module check applies only
when it is loaded as a module.

## Freshness and observation

A recipe that reaches an imported or included definition records the defining
file and the nested module files actually reached as content dependencies.
Each is identified by its source path and a digest of its bytes, not mtime. An
unreached definition adds no recipe dependency. Holding an imported function
value in a definition does not by itself make a recipe uncacheable or force a
rebuild. Callables are never published into the cache; existing cache
validation and publication rules of `008-cache.md` apply.

Stream filter predicates are a conservative exception: they run in a host
callback without an engine observation context, so dispatch records every
signed module source currently loaded. An edit to an unreached module may
therefore rebuild a recipe using a filter. Precise predicate demand tracking is
deferred; this fallback must retain freshness rather than risk missing a
reached dependency.

Dry-run may load and parse a module but does not execute `def` bodies or
commands. Watch treats every selected `include` target and every loaded module
as a source root. Failed loads retain attempted source paths, including missing
or invalid nested modules, so creating or repairing those files can restart a
failed watch cycle. Unselected includes and unreached definitions retain
ordinary demand semantics.

## Conformance

In addition to the checks in `048-kash-practical-scripting.md`, require:

- A `.km` program imports a `.kash` module and calls an exported `def` from a
  Kash rule body, both qualified (`archive/create!`) and through `from`.
- A `.kash` program imports a `.km` module and calls an exported function; the
  module's rule declarations are not registered.
- Unexported names are invisible to importers but callable by the module's own
  definitions. Exporting an unknown or imported name is `DEF_INVALID`; a facade
  re-exports through `def f = lib/f`; exporting a name twice is accepted.
- Two imports binding one namespace, a reserved or invalid stem without a valid
  `as`, and a `from` name colliding with a module definition or another `from`
  name are `DEF_INVALID`; importing a non-exported name is `REF_MISSING`.
- A top-level command or declaration in a `.kash` module is `DEF_INVALID` with
  its span and load frame; test `import`, `from`, `include`, `include?`,
  Kame conditional selection, duplicate names, suffix-selected grammar and
  unchanged authority.
- A Kash source including a `.km` file is `RES_INVALID`; it imports it instead.
- Import cycles and active include cycles are `DEP_CYCLE` naming the chain; a
  module named by several imports loads once.
- A suffixless import with several existing candidates is `RES_INVALID`; with
  none it is `FS_ERR` listing attempted paths; importing a `.kmt` is
  `RES_INVALID`.
- Module paths resolve relative to the authoring file despite a different root,
  working directory or `-C`; a relative module path in inline, stdin and `eval`
  sources is `RES_INVALID`. Non-literal and quoted operands are `PARSE_ERR`.
- `module/dir` in an included or imported function names its own file's
  directory.
- A second run reaching an unchanged module does not rebuild the output;
  changing reached file bytes invalidates it even with unchanged mtime.
  Unreached definitions add no recipe dependency, including nested helpers not
  reached by the recipe, except for the conservative stream-filter fallback
  above.
- Dry-run may parse a demanded module but does not execute its `def` bodies.
