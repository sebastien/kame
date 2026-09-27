## Similar tools

Ronin:
-	`[+]` Pure Python
-	`[-]` Awkward for shell-like operations

[Pyrate](https://github.com/pyrate-build/pyrate-build):
- `[+]` 	Pure Python
- `[-]` 	Very focused on Fortran/GCC

[GENIe](https://github.com/bkaradzic/GENie#genie---project-generator-tool):
- `[+]` Does cross platform
- `[-]` Still has to use makefiles

[Craftr](https://github.com/craftr-build/craftr)
- `[+]` Clean syntax, high-level description
- `[-]` Needs per-language module

Ryan Dahl said about [GYP](https://gyp.gsrc.io/docs/UserDocumentation.md) that
keeping to use it was the single [biggest technical
failure](https://www.youtube.com/watch?v=M3BM9TB-8yA) of the project.

Other references:
- http://250bpm.com/blog:153
- https://pydoit.org/
- https://atilaoncode.blog/2019/04/03/build-systems-are-stupid/

## Make Pain points


### Transparency in selecting rules to apply

For a given output, show what the selection process for which rules to run.
In make, it's not always clear why one rule is chosen over another one.


### Awkward syntax

This is how to define the path parents in Make

```make
path-parents=$(patsubst %/,%,$(patsubst ./,,$(if $(patsubst %.,%,$(1)),$(call path_parents,$(shell dirname $(1))) $(dir $(1)),)))
```

while in `kame`, that could be

```kame
(path-parents ...args)=	args | map (abspath | match {parent}/{_:basename} | @parent)
```

### Lack of clarity of errors

Not communicating the reason of a failure:

```
$ make data/examples/sinc.curved
make: *** No rule to make target 'data/examples/sinc.curved'.  Stop.
```

Should say something like  «missing requirements: …»

