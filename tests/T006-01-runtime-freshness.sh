#!/usr/bin/env bash
# Spec: docs/spec/006-runtime.md — File Rules, Rendering
# Spec: docs/spec/013-tests.md — T006-01-runtime-freshness
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T006-01 runtime freshness"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy fresh freshness

test-step "missing and fresh outputs"
(
	cd freshness
	cli_run -- ./out.txt
	cli_expect_status 0
	cli_expect_file ./out.txt "initial
"
	if [ "$(run_count ./runs.log)" = 1 ]; then
		test-ok "stale output was built once"
	else
		test-fail "runs after first build: $(run_count ./runs.log)"
	fi

	cli_run -- ./out.txt
	cli_expect_status 0
	if [ "$(run_count ./runs.log)" = 1 ]; then
		test-ok "fresh output was skipped"
	else
		test-fail "fresh output rebuilt: $(run_count ./runs.log)"
	fi
)

test-step "output metadata changes preserve accepted content"
(
	cd freshness
	set_mtime ./out.txt 1000000000
	cli_run -- ./out.txt
	cli_expect_status 0
	if [ "$(run_count ./runs.log)" = 1 ]; then
		test-ok "older output with identical bytes was reused"
	else
		test-fail "older output runs: $(run_count ./runs.log)"
	fi
)

test-step "changed input bytes rebuild"
(
	cd freshness
	cli_run -- ./out.txt
	cli_expect_status 0
	printf 'changed\n' >in.txt
	set_mtime ./in.txt 2000000000
	cli_run -- ./out.txt
	cli_expect_status 0
	if [ "$(run_count ./runs.log)" = 2 ]; then
		test-ok "newer input rebuilt"
	else
		test-fail "newer input runs: $(run_count ./runs.log)"
	fi
)

test-step "--force rebuilds a fresh output"
(
	cd freshness
	cli_run -- --force ./out.txt
	cli_expect_status 0
	if [ "$(run_count ./runs.log)" = 3 ]; then
		test-ok "force rebuilt the fresh output"
	else
		test-fail "force runs: $(run_count ./runs.log)"
	fi
)

test-step "a missing output rebuilds"
(
	cd freshness
	rm -f ./out.txt
	cli_run -- ./out.txt
	cli_expect_status 0
	if [ "$(run_count ./runs.log)" = 4 ]; then
		test-ok "missing output rebuilt"
	else
		test-fail "missing output runs: $(run_count ./runs.log)"
	fi
)

test-step "legacy-equivalent file build skips its fresh output"
fixture_copy legacy-runtime legacy-runtime
(
	cd legacy-runtime
	cli_run --
	cli_expect_status 0
	cli_expect_file ./dist/kame "legacy executable payload
"
	cli_run --
	cli_expect_status 0
	if [ "$(run_count ./runs.log)" = 1 ]; then
		test-ok "legacy-equivalent fresh target was skipped"
	else
		test-fail "legacy-equivalent target ran $(run_count ./runs.log) times"
	fi
)

test-step "dynamic body dependencies re-render before freshness"
(
	mkdir -p dynamic/src
	cd dynamic
	printf 'task default : ./list.out\n./list.out :\n\tprintf "%%s\\n" @(wildcard ./src/*) > @>\n' >Makefile.kmk
	cli_run -- ./list.out
	cli_expect_status 0
	cli_expect_file ./list.out
	touch ./src/new.c
	cli_run -- ./list.out
	cli_expect_status 0
	if grep -q './src/new.c' ./list.out; then
		test-ok "dynamic dependency invalidated the output"
	else
		test-fail "dynamic dependency did not invalidate: $(cat ./list.out)"
	fi
)

test-end
