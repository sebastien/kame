# Programs and source composition

A Kame program (`.km`) contains definitions, rules, tasks, services, and
declaration-time control flow. A Kash script (`.kash` or `.ksh`) is an action
block. Both use the same expressions. Inline Kame fragments supplied with
`-c` can also evaluate expressions against the loaded program.

## Definitions

```kame
VERSION = "1.2.3"
FLAGS = ["-Wall", "-Wextra"]
SOURCES = ./src/*.c

def greet name = str "Hello, " name
default = greet "Ada"
```

`NAME = EXPR` defines a lazy, memoized value. It can refer to definitions that
appear later. `def NAME PARAM… = EXPR` defines a function, and an indented
action block can supply its body. Define a zero-argument function with
`def NAME () = EXPR` and call it with `NAME ()`. A final `rest...` parameter
collects remaining arguments. A lazy value can also use an indented block
following `NAME =`, with `return` supplying the value.

`NAME ?= EXPR` supplies a configurable default. `--define NAME=VALUE` or
`NAME=VALUE` passes literal text, while `--set NAME=LITERAL` passes typed
data. Configuration defaults can use an authorized environment value.
Use an explicit `env/NAME` expression to read an environment variable.

```kame
CHECKS ?= []
CHECKS += [./tests/unit]
CHECKS += EXTRA_CHECKS
EXTRA_CHECKS = [./tests/smoke]
```

`+=` lazily concatenates lists in source order, preserving nested items and
duplicates. With no earlier base, the base is `[]`. A CLI or environment
override replaces the aggregate. A contribution that depends on its own
final value causes a dependency cycle.

## Include declarations or import names

```kame
include ./values.km
include ./rules/common.km
include? ./local.km

mode ?= "debug"
if mode == "debug"
	include ./debug.km
else
	include ./release.km

import ./lib/toolchain as tools
from ./lib/config import mode-name as configured-mode
```

Use literal explicit paths relative to the source containing the statement.
Includes share the caller's scope and register declarations at the include
site. Optional includes tolerate missing files. Repeated includes register
again, and active include and import cycles report a dependency cycle.

Imports load a module's exported names. The module declares its public
bindings with `export NAME, …`. A plain import uses the source stem as a
namespace, and `as` sets the namespace explicitly. Use `tools/name` to
access a namespaced binding. A selective `from` import binds names directly
and can rename them. Imported rules are available to the caller through
exported functions returning rule values, which `generate` can register.

Top-level Kame conditionals and loops run during declaration. Use an
indented block for declarations and conditional includes. This phase
applies restrictions on process and filesystem effects. A Kash source
used as a module may declare `def`, imports, exports, and unconditional
includes of Kash modules.

## Run one or more sources

```sh
kame do run --print-result ./greet.km
kame do run ./greet.km --entry VERSION
kame do run ./greet.km -c '(out! (greet "Lin"))'
kame do run --lang expr -c '1 + 2'
kame do run -c '(out! (json args))' -- 'one two' --help
```

Here `greet.km` contains the definitions in the first example. A value
program can provide `default` or expose a named entry. Use
`--print-result` to display the final value of Kame work. An explicit
value entry or an `expr` fragment displays its result.

File suffixes select the parser. Use `--lang km`, `--lang kash`, or
`--lang expr` to select a mode explicitly for following sources. Each
fragment must contain complete statements. Fragments share the execution
session and its definitions. Put literal operands after `--` to bind the
`args` list. The selected `-C` directory supplies the operation's root,
and include and import paths still use the source directory.

Inspect programs with `kame do parse --lang km FILE` and check canonical
formatting with `kame do fmt --lang km -n FILE`.
[CLI](./cli.md) · [Rules](./rules.md) · [Kash](./kash.md)
