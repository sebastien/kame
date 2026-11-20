"""Module: computed_paths — cross-host computed path acceptance without builds."""

from __future__ import annotations

import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time


# -----------------------------------------------------------------------------
# OPERATIONS
# -----------------------------------------------------------------------------

def quote(text: str) -> str:
	# Verbatim strings keep interpolation markers literal.
	assert '"""' not in text
	return '"""' + text + '"""' if '{' in text or '@(' in text else json.dumps(text, ensure_ascii=False)


def walk(value: object):
	if isinstance(value, dict):
		yield value
		for child in value.values():
			yield from walk(child)
	elif isinstance(value, list):
		for child in value:
			yield from walk(child)


def withoutSpans(value: object) -> object:
	if isinstance(value, dict):
		return {key: withoutSpans(child) for key, child in value.items() if key != 'span'}
	if isinstance(value, list):
		return [withoutSpans(child) for child in value]
	return value


def watch(command: list[str], project: Path, environment: dict[str, str]) -> int:
	"""Retain shared consumers while membership moves empty → populated → empty."""
	(project / 'items').mkdir()
	(project / 'other').mkdir()
	(project / 'Makefile.km').write_text(
		'root = "./items"\nmembers = files ${root}/*.txt\n'
		'task one :\n\t(write! ./one (join "," (map str members)))\n'
		'task two :\n\t(write! ./two (join "," (map str members)))\n'
		'task isolated :\n\t(write! ./isolated (join "," (map str (files ./other/*.txt))))\n')
	with (project / 'watch-log').open('w') as log, (project / 'watch-err').open('w') as err:
		process = subprocess.Popen(command + ['--json', '--watch', 'one', 'two', 'isolated'],
			cwd=project, env=environment, stdout=log, stderr=err, start_new_session=True)
		def settled(expected: str, cycles: int) -> None:
			deadline = time.monotonic() + 10
			while time.monotonic() < deadline:
				assert process.poll() is None, 'watch exited before settling'
				# A separate reader must not move the child's stdout file offset.
				events = [json.loads(line) for line in (project / 'watch-log').read_text().splitlines(keepends=True) if line.endswith('\n')]
				# Output writes can become visible while their watch cycle is still active.
				if (events and events[-1].get('type') == 'watch-idle' and events[-1]['cycle'] >= cycles
					and all((project / name).exists() and (project / name).read_text() == expected
						for name in ('one', 'two'))):
					assert not any(event.get('type') == 'diagnostic' for event in events), events
					return
				time.sleep(.025)
			raise AssertionError(('watch did not settle', expected, cycles))
		try:
			settled('', 1)
			isolatedStamp = (project / 'isolated').stat().st_mtime_ns
			(project / 'items/a.txt').write_text('a')
			settled('./items/a.txt', 2)
			assert (project / 'isolated').stat().st_mtime_ns == isolatedStamp, 'unrelated root rebuilt'
			(project / 'items/b.txt').write_text('b')
			settled('./items/a.txt,./items/b.txt', 3)
			(project / 'items/a.txt').unlink()
			settled('./items/b.txt', 4)
			(project / 'items/b.txt').unlink()
			settled('', 5)
			assert (project / 'isolated').stat().st_mtime_ns == isolatedStamp, 'unrelated root rebuilt'
			before = (project / 'watch-log').read_text()
			time.sleep(.3)
			after = (project / 'watch-log').read_text()
			assert after == before, ('settled watch spins while idle', after[len(before):])
			assert not (project / 'watch-err').read_text(), 'watch emitted stderr'
		finally:
			if process.poll() is None:
				os.killpg(process.pid, signal.SIGTERM)
			try:
				process.wait(timeout=3)
			except subprocess.TimeoutExpired:
				os.killpg(process.pid, signal.SIGKILL)
				process.wait(timeout=3)
		assert process.returncode in (143, -signal.SIGTERM), process.returncode
	return 8


# -----------------------------------------------------------------------------
# API
# -----------------------------------------------------------------------------

