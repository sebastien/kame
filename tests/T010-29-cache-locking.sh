#!/usr/bin/env bash
# Spec: docs/spec/008-cache.md — cross-process miss locking
# Spec: docs/spec/013-tests.md — T010-29-cache-locking
set -euo pipefail

# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T010-29 cache miss locking"
cd "$CLI_ROOT"
test-step "build native CLI"
cli_build

project="$TMPDIR/cache-lock"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'SRC'
task cached :
	echo cached >> runs.log
	sleep 0.6
	@(out "cached-result")

task slow :
	echo slow >> slow.log
	sleep 1
	@(out "slow-result")
SRC

# Two independent processes see the same cold identity. The second must wait
# for publication, then read the record instead of running the recipe again.
(cd "$project" && "$CLI_BIN" cached >one.out 2>one.err) & first=$!
sleep 0.08
(cd "$project" && "$CLI_BIN" cached >two.out 2>two.err) & second=$!
wait "$first"
wait "$second"
if [ "$(wc -l <"$project/runs.log")" -eq 1 ] && cmp -s "$project/one.out" "$project/two.out"; then
  test-ok "separate native processes serialize a cold cache miss"
else
  test-fail "cold miss was executed more than once"
fi

# Terminating a process during a cold miss must close its advisory lock. The
# next process must acquire the same stripe, rerun, and publish successfully.
(cd "$project" && exec "$CLI_BIN" slow >slow-one.out 2>slow-one.err) & owner=$!
for _ in $(seq 1 200); do
  [ -f "$project/slow.log" ] && break
  sleep 0.02
done
[ -f "$project/slow.log" ] || test-fail "lock owner did not start its recipe"
kill "$owner" 2>/dev/null || true
wait "$owner" 2>/dev/null || true
if timeout 3s bash -c 'cd "$1" && "$2" slow >/dev/null 2>&1' bash "$project" "$CLI_BIN" && [ "$(wc -l <"$project/slow.log")" -eq 2 ]; then
  test-ok "process termination releases an in-progress cache lock"
else
  test-fail "a later process could not recover the interrupted miss"
fi

# The striped lock namespace has fixed cardinality regardless of key count.
lock_count="$(find "$project/.kame/cache/locks" -maxdepth 1 -type f | wc -l)"
if [ "$lock_count" -le 256 ]; then test-ok "lock files remain bounded to 256 stripes"; else test-fail "lock stripe count is $lock_count"; fi

test-end
