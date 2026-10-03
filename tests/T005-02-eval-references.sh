#!/usr/bin/env bash
# Spec: docs/spec/005-evaluation.md — References and Selectors
# Spec: docs/spec/004-language.md — References
# Spec: docs/spec/013-tests.md — T005-02-eval-references
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T005-02 evaluation references"

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

test-step "field and index references"
evaluates '(let [r [a: 1 b: 2]] r.a)' '1'
evaluates '(let [r [a: 1 b: 2]] r.b)' '2'
evaluates '(let [l [10 20 30]] l.0)' '10'
evaluates '(let [l [10 20 30]] l.2)' '30'
evaluates '(let [l [10 20 30]] l.-1)' '30'

test-step "slice references"
evaluates '(let [l [1 2 3]] l.1..3)' '[2 3]'
evaluates '(let [s "abc"] s.1..3)' '"bc"'
evaluates '(let [l [1 2 3]] l.0..0)' '[]'

test-step "selection references"
evaluates '(let [r [host: "x" port: 80]] r.{host,port})' '[host: "x" port: 80]'
evaluates '(let [r [host: "x" port: 80]] r.{port})' '[port: 80]'

test-step "reference diagnostics"
diag '(let [l [1 2 3]] l.nope)' 'REF_MISSING'
diag '(let [r [a: 1]] r.b)' 'REF_MISSING'
diag '(let [l [1 2 3]] l.9)' 'SEL_INDEX_INVALID'
diag 'unknown-symbol' 'REF_MISSING'
diag '(@<)' 'SEL_NO_CONTEXT'
evaluates '(join @* "-")' '""'

test-end