def main() -> None:
	root, backend, binary, temporary = sys.argv[1:]
	command = [binary] if backend == 'native' else ['node', os.environ.get('KAME_TEST_WASM_WRAPPER', str(Path(root) / 'dist/kame.js'))]
	environment = {key: value for key, value in os.environ.items() if not key.startswith('KAME_') or key == 'KAME_WASM'}
	checks = 0
	with tempfile.TemporaryDirectory(prefix=f'computed-{backend}-', dir=temporary) as directory:
		project = Path(directory)
		(project / 'files').mkdir()
		for name in ('a.txt', 'b.txt', 'literal*.txt', 'literal?.txt'):
			(project / 'files' / name).write_text(name)
		def run(arguments: list[str]) -> subprocess.CompletedProcess[str]:
			return subprocess.run(command + arguments, cwd=project, env=environment,
				text=True, capture_output=True, timeout=20)
		def value(source: str, expected: str, *, lang: str = 'expr', grant: bool = True) -> None:
			nonlocal checks
			result = run(['do', 'run', '--lang', lang, *(['--allow-read=' + directory] if grant else []), '-c', source])
			assert result.returncode == 0 and not result.stderr and result.stdout.rstrip('\n') == expected, (checks, source, result)
			checks += 1
		def reject(source: str, code: str, *, lang: str = 'expr', parse: bool = False) -> None:
			nonlocal checks
			if parse:
				(project / 'invalid.expr').write_text(source)
				result = run(['do', 'parse', '--lang', lang, 'invalid.expr'])
			else:
				result = run(['do', 'run', '--lang', lang, '-c', source])
			assert result.returncode == 1 and code in result.stdout + result.stderr, (source, result)
			checks += 1
		def with_root(body: str, root: str = '"./files"') -> str:
			return f'((root => {body}) {root})'

		all_files = '[./files/a.txt, ./files/b.txt, ./files/literal\\*.txt, ./files/literal\\?.txt]'
		value(with_root('files ${root}/*.txt'), all_files)
		value('root = "./files"\nmembers = files ${root}/*.txt\n(out! (str (count members)))', '4', lang='km')
		value('(count ' + with_root('files ${root}/*.txt') + ')', '4')
		value(with_root('files ${root}/absent*'), '[]')
		value(with_root('files ${root}/a.*'), '[./files/a.txt]')
		value(with_root('[files ${root}/absent*, files ${root}/a.*]'), '[[], [./files/a.txt]]')
		value(with_root('concat (files ${root}/a.*) (files ${root}/absent*) (files ${root}/b.*)'), '[./files/a.txt, ./files/b.txt]')
		value(with_root('{selected: files ${root}/a.*, empty: files ${root}/absent*}'), '{selected: [./files/a.txt], empty: []}')
		for name in ('literal*', 'literal?'):
			value(f'((name => map str (files ./files/${{name}}.*)) {quote(name)})', f'["./files/{name}.txt"]')
			value(f'((name => map str (concat (files ./files/${{name}}.*) [])) {quote(name)})', f'["./files/{name}.txt"]')
		for name in ('{evil:*}', '[]', 'back\\slash', '"quote"', '@()', 'café猫'):
			(project / 'files' / (name + '.txt')).write_text('literal')
			value(f'((name => map read (files ./files/${{name}}.*)) {quote(name)})', '[literal]')
		for char in ('-', ']', '!', '^', '[', '\\'):
			classRoot = project / ('class-' + str(ord(char)))
			classRoot.mkdir()
			for name in ('a', 'z', 'b', 'c', char):
				(classRoot / name).write_text('x')
			prefix = './' + classRoot.name
			wanted = '[' + ', '.join(json.dumps(prefix + '/' + name) for name in sorted({'a', 'z', char})) + ']'
			value(f'((root char => map str (files ${{root}}/[a${{char}}z])) {quote(prefix)} {quote(char)})', wanted)
		# An authored range may use an escaped closing bracket as its endpoint.
		(project / 'range').mkdir()
		for name in ('[', '\\', ']', '^'):
			(project / 'range' / name).write_text('x')
		value(with_root(r'map str (files ${root}/[\[-\]])', '"./range"'), '["./range/[", "./range/\\\\", "./range/]"]')
		for pathRoot in ('./files', '../' + project.name + '/files', str(project / 'files')):
			value(f'((root ext => capture ${{root}}/{{name:*}}.${{ext}} ${{root}}/a.txt) {quote(pathRoot)} "txt")', '{name: "a"}', grant=False)
		value('((root ext => ./files/a-b.txt |> capture ${root}/{first:*}-{second:*}.${ext} |> bind ./out/{second}/{first}) "./files" "txt")', './out/b/a', grant=False)
		value('((root out => ./files/a-a |> capture ${root}/{name:*}-{name:*} |> bind ${out}/{name}) "./files" "./out")', './out/a', grant=False)
		value(with_root('capture ${root}/{name:~[a-z]+} ./files/abc'), '{name: "abc"}', grant=False)
		for payload in ('{evil:*}', '*?[]\\"@()猫', '[a-z]+'):
			value(f'((root ext => match? ${{root}}/{{name:*}}.${{ext}} {quote("./" + payload + "/demo." + payload)}) {quote("./" + payload)} {quote(payload)})', 'true', grant=False)
		value(with_root('"${root}/{name:*}"'), '"./files/{name:*}"', grant=False)
		value('"./files/literal*.txt"', '"./files/literal*.txt"', grant=False)
		value('(if false then (files ./files/*) else "inactive")', '"inactive"', grant=False)
		value('root = "./files"\nunused = files ${root}/*\n(out! "lazy")', 'lazy', lang='km', grant=False)
		reject(with_root('files ${root}/*'), 'CAP_DENIED')
		for operand in ('42', 'true', '[]', '{x: "./files"}', '"files"', '"file://files"', '(parse-json ' + json.dumps(json.dumps('\x00')) + ')'):
			reject(with_root('${root}/{name:*}', operand), 'EXPR_INVALID')
		for operand in ('"files"', '[]'):
			reject(with_root('${root}/*', operand), 'EXPR_INVALID')
		reject('${nop 42}/{name:*}', 'EXPR_INVALID')
		value(with_root('capture ${root}/{a:*}/{b} ./files/dir/item'), '{a: "dir", b: "item"}', grant=False)
		for source in ('./{${NAME}:*}', './{name:${GLOB}}', './{name:~${REGEX}}', '${ROOT}/{name:~(a|)}'):
			reject(source, 'PARSE_ERR', parse=True)
		reject('root = "./"\ninclude ${root}/included.km', 'PARSE_ERR', lang='km', parse=True)
		(project / 'included.km').write_text('message = "literal-include"\n')
		(project / 'parent.km').write_text('include ./included.km\ndefault = message\n')
		includedResult = run(['do', 'run', '--lang', 'km', 'parent.km', 'default'])
		assert includedResult.returncode == 0 and not includedResult.stderr and includedResult.stdout == '"literal-include"', includedResult
		checks += 1
		for lang, boundarySource in (
			('kash', 'printf ./{name:*}\n'),
			('km', 'rule ./out/{name}.o : ./src/{name}.c\n\tprintf unchanged\n')):
			(project / 'boundary').write_text(boundarySource)
			boundary = run(['do', 'parse', '--json', '--lang', lang, 'boundary'])
			assert boundary.returncode == 0 and not boundary.stderr, boundary
			boundaryAst = json.loads(boundary.stdout)['ast']
			assert not any(node.get('kind') == 'interpolation' for node in walk(boundaryAst)), boundaryAst
			if lang == 'kash':
				assert not any(node.get('kind') == 'path-pattern' for node in walk(boundaryAst)), boundaryAst
			boundaryFormat = run(['do', 'fmt', '--lang', lang, 'boundary'])
			assert boundaryFormat.returncode == 0 and not boundaryFormat.stderr and '{name' in boundaryFormat.stdout, boundaryFormat
			checks += 1
		# Inspect the public schema rather than assuming a private AST layout.
		source = '${ROOT}/{one:*}.${EXT}/{two:~[a-z]+}'
		(project / 'source.expr').write_text(source)
		parsed = run(['do', 'parse', '--json', '--lang', 'expr', 'source.expr'])
		assert parsed.returncode == 0 and not parsed.stderr, parsed
		ast = json.loads(parsed.stdout)['ast']
		patterns = [node for node in walk(ast) if node.get('kind') == 'path-pattern']
		assert len(patterns) == 1, ast
		parts = patterns[0]['parts']
		interpolations = [part for part in parts if part['kind'] == 'interpolation']
		assert [part['index'] for part in interpolations] == [0, 1], parts
		assert [part['expr']['text'] for part in interpolations] == ['ROOT', 'EXT'], parts
		assert [(part['span']['start'], part['span']['end']) for part in interpolations] == [(0, 7), (16, 22)], parts
		assert [part['index'] for part in parts if part['kind'] == 'matcher'] == [0, 1], parts
		checks += 1
		formatted = run(['do', 'fmt', '--lang', 'expr', 'source.expr'])
		assert formatted.returncode == 0 and not formatted.stderr and formatted.stdout.strip() == source, formatted
		(project / 'formatted.expr').write_text(formatted.stdout)
		again = run(['do', 'fmt', '--lang', 'expr', 'formatted.expr'])
		assert again.returncode == 0 and not again.stderr and again.stdout == formatted.stdout, again
		reparsed = run(['do', 'parse', '--json', '--lang', 'expr', 'formatted.expr'])
		assert reparsed.returncode == 0 and not reparsed.stderr, reparsed
		assert withoutSpans(json.loads(reparsed.stdout)['ast']) == withoutSpans(ast)
		checks += 1
		watchProject = project / 'watch'
		watchProject.mkdir()
		checks += watch(command, watchProject, environment)
	print(f'{backend}: {checks} computed path checks')


if __name__ == '__main__':
	main()

# EOF
