Ronin::

	[+] Pure Python
	[-] Awkward for shell-like operations

[Pyrate](https://github.com/pyrate-build/pyrate-build)::

	[+] Pure Python
	[-] Very focused on Fortran/GCC

[GENIe](https://github.com/bkaradzic/GENie#genie---project-generator-tool)
	
	[+] Does cross platform
	[-] Still has to use makefiles

[Craftr](https://github.com/craftr-build/craftr)
	
	[+] Clean syntax, high-level description
	[-] Needs per-language module

http://250bpm.com/blog:153
https://pydoit.org/


https://atilaoncode.blog/2019/04/03/build-systems-are-stupid/

Features I wish were easy in make
=================================

- For a given output, show what the selection process for which rules to run. 
  In make, it's not always clear why one rule is chose over another one.

Bad examples of build system
============================

[GYP](https://gyp.gsrc.io/docs/UserDocumentation.md) ― Ryan Dahl said
keeping to use it was single [biggest technical failure](https://www.youtube.com/watch?v=M3BM9TB-8yA) of the project.


Awkard in Make

```
path_parents   =$(patsubst %/,%,$(patsubst ./,,$(if $(patsubst %.,%,$(1)),$(call path_parents,$(shell dirname $(1))) $(dir $(1)),)))
```

Making sure that some files are transient, for instance:

%.gz: %
	cat $< | gzip > $@

if $< was created implicitely, only to satisfy the dependency, it should probably be cleaned up.


Also, being able to format/log command output on per-rule basis.


Not communicating the reason of a failure:

```
$ make data/examples/sinc.curved
make: *** No rule to make target 'data/examples/sinc.curved'.  Stop.
```

Should day: "missing requirements: …"



