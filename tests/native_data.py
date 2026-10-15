"""Cross-host typed record, digest and no-follow filesystem acceptance."""

import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time


def main():
	root, backend, binary = sys.argv[1:]
	command = [binary] if backend == 'native' else ['node', str(Path(root) / 'dist/kame.js')]
	environment = {key: value for key, value in os.environ.items() if not key.startswith('KAME_')}
	checks = 0
	with tempfile.TemporaryDirectory() as directory:
		project = Path(directory)
		def run(*args):
			return subprocess.run(command + list(args), cwd=project, env=environment, text=True, capture_output=True, timeout=20)
		def value(expression, expected, *flags):
			nonlocal checks
			result = run('do', 'run', '--print-result', *flags, '-c', expression)
			assert result.returncode == 0 and result.stdout.strip() == expected, (expression, result.stdout, result.stderr)
			checks += 1
		def reject(expression, code='EXPR_INVALID', *flags):
			nonlocal checks
			result = run('do', 'run', *flags, '-c', expression)
			assert result.returncode == 1 and code in result.stderr, (expression, result.stdout, result.stderr)
			checks += 1
		for expression in [
			'(eq (items [z: 1 a: [x: [2 :nil]]]) [[key: "z" value: 1] [key: "a" value: [x: [2 :nil]]]])',
			'(eq (items (set "z" :nil [z: 1 a: 2])) [[key: "z" value: :nil] [key: "a" value: 2]])',
			'(eq (items (set "a" 2 [z: 1])) [[key: "z" value: 1] [key: "a" value: 2]])',
			'(eq (merge) (parse-json "{}"))',
			'(eq (items (merge [z: [x: 1] a: 2] [z: [y: 3] b: 4])) [[key: "z" value: [y: 3]] [key: "a" value: 2] [key: "b" value: 4]])',
			'(let [r [x: 1]] (eq [(get "x" (set "x" 2 r)) r.x] [2 1]))',
			'(eq (apply (get "value" (first (items [f: ([x] x)]))) [42]) [42])',
			'(eq (apply (get "f" (set "f" ([x] 42) [f: ([x] x)])) [0]) 42)',
			'(eq (apply (get "f" (merge [f: ([x] x)] [f: ([x] 42)])) [0]) 42)',
			'(record? (parse-json "{}"))', '(list? [])', '(string? "")',
			'(not (record? []))', '(not (list? :nil))', '(not (string? "{x:*}"))',
			'(not (string? [f: ([x] x)]))', '(not (list? ([x] x)))',
		]: value(expression, ':true')
		for expression in ['(items [])', '(set "x" 1 [])', '(set 0 2 [x: 1])', '(merge [x: 1] [])', '(items (parse-json "{\\"x\\":1,\\"x\\":2}"))', '(set "x" ([x] x) (parse-json "{\\"x\\":1,\\"x\\":2}"))', '(merge (parse-json "{\\"x\\":1,\\"x\\":2}"))', '(digest "md5" "abc")', '(digest "sha1" 42)', '(digest :nil "abc")']:
			reject(expression)
		for algorithm in ['sha1', 'sha256', 'sha512']:
			for text in ['', 'abc', 'é\u0000\n']:
				literal = '(parse-json ' + json.dumps(json.dumps(text)) + ')'
				value(f'(digest "{algorithm}" {literal})', json.dumps(hashlib.new(algorithm, text.encode()).hexdigest()))
			for length in [0, 1, 55, 56, 63, 64, 65, 111, 112, 127, 128, 129, 10000, 1000000]:
				data = bytes((i * 37) % 256 for i in range(length))
				(project / 'data').write_bytes(data)
				value(f'(digest "{algorithm}" (read ./data))', json.dumps(hashlib.new(algorithm, data).hexdigest()), '--allow-read')
		(project / 'anchor').write_text('anchor')
		value('(eq (get "data" (set "data" (read ./data) [data: :nil])) (read ./data))', ':true', '--allow-read')
		value('(not (string? (read ./data)))', ':true', '--allow-read')
		(project / 'Makefile.kmk').write_text('./hash : ./anchor\n\tprintf %s @(digest "sha512" (read ./data)) > @>\n')
		for data in [b'A\x00\xff\n', b'B\x00\xff\n']:
			(project / 'data').write_bytes(data)
			result = run('./hash')
			assert result.returncode == 0, (result.stdout, result.stderr)
			assert (project / 'hash').read_text() == hashlib.sha512(data).hexdigest()
			checks += 1
		(project / 'one').write_text('equal')
		(project / 'two').write_text('equal')
		(project / 'sub').mkdir()
		os.symlink('one', project / 'link')
		os.symlink('absent', project / 'dangling')
		os.symlink('loop', project / 'loop')
		os.symlink('sub', project / 'dir-link')
		os.symlink('\ufeff../one', project / 'bom-link')
		os.symlink('x' * 600, project / 'long-link')
		for expression, expected in [
			('(get "link" (lstat ./link))', ':true'),
			('(get "link" (lstat ./one))', ':false'),
			('(get "dir" (lstat ./dir-link))', ':false'),
			('(get "dir" (stat ./dir-link))', ':true'),
			('(get "link" (lstat ./dangling))', ':true'),
			('(get "link" (lstat ./loop))', ':true'),
			('(readlink ./link)', '"one"'), ('(readlink ./dangling)', '"absent"'),
			('(readlink ./loop)', '"loop"'),
			('(readlink ./bom-link)', '"\ufeff../one"'),
			('(readlink ./long-link)', json.dumps('x' * 600)),
			('(readlink (resource "file://' + str(project / 'link') + '"))', '"one"'),
		]: value(expression, expected, '--allow-read')
		for expression in ['(readlink ./one)', '(readlink ./missing)', '(lstat ./missing)', '(stat ./dangling)']:
			reject(expression, 'FS_ERR', '--allow-read')
		os.symlink('../link', project / 'sub/inner')
		value('(readlink ./dir-link/inner)', '"../link"', '--allow-read')
		value('(get "link" (lstat (resource "file://' + str(project / 'link') + '")))', ':true', '--allow-read')
		for operation in ['lstat', 'readlink']:
			(project / 'Makefile.kmk').write_text(f'@({operation} ./link) :\n\ttouch forbidden\n')
			result = run('do', 'plan', '--json', './any')
			assert result.returncode == 1 and any(code in result.stdout for code in ['PHASE_INVALID', 'CAP_DENIED']), (result.stdout, result.stderr)
			assert not (project / 'forbidden').exists()
			checks += 1
		(project / 'Makefile.kmk').write_text('probe : @(if (get "link" (lstat ./dangling)) [] ["missing"])\n')
		result = run('do', 'plan', '--json', 'probe')
		assert result.returncode == 0, (result.stdout, result.stderr)
		checks += 1
		reject('(readlink ./link)', 'CAP_DENIED')
		reject('(lstat ./link)', 'CAP_DENIED')
		os.symlink(b'\xff', os.fsencode(project / 'invalid-link'))
		reject('(readlink ./invalid-link)', 'FS_ERR', '--allow-read')
		(project / 'Makefile.kmk').write_text('task inspect : ./anchor\n\tprintf %s @(readlink ./link) > seen\n\tprintf x >> runs\n')
		for target in ['one', 'one', 'two']:
			if target == 'two':
				(project / 'link').unlink()
				os.symlink(target, project / 'link')
			result = run('inspect')
			assert result.returncode == 0, (result.stdout, result.stderr)
			assert (project / 'seen').read_text() == target
		assert (project / 'runs').read_text() == 'xx', 'link target observation was not revalidated'
		checks += 1
		# Both old and new targets are absent: content/existence cannot detect this change.
		(project / 'Makefile.kmk').write_text('task watch-link : ./anchor\n\tprintf %s @(readlink ./dangling) > watched\n')
		with (project / 'watch.log').open('w') as log:
			process = subprocess.Popen(command + ['--watch', 'watch-link'], cwd=project, env=environment, stdout=log, stderr=log, start_new_session=True)
			try:
				for target in ['absent', 'also-absent']:
					if target != 'absent':
						(project / 'dangling').unlink()
						os.symlink(target, project / 'dangling')
					deadline = time.monotonic() + 15
					while not (project / 'watched').exists() or (project / 'watched').read_text() != target:
						assert process.poll() is None, (project / 'watch.log').read_text()
						assert time.monotonic() < deadline, (project / 'watch.log').read_text()
						time.sleep(0.05)
				checks += 1
			finally:
				if process.poll() is None: os.killpg(process.pid, signal.SIGTERM)
				try: process.wait(timeout=5)
				except subprocess.TimeoutExpired:
					os.killpg(process.pid, signal.SIGKILL)
					process.wait(timeout=5)
	print(f'{backend}: {checks} native data checks passed')


if __name__ == '__main__':
	main()
