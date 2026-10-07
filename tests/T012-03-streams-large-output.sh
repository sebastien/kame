#!/usr/bin/env bash
# Spec: docs/spec/012-streams.md — bounded retention, lossless live streams
# Spec: docs/spec/010-wasm.md — bounded instance memory
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T012-03 large CLI streams"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/project"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'KMK'
default :
	cat ./input.bin; cat ./input.bin >&2
failed :
	cat ./input.bin; cat ./input.bin >&2; exit 3
KMK
for vector in nul binary; do
 for mode in human json; do
  test-step "$vector $mode streams exceed transient memory on both hosts"
  if python3 - "$project" "$CLI_BIN" "$CLI_ROOT/dist/kame.js" "$vector" "$mode" <<'PYTEST'
import base64, hashlib, json, subprocess, sys
from pathlib import Path
folder, native, wasm, vector, mode = sys.argv[1:]
folder = Path(folder)
unit = b'\0' if vector == 'nul' else b'\xff\0\xfe\x01'
expected = unit * (8388608 // len(unit))
(folder / 'input.bin').write_bytes(expected)
for backend, runner in [('native', [native]), ('wasm', ['node', wasm])]:
    for target in ['default', 'failed']:
        output = folder / f'{backend}-{vector}-{mode}-{target}.out'
        errors = output.with_suffix('.err')
        command = runner + (['--json'] if mode == 'json' else []) + ['-C', str(folder), target]
        with output.open('wb') as out, errors.open('wb') as err:
            result = subprocess.run(command, stdout=out, stderr=err, timeout=40)
        assert result.returncode == (0 if target == 'default' else 1), (backend, errors.read_text(errors='replace'))
        if mode == 'human':
            assert output.read_bytes() == expected, (backend, output.stat().st_size)
            assert errors.read_bytes().count(expected) == 1, (backend, errors.stat().st_size)
        else:
            assert errors.stat().st_size == 0, errors.read_bytes()
            hashes = {'stdout': hashlib.sha256(), 'stderr': hashlib.sha256()}
            sizes = dict.fromkeys(hashes, 0)
            terminal = []
            with output.open('rb') as lines:
                for line in lines:
                    event = json.loads(line)
                    kind = event['type']
                    if kind in hashes:
                        data = base64.b64decode(event['data']) if event['encoding'] == 'base64' else event['data'].encode()
                        hashes[kind].update(data)
                        sizes[kind] += len(data)
                    if kind in ['target-failed', 'target-completed']: terminal.append(event)
            for kind in hashes:
                assert sizes[kind] == len(expected) and hashes[kind].digest() == hashlib.sha256(expected).digest(), (backend, kind, sizes)
            assert len(terminal) == 1, terminal
            if target == 'failed':
                cause = terminal[0]['diagnostic']['cause']
                assert cause['stdoutTruncated'] and cause['stderrTruncated'] and cause['stdoutLimit'] == cause['stderrLimit'] == 0, cause
PYTEST
  then test-ok "$vector $mode forwards complete bytes and retains truncation metadata"; else test-fail "$vector $mode large streams"; fi
 done
done
test-step "cached tasks retain and replay bounded prefixes"
if python3 - "$project" "$CLI_BIN" "$CLI_ROOT/dist/kame.js" <<'PYCACHE'
import json, subprocess, sys
from pathlib import Path
folder, native, wasm = sys.argv[1:]
for backend, runner in [('native', [native]), ('wasm', ['node', wasm])]:
    work = Path(folder) / backend
    work.mkdir()
    (work / 'Makefile.kmk').write_text('task cached :\n\techo run >> ./runs; head -c 8388608 /dev/zero\n')
    for invocation in range(2):
        output = work / f'{invocation}.json'
        with output.open('wb') as out:
            result = subprocess.run(runner + ['--json', '-C', str(work), 'cached'], stdout=out, stderr=subprocess.PIPE, timeout=40)
        assert result.returncode == 0 and not result.stderr, (backend, result.stderr)
        count = 0
        truncated = False
        with output.open('rb') as lines:
            for line in lines:
                event = json.loads(line)
                if event['type'] == 'stdout':
                    count += len(event['data'])
                    truncated |= event.get('truncated', False)
                    if invocation == 1: assert event.get('cached') is True, event
        assert count == (8388608 if invocation == 0 else 65536), (backend, invocation, count)
        if invocation == 1: assert truncated, backend
    assert (work / 'runs').read_text() == 'run\n', backend
PYCACHE
then test-ok "both hosts replay only the 64 KiB cache prefix with truncation"; else test-fail "bounded cache output retention"; fi
test-step "configured log limits reach primary builds and sessions"
if python3 - "$project" "$CLI_BIN" "$CLI_ROOT/dist/kame.js" <<'PYLIMIT'
import json, os, signal, subprocess, sys, time
from pathlib import Path
folder, native, wasm = sys.argv[1:]
source = 'failed :\n\thead -c 8192 /dev/zero; head -c 8192 /dev/zero >&2; exit 3\n'
for backend, runner in [('native', [native]), ('wasm', ['node', wasm])]:
    work = Path(folder) / f'{backend}-limits'
    work.mkdir()
    (work / 'Makefile.kmk').write_text(source)
    for invocation in ['primary', 'batch', 'session']:
        args = (['do', 'run', '--timeout', '5000', '-l', 'kmk', '-c', source, 'failed']
                if invocation == 'session' else ['failed'] * (2 if invocation == 'batch' else 1))
        for limit in [1024, 16384]:
            options = ['--json', '-C', str(work), '--log-limit', str(limit)]
            command = runner + (args[:2] + options + args[2:] if invocation == 'session' else options + args)
            result = subprocess.run(command,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=15)
            assert result.returncode == 1 and not result.stderr, (backend, invocation, result)
            events = [json.loads(line) for line in result.stdout.splitlines()]
            failed = next(event for event in events if event['type'] == 'target-failed')
            cause = failed['diagnostic']['cause']
            assert cause['stdoutLimit'] == cause['stderrLimit'] == limit, (backend, invocation, cause)
            assert cause['stdoutTruncated'] == cause['stderrTruncated'] == (limit < 8192), cause
            assert 'stdout' not in cause and 'stderr' not in cause, 'retention leaked process data'
            for kind in ['stdout', 'stderr']:
                assert sum(len(event['data']) for event in events if event['type'] == kind) == 8192
    # A retained watch uses the same configured limit.
    with (work / 'watch.json').open('w+') as out, (work / 'watch.err').open('w+') as err:
        process = subprocess.Popen(runner + ['--watch', '--json', '-C', str(work), '--log-limit', '1024', 'failed'],
                                   stdout=out, stderr=err, start_new_session=True)
        try:
            deadline = time.monotonic() + 15
            while True:
                out.seek(0)
                lines = out.read().splitlines(keepends=True)
                events = [json.loads(line) for line in lines if line.endswith('\n')]
                failed = [event for event in events if event['type'] == 'target-failed']
                if failed:
                    assert failed[0]['diagnostic']['cause']['stdoutLimit'] == 1024, failed[0]
                    break
                assert process.poll() is None and time.monotonic() < deadline, 'watch limit test did not fail its target'
                time.sleep(.02)
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
            process.wait(timeout=5)
        err.seek(0)
        assert not err.read(), 'JSON watch wrote human diagnostics'
PYLIMIT
then test-ok "native/WASM retention limits preserve live streams and diagnostic privacy"; else test-fail "configured log-limit transport"; fi
test-end
