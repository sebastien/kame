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


# Function: uniqueObject
# Reject duplicate JSON fields instead of silently discarding semantic facts.
def uniqueObject(items: list[tuple[str, object]]) -> dict[str, object]:
	result: dict[str, object] = {}
	for key, value in items:
		assert key not in result, ('duplicate JSON field', key)
		result[key] = value
	return result


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
		(project / 'included.kmk').write_text('./out/{name}.o : ./src/{name}.c\n\ttouch forbidden\n')
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
			built = run([*(overrides or []), target])
			assert built.returncode == 1, (built.returncode, built.stdout, built.stderr)
			assert any(item in built.stdout + built.stderr for item in codes), built.stderr
			assert not (project / 'forbidden').exists(), 'registration performed an effect'
			if 'TGT_AMBIG' in codes:
				diagnostics = [json.loads(line)['diagnostic'] for line in result.stdout.splitlines()
					if json.loads(line).get('diagnostic', {}).get('code') == 'TGT_AMBIG']
				assert diagnostics and diagnostics[0].get('related'), result.stdout
				assert diagnostics[0]['related'][0].get('source'), result.stdout
				if source.startswith('(include '):
					assert diagnostics[0]['related'][0]['source'].endswith('included.kmk'), result.stdout
			if 'PHASE_INVALID' in codes:
				assert any(json.loads(line).get('diagnostic', {}).get('tips')
					for line in result.stdout.splitlines()), result.stdout
			return
		assert result.returncode == 0, (arguments, result.stdout, result.stderr)
		plan = json.loads(result.stdout)
		assert plan['producers'], plan
		resources = {resource['id']: resource for resource in plan['resources']}
		assert [resources[item]['display'] for item in plan['producers'][0]['outputs']] == outputs, plan
		assert plan['producers'][0]['target'] == target, plan
		if inputs is not None:
			assert [resources[edge['resource']]['display'] for edge in plan['dependencies']
				if edge['producer'] == 1 and edge.get('group')] == inputs, plan
		assert not (project / 'forbidden').exists(), 'planning ran a recipe'
		if formatting:
			parsed = run(['do', 'parse', '--json', '--lang', 'script', 'Makefile.kmk'])
			assert parsed.returncode == 0, parsed.stderr
			json.loads(parsed.stdout, object_pairs_hook=uniqueObject)
			formatted = run(['do', 'fmt', '--lang', 'script', 'Makefile.kmk'])
			assert formatted.returncode == 0, formatted.stderr
			if '@(' in source or '@{' in source:
				assert '@(' in formatted.stdout or '@{' in formatted.stdout, 'formatter erased computed syntax'
			(project / 'formatted.kmk').write_text(formatted.stdout)
			again = run(['do', 'fmt', '--lang', 'script', 'formatted.kmk'])
			assert again.returncode == 0 and again.stdout == formatted.stdout, again.stderr
			replanned = run(['do', 'plan', '--json', '-f', 'formatted.kmk', *(overrides or []), target])
			assert replanned.returncode == 0, replanned.stderr
			replanned_doc = json.loads(replanned.stdout)
			replanned_resources = {resource['id']: resource for resource in replanned_doc['resources']}
			assert [replanned_resources[item]['display'] for item in replanned_doc['producers'][0]['outputs']] == outputs
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
				f'ROOT = "./{directory}"\ndefault : @{{ROOT}}/demo.o\n'
				'@{ROOT}/{name}.o : ./seed\n\tcat @< > @>\n')
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


def check_output_membership(command: list[str]) -> None:
	with tempfile.TemporaryDirectory() as temporary:
		project = Path(temporary)
		(project / 'seed').write_text('owned')
		(project / 'unrelated').write_text('preserve')
		environment = {key: value for key, value in os.environ.items() if not key.startswith('KAME_')}
		def source(members: str) -> None:
			(project / 'Makefile.kmk').write_text(f'OUTPUTS = [{members}]\n@{{OUTPUTS}} : ./seed\n\tfor path in @>*; do cat @< > "$path"; done\n\tprintf x >> runs\n')
		def build() -> None:
			result = subprocess.run(command + ['./a'], cwd=project, env=environment, text=True, capture_output=True, timeout=20)
			assert result.returncode == 0, (result.stdout, result.stderr)
		source('./a ./b')
		build()
		build()
		assert (project / 'runs').read_text() == 'x'
		(project / 'b').unlink()
		build()
		assert (project / 'b').read_text() == 'owned'
		(project / 'b').write_text('tampered')
		build()
		assert (project / 'b').read_text() == 'owned'
		assert (project / 'runs').read_text() == 'xxx'
		source('./a ./c')
		build()
		assert (project / 'c').read_text() == 'owned'
		assert (project / 'b').read_text() == 'owned', 'removed member was implicitly deleted'
		assert (project / 'unrelated').read_text() == 'preserve'


