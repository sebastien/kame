# Rules: describe what must exist

Start with [the publication](publication/README.md). It is a standalone project:
you do not need to run the expression or Kash lessons first.

The `.kmk` file describes outputs, dependencies, and recipes. Its expressions
calculate which outputs are required; its recipes use ordinary POSIX shell.
