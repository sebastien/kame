#!/usr/bin/env bash
# Spec: docs/spec/014-patterns.md — Placeholder Sections, Pattern Values, Pattern Replace
# Spec: docs/spec/013-tests.md — T014-01-patterns-sections
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T014-01 placeholder sections and pattern substitution"

test-step "toolchain and binary"
cli_require_tools
cli_build

# evaluates EXPR to EXPECTED via `do run --lang expr`
evaluates() { # EXPR EXPECTED
	local expr="$1"
	local expected="$2"
	cli_run -- do run --lang expr -c "$expr"
	cli_expect_status 0 "$expr"
	cli_expect_stdout "$expected" "$expr"
	cli_expect_stderr_empty
}

diag() { # EXPR CODE
	local expr="$1"
	local code="$2"
	cli_run -- do run --lang expr -c "$expr"
	cli_expect_status 1 "$expr"
	cli_expect_stderr_contains "$code"
}

test-step "placeholder sections evaluate as lambda equivalents"
evaluates '(nop _0) 7' '7'
evaluates '(list _0 _0) "same"' '["same", "same"]'
evaluates '(list _0 __ _2) "a" "b" "c"' '["a", "b", "c"]'
evaluates '(list _1) "unused" "kept"' '["kept"]'

test-step "section arity and missing arguments"
evaluates '(nop _0) ()' 'nil'
diag '(nop _0) 1 2' 'EXPR_INVALID'
diag '_0x' 'REF_MISSING'

test-step "patterns classify and pattern substitution matches"
evaluates '(subst ./{**}/{*}.c ./build/{_0}/{_1}.c "./a/b/x.c")' './build/a/b/x.c'
evaluates '(subst ./{**}/{*}.c ./build/{_0}/{_1}.c "./a.c")' '"./a.c"'
evaluates '(subst ./{**}/{*}.c ./build/{_0}/{_1}.c ["./a.c", "./x/y.c", "./no.obj"])' '["./a.c", ./build/x/y.c, "./no.obj"]'
evaluates '(subst ./src/{name:*}.c ./build/{name}.o "./src/demo.c")' './build/demo.o'
evaluates '(subst ./src/txto/{prefix:**}.txto ./build/data/{prefix}.json "./src/txto/blog/2026/deep/post.txto")' './build/data/blog/2026/deep/post.json'
evaluates '(map (subst ./{**}/{*}.c ./build/{_0}.o) ["./a.c", "./x/y.c"])' '["./a.c", ./build/x.o]'
evaluates '(subst ./{**}/{*}.c ./x/{_0}.o "./a/b.c")' './x/a.o'
evaluates '(subst ./{**}/{*}.c ./x/{_1}.o "./a/b.c")' './x/b.o'
diag '(str ./{**}/{*}.c)' 'EXPR_INVALID'
evaluates '(subst "a" "b" "banana")' '"bbnbnb"'

test-step "invalid pattern combinations"
diag '(subst "a" "b")' 'EXPR_INVALID'
diag '(subst ./{**}/{*}.c ./build/{_0}/{_9}.c "./a/b.c")' 'PAT_INVALID'
diag '(subst ./{**}/{*}.c ./build/{_0}/{_1}.c 42)' 'EXPR_INVALID'

test-step "patterns render as their text in rule inputs"
(
	mkdir -p patternproj/src
	cd patternproj
	cat > Makefile.km <<'KM'
sources = [./src/a.c, ./src/b.c]
objects = (subst ./src/{name:*}.c ./build/{name}.o sources)
rule ./build/marker : ${objects}
	cat $...inputs > $outputs.0
rule ./build/{name}.o : ./src/{name}.c
	cp $inputs.0 $outputs.0
KM
	echo a > src/a.c
	echo b > src/b.c
	cli_run -- do plan ./build/marker
	cli_expect_status 0
	cli_expect_stdout_contains './build/a.o'
	cli_expect_stdout_contains './build/b.o'
)

test-end
