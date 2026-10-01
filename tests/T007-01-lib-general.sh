#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — General Operations
# Spec: docs/spec/013-tests.md — T007-01-lib-general
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T007-01 library general operations"

test-step "toolchain and binary"
cli_require_tools
cli_build

evaluates() { # EXPR EXPECTED
	cli_run -- do expr -c "$1"
	cli_expect_status 0 "$1"
	cli_expect_stdout "$2" "$1"
	cli_expect_stderr_empty
}

test-step "not and bool use language truth rules"
evaluates '(not :true)' ':false'
evaluates '(not :false)' ':true'
evaluates '(not :nil)' ':true'
evaluates '(not 0)' ':false'
evaluates '(not "")' ':false'
evaluates '(bool :nil)' ':false'
evaluates '(bool "x")' ':true'
evaluates '(bool 0)' ':true'

test-step "str encodes scalars and containers canonically"
evaluates '(str 42)' '"42"'
evaluates '(str 1.5)' '"1.5"'
evaluates '(str :true)' '"true"'
evaluates '(str :nil)' '"nil"'
evaluates '(str "text")' '"text"'
evaluates '(str (list 1 "a"))' '"[1,\"a\"]"'
evaluates '(str [b: 2 a: "x"])' '"{\"a\":\"x\",\"b\":2}"'

test-step "count uses code points, item counts, and field counts"
evaluates '(count "héllo")' '5'
evaluates '(count (list 1 2 3))' '3'
evaluates '(count [a: 1 b: 2])' '2'
evaluates '(count "")' '0'

test-step "first and nth"
evaluates '(first (list "a" "b"))' '"a"'
evaluates '(first [])' ':nil'
evaluates '(nth (list "a" "b" "c") 1)' '"b"'
evaluates '(nth (list "a" "b" "c") -1)' '"c"'
evaluates '(nth "abc" 1)' '"b"'

test-step "list builds a list from its arguments"
evaluates '(list 1 "a" :true)' '[1 "a" :true]'
evaluates '(list)' '[]'

test-step "apply calls a function with a list of arguments"
evaluates '(apply ([x y] (join [x y] "-")) (list "a" "b"))' '"a-b"'
evaluates '(apply (list "a" "b") ([x y] (join [x y] "-")))' '"a-b"'

test-step "legacy core-make expressions evaluate against a fixture tree"
fixture_copy legacy-core legacy-core
(
	cd legacy-core
	cli_run -- default
	cli_expect_status 0
	cli_expect_file ./default "2
"
)

test-end
