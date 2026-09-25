#!/usr/bin/env bash
# Spec: docs/spec/008-cache.md — Fingerprint, Invalidations
# Spec: docs/spec/013-tests.md — T008-03-cache-fingerprint
set -euo pipefail

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-testing.sh"
# shellcheck disable=SC1091
source "$(dirname "$BASE")/tests/lib-cli.sh"

test-start "T008-03 cache fingerprint invalidations"

test-step "toolchain and binary"
cli_require_tools
cli_build

mkdir -p fingerprint/globs

test-step "glob membership invalidates the record"
(
	cd fingerprint
	printf 'task g :\n\techo run >> runs.log\n\t@(out (str (count (wildcard ./globs/*))))\n' >G.lmk
	cli_run -- -f G.lmk g
	cli_expect_status 0
	cli_expect_stdout "0"
	cli_run -- -f G.lmk g
	cli_expect_status 0
	if [ "$(run_count ./runs.log)" = 1 ]; then
		test-ok "second g run was a cache hit"
	else
		test-fail "second g run re-executed: $(run_count ./runs.log)"
	fi
	touch ./globs/new.txt
	cli_run -- -f G.lmk g
	cli_expect_status 0
	cli_expect_stdout "1"
	if [ "$(run_count ./runs.log)" = 2 ]; then
		test-ok "glob membership invalidated the record"
	else
		test-fail "glob change runs: $(run_count ./runs.log)"
	fi
)

test-step "a bare dependency blocks cache commits"
(
	cd fingerprint
	printf 'task outer : bare\n\techo outer >> outer.log\n\t@(out "outer\\n")\nbare :\n\ttrue\n' >B.lmk
	cli_run -- -f B.lmk outer
	cli_expect_status 0
	cli_run -- -f B.lmk outer
	cli_expect_status 0
	if [ "$(run_count ./outer.log)" = 2 ]; then
		test-ok "cached task with a bare dependency re-ran"
	else
		test-fail "outer runs: $(run_count ./outer.log)"
	fi
)

test-step "rule body changes invalidate the record"
(
	cd fingerprint
	printf 'task body :\n\techo run >> body.log\n\t@(out "one\\n")\n' >C.lmk
	cli_run -- -f C.lmk body
	cli_expect_status 0
	cli_run -- -f C.lmk body
	cli_expect_status 0
	if [ "$(run_count ./body.log)" = 1 ]; then
		test-ok "unchanged body was a cache hit"
	else
		test-fail "unchanged body runs: $(run_count ./body.log)"
	fi
	printf 'task body :\n\techo run >> body.log\n\t@(out "two")\n' >C.lmk
	cli_run -- -f C.lmk body
	cli_expect_status 0
	cli_expect_stdout "two"
	if [ "$(run_count ./body.log)" = 2 ]; then
		test-ok "body change invalidated the record"
	else
		test-fail "body change runs: $(run_count ./body.log)"
	fi
)

test-step "template-named tasks are distinct instances"
(
	cd fingerprint
	printf 'task run-{name} :\n\techo @> >> names.log\n\t@(out "ran\\n")\n' >T.lmk
	cli_run -- -f T.lmk run-alpha
	cli_expect_status 0
	cli_run -- -f T.lmk run-alpha
	cli_expect_status 0
	cli_run -- -f T.lmk run-beta
	cli_expect_status 0
	cli_run -- -f T.lmk run-beta
	cli_expect_status 0
	if [ "$(run_count ./names.log)" = 2 ]; then
		test-ok "one execution per template instance"
	else
		test-fail "template instances ran $(run_count ./names.log) times"
	fi
)

test-step "retry policy participates in the fingerprint"
(
	cd fingerprint
	printf 'task retried :\n\techo run >> retry.log\n\t@(out "ok\\n")\n' >R.lmk
	cli_run -- -f R.lmk --retry 0 retried
	cli_expect_status 0
	cli_run -- -f R.lmk --retry 0 retried
	cli_expect_status 0
	if [ "$(run_count ./retry.log)" = 1 ]; then
		test-ok "unchanged retry policy was a cache hit"
	else
		test-fail "retry log after hit: $(run_count ./retry.log)"
	fi
	cli_run -- -f R.lmk --retry 1 retried
	cli_expect_status 0
	if [ "$(run_count ./retry.log)" = 2 ]; then
		test-ok "retry policy change invalidated the record"
	else
		test-fail "retry change runs: $(run_count ./retry.log)"
	fi
)

test-end
