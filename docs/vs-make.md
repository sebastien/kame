# Kame vs Make

This document shows some comparisons between `make` and `kame`. I hope
this show how `kame` is similar to make, and how it improves on some of
the edge cases.

## Shell scripting

## Tasks vs Outputs

`make` does not make the distinction between a task and an output:

``` make
SOURCES=$(find src/*.*)
PACKAGE=$(SOURCES:src/%=dist/%)
PACKAGE_HOST=server:~/packages

# This is a file
package.tar.gz: $(PACKAGE)  
    env -C dist tar cvfz $(abspath $@) $<

# This is a rule
upload: package.tar.gz
    scp $< $(PACKAGE_HOST)

.PHONY: upload
```

while in `kame`, the distinction is explicit and there is no need for a
`.PHONY` rule.

``` kame
sources=(find src/*.*)
package=sources | (rewrite src/{} dist/{})
package-host=server:~/packages

# An output
package.tar.gz <- (all sources)
    env -C dist tar cvfz @(abspath @) @<)

# A rule
@build <- package.tar.gz
    scp @< @{package-host}
        
```

## Rule parameters

``` make
HOST_PRODUCTION=production.acme.corp
HOST_STAGING=staging.acme.corp
env-var=$($1_$*)
release: release-development
release-%:
    if [ -z "$(call env-var,HOST)" ]; then
        echo "Error: Environment not found $*"
        exit 1
    else
        echo "Releasing to env: $*"
        echo "Host: $(call env-var,HOST)"
    fi
.PHONY: release
.ONESHELL
```

With kame, it's possible to use string pattern matching to extract
values from the rule name.

``` kame
host.production=production.acme.corp
host.staging=staging.acme.corp
@release = @release/development
@release/{env}:
    if [ -z "@{host.{env}}" ]; then
        echo "Error: Environment not found @{env}"
        exit 1
    else
        echo "Releasing to env: @{env}"
        echo "Host: @{host.{env}}"
    fi
```

## Implicit dependencies

In `make`, you will have to managed every single tool you refer to if
you want them to be actual dependencies:

``` make
uses=$(foreach T,$1,$(require-$T))
list-users: $(call uses,cat sort cut)
    cat /etc/passwrd | cut -d -f1 | sort
require-%:
    if [ -z "$$(which $* 2> /dev/null)" ]; then
        echo "ERR: $* not found"
        exit 1
    fi
.PHONY: list-users
.ONESHELL
```

while in `kame`, this can be implicit

``` kame
@list-users:
    @`cat` /etc/passwd | @`cut`  
```

or explicit

``` kame
use(t...)=t | (all (assert (sh/which?) "Tool not found @{}))
@list-users: [cat sort cut] | use
    cat /etc/passwd | cut  
```

## Depending on configuration variables

``` make
USE_NIX=coreutils gnugrep bash
use-var=.var-$1

shell.nix: $(use-var,USE_NIX)
    $@ <<EOF
    { pkgs ? import <nixpkgs> {} }:
    pkgs.mkShell {
        buildInputs = [
            $(foreach P,$(USE_NIX),pkgs.$P)
        ];
        shellHook = ''
            echo "Nix shell provisioned with core utilities and bash."
        '';
    }
    EOF

shell: shell.nix
    nix-shell shell.nix

# We need to map the variable to a file and detect a content change
# to ensure the dependency works.
.var-%: FORCE
    if [ ! -e "$@" ] || [ "$$(cat $@)" != "$($*)" ]; then
        echo "$($*)" > $@
    fi

.PHONY: shell
FORCE:
.ONESHELL
```

while in `kame`, a reference will be enough

``` kame
use-nix=coreutils gnugrep bash

# Here Kame will detect the `use-nix` reference. If `use-nix` changes,
# then `shell.nix` will be rebuilt.
shell.nix
    @@ <<EOF
    { pkgs ? import <nixpkgs> {} }:
    pkgs.mkShell {
        buildInputs = [
            @{use-nix | (map "pkgs.{}")}
        ];
        shellHook = ''
            echo "Nix shell provisioned with core utilities and bash."
        '';
    }
    EOF

shell: shell.nix
    nix-shell shell.nix
```

## Memoized variables
