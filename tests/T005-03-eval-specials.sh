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

test-step "let bindings"
cli_run -- do run --lang kash -c $'let name = "app"\nlet path = ./src\n(out! (str name " " path))'
cli_expect_status 0
cli_expect_stdout 'app ./src'
cli_expect_stderr_empty
cli_run -- do run --lang kash -c $'let x = 1\nlet y = 2\n(out! (str x "-" y))'
cli_expect_status 0
cli_expect_stdout '1-2'
cli_expect_stderr_empty

test-step "def bindings"
cli_run -- do run --lang kash -c $'def greeting = "hello"\n(out! greeting)'
cli_expect_status 0
cli_expect_stdout 'hello'
cli_expect_stderr_empty
diag 'eval "def name = 1"' 'PARSE_ERR'

test-step "eval evaluates expression text"
evaluates 'eval "join \"-\" [\"a\", \"b\"]"' '"a-b"'
evaluates 'eval "true"' 'true'
diag '(eval 1)' 'EXPR_INVALID'

test-step "optional fallback"
evaluates 'missing-name ?? "fallback"' '"fallback"'
evaluates '(x => x ?? "fallback") 1' '1'

test-step "pipes"
evaluates '1 |> str' '"1"'
evaluates '"a-b" |> subst "-" "+"' '"a+b"'
evaluates '"x" |> uppercase' '"X"'
evaluates '1 |> str |> uppercase' '"1"'
evaluates '"a" |> uppercase |> str _ "!"' '"A!"'
evaluates '(add-mark => "a" |> uppercase |> add-mark) (v => str v "!")' '"A!"'

test-end
