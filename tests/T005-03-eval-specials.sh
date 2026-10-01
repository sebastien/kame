#!/usr/bin/env bash
# Spec: docs/spec/005-evaluation.md — Special Forms, Pipes
# Spec: docs/spec/004-language.md — Pipes, Special Forms
# Spec: docs/spec/013-tests.md — T005-03-eval-specials
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T005-03 evaluation special forms and pipes"

test-step "toolchain and binary"
cli_require_tools
cli_build

evaluates() { # EXPR EXPECTED
	cli_run -- do expr -c "$1"
	cli_expect_status 0 "$1"
	cli_expect_stdout "$2" "$1"
	cli_expect_stderr_empty
}

diag() { # EXPR CODE
	cli_run -- do expr -c "$1"
	cli_expect_status 1 "$1"
	cli_expect_stderr_contains "$2"
	cli_expect_printable "$CLI_ERR"
}

test-step "let bindings"
evaluates '(let [name "app" path ./src] (join [name path] " "))' '"app ./src"'
evaluates '(let [x 1 y 2] (join [(str x) (str y)] "-"))' '"1-2"'
diag '(let)' 'EXPR_INVALID'
diag '(let [x] x)' 'EXPR_INVALID'

test-step "def bindings"
evaluates '(def greeting "hello")' '"hello"'
diag '(def)' 'DEF_INVALID'
diag '(def name)' 'DEF_INVALID'

test-step "eval evaluates expression text"
evaluates '(eval "(join [\"a\" \"b\"] \"-\")")' '"a-b"'
evaluates '(eval ":true")' ':true'
diag '(eval 1)' 'EXPR_INVALID'

test-step "optional fallback"
evaluates '(? missing-name "fallback")' '"fallback"'
evaluates '(let [x 1] (? x "fallback"))' '1'

test-step "pipes"
evaluates '(1 | str)' '"1"'
evaluates '("a-b" | replace _ "-" "+")' '"a+b"'
evaluates '("x" | uppercase)' '"X"'
evaluates '(1 | str | uppercase)' '"1"'
evaluates '(("a" | uppercase) | ([v] (join [v "!"] "")))' '"A!"'

test-end
