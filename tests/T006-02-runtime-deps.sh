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
	printf 'rule ./shared\n\techo run >> runs.log\n\ttouch $outputs.0\nrule ./a : ./shared\n\ttouch $outputs.0\nrule ./b : ./shared\n\ttouch $outputs.0\nrule ./top : ./a ./b\n\ttouch $outputs.0\n' >Makefile.km
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
	cli_run -- --json ./direct.out
	cli_expect_status 1
	cli_expect_event "$CLI_OUT" target-failed '.target == "./dep-fails.out"'
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
	printf 'rule ./missing.out : ./no-such-input.c\n\tcp $inputs.0 $outputs.0\n' >Missing.km
	cli_run -- -f Missing.km ./missing.out
	cli_expect_status 1
	cli_expect_stderr_contains 'required input does not exist' 'no-such-input.c'
	cli_expect_no_file ./missing.out
)

test-step "independent roots continue when another root fails"
(
	mkdir mixed
	cd mixed
	printf 'rule ./bad.out\n\texit 4\nrule ./good.out\n\techo good > $outputs.0\n' >Makefile.km
	cli_run -- ./bad.out ./good.out
	cli_expect_status 1
	cli_expect_file ./good.out "good
"
	cli_expect_no_file ./bad.out
)

test-step "sibling outputs of one declaration share one execution"
(
	mkdir siblings
	cd siblings
	printf 'rule ./a.o ./b.o\n\techo run >> sib.log\n\ttouch $...outputs\n' >Makefile.km
	cli_run -- ./a.o ./b.o
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
	printf 'rule ./nested/deep/out.txt\n\tprintf built > $outputs.0\n' >Nested.km
	cli_run -- -f Nested.km ./nested/deep/out.txt
	cli_expect_status 0
	cli_expect_file ./nested/deep/out.txt 'built'
)

test-end
