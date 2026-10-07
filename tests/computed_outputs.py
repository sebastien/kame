"""Module: computed_outputs — public CLI acceptance for resolved rule outputs."""

from __future__ import annotations

import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time


# Function: check
# Run one isolated source fixture; failures cannot contaminate another case.
def check(command: list[str], source: str, target: str, *,
	outputs: list[str] | None = None, inputs: list[str] | None = None,
	code: str | tuple[str, ...] | None = None, overrides: list[str] | None = None,
	build: bool = False, formatting: bool = False) -> None:
	with tempfile.TemporaryDirectory() as temporary:
		project = Path(temporary)
		(project / 'Makefile.kmk').write_text(source)
		(project / 'src').mkdir()
		(project / 'src/demo.c').write_text('computed-output-ok')
		(project / 'seed').write_text('seed')
		environment = {key: value for key, value in os.environ.items()
			if not key.startswith('KAME_')}

		def run(arguments: list[str]) -> subprocess.CompletedProcess[str]:
			return subprocess.run(command + arguments, cwd=project, env=environment,
				text=True, capture_output=True, timeout=20)

		arguments = ['do', 'plan', '--json', *(overrides or []), target]
		result = run(arguments)
		if code is not None:
			codes = (code,) if isinstance(code, str) else code
			assert result.returncode == 1, (result.returncode, result.stdout, result.stderr)
			assert any(item in result.stdout + result.stderr for item in codes), (code, result.stdout, result.stderr)
			assert not result.stderr, result.stderr
			assert any(json.loads(line).get('diagnostic', {}).get('source', '').endswith('Makefile.kmk')
				for line in result.stdout.splitlines()), result.stdout
			built = run([target])
			assert built.returncode == 1, (built.returncode, built.stdout, built.stderr)
			assert any(item in built.stdout + built.stderr for item in codes), built.stderr
			assert not (project / 'forbidden').exists(), 'registration performed an effect'
			return
		assert result.returncode == 0, (arguments, result.stdout, result.stderr)
		plan = json.loads(result.stdout)
		assert plan['outputs'] == outputs, plan
		if inputs is not None:
			assert plan['inputs'] == inputs, plan
		assert not (project / 'forbidden').exists(), 'planning ran a recipe'
		if formatting:
			formatted = run(['do', 'fmt', '--lang', 'script', 'Makefile.kmk'])
			assert formatted.returncode == 0, formatted.stderr
			assert '@(' in formatted.stdout, 'formatter erased computed syntax'
			(project / 'formatted.kmk').write_text(formatted.stdout)
			again = run(['do', 'fmt', '--lang', 'script', 'formatted.kmk'])
			assert again.returncode == 0 and again.stdout == formatted.stdout, again.stderr
			replanned = run(['do', 'plan', '--json', '-f', 'formatted.kmk', *(overrides or []), target])
			assert replanned.returncode == 0, replanned.stderr
			assert json.loads(replanned.stdout)['outputs'] == outputs
		if build:
			built = run([*(overrides or []), target])
			assert built.returncode == 0, (built.stdout, built.stderr)
			for output in outputs or []:
				assert (project / output).read_text() == 'computed-output-ok', output


# Function: check_watch
# Source recompilation replaces the computed target index for a retained root.
def check_watch(command: list[str]) -> None:
	with tempfile.TemporaryDirectory() as temporary:
		project = Path(temporary)
		(project / 'seed').write_text('watch-ok')
		def source(directory: str) -> None:
			(project / 'Makefile.kmk').write_text(
				f'ROOT = "./{directory}"\ndefault : @(ROOT)/demo.o\n'
				'@(ROOT)/{name}.o : ./seed\n\tcat @< > @>\n')
		source('one')
		environment = {key: value for key, value in os.environ.items()
			if not key.startswith('KAME_')}
		with (project / 'log').open('w+') as log:
			process = subprocess.Popen(command + ['--watch', 'default'], cwd=project,
				env=environment, stdout=log, stderr=log, start_new_session=True)
			try:
				for directory in ('one', 'two'):
					if directory == 'two':
						source(directory)
					deadline = time.monotonic() + 15
					artifact = project / directory / 'demo.o'
					while not artifact.exists() or artifact.read_text() != 'watch-ok':
						assert process.poll() is None, (project / 'log').read_text()
						assert time.monotonic() < deadline, (project / 'log').read_text()
						time.sleep(0.05)
					assert artifact.read_text() == 'watch-ok'
			finally:
				if process.poll() is None:
					os.killpg(process.pid, signal.SIGTERM)
				try:
					process.wait(timeout=5)
				except subprocess.TimeoutExpired:
					os.killpg(process.pid, signal.SIGKILL)
					process.wait(timeout=5)


