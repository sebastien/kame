#!/usr/bin/env bash
# Spec: docs/spec/038-direct-kash-and-values.md — service and executable-watch ownership
set -euo pipefail
# shellcheck disable=SC1091,SC2016
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T038-06 direct Kash recipe lifecycle"
cli_build
make -C "$CLI_ROOT" dist-wasm >/dev/null
for backend in native wasm; do
	work="$TMPDIR/$backend"
	mkdir -p "$work"
	if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	test-step "$backend managed service keeps direct Kash ownership"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
service daemon : ; [ready: [argv: ["test" "-f" "service-ready"] interval-ms: 10 timeout-ms: 3000]]
	sh -c (nop """touch service-ready; printf %s "$$" > service-pid; exec sleep 30""")
consumer : daemon
	printf service
KMK
	if timeout 15 "${runner[@]}" -C "$work" consumer >"$work/out" 2>"$work/err" && [ "$(cat "$work/out")" = service ]; then test-ok "$backend service consumer"; else
		test-fail "$backend service consumer"
		cat "$work/err"
	fi
	if [ -s "$work/service-pid" ]; then
		pid="$(cat "$work/service-pid")"
		for _attempt in {1..100}; do
			if ! kill -0 "$pid" 2>/dev/null; then break; fi
			sleep .01
		done
		if ! kill -0 "$pid" 2>/dev/null; then test-ok "$backend service released its process"; else
			test-fail "$backend service process survived"
			kill "$pid" 2>/dev/null || true
		fi
	else test-fail "$backend service did not start"; fi
	test-step "$backend retained watch observes executable edits"
	printf input >"$work/input"
	cat >"$work/Makefile.kmk" <<'KMK'
SHELL = kash
./cached : ./input
	./cached-tool @>
KMK
	# shellcheck disable=SC2016
	printf '#!/bin/sh\nprintf first > "$1"\nprintf x >> tool-runs\n' >"$work/cached-tool"
	chmod +x "$work/cached-tool"
	"${runner[@]}" -C "$work" ./cached >"$work/out" 2>"$work/err"
	timeout 20 "${runner[@]}" -C "$work" --json --watch ./cached >"$work/watch.log" 2>"$work/watch.err" &
	watcher=$!
	for _attempt in {1..200}; do
		if grep -q '"type":"watch-idle"' "$work/watch.log"; then break; fi
		sleep .025
	done
	# shellcheck disable=SC2016
	printf '#!/bin/sh\nprintf watched > "$1"\nprintf x >> tool-runs\n' >"$work/cached-tool"
	for _attempt in {1..300}; do
		if [ "$(cat "$work/cached")" = watched ] && [ "$(grep -c '"type":"watch-idle"' "$work/watch.log")" -ge 2 ]; then break; fi
		sleep .025
	done
	if [ "$(cat "$work/cached")" = watched ]; then test-ok "$backend watch retains executable dependency"; else
		test-fail "$backend executable watch"
		cat "$work/watch.err"
	fi
	kill -TERM "$watcher" 2>/dev/null || true
	wait "$watcher" 2>/dev/null || true
done
test-end
