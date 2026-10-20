#!/usr/bin/env bash
# Spec: docs/spec/013-tests.md — Harness contract
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T013-08 harness scheduling and results"
repo="$TEST_PATH/runner"
mkdir -p "$repo/tests"
cp "$CLI_ROOT/tests/harness.sh" "$CLI_ROOT/tests/lib-testing.sh" "$repo/tests/"
printf 'build/kame.debug dist-wasm:\n\t@:\n' >"$repo/Makefile"

cat >"$repo/tests/a.sh" <<'SH'
touch pool-active
until [ -e third-started ]; do sleep 0.01; done
sleep 2
rm pool-active
touch pool-finished
SH
printf 'exit 0\n' >"$repo/tests/b.sh"
printf 'touch third-started\n' >"$repo/tests/c.sh"
printf 'echo failure-detail; exit 7\n' >"$repo/tests/d.sh"
cat >"$repo/tests/e.sh" <<'SH'
set -eu
[ ! -e pool-active ] && [ -e pool-finished ]
touch exclusive-active
sleep 0.1
rm exclusive-active
touch first-exclusive-finished
SH
cat >"$repo/tests/f.sh" <<'SH'
set -eu
[ ! -e pool-active ] && [ ! -e exclusive-active ] && [ -e first-exclusive-finished ]
SH
for file in a b c d; do
	printf 'tests/%s.sh\tSynthetic worker for scheduling regression\n' "$file"
done >"$repo/tests/PARALLEL.tsv"

harness_jobs=1
run_harness() {
	local status=0
	(cd "$repo" && env -u CLI_BIN KAME_TEST_VERBOSITY=compact KAME_TEST_JOBS="$harness_jobs" \
		TEST_RUNTIME_CACHE_DIR="$repo/cache" timeout 15 bash tests/harness.sh "$@") >"$repo/output" 2>&1 || status=$?
	return "$status"
}

test-step "parallel workers drain before unclassified tests run exclusively"
status=0
if help wait | grep -q -- '-p var'; then
	# e is requested first but must wait for the pool; e and f must not overlap.
	run_harness -j 2 tests/e.sh tests/a.sh tests/b.sh tests/f.sh tests/c.sh tests/d.sh || status=$?
	if [ "$status" = 1 ] && [ -e "$repo/third-started" ] &&
		grep -q 'failure-detail' "$repo/output" && grep -q '1/6.*failed' "$repo/output" &&
		grep -q '4 parallel-safe.*2 exclusive' "$repo/output" &&
		[ "$(cat "$repo/cache/a.sh.seconds")" -ge 2 ] &&
		[ "$(cat "$repo/cache/b.sh.seconds")" -lt "$(cat "$repo/cache/a.sh.seconds")" ]; then
		test-ok "workers refill, preserve failures and timings, then run unclassified tests alone"
	else
		test-fail "parallel harness failed (status=$status): $(cat "$repo/output")"
	fi
else
	run_harness -j 2 tests/b.sh || status=$?
	if [ "$status" = 2 ]; then test-ok "older Bash rejects parallel mode explicitly"; else test-fail "older Bash parallel status=$status"; fi
fi

test-step "serial and verbose modes retain child failures"
for mode in -q -v; do
	status=0
	run_harness "$mode" tests/b.sh tests/d.sh || status=$?
	if [ "$status" = 1 ] && grep -q 'failure-detail' "$repo/output"; then
		test-ok "$mode reports child failures"
	else
		test-fail "$mode lost a child failure: $(cat "$repo/output")"
	fi
done

test-step "invalid worker counts fail as usage errors"
status=0
run_harness -j 0 tests/b.sh || status=$?
if [ "$status" = 2 ]; then test-ok "invalid worker count rejected"; else test-fail "invalid worker status=$status"; fi

test-step "an empty selection succeeds"
if run_harness -j 1 tests/lib-testing.sh; then test-ok "empty selection succeeds"; else test-fail "empty selection failed"; fi
test-step "the default uses reviewed workers when supported"
harness_jobs=''
status=0
run_harness tests/b.sh tests/c.sh || status=$?
if [ "$status" = 0 ] && { ! help wait | grep -q -- '-p var' || grep -q '2 parallel-safe.*0 exclusive' "$repo/output"; }; then
	test-ok "default selects the safe pool or the older-Bash sequential fallback"
