#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — Collection Operations
# Spec: docs/spec/013-tests.md — T007-02-lib-collections
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T007-02 library collection operations"

test-step "toolchain and binary"
cli_require_tools
cli_build

evaluates() { # EXPR EXPECTED
	cli_run -- do run --lang expr -c "$1"
	cli_expect_status 0 "$1"
	cli_expect_stdout "$2" "$1"
	cli_expect_stderr_empty
}

diag() { # EXPR CODE
	cli_run -- do run --lang expr -c "$1"
	cli_expect_status 1 "$1"
	cli_expect_stderr_contains "$2"
	cli_expect_printable "$CLI_ERR"
}

test-step "map preserves input order"
evaluates '(map ([x] (str x)) (list 1 2 3))' '["1" "2" "3"]'
evaluates '(map ([x] (str x)) (list "b" "a"))' '["b" "a"]'
evaluates '(map ([x] (str x)) (list))' '[]'

test-step "map passes value then key when the callback declares it"
evaluates '(map ([value key] (list value key)) (list "a" "b"))' '[["a" 0] ["b" 1]]'
evaluates '(map ([value key extra] (list value key extra)) (list "a"))' '[["a" 0 :nil]]'
evaluates '(flatmap ([value key] (list value key)) (list "a"))' '["a" 0]'
evaluates '(filter ([value key] (eq? key 1)) (list "a" "b"))' '["b"]'

test-step "map and filter keep records, passing field names as keys"
evaluates '(map ([value key] (str key "=" value)) [a: 1 b: 2])' '[a: "a=1" b: "b=2"]'
evaluates '(map ([value] (str value "!")) [a: 1 b: 2])' '[a: "1!" b: "2!"]'
evaluates '(filter ([value key] (eq? key "b")) [a: 1 b: 2 c: 3])' '[b: 2]'
evaluates '(filter-out 2 [a: 1 b: 2])' '[a: 1]'
diag '(flatmap ([value] [value]) [a: 1])' 'EXPR_INVALID'

test-step "reduce passes the position to a third parameter"
evaluates '(reduce ([acc item index] (concat acc [[item index]])) [] (list "a" "b"))' '[["a" 0] ["b" 1]]'
evaluates '(reduce ([acc item] (str acc item)) (list "a" "b"))' '"ab"'

test-step "flatmap flattens one level"
evaluates '(flatmap ([x] (list x x)) (list 1 2))' '[1 1 2 2]'
evaluates '(flatmap ([x] (list x)) (list 1 2))' '[1 2]'

test-step "filter and filter-out"
evaluates '(filter ([x] (includes? "a" x)) (list "a" "b" "aa"))' '["a" "aa"]'
evaluates '(filter-out ([x] (includes? "a" x)) (list "a" "b" "aa"))' '["b"]'
evaluates '(filter "a" (list "a" "b" "a"))' '["a" "a"]'
evaluates '(filter-out "a" (list "a" "b" "a"))' '["b"]'
evaluates '(filter 1.0 (list 1 "1" 1.0))' '[1 1]'
evaluates '(filter (list 1) (list 1))' '[]'

test-step "reduce folds from the left"
evaluates '(reduce ([a b] (join "" [b a])) "z" (list "a" "b"))' '"baz"'
evaluates '(reduce ([a b] (str a)) 1 (list 3))' '"1"'

test-step "concat appends lists and scalars"
evaluates '(concat (list 1 2) 3)' '[1 2 3]'
evaluates '(concat (list 1) (list 2 3))' '[1 2 3]'
evaluates '(concat "a" "b")' '["a" "b"]'

test-step "slice uses half-open bounds"
evaluates '(slice 1 3 (list 1 2 3 4))' '[2 3]'
evaluates '(slice 0 0 (list 1 2 3))' '[]'

test-step "sorted and unique"
evaluates '(sorted (list "b" "a" "c"))' '["a" "b" "c"]'
evaluates '(sorted (list 3 1 2))' '[1 2 3]'
evaluates '(unique (list "a" "b" "a"))' '["a" "b"]'
evaluates '(unique (list 1 1 2 1))' '[1 2]'

test-step "mixed-kind sorting is invalid"
diag '(sorted (list 1 "a"))' 'EXPR_INVALID'
diag '(unique (list 1 "a" 1))' 'EXPR_INVALID'

test-end
