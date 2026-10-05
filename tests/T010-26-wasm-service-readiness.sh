#!/usr/bin/env bash
# Spec: docs/spec/024-managed-services.md — forwarded readiness probes
# Spec: docs/spec/013-tests.md — T010-26-wasm-service-readiness
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-26 wasm service readiness forwarding"
cd "$CLI_ROOT"

test-step "build WASM CLI"
make dist-wasm >/dev/null

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cli="$CLI_ROOT/dist/kame.js"

cat >"$work/Makefile.kmk" <<'EOF'
service daemon : ; [ready: [argv: ["test" "-f" "ready-marker"] interval-ms: 10 timeout-ms: 3000]]
	touch ready-marker; while :; do sleep 1; done
consumer : daemon
	test -f ready-marker
EOF
if (cd "$work" && timeout 10 node "$cli" consumer >out 2>err) && [ -f "$work/ready-marker" ]; then
	test-ok "forwarded probe gates the dependent until ready"
else
	test-fail "forwarded readiness did not release its dependent"
fi

cat >"$work/Makefile.kmk" <<'EOF'
service daemon : ; [ready: [argv: ["false"] interval-ms: 10 timeout-ms: 100]]
	while :; do sleep 1; done
consumer : daemon
	true
EOF
set +e
(cd "$work" && timeout 10 node "$cli" consumer >out 2>err)
status=$?
set -e
if [ "$status" = 1 ] && grep -q 'SERVICE_READY_TIMEOUT' "$work/err"; then
	test-ok "forwarded readiness timeout cancels the service"
else
	test-fail "forwarded readiness timeout: status=$status err=$(cat "$work/err")"
fi

cat >"$work/Makefile.kmk" <<'EOF'
service daemon : ; [ready: [argv: ["test" "-f" "ready-marker"] interval-ms: 10 timeout-ms: 3000] restart: [attempts: 1 backoff-ms: 10]]
	if [ ! -f first-start ]; then touch first-start; exit 1; fi; touch ready-marker; while :; do sleep 1; done
consumer : daemon
	test -f ready-marker
EOF
if (cd "$work" && timeout 10 node "$cli" consumer >out 2>err) && [ -f "$work/first-start" ] && [ -f "$work/ready-marker" ]; then
	test-ok "forwarded startup failure restarts the service and releases its dependent"
else
	test-fail "forwarded service restart did not recover: status=$? err=$(cat "$work/err")"
fi

cat >"$work/Makefile.kmk" <<'EOF'
service daemon : ; [ready: [argv: ["test" "-f" "ready-marker"] interval-ms: 10 timeout-ms: 3000] health: [argv: ["test" "-f" "healthy-marker"] interval-ms: 10 failures: 1] restart: [attempts: 1 backoff-ms: 10]]
	if [ ! -f first-start ]; then touch first-start; else touch healthy-marker; fi; touch ready-marker; while :; do sleep 1; done
consumer : daemon
	test -f ready-marker
EOF
if (cd "$work" && timeout 10 node "$cli" consumer >out 2>err) && [ -f "$work/healthy-marker" ]; then
	test-ok "forwarded health failure restarts the service and releases its dependent"
else
	test-fail "forwarded health restart did not recover: status=$? err=$(cat "$work/err")"
fi

cat >"$work/Makefile.kmk" <<'EOF'
service daemon : ; [stop: [grace-ms: 0]]
	trap '' TERM; while :; do sleep 1; done
consumer : daemon
	true
EOF
if (cd "$work" && timeout 3 node "$cli" consumer >out 2>err); then
	test-ok "forwarded stop request applies the configured grace period"
else
	test-fail "forwarded service stop exceeded its zero grace period"
fi

test-end
