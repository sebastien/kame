"""Exercise public pipe pressure without collecting the output in memory."""
import json
import os
from pathlib import Path
import signal
import select
import subprocess
import sys
import tempfile
import time

def drain(pipe, json_events=False, expected_bytes=None):
    deadline = time.monotonic() + 20
    total = 0
    pending = b''
    while True:
        remaining = deadline - time.monotonic()
        assert remaining > 0 and select.select([pipe], [], [], remaining)[0], 'public drain stalled'
        chunk = os.read(pipe.fileno(), 65536)
        if not chunk:
            assert not pending, 'partial JSON publication'
            return total
        if not json_events:
            total += chunk.count(b'Z')
            if expected_bytes is not None and total >= expected_bytes:
                return total
            continue
        pending += chunk
        while b'\n' in pending:
            line, pending = pending.split(b'\n', 1)
            event = json.loads(line)
            if event.get('type') == 'stdout':
                total += len(event.get('data', ''))
        if expected_bytes is not None and total >= expected_bytes:
            return total


root = Path(sys.argv[1])
case = sys.argv[2]
with tempfile.TemporaryDirectory() as temporary:
    project = Path(temporary)
    channel = 2 if case == 'stderr' else 1
    cancel = case == 'cancel'
    timeout = case == 'timeout'
    endless = cancel or timeout
    (project / 'emit.py').write_text(
        "import os,pathlib\n"
        "pathlib.Path('ready').touch()\n"
        f"for _ in range({1000000 if endless else 16 if case == 'cache' else 128}): os.write({channel},b'Z'*65536)\n"
        "pathlib.Path('done').touch()\n"
    )
    # Cover direct argv publication as well as legacy shell recipes.
    structured = case == 'argv'
    recipe = '$(python3 emit.py)' if structured else 'python3 emit.py'
    (project / 'Makefile.kmk').write_text(f'emit :\n\t{recipe}\n')
    command = ['node', str(root / 'dist/kame.js')]
    if structured:
        command += ['do', 'run', '-C', str(project), '-l', 'kash', '-c', 'python3 emit.py']
    else:
        command += ['-C', str(project)]
        if case == 'json':
            command.append('--json')
        if case == 'watch':
            command.append('--watch')
        if timeout:
            command += ['--timeout', '2000']
        command.append('emit')
    blocked_stderr = case == 'stderr'
    cached = case == 'cache'
    if cached:
        # Distinct cached tasks in a dependency chain: repeated target arguments
        # share one root, and one 64 KiB prefix can fit in the OS pipe buffer.
        rules = []
        for index in range(16):
            prerequisite = f'emit{index - 1}' if index else ''
            rules.append(f'task emit{index} : {prerequisite}\n\tpython3 emit.py\n')
        (project / 'Makefile.kmk').write_text(''.join(rules) + 'marker : emit15\n\ttouch proceeded\n')
        command[-1] = 'emit15'
        subprocess.run(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True, timeout=15)
        (project / 'ready').unlink()
        (project / 'done').unlink()
        command += ['marker']
    process = subprocess.Popen(command,
        stdout=subprocess.DEVNULL if blocked_stderr else subprocess.PIPE,
        stderr=subprocess.PIPE if blocked_stderr else subprocess.DEVNULL)
    pipe = process.stderr if blocked_stderr else process.stdout
    try:
        if cached:
            time.sleep(1)
            assert not (project / 'proceeded').exists(), 'cached publication did not stop runtime stepping'
            assert process.poll() is None, 'cache replay ignored unread sink'
        else:
            deadline = time.monotonic() + 15
            while not (project / 'ready').exists():
                assert process.poll() is None, 'CLI exited before producer readiness'
                assert time.monotonic() < deadline, 'producer readiness timeout'
                time.sleep(.01)
            time.sleep(.5)
            assert not (project / 'done').exists(), 'producer drained while public pipe was unread'
        if cancel:
            process.send_signal(signal.SIGINT)
            assert process.wait(timeout=5) == 130, 'paused cancellation did not reap process group'
        elif timeout:
            time.sleep(2)
            drain(pipe)
            assert process.wait(timeout=5) != 0, 'paused timeout succeeded'
            assert not (project / 'done').exists(), 'timed out producer completed'
        elif case == 'watch':
            expected = 8 * 1024 * 1024
            total = drain(pipe, expected_bytes=expected)
            assert total == expected, f'published byte count {total}'
            deadline = time.monotonic() + 5
            while not (project / 'done').exists():
                assert process.poll() is None, 'watch exited before settling'
                assert time.monotonic() < deadline, 'watch producer did not finish after public drain'
                time.sleep(.01)
            process.send_signal(signal.SIGINT)
            assert process.wait(timeout=5) == 130, 'watch cancellation after drain failed'
        else:
            total = drain(pipe, case == 'json')
            assert process.wait(timeout=15) == 0, 'resumed CLI failed'
            assert (project / ('proceeded' if cached else 'done')).exists(), 'work did not finish after public drain'
            expected = 1048576 if cached else 8 * 1024 * 1024
            assert total == expected, f'published byte count {total}'
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        pipe.close()
