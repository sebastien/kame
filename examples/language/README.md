# Kame language examples

These directories are independent build projects. Run the CLI from the example
directory so relative inputs and outputs stay within that project:

```sh
(cd examples/language/hello && ../../../build/kame.debug)
(cd examples/language/patterns && ../../../build/kame.debug)
```

The hello example introduces definitions, includes, rules, and recipe
interpolation. The patterns example shows a captured target template and
explicit file inputs and outputs.
