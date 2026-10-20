#!/usr/bin/env bash
# Spec: docs/spec/017-kash.md — deferred cleanup deadlines and cancellation
set -euo pipefail
# shellcheck disable=SC1091,SC2016
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T017-24 Kash deferred cleanup lifecycle"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	test-step "$backend default cleanup deadline remains bounded"
	status=0
	timeout -k 2 15 "${runner[@]}" "do" run -C "$work" --allow-run --allow-read --allow-write -l kash -c 'defer sleep 30' >"$work/out" 2>"$work/err" || status=$?
	if [ "$status" = 1 ] && [ ! -s "$work/out" ] && grep -q RECIPE_TIMEOUT "$work/err"; then test-ok "$backend deferred deadline"; else
		test-fail "$backend deferred deadline status=$status"
		cat "$work/err"
	fi
	test-step "$backend cancellation reaps user work before command/expression finalizers"
	cat >"$work/worker" <<'SH'
#!/bin/sh
printf '%s' "$$" > worker.pid
exec sleep 30
SH
	cat >"$work/cleanup" <<'SH'
#!/bin/sh
if kill -0 "$(cat worker.pid)" 2>/dev/null; then exit 9; fi
printf reaped > reaped
SH
	chmod +x "$work/worker" "$work/cleanup"
	for scenario in signal timeout background combined; do
		rm -f "$work/worker.pid" "$work/reaped" "$work/expression-cleaned" "$work/finally-cleaned"
		text='defer (write! ./expression-cleaned "yes"); defer ./cleanup; ./worker'
		options=()
		if [ "$scenario" = timeout ]; then options=(--timeout 150); fi
		if [ "$scenario" = background ]; then text='defer (write! ./expression-cleaned "yes"); defer ./cleanup; ./worker &; sh -c "while test ! -s worker.pid; do sleep .01; done"; (fail! "stop")'; fi
		if [ "$scenario" = combined ]; then
			text='defer (write! ./expression-cleaned "yes"); defer ./cleanup; (finally! ([] (run! "./worker")) ([] (write! ./finally-cleaned "yes")))'
		fi
		if [ "$scenario" = signal ] || [ "$scenario" = combined ]; then
			"${runner[@]}" "do" run -C "$work" --allow-run --allow-write -l kash -c "$text" >"$work/out" 2>"$work/err" &
			watchdog=$!
			for _attempt in {1..500}; do
				[ -s "$work/worker.pid" ] && break
				sleep .01
			done
			# Signal the runner itself: GNU timeout can deliver TERM twice, which
			# intentionally requests forced shutdown instead of normal finalization.
			kill -TERM "$watchdog"
			for _attempt in {1..1000}; do
				kill -0 "$watchdog" 2>/dev/null || break
				sleep .01
			done
			if kill -0 "$watchdog" 2>/dev/null; then kill -KILL "$watchdog"; fi
		else
			timeout -k 2 15 "${runner[@]}" "do" run -C "$work" --allow-run --allow-write -l kash "${options[@]}" -c "$text" >"$work/out" 2>"$work/err" &
			watchdog=$!
		fi
		status=0
		wait "$watchdog" || status=$?
		if [ -e "$work/reaped" ] && [ -e "$work/expression-cleaned" ] && [ "$status" != 0 ] && [ "$status" != 137 ]; then test-ok "$backend $scenario reaps before finalization"; else
			test-fail "$backend $scenario cleanup (status $status)"
			cat "$work/err"
		fi
		if [ "$scenario" = combined ]; then
			if [ -e "$work/finally-cleaned" ]; then test-ok "$backend finally! and defer both finalize on interruption"; else test-fail "$backend combined finalization lost finally! cleanup"; fi
		fi
	done
done
test-end
