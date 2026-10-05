#!/usr/bin/env bash
# Spec: docs/spec/008-cache.md — forwarded-host cache lock lifecycle
# Spec: docs/spec/013-tests.md — T010-30-wasm-cache-locking
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-30 forwarded wasm cache locking"
cd "$CLI_ROOT"

test-step "build wasm and native fixtures"
make dist-wasm >/dev/null
cli_build

project="$TMPDIR/wasm-cache-lock"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'EOF_MAKE'
task cached :
	sleep 0.4
	echo run >> runs.log
	@(out "locked-output")
EOF_MAKE

# Start two independent Node hosts against the same cold identity.
(cd "$project" && node "$CLI_ROOT/dist/kame.js" cached) >"$project/one.out" 2>"$project/one.err" &
one=$!
sleep 0.05
(cd "$project" && node "$CLI_ROOT/dist/kame.js" cached) >"$project/two.out" 2>"$project/two.err" &
two=$!
lock_path=""
for _ in $(seq 1 100); do
	lock_path="$(find "$project/.kame/cache/locks" -maxdepth 1 -type d -name 'host-*.lock' -print -quit 2>/dev/null || true)"
	[ -n "$lock_path" ] && break
	sleep 0.02
done
wait "$one"
wait "$two"

runs="$(wc -l <"$project/runs.log" | tr -d ' ')"
if [ "$runs" = 1 ] && cmp -s "$project/one.out" "$project/two.out" && grep -q locked-output "$project/one.out"; then
	test-ok "two forwarded hosts execute one cold cache miss and replay the same result"
else
	test-fail "concurrent forwarded runs=$runs; one=$(cat "$project/one.err"); two=$(cat "$project/two.err")"
fi
if [ -d "$project/.kame/cache/locks" ] && find "$project/.kame/cache/locks" -mindepth 1 -print -quit | grep -q .; then
	test-fail "forwarded cache lock remained after publication"
else
	test-ok "forwarded lock is released after atomic publication"
fi

test-step "a lock left by a dead owner is reclaimed"
if [ -n "$lock_path" ]; then
	mkdir -p "$lock_path"
	printf '{"pid":2147483647,"token":"stale"}\n' >"$lock_path/owner.json"
	(cd "$project" && node "$CLI_ROOT/dist/kame.js" cached) >"$project/reclaimed.out" 2>"$project/reclaimed.err"
	if cmp -s "$project/one.out" "$project/reclaimed.out" && [ ! -e "$lock_path" ]; then
		test-ok "cache hit reclaims the stale lock and removes it"
	else
		test-fail "stale lock recovery failed: $(cat "$project/reclaimed.err")"
	fi
else
	test-fail "did not observe the forwarded lock path during the cold miss"
fi

test-step "a failed cached recipe releases its forwarded lock"
failed="$TMPDIR/wasm-cache-lock-failed"
mkdir -p "$failed"
printf 'task cached :\n\texit 1\n' >"$failed/Makefile.kmk"
if (cd "$failed" && node "$CLI_ROOT/dist/kame.js" cached) >"$failed/out" 2>"$failed/err"; then
	failed_status=0
else
	failed_status=$?
fi
if [ "$failed_status" -ne 0 ] && ! find "$failed/.kame/cache/locks" -mindepth 1 -print -quit 2>/dev/null | grep -q .; then
	test-ok "failed recipe releases the forwarded cache lock"
else
	test-fail "failed recipe status=$failed_status; stderr=$(cat "$failed/err")"
fi

test-step "interrupting the lock owner releases its forwarded lock"
cancel="$TMPDIR/wasm-cache-lock-cancel"
mkdir -p "$cancel"
cat >"$cancel/Makefile.kmk" <<'EOF_CANCEL'
task cached :
	sleep 10
EOF_CANCEL
(cd "$cancel" && node "$CLI_ROOT/dist/kame.js" cached) >"$cancel/out" 2>"$cancel/err" &
cancel_pid=$!
for _ in $(seq 1 200); do
	if find "$cancel/.kame/cache/locks" -mindepth 1 -type d -name 'host-*.lock' -print -quit 2>/dev/null | grep -q .; then break; fi
	sleep 0.02
done
kill -TERM "$cancel_pid" 2>/dev/null || true
if wait "$cancel_pid"; then
	cancel_status=0
else
	cancel_status=$?
fi
if [ "$cancel_status" -ne 0 ] && ! find "$cancel/.kame/cache/locks" -mindepth 1 -print -quit 2>/dev/null | grep -q .; then
	test-ok "interruption terminates the recipe and releases the lock"
else
	test-fail "interrupted owner status=$cancel_status; stderr=$(cat "$cancel/err")"
fi

test-step "an interrupted waiter leaves the lock owner undisturbed"
waiters="$TMPDIR/wasm-cache-lock-waiters"
mkdir -p "$waiters"
cat >"$waiters/Makefile.kmk" <<'EOF_WAITERS'
task cached :
	sleep 1
	echo run >> runs.log
	@(out "waiter-output")
EOF_WAITERS
(cd "$waiters" && node "$CLI_ROOT/dist/kame.js" cached) >"$waiters/owner.out" 2>"$waiters/owner.err" &
owner_pid=$!
for _ in $(seq 1 200); do
	if find "$waiters/.kame/cache/locks" -mindepth 1 -type d -name 'host-*.lock' -print -quit 2>/dev/null | grep -q .; then break; fi
	sleep 0.02
done
(cd "$waiters" && node "$CLI_ROOT/dist/kame.js" cached) >"$waiters/waiter.out" 2>"$waiters/waiter.err" &
waiter_pid=$!
sleep 0.1
kill -TERM "$waiter_pid" 2>/dev/null || true
if wait "$waiter_pid"; then
	waiter_status=0
else
	waiter_status=$?
fi
wait "$owner_pid"
if [ "$waiter_status" -ne 0 ] && [ "$(wc -l <"$waiters/runs.log" | tr -d ' ')" = 1 ] && ! find "$waiters/.kame/cache/locks" -mindepth 1 -print -quit 2>/dev/null | grep -q .; then
	test-ok "cancelled waiter exits without disturbing publication or leaving a lock"
else
	test-fail "waiter status=$waiter_status; owner=$(cat "$waiters/owner.err"); waiter=$(cat "$waiters/waiter.err")"
fi

test-end
