#!/usr/bin/env bash
# Spec: docs/spec/005-evaluation.md — Values
# Spec: docs/spec/004-language.md — Common Atoms, Strings
# Spec: docs/spec/013-tests.md — T005-01-eval-values
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T005-01 evaluation values"

test-step "toolchain and binary"
cli_require_tools
cli_build

# Function: evaluates EXPR to EXPECTED
evaluates() { # EXPR EXPECTED
	local expr="$1"
	local expected="$2"
	cli_run -- do expr -c "$expr"
	cli_expect_status 0 "$expr"
	cli_expect_stdout "$expected" "$expr"
	cli_expect_stderr_empty
}

test-step "atomic literal output"
evaluates ':nil' 'nil'
evaluates ':true' 'true'
evaluates ':false' 'false'
evaluates '42' '42'
evaluates '-7' '-7'
evaluates '0x10' '16'
evaluates '0o17' '15'
evaluates '0b101' '5'
evaluates '1_000' '1000'
evaluates '1.5' '1.5'
evaluates '1e3' '1000'
evaluates '"hello"' 'hello'

test-step "escape sequences"
evaluates '"\n"' '
'
evaluates '"\t"' '	'
evaluates '"a\"b"' 'a"b'
evaluates '"back\\slash"' 'back\slash'

test-step "string interpolation"
evaluates '"value={(join ["x" "y"] "")}"' 'value=xy'
evaluates '"value={(nth [10 20] 1)}"' 'value=20'
evaluates '"outer {(join ["x" "y"] "")} end"' 'outer xy end'

test-step "lists and records"
evaluates '(list 1 "a" :true)' '[1 a true]'
evaluates '(list)' '[]'
evaluates '(str [b: 2 a: "x"])' '{"a":"x","b":2}'
evaluates '(str [a: 1])' '{"a":1}'

test-step "list rendering joins items with spaces"
evaluates '(list "a" "b" "c")' '[a b c]'
evaluates '(list (list 1 2) (list 3))' '[[1 2] [3]]'

test-end