else
	test-fail "default scheduling failed: $(cat "$repo/output")"
fi
harness_jobs=1
test-step "missing metadata defaults to exclusive without dropping tests"
mv "$repo/tests/PARALLEL.tsv" "$repo/tests/PARALLEL.saved"
if run_harness -j 2 tests/b.sh tests/c.sh && grep -q '0 parallel-safe.*2 exclusive' "$repo/output"; then
	test-ok "unclassified tests run sequentially"
elif ! help wait | grep -q -- '-p var'; then
	test-ok "older Bash only supports sequential execution"
else
	test-fail "unclassified selection failed: $(cat "$repo/output")"
fi
mv "$repo/tests/PARALLEL.saved" "$repo/tests/PARALLEL.tsv"
test-step "invalid parallel-safe metadata fails before execution"
printf 'tests/missing.sh\tInvalid entry\n' >>"$repo/tests/PARALLEL.tsv"
status=0
run_harness -j 2 tests/b.sh || status=$?
if [ "$status" = 2 ]; then test-ok "invalid metadata rejected"; else test-fail "invalid metadata status=$status"; fi
test-step "verbose mode remains serial even when concurrency is requested"
if run_harness -v -j 2 tests/b.sh tests/c.sh; then test-ok "verbose selection succeeds"; else test-fail "verbose selection failed: $(cat "$repo/output")"; fi
test-step "the suite owns its shared-log lock and releases it on completion"
cat >"$repo/tests/lock-check.sh" <<'SH'
exec 9>build/tests/.harness.lock
if flock -n 9; then echo missing-suite-lock; exit 1; fi
SH
if run_harness -j 1 tests/lock-check.sh && flock -n "$repo/build/tests/.harness.lock" true; then
	test-ok "shared-log lock is held only for the suite lifetime"
else
	test-fail "suite lock failed: $(cat "$repo/output")"
fi
test-step "signals cancel workers without failures or further scheduling"
cat >"$repo/tests/cancel.sh" <<'SH'
source tests/lib-testing.sh
test-start cancellation
printf '%s\n' "$TEST_PATH" >"$ORIGINAL_PATH/scratch-path"
sleep 60 &
child=$!
printf '%s\n' "$child" >"$ORIGINAL_PATH/child-pid"
python3 - "$ORIGINAL_PATH" <<'PYCHILD' &
import subprocess
import sys

# Match watch helpers which create a separate session; also exercise KILL.
child = subprocess.Popen([sys.executable, '-c', '''
from pathlib import Path
import os, signal, time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
Path("detached-pid").write_text(str(os.getpid()))
time.sleep(60)
'''], cwd=sys.argv[1], start_new_session=True)
child.wait()
PYCHILD
until [ -s "$ORIGINAL_PATH/detached-pid" ]; do sleep .01; done
touch "$ORIGINAL_PATH/ready"
wait "$child"
touch "$ORIGINAL_PATH/continued"
SH
printf 'touch scheduled-after-cancel\n' >"$repo/tests/after.sh"
printf 'tests/cancel.sh\tcancellation fixture\n' >"$repo/tests/PARALLEL.tsv"
if python3 - "$repo" <<'PY'; then
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

repo = Path(sys.argv[1])
def assert_stopped(pid):
	deadline = time.monotonic() + 3
	stat = Path(f'/proc/{pid}/stat')
	while stat.exists():
		try:
			if stat.read_text().split()[2] == 'Z':
				return
		except FileNotFoundError:
			return
		assert time.monotonic() < deadline, f'child {pid} survived'
		time.sleep(0.01)