# Function: main
# Exercise both successful expansion and registration failures before effects.
def main() -> None:
	root, backend, binary = sys.argv[1:]
	command = [binary] if backend == 'native' else ['node', str(Path(root) / 'dist/kame.js')]
	cases = [
		('interpolated capture', 'ROOT = "./build"\n@(ROOT)/{name}.o : ./src/{name}.c\n\tcat @< > @>\n', './build/demo.o',
			dict(outputs=['./build/demo.o'], inputs=['./src/demo.c'], build=True, formatting=True)),
		('arbitrary pure application', '@(cat "./build/" "demo.o") : ./src/demo.c\n\tcat @< > @>\n', './build/demo.o',
			dict(outputs=['./build/demo.o'], build=True)),
		('quoted interpolation', 'ROOT = "./build dir"\n"@(ROOT)/{name}.o" : ./src/{name}.c\n\tcat @< > "@>"\n', './build dir/demo.o',
			dict(outputs=['./build dir/demo.o'], inputs=['./src/demo.c'], build=True, formatting=True)),
		('expression-supplied capture', 'PATTERN = (cat "./build/" "{" "name" "}.o")\n@(PATTERN) : ./src/{name}.c\n\tcat @< > @>\n', './build/demo.o',
			dict(outputs=['./build/demo.o'], inputs=['./src/demo.c'], build=True)),
		('pattern value output', 'PATTERN = "./build/{name:*}.o"\n@(PATTERN) : ./src/{name}.c\n\tcat @< > @>\n', './build/demo.o',
			dict(outputs=['./build/demo.o'], inputs=['./src/demo.c'], build=True)),
		('nested list order and omissions', 'OUTPUTS = [./one [./two :nil] []]\n@(OUTPUTS) ./three : ./src/demo.c\n\tcat @< > ./one\n\tcat @< > ./two\n\tcat @< > ./three\n', './two',
			dict(outputs=['./one', './two', './three'], build=True)),
		('nil beside literal', '@(:nil) ./one :\n', './one', dict(outputs=['./one'])),
		('computed bare task', 'NAME = "chosen"\n@(NAME) :\n\ttouch forbidden\n', 'chosen', dict(outputs=['chosen'])),
		('computed cached task', 'NAME = "chosen"\ntask @(NAME) :\n', 'chosen', dict(outputs=['chosen'])),
		('nil beside cached task', 'task @(:nil) chosen :\n', 'chosen', dict(outputs=['chosen'])),
		('pure helper output', '(output name) = (cat "./" name)\n@(output "one") :\n', './one', dict(outputs=['./one'])),
		('computed service', 'NAME = "chosen"\nservice @(NAME) :\n', 'chosen', dict(outputs=['chosen'])),
		('always file', 'ROOT = "./build"\nalways @(ROOT)/demo.o :\n', './build/demo.o', dict(outputs=['./build/demo.o'])),
		('configured override', 'ROOT = "./old"\n@(ROOT)/{name}.o :\n', './new/demo.o',
			dict(outputs=['./new/demo.o'], overrides=['--define', 'ROOT=./new'])),
	]
	invalid = [
		('missing explicit prefix', 'ROOT = "build"\n@(ROOT)/{name}.o :\n', 'PARSE_ERR'),
		('empty list', '@([]) :\n', 'EXPR_INVALID'),
		('nil only', '@(:nil) :\n', 'EXPR_INVALID'),
		('empty string', '@(cat "") :\n', 'EXPR_INVALID'),
		('number', '@(first [42]) :\n', 'EXPR_INVALID'),
		('record', '@(first [[key: "value"]]) :\n', 'EXPR_INVALID'),
		('mixed output kinds', '@([./one "chosen"]) :\n', 'PARSE_ERR'),
		('multiple task outputs', 'task @(["one" "two"]) :\n', 'PARSE_ERR'),
		('file service', 'service @(cat "./one") :\n', 'PARSE_ERR'),
		('always logical name', 'always @(cat "chosen") :\n', 'PARSE_ERR'),
		('duplicate literal targets', '@(cat "./one") :\n./one :\n', 'TGT_AMBIG'),
		('duplicate within rule', '@([./one ./one]) :\n', 'PARSE_ERR'),
		('generated collision', '@(cat "./one") :\ngenerate batch = [[kind: "file" target: "./one" inputs: [] order-only: [] recipe: ["touch forbidden"]]]\n', 'TGT_AMBIG'),
		('malformed resolved pattern', '@(cat "./" "{" "name") :\n', 'PARSE_ERR'),
		('missing reference', '@(missing) :\n', 'REF_MISSING'),
		('capture unavailable', 'ROOT = (cat "./" name)\n@(ROOT)/{name}.o :\n', 'REF_MISSING'),
		('definition cycle', 'ROOT = (OTHER)\nOTHER = (ROOT)\n@(ROOT) :\n', 'DEP_CYCLE'),
		('indirect effect', 'ROOT = (out "forbidden")\n@(ROOT)/demo.o :\n', 'PHASE_INVALID'),
		('indirect read', 'ROOT = (read ./seed)\n@(ROOT)/demo.o :\n', ('PHASE_INVALID', 'CAP_DENIED')),
		('indirect process', 'ROOT = (shell "touch forbidden")\n@(ROOT)/demo.o :\n', ('PHASE_INVALID', 'CAP_DENIED')),
		('indirect write', 'ROOT = (write ./forbidden "bad")\n@(ROOT)/demo.o :\n', ('PHASE_INVALID', 'CAP_DENIED')),
	]
	for name, source, code in invalid:
		cases.append((name, source + 'sentinel :\n\ttouch forbidden\n', 'sentinel', dict(code=code)))
	failures = []
	for name, source, target, options in cases:
		try:
			check(command, source, target, **options)
		except (AssertionError, subprocess.TimeoutExpired) as error:
			failures.append(name)
			print(f'{backend}: FAIL {name}: {error}', file=sys.stderr)
	try:
		check_watch(command)
	except (AssertionError, subprocess.TimeoutExpired) as error:
		failures.append('watch target replacement')
		print(f'{backend}: FAIL watch target replacement: {error}', file=sys.stderr)
	print(f'{backend}: {len(cases) + 1 - len(failures)}/{len(cases) + 1} computed output cases passed')
	if failures:
		raise SystemExit(1)


if __name__ == '__main__':
	main()

# EOF
