#!/usr/bin/env bash
# Spec: docs/spec/024-managed-services.md — forwarded readiness probes
# Spec: docs/spec/013-tests.md — T010-26-wasm-service-readiness
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-26 wasm service readiness forwarding"
cd "$CLI_ROOT"

test-step "build WASM CLI"
if [ -n "${KAME_WASM:-}" ]; then
	wasm_cli="$CLI_ROOT/src/js/kame.js"
else
	make dist-wasm >/dev/null
	wasm_cli="$CLI_ROOT/dist/kame.js"
fi

work="$TMPDIR/service-readiness"
mkdir -p "$work"
cli="$wasm_cli"

cat >"$work/Makefile.km" <<'EOF'
service daemon ; {shell: "sh", ready: {argv: ["test", "-f", "ready-marker"], interval-ms: 10, timeout-ms: 3000}}
	touch ready-marker; while :; do sleep 1; done
task consumer : daemon
	test -f ready-marker
EOF
if (cd "$work" && timeout --kill-after=2 10 node "$cli" consumer >out 2>err) && [ -f "$work/ready-marker" ]; then
	test-ok "forwarded probe gates the dependent until ready"
else
	test-fail "forwarded readiness did not release its dependent"
fi

cat >"$work/Makefile.km" <<'EOF'
service daemon ; {shell: "sh", ready: {argv: ["false"], interval-ms: 10, timeout-ms: 100}}
	printf '%s\n' "$$" > timeout-service.pid; while :; do sleep 1; done
task consumer : daemon
	true
EOF
# Scope cleanup traps to this probe; preserve the parent harness traps.
(
	cleanup_timeout_service() {
		local pid=""
		if [ -f "$work/timeout-service.pid" ]; then
			pid="$(cat "$work/timeout-service.pid")"
		fi
		if [[ "$pid" =~ ^[1-9][0-9]*$ ]] && kill -0 "$pid" 2>/dev/null; then
			kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
			wait_for_pid_gone "$pid" 2 || true
		fi
	}
	trap cleanup_timeout_service EXIT
	trap 'exit 130' INT
	trap 'exit 143' TERM
	set +e
	(cd "$work" && timeout --kill-after=2 10 node "$cli" consumer >out 2>err)
	status=$?
	set -e
	service_pid="$(cat "$work/timeout-service.pid" 2>/dev/null || true)"
	if [ "$status" = 1 ] && grep -q 'SERVICE_READY_TIMEOUT' "$work/err" &&
		[[ "$service_pid" =~ ^[1-9][0-9]*$ ]] && wait_for_pid_gone "$service_pid" 3; then
		test-ok "forwarded readiness timeout cancels and reaps the started service"
	else
		test-fail "forwarded readiness timeout: status=$status service_pid=$service_pid err=$(cat "$work/err")"
	fi
)

rm -f "$work/first-start" "$work/ready-marker" "$work/healthy-marker" "$work/starts"
cat >"$work/Makefile.km" <<'EOF'
service daemon ; {shell: "sh", ready: {argv: ["test", "-f", "ready-marker"], interval-ms: 10, timeout-ms: 3000}, restart: {attempts: 1, backoff-ms: 10}}
	printf 'start\n' >> starts; if [ ! -f first-start ]; then touch first-start; exit 1; fi; touch ready-marker; while :; do sleep 1; done
task consumer : daemon
	test -f ready-marker
EOF
if (cd "$work" && timeout --kill-after=2 10 node "$cli" consumer >out 2>err) && [ -f "$work/first-start" ] && [ -f "$work/ready-marker" ] && [ "$(wc -l <"$work/starts")" = 2 ]; then
	test-ok "forwarded startup failure restarts the service and releases its dependent"
else
	test-fail "forwarded service restart did not recover: status=$? err=$(cat "$work/err")"
fi

rm -f "$work/first-start" "$work/ready-marker" "$work/healthy-marker" "$work/starts"
cat >"$work/Makefile.km" <<'EOF'
service daemon ; {
	shell: "sh",
	ready: {argv: ["test", "-f", "ready-marker"], interval-ms: 10, timeout-ms: 3000},
	health: {argv: ["test", "-f", "healthy-marker"], interval-ms: 10, failures: 1},
	restart: {attempts: 1, backoff-ms: 10},
}
	printf 'start\n' >> starts; if [ ! -f first-start ]; then touch first-start; else touch healthy-marker; fi; touch ready-marker; while :; do sleep 1; done
task consumer : daemon ; {shell: "sh"}
	for attempt in 1 2 3 4 5 6 7 8 9 10; do if test -f healthy-marker; then exit 0; fi; sleep 0.1; done; exit 1
EOF
if (cd "$work" && timeout --kill-after=2 10 node "$cli" consumer >out 2>err) && [ -f "$work/first-start" ] && [ -f "$work/healthy-marker" ] && [ "$(wc -l <"$work/starts")" = 2 ]; then
	test-ok "forwarded health failure restarts the service and releases its dependent"
else
	test-fail "forwarded health restart did not recover: status=$? err=$(cat "$work/err")"
fi

cat >"$work/Makefile.km" <<'EOF'
service daemon ; {shell: "sh", ready: {argv: ["test", "-f", "stop-ready"], interval-ms: 10, timeout-ms: 3000}, stop: {grace-ms: 0}}
	trap '' TERM; touch stop-ready; while :; do sleep 1; done
task consumer : daemon
	python3 -c 'import time; print(time.monotonic_ns())' > consumer-finished
EOF
if (cd "$work" && timeout --kill-after=2 12 node "$cli" consumer >out 2>err) &&
	python3 - "$work/consumer-finished" <<'PY'
import pathlib
import sys
import time

started = int(pathlib.Path(sys.argv[1]).read_text())
elapsed = (time.monotonic_ns() - started) / 1_000_000_000
assert elapsed < 3.5, f"service shutdown took {elapsed:.3f}s with zero grace"
PY
then
	test-ok "forwarded stop request applies the configured grace period"
else
	test-fail "forwarded service stop failed: $(cat "$work/err")"
fi

test-end
