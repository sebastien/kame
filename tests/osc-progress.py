"""Module: osc-progress — exercise CLI taskbar progress on a pseudo-terminal."""

from __future__ import annotations

import errno
import os
from pathlib import Path
import pty
import re
import select
import signal
import subprocess
import sys
import time


OSC = rb'\x1b\]9;4;(\d);(\d+)\x1b\\'


def run(root: Path, runner: list[str], args: list[str], *, terminal: bool = True, term: str = 'xterm', cancel: bool = False, watch: bool = False) -> tuple[int, bytes, list[tuple[int, int]]]:
	master, slave = pty.openpty()
	process = subprocess.Popen([*runner, '-C', str(root), *args], env=dict(os.environ, TERM=term),
		stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=slave if terminal else subprocess.PIPE)
	os.close(slave)
	output = bytearray()
	changed = False
	deadline = time.monotonic() + 15
	try:
		if terminal:
			while time.monotonic() < deadline:
				if not select.select([master], [], [], .05)[0]:
					continue
				try:
					chunk = os.read(master, 65536)
				except OSError as error:
					if error.errno != errno.EIO:
						raise
					break
				if not chunk:
					break
				output.extend(chunk)
				if cancel and re.search(OSC, output):
					process.send_signal(signal.SIGTERM)
					cancel = False
				if watch:
					idle = output.count('watch · waiting for changes'.encode())
					if idle == 1 and not changed:
						(root / 'seed').write_text('watch change')
						changed = True
					if idle >= 2:
						process.send_signal(signal.SIGTERM)
						watch = False
		stdout, stderr = process.communicate(timeout=20)
		output.extend(stderr or b'')
	finally:
		os.close(master)
		if process.poll() is None:
			process.kill()
			process.wait()
	assert not re.search(OSC, stdout), stdout
	return process.returncode, bytes(output), [(int(state), int(percent)) for state, percent in re.findall(OSC, output)]


def check(root: Path, runner: list[str]) -> None:
	root.mkdir()
	(root / 'seed').write_text('seed')
	(root / 'Makefile.kmk').write_text('''default : ./one ./two
./one : ./seed
	sleep .5; cp @< @>
./two : ./seed
	sleep .5; cp @< @>
fail :
	sleep .1; exit 1
wait :
	sleep 60
''')
	status, output, cold = run(root, runner, ['default'])
	assert status == 0 and (3, 0) in cold and cold[-1] == (0, 0), (status, output)
	status, output, warm = run(root, runner, ['--force', 'default'])
	percentages = [percent for state, percent in warm if state == 1]
	assert status == 0 and percentages and percentages[-1] == 100, (status, output)
	assert any(0 < percent < 100 for percent in percentages), percentages
	assert percentages == sorted(percentages), percentages
	assert warm[-1] == (0, 0), warm
	history = {path: path.read_bytes() for path in (root / '.kame/cache').rglob('*') if path.is_file() and path.read_bytes().startswith(b'KTM1')}
	assert history, 'no duration history persisted'
	assert max(int.from_bytes(data[4:], 'little') for data in history.values()) >= 400, history
	status, output, _ = run(root, runner, ['default'])
	assert status == 0 and all(path.read_bytes() == data for path, data in history.items()), 'cache hits replaced execution timings'
	for args, term, terminal in [(['default'], 'dumb', True), (['--output', 'text', 'default'], 'xterm', True), (['--output', 'json', 'default'], 'xterm', True), (['-n', 'default'], 'xterm', True), (['default'], 'xterm', False)]:
		status, output, sequences = run(root, runner, args, term=term, terminal=terminal)
		assert status == 0 and not sequences, (args, output)
	# TERM changes are legitimate recipe-environment invalidations above.
	history = {path: path.read_bytes() for path in history}
	status, output, sequences = run(root, runner, ['fail'])
	assert status != 0 and any(state == 2 for state, _ in sequences) and sequences[-1] == (0, 0), output
	assert all(path.read_bytes() == data for path, data in history.items()), 'failed execution replaced history'
	for path in history:
		path.write_bytes(b'KTM1' + b'\xff' * 8)
	status, output, sequences = run(root, runner, ['--force', 'default'])
	assert status == 0 and (3, 0) in sequences and sequences[-1] == (0, 0), output
	status, output, sequences = run(root, runner, ['--watch', '--force', 'default'], watch=True)
	assert status != 0 and sequences.count((1, 100)) >= 2 and sequences.count((3, 0)) >= 2 and sequences[-1] == (0, 0), output
	status, output, sequences = run(root, runner, ['wait'], cancel=True)
	assert status != 0 and sequences[-1] == (0, 0), output


if __name__ == '__main__':
	for name, runner in [('native', [sys.argv[1]]), ('wasm', ['node', sys.argv[2]])]:
		check(Path(sys.argv[3]) / name, runner)
		print(f'{name}: OSC progress passed')

# EOF
