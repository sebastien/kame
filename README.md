```

       ▄▄▄
     ▄▒▒▒▀
    ░░░▀  ▄    ▄░▒▄▄                   ▄░▒▄▄
   ░░░▀▄▄▄▓░  ▓▒░░░░▄   ▄░░▀▄▄ ▄▄▄   ▄█▀▄░░▓▌
 ▄▒▒▒▒░░▒▓▀  █▒▒▀▐░▒▓ ▄▒▒▒▒░░█▄░░█   ██▓█▀▒▒▌
 ▓▓▓▓▀▒▒█    █▓▌ ▒▒▓▌ ▓▓▓▓▀▒▒▄▒▒▒█  ▐█▓▌▀▀▀
▓██▓▀ ▓▓▓▄█ ▄██▄▄▓▀█▌▓██▓▀ ▓▓▓█▓▓▓▄█▐██▄▄▓▓▄▄
▀██▌ ▐██▀▀  ▐▀▀▀▀▀  ▀▀██▌ ▐██▀▀██▀▀ ▐▀▀▀▀▀▀▀▀
      ▀▀                   ▀▀  ▀▀
```

# Kame

`kame` (*kah-meh*) is the spiritual successor of `make`, redesigned top-down, trying to keep the good parts and resolve the bad parts, while trying to feel as familiar as possible, and stay true to its legendary versatility.

`kame`'s key goals are:

* **Familiarity**: we're big `make` users and we want everyone to feel welcome and at home so that there's minimal learning required to migrate, but we break clean when needed so that newcomers don't get hurt by papercuts.
* **Modern design**: we look at the entire SDLC, provisioning tools and dependencies, building artifacts, running tests, deploying to infrastructure. We support artifacts (files), targets (rules with no output) and services (long running tasks); no more `.PHONY` required.
* **Saner syntax**: `make` really wanted to have Lisp as the expression language, and Bash as the rule language. `kame` sports a Lisp-like language for expressions, non-clashing expansion using `@(…)` syntax in rules, and first-class representation of paths vs symbols. No more `.ONESHELL`, and composable languages.
* **Powerful model**: we read the [Build Systems à La Carte](https://dl.acm.org/doi/10.1145/3236774 "https://dl.acm.org/doi/10.1145/3236774") paper. `kame` sports an incremental, streaming, lazy reactive pipeline, with a virtual file system model for heterogeneous resource access. That gives you dynamic dependency resolution and lazy materialization of values.

Here's what `kame` looks like

```
# Symbols are aliases to expressions/values
cc = "gcc"
include-path = ./lib/h

# Paths and wildcards are first class: wildcard expansions are values
sources-c = (wildcard ./src/**/*.c)
headers = (wildcard ./src/**/*.h)

# Space separated references automatically create a flattened list
sources = sources-c headers

# Pattern replace remaps paths, and map applies it to every source
objects = (map sources-c (replace ./src/{path:**}/{name:*}.c ./build/{path}/{name}.o))

# Default rule will be triggered when no argument is given
default : build

# Dependencies can be paths, target names or expression/symbol values
build : @(objects)
	echo @(count objects) products built from @(count sources) inputs

# Target templates capture the matching target and expand it in inputs
./build/{path:**}/{name:*}.o : ./src/{path}/{name}.c @(headers)
	# No clash with the shell's $, more natural shorthands @< inputs @> outputs.
	@(cc) -I @(include-path) -c @< -o @>
	echo compiled @< to @>

archive : @(sources)
	tar cvfz archive-$(date +%F).tar.gz @<*
	echo created archive of size $(du -shc @<*)

# Shows statistics about this project.
stats :
	echo @(count sources) source files
	du -shc @(sources)
	echo @(count objects) object files
	du -shc @(filter ((exists? _)) objects)
```

## Why?

The venerable `make` is an incredibly versatile (but somewhat unloved) tool, that can be used to quickly automate running scripts and tools, and scales to building large programs while supporting virtually any language and toolchain.

Modern build systems tend to be quite language specific, or focus on running large distributed environment with complex setup and infrastructure, leaving a gap for a versatile solution that works for small to mid-sized projects and remain usable across a wide range of use cases, from local development to CI/CD, to building assets, provisioning toolchains, running tests and deploying assets.

What we love about `make`:

* **Versatile**, as it embraces the Unix philosophy and leverages the shell as glue, it makes little assumption about the underlying tools that you're orchestrating. This is why `make` is still viable today, because it focuses on composing and orchestrating, independently of the program.
* **Simple model**, consisting of targets, dependencies, and a rule that is lazily expanded into a script to produce the target output (or its effect).
* **Expressive**, where there is little overhead in taking existing shell scripts and orchestrating them with `make`. You can emulate make with things like `redo`, but at the end having a syntax designed for expressing dependencies beats generic scripting.

What we don't love about `make`:

* **Clunky**: the `$(VAR)` syntax clashes with shell's `$VAR` syntax, forcing `$$` in many places and creating confusion on which variable comes from where. `.PHONY`, `.FORCE`, `.ONESHELL` are pretty much required all the time. Function invocation syntax can be verbose and error prone, the variants in variable value evaluation (`:=` vs `=` ) can be confusing.
* **Brittle**: a rule changing does not invalidate the target, implicit dependencies (eg. variable changing) are not taken into account, full reproducibility is uncertain.
* **Opaque**: it's hard to understand what are the dependencies or the consumers of a rule, or what went wrong when something fails, or doesn't work as expected.

What we want `kame` to bring:

* **Familiarity**: have a clear lineage to `make` so that onboarding is relatively easy, borrowing the good parts from the syntax and model, and resolving the bad parts.
* **Clarity**: the mental model and dependency resolution should be both clear conceptually and easy to introspect and test, so that understanding how things work gets more intuitive.
* **Reliability**: while `make` is a reliable tool, its usage can get unreliable due to its limitations, in particular stale builds, cache contamination that can lead to builds that won't reproduce. Note that we're not aiming for full reproducibility, as this is solved by a combination of environment and build system.

The name `kame` is *verlan* for `make`, a nod to the process of starting from the result (a makefile without the quirks), and building the language/system to support it.

## Actually Portable Executable (APE)

An experimental APE build is available with `make dist-ape` or `kame dist-ape`. The target provisions the official Cosmopolitan `cosmocc` toolchain under `build/tools/cosmocc` and produces `dist/kame.com`, without changing the default native build. It requires `curl` and `unzip`; set `COSMOCC_URL` to override the default toolchain archive URL.

The APE is intended for POSIX environments supported by Cosmopolitan. Kame still runs build recipes through `/bin/sh` by default, so projects need a POSIX shell and their usual build tools; the APE does not bundle these dependencies or provide native Windows recipe support.
