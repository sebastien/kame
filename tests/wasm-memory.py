"""Drive real CLI process publishers with an undersized logical heap."""
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile

root = Path(sys.argv[1])
source = (root / 'src/js/kame.js').read_text()
creation = 'const instance = this.exports.kame_wasm_instance_create();'
assert creation in source
# Use the public ABI budget setter in a private copy of the canonical host;
# production has no test environment variable or altered default heap budget.
source = source.replace(creation, creation + '\n'
    'if (instance !== 0n) this.exports.kame_wasm_instance_set_heap_limit(instance, 262144);')
with tempfile.TemporaryDirectory() as temporary:
    project = Path(temporary)
    host = project / 'kame.mjs'
    host.write_text(source)
    environment = dict(os.environ, KAME_WASM=str(root / 'build/wasm/kame.wasm'))
    for backend in ['recipe', 'argv']:
        for channel in [1, 2]:
            pid_file = project / 'pid'
            pid_file.unlink(missing_ok=True)
            (project / 'emit.py').write_text(
                "import os,pathlib\n"
                "pathlib.Path('pid').write_text(f'{os.getpid()} {os.getpgrp()}')\n"
                f"while True: os.write({channel}, b'Z'*65536)\n")
            (project / 'Makefile.kmk').write_text('emit :\n\tpython3 emit.py\n')
            arguments = (['-C', str(project), 'emit'] if backend == 'recipe' else
                ['do', 'run', '-C', str(project), '-l', 'kash', '-c', 'python3 emit.py'])
            try:
                result = subprocess.run(['node', str(host), *arguments], env=environment,
                    stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, timeout=15)
                assert result.returncode == 1, (backend, channel, result.returncode)
                assert b'error NO_MEMORY: instance heap exhausted\n' in result.stderr, result.stderr[-1000:]
                assert b'file://' not in result.stderr and b'RuntimeError' not in result.stderr, result.stderr[-1000:]
                assert pid_file.exists(), 'exhaustion happened before launching the process'
                pid, group = map(int, pid_file.read_text().split())
                try:
                    os.kill(pid, 0)
                except ProcessLookupError:
                    pass
                else:
                    raise AssertionError(f'{backend}/{channel} left process {pid} alive')
                print(f'{backend}/{channel}: NO_MEMORY preserved and child reaped')
            finally:
                if pid_file.exists():
                    _, group = map(int, pid_file.read_text().split())
                    try:
                        os.killpg(group, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