for args in (['-q', '-j', '1'], ['-q', '-j', '2'], ['-v', '-j', '1']):
	for sig in (signal.SIGINT, signal.SIGTERM):
		for name in ('ready', 'continued', 'scheduled-after-cancel', 'detached-pid'):
			(repo / name).unlink(missing_ok=True)
		with (repo / 'cancel-output').open('w') as output:
			process = subprocess.Popen(['bash', 'tests/harness.sh', *args, 'tests/cancel.sh', 'tests/after.sh'], cwd=repo, stdout=output, stderr=output, start_new_session=True)
			try:
				deadline = time.monotonic() + 10
				while not (repo / 'ready').exists():
					assert process.poll() is None, (repo / 'cancel-output').read_text()
					assert time.monotonic() < deadline, 'worker did not start'
					time.sleep(0.01)
				# Alternate terminal-style group delivery and parent-only cancellation.
				if sig == signal.SIGINT:
					os.killpg(process.pid, sig)
				else:
					process.send_signal(sig)
				assert process.wait(timeout=5) == 128 + sig
				for name in ('child-pid', 'detached-pid'):
					assert_stopped(int((repo / name).read_text()))
			finally:
				if process.poll() is None:
					os.killpg(process.pid, signal.SIGKILL)
					process.wait()
				if (repo / 'detached-pid').exists():
					try:
						os.killpg(int((repo / 'detached-pid').read_text()), signal.SIGKILL)
					except ProcessLookupError:
						pass
		text = (repo / 'cancel-output').read_text()
		assert 'Tests cancelled' in text and 'FAIL' not in text, text
		assert not (repo / 'continued').exists(), text
		assert not (repo / 'scheduled-after-cancel').exists(), text
		assert not Path((repo / 'scratch-path').read_text().strip()).exists(), 'scratch not cleaned'

# Prebuild must be cancellable before any test worker is scheduled.
bin_dir = repo / 'bin'
bin_dir.mkdir()
make = bin_dir / 'make'
make.write_text('#!/bin/sh\necho $$ > setup-pid\ntouch setup-ready\nsleep 60\n')
make.chmod(0o755)
for sig in (signal.SIGINT, signal.SIGTERM):
	for name in ('setup-ready', 'ready', 'scheduled-after-cancel'):
		(repo / name).unlink(missing_ok=True)
	with (repo / 'setup-output').open('w') as output:
		process = subprocess.Popen(['bash', 'tests/harness.sh', '-j', '2', 'tests/cancel.sh', 'tests/after.sh'], cwd=repo,
			env={**os.environ, 'PATH': str(bin_dir) + os.pathsep + os.environ['PATH']},
			stdout=output, stderr=output, start_new_session=True)
		try:
			deadline = time.monotonic() + 10
			while not (repo / 'setup-ready').exists():
				assert process.poll() is None, (repo / 'setup-output').read_text()
				assert time.monotonic() < deadline, 'prebuild did not start'
				time.sleep(0.01)
			process.send_signal(sig)
			assert process.wait(timeout=5) == 128 + sig
			assert_stopped(int((repo / 'setup-pid').read_text()))
		finally:
			if process.poll() is None:
				os.killpg(process.pid, signal.SIGKILL)
				process.wait()
			if (repo / 'setup-pid').exists():
				try:
					os.killpg(int((repo / 'setup-pid').read_text()), signal.SIGKILL)
				except ProcessLookupError:
					pass
	text = (repo / 'setup-output').read_text()
	assert 'Tests cancelled' in text and 'FAIL' not in text, text
	assert not (repo / 'ready').exists() and not (repo / 'scheduled-after-cancel').exists(), text
make.unlink()
PY
	test-ok "INT/TERM stop suites, detached descendants and prebuilds without scheduling more work"
else
	test-fail "suite cancellation regression"
fi
test-step "editing the harness file cannot corrupt an active run"
cat >"$repo/tests/rewrite.sh" <<'SH'
python3 - <<'PY'
from pathlib import Path
path = Path('tests/harness.sh')
path.write_text('\n' * 10000 + path.read_text())
PY
SH
printf 'touch after-rewrite\n' >"$repo/tests/g.sh"
status=0
run_harness -j 1 tests/rewrite.sh tests/g.sh || status=$?
if [ "$status" = 0 ] && [ -f "$repo/after-rewrite" ]; then
	test-ok "active harness uses a stable source snapshot"
else
	test-fail "source edit corrupted the active harness (status=$status): $(tail -10 "$repo/output")"
fi
test-end