# Function: main
# Exercise both successful expansion and registration failures before effects.
def main() -> None:
	root, backend, binary = sys.argv[1:]
	command = [binary] if backend == 'native' else ['node', str(Path(root) / 'dist/kame.js')]
	cases = [
		('interpolated capture', 'ROOT = "./build"\n@{ROOT}/{name}.o : ./src/{name}.c\n\tcat @< > @>\n', './build/demo.o',
			dict(outputs=['./build/demo.o'], inputs=['./src/demo.c'], build=True, formatting=True)),
		('arbitrary pure application', '@(str "./build/" "demo.o") : ./src/demo.c\n\tcat @< > @>\n', './build/demo.o',
			dict(outputs=['./build/demo.o'], build=True)),
		('quoted interpolation', 'ROOT = "./build dir"\n"@{ROOT}/{name}.o" : ./src/{name}.c\n\tcat @< > "@>"\n', './build dir/demo.o',
			dict(outputs=['./build dir/demo.o'], inputs=['./src/demo.c'], build=True, formatting=True)),
		('expression-supplied capture', 'PATTERN = (str "./build/" "{" "name" "}.o")\n@{PATTERN} : ./src/{name}.c\n\tcat @< > @>\n', './build/demo.o',
			dict(outputs=['./build/demo.o'], inputs=['./src/demo.c'], build=True)),
		('pattern value output', 'PATTERN = "./build/{name:*}.o"\n@{PATTERN} : ./src/{name}.c\n\tcat @< > @>\n', './build/demo.o',
			dict(outputs=['./build/demo.o'], inputs=['./src/demo.c'], build=True)),
		('nested list order and omissions', 'OUTPUTS = [./one [./two :nil] []]\n@{OUTPUTS} ./three : ./src/demo.c\n\tcat @< > ./one\n\tcat @< > ./two\n\tcat @< > ./three\n', './two',
			dict(outputs=['./one', './two', './three'], build=True)),
		('nil beside literal', '@(nop :nil) ./one :\n', './one', dict(outputs=['./one'])),
		('computed bare task', 'NAME = "chosen"\n@{NAME} :\n\ttouch forbidden\n', 'chosen', dict(outputs=[])),
		('computed cached task', 'NAME = "chosen"\ntask @{NAME} :\n', 'chosen', dict(outputs=[])),
		('nil beside cached task', 'task @(nop :nil) chosen :\n', 'chosen', dict(outputs=[])),
		('pure helper output', '(output name) = (str "./" name)\n@(output "one") :\n', './one', dict(outputs=['./one'])),
		('computed service', 'NAME = "chosen"\nservice @{NAME} :\n', 'chosen', dict(outputs=[])),
		('always file', 'ROOT = "./build"\nalways @{ROOT}/demo.o :\n', './build/demo.o', dict(outputs=['./build/demo.o'])),
		('configured override', 'ROOT = "./old"\n@{ROOT}/{name}.o :\n', './new/demo.o',
			dict(outputs=['./new/demo.o'], overrides=['--define', 'ROOT=./new'])),
	]
	for name in ('plain', 'out file', 'out"quote', 'out\\slash', 'out\ttab', 'out\nline', 'out@(missing)'):
		output = './' + name
		quoted = json.dumps(output).replace('@', '\\@')
		cases.append(('quoted literal ' + repr(name), f'SHELL = kash\n{quoted} : ./src/demo.c\n\tcat @< > @>\n', output,
			dict(outputs=[output], inputs=['./src/demo.c'], build=True, formatting=True)))
	for sibling in ('./second', '@(joinpath ./ "second")'):
		cases.append(('quoted grouped output ' + sibling,
			f'SHELL = kash\n"./out file" {sibling} : ./src/demo.c\n\tcat @< > (nth 0 outputs)\n\tcat @< > (nth 1 outputs)\n',
			'./out file', dict(outputs=['./out file', './second'], build=True, formatting=True)))
	cases.append(('quoted capture', 'SHELL = kash\n"./out dir/{name}.o" : ./src/{name}.c\n\tcat @< > @>\n', './out dir/demo.o',
		dict(outputs=['./out dir/demo.o'], inputs=['./src/demo.c'], build=True, formatting=True)))
	for prefix in ('./out\\dir/', './out\t', './out\\'):
		quoted = json.dumps(prefix + '{name}.o')
		output = prefix + 'demo.o'
		cases.append(('quoted escaped capture ' + repr(prefix),
			f'SHELL = kash\n{quoted} : ./src/{{name}}.c\n\tcat @< > @>\n', output,
			dict(outputs=[output], inputs=['./src/demo.c'], build=True, formatting=True)))
		cases.append(('computed escaped capture ' + repr(prefix),
			f'SHELL = kash\nROOT = {json.dumps(prefix.rstrip("/"))}\n"@{{ROOT}}/{{name}}.o" : ./src/{{name}}.c\n\tcat @< > @>\n', prefix.rstrip('/') + '/demo.o',
			dict(outputs=[prefix.rstrip('/') + '/demo.o'], inputs=['./src/demo.c'], build=True, formatting=True)))
	cases.append(('computed string capture with backslash',
		'SHELL = kash\n@(str "./out\\\\dir/" "{" "name" "}.o") : ./src/{name}.c\n\tcat @< > @>\n', './out\\dir/demo.o',
		dict(outputs=['./out\\dir/demo.o'], inputs=['./src/demo.c'], build=True, formatting=True)))
	cases.append(('computed pattern capture with backslash',
		'SHELL = kash\nPATTERN = ./out\\\\dir/{name:*}.o\n@{PATTERN} : ./src/{name}.c\n\tcat @< > @>\n', './out\\dir/demo.o',
		dict(outputs=['./out\\dir/demo.o'], inputs=['./src/demo.c'], build=True, formatting=True)))
	for header in ('@{PATTERN}', '"@{PATTERN}"'):
		cases.append(('pattern segment provenance ' + header,
			f'SHELL = kash\nPATTERN = ./out\\\\dir/{{name:*}}.o\n{header} : ./src/{{name}}.c\n\tcat @< > @>\n', './out\\dir/demo.o',
			dict(outputs=['./out\\dir/demo.o'], inputs=['./src/demo.c'], build=True, formatting=True)))
		for definition, output in (
			('ROOT = "./out\\\\dir"\nPATTERN = @{ROOT}/{name:*}.o', './out\\dir/demo.o'),
			('PATTERN = ./out\\dir/{name:*}.o', './outdir/demo.o'),
		):
			cases.append(('pattern spelling provenance ' + header + definition,
				f'SHELL = kash\n{definition}\n{header} : ./src/{{name}}.c\n\tcat @< > @>\n', output,
				dict(outputs=[output], inputs=['./src/demo.c'], build=True, formatting=True)))
	for header in ('@{ROOT}{name}.o', '"@{ROOT}{name}.o"'):
		cases.append(('literal-only pattern segment ' + header,
			f'SHELL = kash\nROOT = (pattern "./out\\\\\\\\dir/")\n{header} : ./src/{{name}}.c\n\tcat @< > @>\n', './out\\dir/demo.o',
			dict(outputs=['./out\\dir/demo.o'], inputs=['./src/demo.c'], build=True, formatting=True)))
	for header in ('@{PATTERN}@{TAIL}', '"@{PATTERN}@{TAIL}"', '"@{PATTERN}\\\\tail.o"', '"@(nop [PATTERN TAIL])"'):
		output = './out\\dir/demo' + (' ' if '[PATTERN TAIL]' in header else '') + '\\tail.o'
		cases.append(('mixed pattern and decoded backslashes ' + header,
			f'SHELL = kash\nPATTERN = ./out\\\\dir/{{name:*}}\nTAIL = "\\\\tail.o"\n{header} : ./src/{{name}}.c\n\tcat @< > @>\n', output,
			dict(outputs=[output], inputs=['./src/demo.c'], build=True, formatting=True)))
	cases.append(('pattern escaped braces remain literal',
		'SHELL = kash\nPATTERN = ./out\\{literal\\}/{name:*}.o\n"@{PATTERN}" : ./src/{name}.c\n\tcat @< > @>\n', './out{literal}/demo.o',
		dict(outputs=['./out{literal}/demo.o'], inputs=['./src/demo.c'], build=True, formatting=True)))
	invalid = [
		('quoted logical output', '"chosen" :\n', 'PARSE_ERR'),
		('missing explicit prefix', 'ROOT = "build"\n@{ROOT}/{name}.o :\n', 'PARSE_ERR'),
		('empty list', '@(nop []) :\n', 'EXPR_INVALID'),
		('nil only', '@(nop :nil) :\n', 'EXPR_INVALID'),
		('empty string', '@(str "") :\n', 'EXPR_INVALID'),
		('number', '@(first [42]) :\n', 'EXPR_INVALID'),
		('record', '@(first [[key: "value"]]) :\n', 'EXPR_INVALID'),
		('mixed output kinds', '@(nop [./one "chosen"]) :\n', 'PARSE_ERR'),
		('multiple task outputs', 'task @(nop ["one" "two"]) :\n', 'PARSE_ERR'),
		('file service', 'service @(str "./one") :\n', 'PARSE_ERR'),
		('always logical name', 'always @(str "chosen") :\n', 'PARSE_ERR'),
		('duplicate literal targets', '@(str "./one") :\n./one :\n', 'TGT_AMBIG'),
		('duplicate within rule', '@(nop [./one ./one]) :\n', 'PARSE_ERR'),
		('generated collision', '@(str "./one") :\ngenerate batch = [[kind: "file" target: "./one" inputs: [] order-only: [] recipe: ["touch forbidden"]]]\n', 'TGT_AMBIG'),
		('malformed resolved pattern', '@(str "./" "{" "name") :\n', 'PARSE_ERR'),
		('malformed mixed resolved pattern', 'PATTERN = ./out\\\\dir/{name:*}\n"@{PATTERN}{bad" :\n', 'PARSE_ERR'),
		('mixed pattern cleanup after later failure', 'PATTERN = ./out\\\\dir/{name:*}\n"@{PATTERN}" @(first [42]) :\n', 'EXPR_INVALID'),
		('missing reference', '@{missing} :\n', 'REF_MISSING'),
		('capture unavailable', 'ROOT = (str "./" name)\n@{ROOT}/{name}.o :\n', 'REF_MISSING'),
		('definition cycle', 'ROOT = (OTHER)\nOTHER = (ROOT)\n@{ROOT} :\n', 'DEP_CYCLE'),
		('indirect effect', 'ROOT = (out! "forbidden")\n@{ROOT}/demo.o :\n', 'PHASE_INVALID'),
		('indirect read', 'ROOT = (read ./seed)\n@{ROOT}/demo.o :\n', ('PHASE_INVALID', 'CAP_DENIED')),
		('indirect process', 'ROOT = (sh! "touch forbidden")\n@{ROOT}/demo.o :\n', ('PHASE_INVALID', 'CAP_DENIED')),
		('indirect write', 'ROOT = (write! ./forbidden "bad")\n@{ROOT}/demo.o :\n', ('PHASE_INVALID', 'CAP_DENIED')),
	]
	for name, source, code in invalid:
		cases.append((name, source + 'sentinel :\n\ttouch forbidden\n', 'sentinel', dict(code=code)))
	failures = []
	cases.extend([
		('lazy read output alias', 'OUTPUTS = (text (read ./seed))\n@{OUTPUTS} :\n\ttouch forbidden\n', 'default', {'code': ('PHASE_INVALID', 'CAP_DENIED'), 'overrides': ['--allow-read=.']}),
		('included producer location', '(include ./included.kmk)\n./out/{name}.o : ./src/{name}.cpp\n\ttouch forbidden\n', 'out/demo.o', {'code': 'TGT_AMBIG'}),
		('competing source patterns', './out/{name}.o : ./src/{name}.c\n\ttouch forbidden\n./out/{name}.o : ./src/{name}.cpp\n\ttouch forbidden\n', 'out/demo.o', {'code': 'TGT_AMBIG'}),
		('canonical duplicate outputs', './out/../same :\n\ttouch forbidden\n./same :\n\ttouch forbidden\n', 'same', {'code': 'TGT_AMBIG'}),
		('quoted canonical duplicate', '"./out/../same" :\n\ttouch forbidden\n./same :\n\ttouch forbidden\n', 'same', {'code': 'TGT_AMBIG'}),
		('quoted grouped canonical duplicate', '"./out/../same" ./same :\n\ttouch forbidden\n', 'same', {'code': 'PARSE_ERR'}),
	])
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
	try:
		check_output_membership(command)
	except (AssertionError, subprocess.TimeoutExpired) as error:
		failures.append('output membership')
		print(f'{backend}: FAIL output membership: {error}', file=sys.stderr)
	print(f'{backend}: {len(cases) + 2 - len(failures)}/{len(cases) + 2} computed output cases passed')
	if failures:
		raise SystemExit(1)


if __name__ == '__main__':
	main()

# EOF
