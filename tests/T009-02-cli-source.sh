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

test-step "discovery order: Makefile.kmk, then make.kmk, then src/kmk/main.kmk"
fixture_copy discovery order
(
	cd order
	cli_run -- where
	cli_expect_status 0
	cli_expect_stdout "Makefile.kmk
"

	rm -f Makefile.kmk
	cli_run -- where
	cli_expect_status 0
	cli_expect_stdout "make.kmk
"

	rm -f make.kmk
	cli_run -- where
	cli_expect_status 0
	cli_expect_stdout "src/kmk/main.kmk
"

	rm -rf src
	cli_run -- where
	cli_expect_status 1
	cli_expect_stderr_contains "BUILD_NO_SOURCE" "Makefile.kmk" "make.kmk" "src/kmk/main.kmk"
)

test-step "explicit file selection and --file= form"
fixture_copy discovery explicit
(
	cd explicit
	cli_run -- -f make.kmk where
	cli_expect_status 0
	cli_expect_stdout "make.kmk
"

	cli_run -- --file=Makefile.kmk where
	cli_expect_status 0
	cli_expect_stdout "Makefile.kmk
"

	cli_run -- -f ./src/kmk/main.kmk where
	cli_expect_status 0
	cli_expect_stdout "src/kmk/main.kmk
"
)

test-step "inline source with -c and --command="
INLINE=$'task hi :\n\t@(out "inline\\n")'
cli_run -- -l kmk -c "$INLINE" hi
cli_expect_status 0
cli_expect_stdout "inline
"

test-step "file includes merge rules and definitions in source order"
mkdir -p includes
cat > includes/child.kmk <<'EOF'
name = "included"
task child :
	@(out "child\n")
EOF
cat > includes/Makefile.kmk <<'EOF'
include ./child.kmk
task default : child
	@(out "@(name)\n")
EOF
(
	cd includes
	cli_run -- default
	cli_expect_status 0
	cli_expect_stdout "child
included
"
)

test-step "include cycles fail before compilation"
mkdir -p include-cycle
cat > include-cycle/first.kmk <<'EOF'
include ./second.kmk
EOF
cat > include-cycle/second.kmk <<'EOF'
include ./first.kmk
EOF
(
	cd include-cycle
	cli_run -- -f first.kmk
	cli_expect_status 1
	cli_expect_stderr_contains "DEP_CYCLE"
)

cli_run -- -l kmk "--command=$INLINE" hi
cli_expect_status 0
cli_expect_stdout "inline
"

test-step "--file and --command compose in input order"
cli_run -- -f explicit/make.kmk where -c '(out "after")'
cli_expect_status 0
cli_expect_stdout $'make.kmk\nafter"after"'

cli_run -- --allow-run -c 'name = "shared"' --file=explicit/make.kmk where -c 'name'
cli_expect_status 0
cli_expect_stdout $'make.kmk\n"shared"'

test-step "-C discovers beneath the given directory"
mkdir -p elsewhere
cli_run --dir elsewhere -- -C ../explicit where
cli_expect_status 0
cli_expect_stdout "Makefile.kmk
"

test-step "--directory= form"
cli_run -- "--directory=explicit" where
cli_expect_status 0
cli_expect_stdout "Makefile.kmk
"

test-step "relative explicit sources resolve beneath -C in either option order"
cli_run -- -C explicit -f make.kmk where
cli_expect_status 0
cli_expect_stdout $'make.kmk\n'
cli_run -- -f make.kmk -C explicit where
cli_expect_status 0
cli_expect_stdout $'make.kmk\n'
cli_run -- -C explicit -f "$(pwd)/explicit/make.kmk" where
cli_expect_status 0
cli_expect_stdout $'make.kmk\n'
cli_run -- do plan -C explicit -f make.kmk where
cli_expect_status 0
cli_run -- -C includes -f Makefile.kmk default
cli_expect_status 0
cli_expect_stdout $'child\nincluded\n'

test-step "positional .kmk operands select explicit sources"
cli_run --dir explicit -- make.kmk where
cli_expect_status 0
cli_expect_stdout $'make.kmk\n'

test-step "-- ends option parsing; later tokens are program arguments"
cli_run -- -l kmk -c "$INLINE" hi -- literal
cli_expect_status 0
cli_expect_stdout "inline
"

cli_run --dir explicit -- -f make.kmk where -- --force
cli_expect_status 0
cli_expect_stdout $'make.kmk\n'

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
