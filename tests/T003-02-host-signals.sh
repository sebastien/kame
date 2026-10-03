#!/usr/bin/env bash
# Spec: docs/spec/009-cli.md — Signals and Exit Status
# Spec: docs/spec/003-posix-process.md — Cancellation
# Spec: docs/spec/013-tests.md — T003-02-host-signals
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T003-02 process-group cancellation on signals"

test-step "toolchain and binary"
cli_require_tools
cli_build

fixture_copy signals signals

test-step "SIGINT cancels the active root and reaps its process group"
(
	cd signals
	cli_spawn . ./sleeper.out
	pid="$CLI_SPAWN_PID"
	if wait_for_file ./shell.pid 10 && wait_for_file ./child.pid 10; then
		test-ok "recipe started"
	else
		test-fail "recipe did not start"
		kill_tree "$pid"
		exit 1
	fi
	shell_pid="$(cat ./shell.pid)"
	child_pid="$(cat ./child.pid)"
	kill -INT "$pid" 2>/dev/null || true
	wait_for_status "$pid" 15
	result="$CLI_WAIT_STATUS"
	if [ "$result" = "exited 1" ]; then
		test-ok "first SIGINT exits 1"
	else
		test-fail "signal exit was $result, wanted exited 1"
	fi
	if wait_for_pid_gone "$child_pid" 10; then
		test-ok "child process was reaped"
	else
		test-fail "child process $child_pid still alive"
	fi
	if wait_for_pid_gone "$shell_pid" 5; then
		test-ok "recipe shell exited"
	else
		test-fail "recipe shell $shell_pid still alive"
	fi
	kill_tree "$pid"
)

test-step "SIGTERM cancels the active root"
(
	cd signals
	rm -f shell.pid child.pid sleeper.out
	cli_spawn . ./sleeper.out
	pid="$CLI_SPAWN_PID"
	if wait_for_file ./child.pid 10; then
		test-ok "recipe started"
	else
		test-fail "recipe did not start"
		kill_tree "$pid"
		exit 1
	fi
	child_pid="$(cat ./child.pid)"
	kill -TERM "$pid" 2>/dev/null || true
	wait_for_status "$pid" 15
	result="$CLI_WAIT_STATUS"
	if [ "$result" = "exited 1" ]; then
		test-ok "first SIGTERM exits 1"
	else
		test-fail "signal exit was $result, wanted exited 1"
	fi
	if wait_for_pid_gone "$child_pid" 10; then
		test-ok "child process was reaped"
	else
		test-fail "child process $child_pid still alive"
	fi
	kill_tree "$pid"
)

test-step "a repeated signal uses the conventional signal exit status"
(
	cd signals
	rm -f shell.pid child.pid sleeper.out
	cli_spawn . ./sleeper.out
	pid="$CLI_SPAWN_PID"
	if ! wait_for_file ./child.pid 10; then
		test-fail "recipe did not start"
		kill_tree "$pid"
		exit 1
	fi
	# Standard signals of the same kind may coalesce before delivery. Queue
	# distinct kinds so two notifications reach the handler; pending-signal
	# delivery order is unspecified, so either conventional status is valid.
	kill -INT "$pid" 2>/dev/null || true
	kill -TERM "$pid" 2>/dev/null || true
	wait_for_status "$pid" 15
	if [ "$CLI_WAIT_STATUS" = "exited 130" ] || [ "$CLI_WAIT_STATUS" = "exited 143" ]; then
		test-ok "a second signal uses its conventional exit status"
	else
		test-fail "repeated signal exit was $CLI_WAIT_STATUS, wanted exited 130 or 143"
	fi
	kill_tree "$pid"
)

test-end
