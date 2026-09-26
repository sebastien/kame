#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Invocation, Source Discovery
# Spec: docs/spec/013-tests.md — T009-02-cli-source
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T009-02 CLI source discovery and options"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "discovery order: Makefile.lmk, then make.lmk, then src/lmk/main.lmk"
fixture_copy discovery order
(
	cd order
	cli_run -- where
	cli_expect_status 0
	cli_expect_stdout "Makefile.lmk
"

	rm -f Makefile.lmk
	cli_run -- where
	cli_expect_status 0
	cli_expect_stdout "make.lmk
"

	rm -f make.lmk
	cli_run -- where
	cli_expect_status 0
	cli_expect_stdout "src/lmk/main.lmk
"

	rm -rf src
	cli_run -- where
	cli_expect_status 1
	cli_expect_stderr_contains "BUILD_NO_SOURCE" "Makefile.lmk" "make.lmk" "src/lmk/main.lmk"
)

test-step "explicit file selection and --file= form"
fixture_copy discovery explicit
(
	cd explicit
	cli_run -- -f make.lmk where
	cli_expect_status 0
	cli_expect_stdout "make.lmk
"

	cli_run -- --file=Makefile.lmk where
	cli_expect_status 0
	cli_expect_stdout "Makefile.lmk
"

	cli_run -- -f ./src/lmk/main.lmk where
	cli_expect_status 0
	cli_expect_stdout "src/lmk/main.lmk
"
)

test-step "inline source with -c and --command="
INLINE=$'task hi :\n\t@(out "inline\\n")'
cli_run -- -c "$INLINE" hi
cli_expect_status 0
cli_expect_stdout "inline
"

cli_run -- "--command=$INLINE" hi
cli_expect_status 0
cli_expect_stdout "inline
"

test-step "--file and --command are mutually exclusive"
cli_run -- -f Makefile.lmk -c "task x :"
cli_expect_status 2
cli_expect_stderr_contains "OPT_CONFLICT"

cli_run -- --command=x --file=Makefile.lmk
cli_expect_status 2
cli_expect_stderr_contains "OPT_CONFLICT"

test-step "-C discovers beneath the given directory"
mkdir -p elsewhere
cli_run --dir elsewhere -- -C ../explicit where
cli_expect_status 0
cli_expect_stdout "Makefile.lmk
"

test-step "--directory= form"
cli_run -- "--directory=explicit" where
cli_expect_status 0
cli_expect_stdout "Makefile.lmk
"

test-step "positional .lmk values are target names, not sources"
cli_run --dir explicit -- make.lmk
cli_expect_status 1
cli_expect_stderr_contains "TGT_NO_RULE"

test-step "-- ends option parsing; later tokens are targets"
cli_run -- -c "$INLINE" -- hi
cli_expect_status 0
cli_expect_stdout "inline
"

cli_run --dir explicit -- -f make.lmk where -- --force
cli_expect_status 1
cli_expect_stderr_contains "TGT_NO_RULE"

test-step "option validation for source-related options"
cli_run -- -f
cli_expect_status 2
cli_expect_stderr_contains "OPT_NO_VALUE"

cli_run -- --file=
cli_expect_status 2
cli_expect_stderr_contains "OPT_VALUE_INVALID"

cli_run -- -x
cli_expect_status 2
cli_expect_stderr_contains "OPT_UNKNOWN"

test-end
