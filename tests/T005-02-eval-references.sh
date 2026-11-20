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
evaluates '(r => r.a) {a: 1, b: 2}' '1'
evaluates '(r => r.b) {a: 1, b: 2}' '2'
evaluates '(l => l.0) [10, 20, 30]' '10'
evaluates '(l => l.2) [10, 20, 30]' '30'
evaluates '(l => l.-1) [10, 20, 30]' '30'

test-step "slices use the slice operation"
evaluates 'slice 1 3 [1, 2, 3]' '[2, 3]'
evaluates 'slice 1 3 "abc"' '"bc"'
evaluates 'slice 0 0 [1, 2, 3]' '[]'
diag '(l => l.1..3) [1, 2, 3]' 'PARSE_ERR'

test-step "selected fields can form a record"
evaluates '(r => {host: r.host, port: r.port}) {host: "x", port: 80}' '{host: "x", port: 80}'
evaluates '(r => {port: r."port number"}) {"port number": 80}' '{port: 80}'
diag '(r => r.{port}) {host: "x", port: 80}' 'PARSE_ERR'

test-step "reference diagnostics"
diag '(l => l.nope) [1, 2, 3]' 'REF_MISSING'
diag '(r => r.b) {a: 1}' 'REF_MISSING'
diag '(l => l.9) [1, 2, 3]' 'SEL_INDEX_INVALID'
diag 'unknown-symbol' 'REF_MISSING'
diag 'captures' 'SEL_NO_CONTEXT'
evaluates 'join "-" args' '""'

test-end
