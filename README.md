== kame: A modern make replacement

<script async="" defer="" src="https://factlink.com/lib/dist/factlink_loader.min.js">
</script>

`make` is an amazing tool, but over the years I've found that it created
some friction with the following use cases:

- Hard to properly *detect implicit dependencies* between files (`import`, `require`, etc)
- Hard to guarantee *build reproducibility* (`make build` vs `make clean ; make build`)
- Hard to understand *how variables and rules are expanded* (need to split declaration and rules when `include`'ing files)

Based on these limitations, `kame`is designed with the following **design goals** in mind:

- *Dynamic*: automatically discover per-file dependencies
- *Reliable*: validates build reproducibility
- *Debuggable*: Full variables and rule introspection
- *Versatile*: don't be tied to a specific language or task (compiling files)

Like `make` we want to integrate well with a UNIX environment:

- Shell is the main language for writing rules and customizing the build
- Command line interface with man page
- Programming language agnostic

We also add features that are important in today's environment:

- Detect and configure the environment (commands, packages, modules)
- Logging and error messages

Overall, we hope that this tool will have a similar feel to `make`, while
relieving from some of its pain points.

```
# Defines an alias that will be available in scripts
tool.cc = (require gcc) {path.include}
path.include = lib/h
sources.c = (find **/*.c)
sources.h = (find **/*.h)
archive = archive-(shell date +'%F').tar.gz

# Defines how to collect dependencies about a specific file (<<)
{name:*}.c <<
	# If for some reason the compiler changes, or the include path changes
	# We will invalidate the rules. Using @{alias.cc} directly in the script
	# would make the following line redundant.
	@tool.cc -MM @name > @output

# Defines how to build a C file (<-). Any change to the dependencies of the inputs
# will re-trigger a change.
{*}.o <- {0}.c
	# The @< and @> are respectively the inputs and outputs of the rule
	@tool.cc @< -o @>
	# We log a message to inform the user
	(log Compiled @input to @output)

# Defines how to build the archive (<-)
{archive} <- (all sources)
	@tool.tar cvfz @> @<
	(log Create an archive of size (shell du -hs @<))

build: (all sources | replace {**}/{*}.c .build/{0}/{1}.o)
| Builds the project
	(log (count (products inputs)) products built from (count inputs))

# Defines a rule that doesn't produce anything  (a PHONY rule)
stats:
| Shows statistics about this project.
	(log (count (all sources)) source files = (shell du -hs @(all sources)))
	(log (count (all outputs)) product files = (shell du -hs @(all outputs | filter-with exists?)))

```  

Shows the **list** of  non-building rules available along with their *docstring*.

```
$ kame -l
build 
builds ― Builds the project
stats  ― Shows statistics about this project
```

Shows the **tree** of dependencies between rules and configuration parameters

```
$ kame -t
  ├── alias.cc
  │   └── path.include
  ├── path.includes
  ├── sources.c
  ├── sources.h
  ├── build
  │   └── sources
  │       ├── sources.c
  │       └── sources.h
  # TODO: How do we represent the transformation into output
  ├── stats
  │   └── sources
  │       ├── sources.c
  │       └── sources.h
  └── archive
      └── sources
          ├── sources.c
          └── sources.h
```

Shows the **tree** of dependencies for a given parameter or rule

```
$ kame -t alias.cc
alias.cc: (require gcc) {path.include}
├── (require gcc): gcc
└── {path.include}: {}
```

Shows the **value** of the alias with the given name

```
$ kame -p alias.cc
gcc -I lib/H
``` 

This works for a rule's inputs

``` 
$ kame -p build.inputs
src/c/a.c
src/c/b.c
```

and outputs

```
$ kame -p build.outputs
.build/a/a.o
.build/c/b.o
``` 

The following outputs the **expanded shell script** that can make the
given files.

```
$ kame -p build/dist/*.o
``` 

## Relations with `make`

Here are the similar aspects:

- Variables: can be declared in non-lexical order.
- Rules: create files while depending on other files.
- Integrates with the shell: rules are shell scripts.
- Lisp-ish extension language.

Differences:

- Syntax does not interfere with the shell: no need for weird `$$` escapes.
- Difference between building and non-building (`PHONY`) rules.
- Extension language that supports lists and strings.
- Path and string templates with substitution.
- Debugging is easier: everything can be introspected, including the
  shell scripts attached to rule bodies.

## Model

The core model is build on the notions of _rules_, _builders_ and _configuration parameters_. All
of these are represented as _nodes_ in a graph the tracks dependencies:

```
NODE     ――――――――{ depends  *}――――――――→ NODE
```


`PARAMETER`s are `NODE`s that define **configuration parameters**:

- `name`: a globally unique name for the parameter

`RULE`s are `NODE`s that define a sequence of instructions, but have no tracked output:

- `name`: a globally unique name for the rule
- `inputs`
- `instructions`

BUILDERSs  are NODEs that define how to create OUTPUTs from INPUTS

- `name`
- `inputs`
- `outputs`
- `instructions`

LINKERs  are NODEs that define dependencies between NODEs

- `nodes`
- `depends`

EXPRESSIONs are NODES that define a computation involving other NODEs

- `functor`
- `arguments`

This model supports the following features:

- Dependencies between both files and configuration parameters (a parameter change might trigger a rebuild)
- Conditional application of rules ()

- Rule.input: the actual input files (must be files)
- Rule.output: the output files (must be files)
- Rule.tools: the tools/commands used to build the rule
- Rule.requires: the externals/environment requirements (modules, libraries, etc)
- Rule.dependencies: the actual dependencies (files) 
- Rule.condition: a guard that activates the rule or not

## Cookbook

### Transforming and renaming sets of files

The typical example is getting a list of sources and rewriting this list
as output files.

```
sources     = (find src/**/*.*)
products.o  = (filter sources src/{**}/{*}.c | rewrite build/{}/{}.o)
products.py = (filter sources src/{%}        | rewrite build/{})
```

### Specifying how to extract dependencies from a file

Most program files import, reference or use other files that might affect
their build version. Imagine that you have a JavaScript source tree and
that you want to know which JavaScript files are referenced by another:

```
{*}.js <<
	cat "@0" | grep "import" | ‥ > $@
```


### Conditional configuration

In this example, we let the user define the version of Python to use
and we'll detect that the version is installed and configure aliases
for `python` and `pip` that match the specific version.

```
python.version     = 3
python.realversion = (shell python@{python.version} --version | cut -d' ' -f2 | cut -d'.' -f1-2)
commands.python    = python@{python.realversion}
commands.pip       = pip@{python.realversion}
```

### Ensuring the presence of an external dependency

You might want to check that there is a specific version of a C library installed.
installed.

```
libraries.c       = gtk+-3.0 libpcre
libraries.exist   = (filter libraries.c (shell pkg-config @_ ))
libraries.missing = (filter libraries.c (in libraries.c))

build:
	@if (empty? libraries.missing)
		# Put your build code here
		‥
	@else
		# We log the missing libraries and we fail
		(for libraries.missing (log Missing library {_}))
		(fail! Some libraries are missing)
```

### Adding your custom functions

Here is an example of defining new functions (and parameters) that allow
for the automatic installing of Python packages.

```
commands.pip = pip
pip.packages = (shell @{commands.pip} list)
(python-module? module) = (in pip.packages module)
(python-require module) =
	if (python-module? module | not)
		shell @{commands.pip} install --user --upgrade @{module}
```

### Meta-programming

Sometimes you might want to generate new rules based on some variables. In
this case we're going to take a list of source JavaScript file and
introspect their dependencies:

```
TODO
```
