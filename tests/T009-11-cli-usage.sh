#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Invocation, Signals and Exit Status
# Spec: docs/spec/013-tests.md — T009-11-cli-usage
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T009-11 CLI usage and exit status matrix"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "missing option values are OPT_NO_VALUE with status 2"
for option in -f --file -c --command -C --directory -j --jobs --shell --timeout --retry --log-limit --env; do
	cli_run -- "$option"
	cli_expect_status 2 "$option without value"
	cli_expect_stderr_contains "OPT_NO_VALUE"
done

test-step "invalid option values are OPT_VALUE_INVALID with status 2"
cli_run -- -j 0
cli_expect_status 2
cli_expect_stderr_contains "OPT_VALUE_INVALID" "jobs must be a positive integer"

cli_run -- -j abc
cli_expect_status 2
cli_expect_stderr_contains "OPT_VALUE_INVALID"

cli_run -- --jobs=-1
cli_expect_status 2
cli_expect_stderr_contains "OPT_VALUE_INVALID"

cli_run -- --timeout -1
cli_expect_status 2
cli_expect_stderr_contains "OPT_VALUE_INVALID"

cli_run -- --retry -1
cli_expect_status 2
cli_expect_stderr_contains "OPT_VALUE_INVALID"

cli_run -- --log-limit 0
cli_expect_status 2
cli_expect_stderr_contains "OPT_VALUE_INVALID"

cli_run -- --env invalid
cli_expect_status 2
cli_expect_stderr_contains "OPT_VALUE_INVALID" "NAME=VALUE"

cli_run -- --timeout nope
cli_expect_status 2
cli_expect_stderr_contains "OPT_VALUE_INVALID"

test-step "unknown options are OPT_UNKNOWN with status 2"
cli_run -- --definitely-not-an-option
cli_expect_status 2
cli_expect_stderr_contains "OPT_UNKNOWN" "--definitely-not-an-option"

test-step "usage errors do not require a build source"
cli_run -- -j 0
cli_expect_status 2
cli_expect_stderr_contains "OPT_VALUE_INVALID"

test-step "targets after -- are not options"
cli_run -- -c "task default :" -- --force
cli_expect_status 1
cli_expect_stderr_contains "TGT_NO_RULE"

test-step "success is 0, build failure is 1, usage error is 2"
cli_run -- --version
cli_expect_status 0
cli_run -- -c "task default :" -j 0
cli_expect_status 2
fixture_copy errors errors
(
	cd errors
	cli_run -- ./failing.out
	cli_expect_status 1
	cli_expect_stderr_contains "RECIPE_FAIL"
)

test-step "-j 1 is deterministic across runs"
fixture_copy project project
(
	cd project
	cli_run -- -j 1 ./build/main.o ./build/util.o
	cli_expect_status 0
	first_out="$(cat "$CLI_OUT")"
	first_err="$(sed 's/[0-9]\+/N/g' "$CLI_ERR")"
	cli_run -- -j 1 --force ./build/main.o ./build/util.o
	cli_expect_status 0
	second_out="$(cat "$CLI_OUT")"
	second_err="$(sed 's/[0-9]\+/N/g' "$CLI_ERR")"
	if [ "$first_out" = "$second_out" ] && [ "$first_err" = "$second_err" ]; then
		test-ok "-j 1 output is deterministic"
	else
		test-fail "-j 1 output differs between runs"
	fi
)

test-step "-j 2 executes independent nodes"
(
	cd project
	rm -rf build runs.log
	cli_run -- -j 2 ./build/main.o ./build/util.o
	cli_expect_status 0
	cli_expect_file ./build/main.o
	cli_expect_file ./build/util.o
	cli_expect_stdout_empty
)

test-step "parallel roots share dependencies"
(
	cd project
	rm -rf build runs.log
	cli_run -- -j 2 ./build/main.o ./build/app
	cli_expect_status 0
	if [ "$(grep -c -F 'compile-./build/main.o' ./runs.log)" = 1 ]; then
		test-ok "shared prerequisite compiled once with -j 2"
	else
		test-fail "shared prerequisite compiled $(grep -c -F 'compile-./build/main.o' ./runs.log) times"
	fi
)

test-step "-j 2 keeps concurrent recipe output line-integral"
(
	mkdir -p interleaving
	cd interleaving
	cat >Makefile.lmk <<'EOF'
first:
	for n in 1 2 3; do printf 'first-%s\n' "$n"; sleep 0.01; done
second:
	for n in 1 2 3; do printf 'second-%s\n' "$n"; sleep 0.01; done
default : first second
EOF
	cli_run -- -j 2 default
	cli_expect_status 0
	if [ "$(grep -Ec '^(first|second)-[123]$' "$CLI_OUT")" = 6 ] && ! grep -Ev '^(first|second)-[123]$' "$CLI_OUT" >/dev/null; then
		test-ok "concurrent recipe output has six intact tagged lines"
	else
		test-fail "concurrent recipe output was torn or incomplete: $(tr '\n' ' ' <"$CLI_OUT")"
	fi
)

test-end
