# Kame − Language reference

## Overview

`kame` is a set of three simple languages composed together to define
dependencies:

-   The **structural language**, which defines configuration parameters
    and rules.

-   The **expression language**, which defines Lisp-like transformation
    of strings and lists of strings.

-   The **template language**, which define patterns that can match
    paths and strings.

## Syntax

The language's syntax is designed with the following use-cases:

-   Play nice with the shell. Expressions are contained within `@(…)` which
    does not clash with the `$(…)` or `${…}` syntax of the shell.

-   Structured and easy to parse. Like Python, it uses indentation for its
    structure.

## Values

### STRING

A `STRING` content is a sequence of characters that might contain spaces and
any unicode character.

```
one_two_three
one two three
```

When unquoted, strings can't contain unescaped `()` or `{}` or `|` or `\`, but these
characters can be used when using single or double-quotes to surround the string:

```
"Hello (world)"
'Hello | world'
```

In case you don't want to quote the whole string, you can escape these
characters using `\`. If you want a backlash, double it like so `\\`

```
Hello \| world
Hello \(world\)
```

Whenever a string contains `@(` and `)` , the `@(`…`)` sequence is expanded
as an expression:

```
This project has @(all sources | count) source files
```

Whenever a string contains a `NAME` within `@{…}`, the name will be expanded
to its corresponding value.

```
Project @{project.name} revision @{project.revision}
```

The `@` character was chosen because it doesn't clash with the `$` of shell
scripts, allowing expressions like "`Project @{project.name} ${BUILD_ID}`".

### TEMPLATE

A **template** is a string that contains _wildcards_ that can match one or
more strings. The `*`, `**` and `?` wildcards works just like in shell:

```
src/c/*.c
src/**/*.c
src/*/*.?
```

Additionally `[N…]` will match any of the characters between brackets,
and `[N-M…]` will match any character between `N` and `M`, inclusively.

```
src/[ch]/*.[ch]
```

Any part of a template, including wildcards, can be **matched and assigned**
to a group if it is enclosed in `{` and `}`.

```
src/c/{*}.c
src/{**}/{*}.c
```

Matched groups can also be **named** by specifying the name followed
by a `:` within the _template group_.

```
src/{stem:*}
src/{parents:**}/{name:*}.c
```

Groups can contain PCRE-compatible regular expressions when the
template group is of the form `{(…)}`.

```
src/{(.+\.c)}
```

### DEREFERENCE

A derference to a symbol (parameter/rule) is a dot-separated string, prefixed with `@` and
optionally wrapped in `{…}`. The dots act as a destructuring operator in case the referenced
value is composite. Names are a subset of strings and can't contain
any template expression. Special characters are not allowed in names, except
`-` and `_`.

```
@timestamp
@{timestamp}
@{commands.python}
@{sources.images.0}
```

### EXPRESSION

Expressions follow a syntax that feels like a hybrid of Lisp and shell:

```
# Returns the value of the `command.python` variable
(get command.python)

# Returns up to 5 files that match *.c within source.files
(get source.files | filter *.c | slice 0 5)
```

All arguments in expressions are _lazily evaluated_ and might be parsed into
numbers/variable references/lists, or left as-is, depending on the function.

Functions can be chained using the pipe `|` operator. For instance, both
lines are equivalent:

```
# Pipe operator
(str hello world | split ' ' | count)
# Expression nesting
(count (split ' ' (str hello world)))
```

Parentheses around the expression are not necessary when the contents is
indented:

```
match (count @input.files)
	case (= 0)
		none
	case (= 1)
		one
	else
		many
```

is equivalent to

```
(match (count @input.files) (case (= 0) none) (case (= 1) one) (else many))
```

### DEFINITION

A **definition** binds a _value_ to a _parameter_ in the _parameter space_. Definitions
take STRINGS, but will interpret EXPRESSIONS and NAME if the trimmed string starts and ends
with `(…)` or with `{…}` respectively.

```
# Assign the value `gcc` to alias.cc
alias.cc  = gcc
# Assigns the result of the evaluation of `(find src/*/*.c)` to sources.c
sources.c = (find src/*/*.c)
# The `(find …)` expression will be evaluated every time `sources.c`
# is referenced or invoked.
(sources.c) = (find src/*/*.c)
# Desfines a new function `count-line`
(count-lines text) = (split "\n" @text | filter (empty | not) | count)
```

Note that the spaces before and after the definition value are trimmed. If you'd
like to keep them, you can use quotes:

```
parameter-with-spaces = "  the leading and trailing spaces will be preserved  "
```

When a definition value is an unquote string containing spaces, it will be
interpreted as a list instead of a single value.

```
libraries.c = gtk+-3.0 libpcre
```

If you want a single declaration to be expressed as a list, do:

```
libraries.c = (list gtk+-3.0)
```

### RULE

A **rule** defines a _mapping between inputs and outputs_. The rule's
_body_ contains a mix of EXPRESSIONS and STRINGS that expand into a
shell script that transforms the _inputs_ into _outputs_.

```
{%}.o ← {}.c
| Compiles a C file into an object file.
	@if options.debug
		gcc -g -c @{<} -o @{>}
	@else
		gcc -c @{<} -o @{>}
```

The generic format for a rule is:

```
TARGETS… ← INPUTS…
| DOCUMENTATION
	OPERATION…
```

Note that if you don't want to write the `←` symbol (`Ctrl-k < -` in Vim),
you can also use the `<-` shorthand.

Some rules produce no output, in which case they will use the `:` instead
of the `←`:

```
clean:
| Cleans any product file
	(all product | filter _ (exists?) | set cleaned | each (shell unlink {_}))
	(log Cleaned (cleaned | count) files)
```

## Special Expressions

### `<`, `<*`, `<N`

The first input, all inputs, the `N`-th input.

### `>`, `>*`, `>N`

The first output, all outputs, the `N`-th output.

### `?`, `?*`, `?N`

The first dependency, all dependencies, the `N`-th dependency.

### `_`, `_*`, `_N`

The first argument, all arguments, the `N`-th argument.

## Meta-Programming

## Grammars

This is the grammar for the expression language:

```
INDENT     :=                {indentation += 1}
DEDENT     :=                {indentation -= 1}
INDENTED   := '\t'+@tabs     {indentation == len(tabs)}
SYMBOL     := '[\w_-]'+
NAME       := '{' SYMBOL ('.' SYMBOL+) * '}'
EXPRESSION := '(' WORD+ ')'
            | '@' WORD+ INDENT ('\n' INDENTED EXPRESSION)+ DEDENT
WORD       := NUMBER | NAME | EXPRESSION | SYMBOL

```

This is the grammar for the string language:

```
EMBED_EXPRESSION := '@' EXPRESSION
EMBED_NAME       := '@' NAME
STRING           := '[^@\n]' | '@\\' | EMBED_NAME | EMBED_EXPRESSION
```

This is the grammar for the structural language

```
EOL        := '\n'
COMMENT    := '#' '[^\n]'* (EOL|EOF)
EMPTY      := '\t '* EOL
DEFINITION := SYMBOL ('.' SYMBOL)* '=' (EXPRESSION | STRING) EOL
LINE       := EMPTY | COMMENT | DEFINITION
```
