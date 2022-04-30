# Semantics

[ ] Introduce the notion of symbol, where a symbol can be attached to
    a file, a subset of a file, or a set of files. This is useful for dependencies,
    where you can list symbols in a file, and also for finding where a symbol is
     defined.
    
[ ] **Meta-programming**: all languages should support escaping/templating
     for meta-programming.

[ ] **API**: as seen with tools like Webpack, the core mechanisms of a build
    system could very well be exported as an API. It's basically a generalization
    of the engine with structured in-memory data instead of just the FS.

# Features

[ ] Target auto-correct :: no more 
    ```
    make dist/components/scracthpad/view.js 
    make: No rule to make target 'dist/components/scracthpad/view.js'.  Stop.
    ```

[ ] Determine if there is a production rule for a given path. This makes it possible
    to know if you need to clean something or not. 

Edge cases
==========


In make, when we have the same rule that apply with variants to a different
set of inputs/outputs. There's no easy way to abstract it.

```
$(PATH_BUILD)/%.json: %.hjson
	@echo "$(GREEN) ◀  $(BOLD)$@$(RESET)$(GRAY) $(BLUE)[HJSON]$(RESET)"
	@mkdir -p `dirname $@`
	@# NOTE: HJSON has encoding issues with Python2
	@$(PYTHON3) -m hjson.tool -j $< > $@

$(PATH_BUILD)/data/%.json: $(PATH_SRC_DATA)%.hjson
	@echo "$(GREEN) ◀  $(BOLD)$@$(RESET)$(GRAY) $(BLUE)[HJSON DATA]$(RESET)"
	@mkdir -p `dirname $@`
	@# NOTE: HJSON has encoding issues with Python2
	@$(PYTHON3) -m hjson.tool -j $< > $@
```


In make, it's not straightforward to define a variable per target. For instance

```
DIST_EXTRA:=$(shell find data/flatdb -name '*.json')
```

This is potentially costly, if there are many files, so we only want the
variable to be defined at a specific stage.

Rules that faile but leave files
--------------------------------

When a rule fails, it should leave the outputs exactly as they were before,
not output  a partially written file.
