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

test-step "discovery order: Kamefile, then Makefile.km, then make.km, then src/km/main.km"
fixture_copy discovery order
(
	cd order
	printf 'task where\n\t(out! "Kamefile\\n")\ntask default : where\n' > Kamefile
	cli_run -- where
	cli_expect_status 0
	cli_expect_stdout $'Kamefile\n'
	cli_run --
	cli_expect_status 0
	cli_expect_stdout $'Kamefile\n'
	cli_run -- -f Kamefile where
	cli_expect_status 0
	cli_expect_stdout $'Kamefile\n'
	cli_run -- ./Kamefile where
	cli_expect_status 0
	cli_expect_stdout $'Kamefile\n'
	rm Kamefile
	cli_run -- where
	cli_expect_status 0
	cli_expect_stdout "Makefile.km
"

	rm -f Makefile.km
	cli_run -- where
	cli_expect_status 0
	cli_expect_stdout "make.km
"

	rm -f make.km
	cli_run -- where
	cli_expect_status 0
	cli_expect_stdout "src/km/main.km
"

	rm -rf src
	cli_run -- where
	cli_expect_status 1
	cli_expect_stderr_contains "BUILD_NO_SOURCE" "Kamefile" "Makefile.km" "make.km" "src/km/main.km"
)

test-step "explicit file selection and --file= form"
fixture_copy discovery explicit
(
	cd explicit
	cli_run -- -f make.km where
	cli_expect_status 0
	cli_expect_stdout "make.km
"

	cli_run -- --file=Makefile.km where
	cli_expect_status 0
	cli_expect_stdout "Makefile.km
"

	cli_run -- -f ./src/km/main.km where
	cli_expect_status 0
	cli_expect_stdout "src/km/main.km
"
)

test-step "inline source with -c and --command="
INLINE=$'task hi\n\t(out! "inline\\n")'
cli_run -- -l km -c "$INLINE" hi
cli_expect_status 0
cli_expect_stdout "inline
"

test-step "file includes merge rules and definitions in source order"
mkdir -p includes
cat > includes/child.km <<'EOF'
name = "included"
task child
	(out! "child\n")
EOF
cat > includes/Makefile.km <<'EOF'
include ./child.km
task default : child
	(out! "${name}\n")
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
cat > include-cycle/first.km <<'EOF'
include ./second.km
EOF
cat > include-cycle/second.km <<'EOF'
include ./first.km
EOF
(
	cd include-cycle
	cli_run -- -f first.km
	cli_expect_status 1
	cli_expect_stderr_contains "DEP_CYCLE"
)

cli_run -- -l km "--command=$INLINE" hi
cli_expect_status 0
cli_expect_stdout "inline
"

test-step "--file and --command compose in input order"
cli_run -- -f explicit/make.km where -c '(out! "after")'
cli_expect_status 0
cli_expect_stdout $'make.km\nafter'

cli_run -- --allow-run -c 'name = "shared"' --file=explicit/make.km where -c 'name'
cli_expect_status 0
cli_expect_stdout $'make.km\n'

test-step "default grants follow the first fragment"
cli_run -- -f explicit/make.km where
cli_expect_status 0
cli_expect_stdout $'make.km\n'
cli_run -- -c '(nop nil)' -f explicit/make.km where
cli_expect_status 1
cli_expect_stderr_contains CAP_DENIED
cli_run -- --allow-read=. -f explicit/make.km where
cli_expect_status 1
cli_expect_stderr_contains CAP_DENIED

test-step "-C discovers beneath the given directory"
mkdir -p elsewhere
cli_run --dir elsewhere -- -C ../explicit where
cli_expect_status 0
cli_expect_stdout "Makefile.km
"

test-step "--directory= form"
cli_run -- "--directory=explicit" where
cli_expect_status 0
cli_expect_stdout "Makefile.km
"

test-step "relative explicit sources resolve beneath -C in either option order"
cli_run -- -C explicit -f make.km where
cli_expect_status 0
cli_expect_stdout $'make.km\n'
cli_run -- -f make.km -C explicit where
cli_expect_status 0
cli_expect_stdout $'make.km\n'
cli_run -- -C explicit -f "$(pwd)/explicit/make.km" where
cli_expect_status 0
cli_expect_stdout $'make.km\n'
cli_run -- do plan -C explicit -f make.km where
cli_expect_status 0
cli_run -- -C includes -f Makefile.km default
cli_expect_status 0
cli_expect_stdout $'child\nincluded\n'

test-step "positional .km operands select explicit sources"
cli_run --dir explicit -- make.km where
cli_expect_status 0
cli_expect_stdout $'make.km\n'

test-step "-- ends option parsing; later tokens are program arguments"
cli_run -- -l km -c "$INLINE" hi -- literal
cli_expect_status 0
cli_expect_stdout "inline
"

cli_run --dir explicit -- -f make.km where -- --force
cli_expect_status 0
cli_expect_stdout $'make.km\n'

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
