#!/usr/bin/env bash
# Spec: docs/spec/006-runtime.md — Scheduling, Rule Instances, Failure
# Spec: docs/spec/013-tests.md — T006-02-runtime-deps
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T006-02 runtime dependency scheduling"

test-step "toolchain and binary"
cli_require_tools
cli_build

test-step "a diamond executes its shared node once per root"
mkdir -p deps
(
	cd deps
	printf './shared :\n\techo run >> runs.log\n\ttouch @>\n./a : ./shared\n\ttouch @>\n./b : ./shared\n\ttouch @>\n./top : ./a ./b\n\ttouch @>\n' >Makefile.lmk
	cli_run -- ./top
	cli_expect_status 0
	cli_expect_file ./top
	if [ "$(run_count ./runs.log)" = 1 ]; then
		test-ok "shared prerequisite executed once"
	else
		test-fail "shared runs: $(run_count ./runs.log)"
	fi
)

test-step "a failed dependency blocks its dependent's recipe"
fixture_copy errors deps-fail
(
	cd deps-fail
	cli_run -- ./direct.out
	cli_expect_status 1
	cli_expect_stderr_contains '[./dep-fails.out] failed'
	if grep -q 'dependent' "$CLI_OUT" "$CLI_ERR"; then
		test-fail "dependent recipe ran after its dependency failed"
	else
		test-ok "dependent recipe did not run"
	fi
	cli_expect_no_file ./direct.out
)

test-step "a missing declared input fails before execution"
(
	cd deps
	printf './missing.out : ./no-such-input.c\n\tcp @< @>\n' >Missing.lmk
	cli_run -- -f Missing.lmk ./missing.out
	cli_expect_status 1
	cli_expect_stderr_contains 'required input does not exist' 'no-such-input.c'
	cli_expect_no_file ./missing.out
)

test-step "independent roots continue when another root fails"
(
	cd deps
	printf './bad.out :\n\texit 4\n./good.out :\n\techo good > @>\n' >Mixed.lmk
	cli_run -- -f Mixed.lmk ./bad.out ./good.out
	cli_expect_status 1
	cli_expect_file ./good.out "good
"
	cli_expect_no_file ./bad.out
)

test-step "sibling outputs of one declaration share one execution"
(
	cd deps
	printf './a.o ./b.o :\n\techo run >> sib.log\n\ttouch @>*\n' >Siblings.lmk
	cli_run -- -f Siblings.lmk ./a.o ./b.o
	cli_expect_status 0
	cli_expect_file ./a.o
	cli_expect_file ./b.o
	if [ "$(run_count ./sib.log)" = 1 ]; then
		test-ok "sibling outputs shared one execution"
	else
		test-fail "sibling executions: $(run_count ./sib.log)"
	fi
)

test-step "output parent directories are created before execution"
(
	cd deps
	printf './nested/deep/out.txt :\n\tprintf built > @>\n' >Nested.lmk
	cli_run -- -f Nested.lmk ./nested/deep/out.txt
	cli_expect_status 0
	cli_expect_file ./nested/deep/out.txt 'built'
)

test-end
