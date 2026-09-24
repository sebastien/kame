#!/usr/bin/env bash
# Spec: docs/spec/007-library.md — Path Operations
# Spec: docs/spec/013-tests.md — T007-04-lib-path
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T007-04 library path operations"

test-step "toolchain and binary"
cli_require_tools
cli_build

evaluates() { # EXPR EXPECTED
	cli_run -- do expr -c "$1"
	cli_expect_status 0 "$1"
	cli_expect_stdout "$2" "$1"
	cli_expect_stderr_empty
}

test-step "basename and dirname"
evaluates '(basename "a/b/c.txt")' 'c.txt'
evaluates '(dirname "a/b/c.txt")' 'a/b'
evaluates '(basename "c.txt")' 'c.txt'
evaluates '(dirname "c.txt")' '.'

test-step "extension helpers"
evaluates '(ext "a/b/c.txt")' '.txt'
evaluates '(ext "a/b/c")' ''
evaluates '(splitext "a/b/c.txt")' '[a/b/c .txt]'
evaluates '(splitext "a.tar.gz")' '[a.tar .gz]'
evaluates '(splitext "archive")' '[archive ]'
evaluates '(splitext ".hidden")' '[.hidden ]'

test-step "joinpath normalizes components"
evaluates '(joinpath "a" "b")' 'a/b'
evaluates '(joinpath "a" "b" ".." "c")' 'a/c'
evaluates '(joinpath "a" "")' 'a'

test-step "relpath and abspath are lexical"
evaluates '(relpath "a/b/c" "a")' 'b/c'
evaluates '(relpath "a" "a")' '.'
cli_run -- do expr -c '(abspath "x")'
cli_expect_status 0
cli_expect_stdout "$PWD/x"

test-step "path operations do not touch the filesystem"
cli_run -- do expr -c '(joinpath "no-such-dir" "file.txt")'
cli_expect_status 0
cli_expect_stdout 'no-such-dir/file.txt'

test-end
