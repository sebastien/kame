#!/usr/bin/env bash
# Spec: docs/spec/014-patterns.md — Placeholder Sections, Pattern Values, Pattern Replace
# Spec: docs/spec/013-tests.md — T014-01-patterns-sections
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T014-01 placeholder sections and pattern replace"

test-step "toolchain and binary"
cli_require_tools
cli_build

# evaluates EXPR to EXPECTED via `do expr`
evaluates() { # EXPR EXPECTED
	local expr="$1"
	local expected="$2"
	cli_run -- do expr -c "$expr"
	cli_expect_status 0 "$expr"
	cli_expect_stdout "$expected" "$expr"
	cli_expect_stderr_empty
}

diag() { # EXPR CODE
	local expr="$1"
	local code="$2"
	cli_run -- do expr -c "$expr"
	cli_expect_status 1 "$expr"
	cli_expect_stderr_contains "$code"
}

test-step "placeholder sections evaluate as lambda equivalents"
evaluates '(((nop _0)) 7)' '7'
evaluates '(((list _0 _0)) "same")' '[same same]'
evaluates '(((list _0 __ _2)) "a" "b" "c")' '[a b c]'
evaluates '(((list _1)) "unused" "kept")' '[kept]'

test-step "section arity is enforced"
diag '(((nop _0)))' 'EXPR_INVALID'
diag '(((nop _0)) 1 2)' 'EXPR_INVALID'
diag '_0x' 'REF_MISSING'

test-step "patterns classify and pattern replace matches"
evaluates '(replace ./{**}/{*}.c ./build/{_0}/{_1}.c "./a/b/x.c")' './build/a/b/x.c'
evaluates '(replace ./{**}/{*}.c ./build/{_0}/{_1}.c "./a.c")' 'nil'
evaluates '(replace ./{**}/{*}.c ./build/{_0}/{_1}.c ["./a.c" "./x/y.c" "./no.obj"])' '[nil ./build/x/y.c nil]'
evaluates '(replace ./src/{name:*}.c ./build/{name}.o "./src/demo.c")' './build/demo.o'
evaluates '(map (replace ./{**}/{*}.c ./build/{_0}.o) ["./a.c" "./x/y.c"])' '[nil ./build/x.o]'
evaluates '(replace ./{**}/{*}.c ./x/{_0}.o "./a/b.c")' './x/a.o'
evaluates '(replace ./{**}/{*}.c ./x/{_1}.o "./a/b.c")' './x/b.o'
evaluates '(str ./{**}/{*}.c)' './{**}/{*}.c'
evaluates '(replace "banana" "a" "b")' 'bbnbnb'

test-step "invalid pattern combinations"
diag '(replace "a" "b")' 'PAT_INVALID'
diag '(replace ./{**}/{*}.c ./build/{_0}/{_9}.c "./a/b.c")' 'PAT_INVALID'
diag '(replace ./{**}/{*}.c ./build/{_0}/{_1}.c 42)' 'EXPR_INVALID'

test-step "patterns render as their text in rule inputs"
(
	mkdir -p patternproj/src
	cd patternproj
	cat > Makefile.kmk <<'LMK'
sources = ["./src/a.c" "./src/b.c"]
objects = (replace ./src/{name:*}.c ./build/{name}.o sources)
./build/marker : @(objects)
	cat @<* > @>
./build/{name}.o : ./src/{name}.c
	cp @< @>
LMK
	echo a > src/a.c
	echo b > src/b.c
	cli_run -- do plan ./build/marker
	cli_expect_status 0
	cli_expect_stdout_contains './build/a.o'
	cli_expect_stdout_contains './build/b.o'
)

test-end
