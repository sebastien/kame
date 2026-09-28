#!/usr/bin/env bash
# Spec: docs/spec/010-wasm.md — JavaScript CLI wrapper, cancellation
# Spec: docs/spec/003-posix-process.md — signal termination
# Spec: docs/spec/013-tests.md — T010-16-wasm-cancellation
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-16 wasm signal cancellation"
cd "$CLI_ROOT"

test-step "build artifacts"
make dist-wasm >/dev/null
cli_build

project="$TMPDIR/wasm-cancel"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'EOF'
./slow.txt :
	printf started > started.txt
	sleep 30
	printf done > done.txt
EOF

run_and_signal() {
	local signal="$1"
	rm -f "$project/started.txt" "$project/done.txt"
	env -C "$project" node "$CLI_ROOT/dist/kame.js" ./slow.txt >/dev/null 2>&1 &
	local pid=$!
	local waited=0
	while [ ! -f "$project/started.txt" ] && [ "$waited" -lt 50 ]; do
		sleep 0.1
		waited=$((waited + 1))
	done
	kill "-$signal" "$pid" 2>/dev/null || true
	set +e
	wait "$pid"
	local status=$?
	set -e
	if [ -f "$project/done.txt" ]; then
		status=999
	fi
	printf '%s' "$status"
}

test-step "SIGTERM cancels the recipe and reports 128 plus the signal"
status="$(run_and_signal TERM)"
if [ "$status" = 143 ]; then
	test-ok "SIGTERM exits 143 and leaves the recipe incomplete"
else
	test-fail "SIGTERM: status=$status"
fi

test-step "SIGINT cancels the recipe and reports 128 plus the signal"
status="$(run_and_signal INT)"
if [ "$status" = 130 ]; then
	test-ok "SIGINT exits 130 and leaves the recipe incomplete"
else
	test-fail "SIGINT: status=$status"
fi

test-end
