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
	printf '%s\n' "$$" > recipe.pid; sleep 30 & printf '%s\n' "$!" > child.pid; printf started > started.txt; wait; printf done > done.txt
EOF

run_and_signal() {
	local signal="$1"
	local recipe_pid="" child_pid=""
	rm -f "$project/started.txt" "$project/done.txt" "$project/recipe.pid" "$project/child.pid"
	env -C "$project" node "$CLI_ROOT/dist/kame.js" ./slow.txt >/dev/null 2>&1 &
	local pid=$!
	# This function runs in a command-substitution subshell; leave the harness traps intact.
	trap "kill -KILL '$pid' 2>/dev/null || true; wait '$pid' 2>/dev/null || true" EXIT
	local waited=0
	while [ ! -f "$project/started.txt" ] && [ "$waited" -lt 50 ]; do
		sleep 0.1
		waited=$((waited + 1))
	done
	if [ ! -f "$project/started.txt" ]; then
		printf 'not-started'
		return
	fi
	recipe_pid="$(cat "$project/recipe.pid")"
	child_pid="$(cat "$project/child.pid")"
	if ! [[ "$recipe_pid" =~ ^[1-9][0-9]*$ && "$child_pid" =~ ^[1-9][0-9]*$ ]] ||
		! kill -0 "$recipe_pid" 2>/dev/null || ! kill -0 "$child_pid" 2>/dev/null; then
		printf 'recipe-not-running'
		return
	fi
	trap "kill -KILL '$pid' '$recipe_pid' '$child_pid' 2>/dev/null || true; wait '$pid' 2>/dev/null || true" EXIT
	kill "-$signal" "$pid"
	waited=0
	while kill -0 "$pid" 2>/dev/null && [ "$waited" -lt 50 ]; do
		sleep 0.1
		waited=$((waited + 1))
	done
	if kill -0 "$pid" 2>/dev/null; then
		printf 'cancel-timeout'
		return
	fi
	set +e
	wait "$pid"
	local status=$?
	set -e
	waited=0
	while { kill -0 "$recipe_pid" 2>/dev/null || kill -0 "$child_pid" 2>/dev/null; } && [ "$waited" -lt 30 ]; do
		sleep 0.1
		waited=$((waited + 1))
	done
	if kill -0 "$recipe_pid" 2>/dev/null || kill -0 "$child_pid" 2>/dev/null; then
		status=998
	fi
	if [ -f "$project/done.txt" ]; then
		status=999
	fi
	if [ "$status" != 998 ]; then trap - EXIT; fi
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
