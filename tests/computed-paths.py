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
	# Verbatim strings keep braces and @() as text, not pattern/template syntax.
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
	(project / 'Makefile.kmk').write_text(
		'SHELL = kash\nROOT = "./items"\nFILES = @(ROOT)/*.txt\n'
		'one :\n\t(write! ./one (join "," FILES))\n'
		'two :\n\t(write! ./two (join "," FILES))\n'
		'isolated :\n\t(write! ./isolated (join "," ./other/*.txt))\n')
	with (project / 'watch-log').open('w+') as log, (project / 'watch-err').open('w+') as err:
		process = subprocess.Popen(command + ['--json', '--watch', 'one', 'two', 'isolated'],
			cwd=project, env=environment, stdout=log, stderr=err, start_new_session=True)
		def settled(expected: str, cycles: int) -> None:
			deadline = time.monotonic() + 10
			while time.monotonic() < deadline:
				assert process.poll() is None, 'watch exited before settling'
				log.seek(0)
				events = [json.loads(line) for line in log.read().splitlines() if line.endswith('}')]
				if (sum(event.get('type') == 'watch-idle' for event in events) >= cycles
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
			log.seek(0)
			before = log.read()
			time.sleep(.3)
			log.seek(0)
			assert log.read() == before, 'settled watch spins while idle'
			err.seek(0)
			assert not err.read(), 'watch emitted stderr'
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
	command = [binary] if backend == 'native' else ['node', str(Path(root) / 'dist/kame.js')]
	environment = {key: value for key, value in os.environ.items() if not key.startswith('KAME_')}
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
		allFiles = '["./files/a.txt" "./files/b.txt" "./files/literal*.txt" "./files/literal?.txt"]'
		value('(let [ROOT "./files"] @(ROOT)/*.txt)', allFiles)
		value('ROOT = "./files"\nFILES = @(ROOT)/*.txt\n(out! (count FILES))', '4', lang='km')
		value('(count (let [ROOT "./files"] @(ROOT)/*.txt))', '4')
		value('(let [ROOT "./files"] @(ROOT)/absent*)', '[]')
		value('(let [ROOT "./files"] @(ROOT)/a.*)', '["./files/a.txt"]')
		value('(let [ROOT "./files"] [@(ROOT)/absent* @(ROOT)/a.*])', '[[] ["./files/a.txt"]]')
		value('(let [ROOT "./files"] (concat @(ROOT)/a.* @(ROOT)/absent* @(ROOT)/b.*))', '["./files/a.txt" "./files/b.txt"]')
		value('(let [ROOT "./files"] [files: @(ROOT)/a.* empty: @(ROOT)/absent*])', '[files: ["./files/a.txt"] empty: []]')
		for name in ('literal*', 'literal?'):
			value(f'(let [ROOT "./files" NAME {quote(name)}] @(ROOT)/@(NAME).*)', f'["./files/{name}.txt"]')
			value(f'(let [ROOT "./files" NAME {quote(name)}] (concat @(ROOT)/@(NAME).* []))', f'["./files/{name}.txt"]')
		for name in ('{evil:*}', '[]', 'back\\slash', '"quote"', '@()', 'café猫'):
			(project / 'files' / (name + '.txt')).write_text('literal')
			value(f'(let [ROOT "./files" NAME {quote(name)}] @(ROOT)/@(NAME).*)', '[' + json.dumps('./files/' + name + '.txt', ensure_ascii=False) + ']')
		for char in ('-', ']', '!', '^', '[', '\\'):
			classRoot = project / ('class-' + str(ord(char)))
			classRoot.mkdir()
			for name in ('a', 'z', 'b', 'c', char):
				(classRoot / name).write_text('x')
			prefix = './' + classRoot.name
			wanted = '[' + ' '.join(json.dumps(prefix + '/' + name) for name in sorted({'a', 'z', char})) + ']'
			value(f'(let [ROOT {quote(prefix)} CHAR {quote(char)}] @(ROOT)/[a@(CHAR)z])', wanted)
		# An authored range may use an escaped closing bracket as its endpoint.
		(project / 'range').mkdir()
		for name in ('[', '\\', ']', '^'):
			(project / 'range' / name).write_text('x')
		value(r'(let [ROOT "./range"] @(ROOT)/[\[-\]])', '["./range/[" "./range/\\\\" "./range/]"]')
		for pathRoot in ('./files', '../' + project.name + '/files', str(project / 'files')):
			value(f'(let [ROOT {quote(pathRoot)} EXT "txt"] (match? @(ROOT)/{{name:*}}.@(EXT) {quote(pathRoot + "/a.txt")}))', ':true', grant=False)
		value('(let [ROOT "./files" EXT "txt"] (sub @(ROOT)/{first:*}-{second:*}.@(EXT) ./out/{second}/{first} "./files/a-b.txt"))', '"./out/b/a"', grant=False)
		value('(let [ROOT "./files" OUT "./out"] (sub @(ROOT)/{name:*}-{name:*} @(OUT)/{name} "./files/a-a"))', '"./out/a"', grant=False)
		value('(let [ROOT "./files"] (match? @(ROOT)/{name:~[a-z]+} "./files/abc"))', ':true', grant=False)
		for payload in ('{evil:*}', '*?[]\\"@()猫', '[a-z]+'):
			value(f'(let [ROOT {quote("./" + payload)} EXT {quote(payload)}] (match? @(ROOT)/{{name:*}}.@(EXT) {quote("./" + payload + "/demo." + payload)}))', ':true', grant=False)
		value('(let [ROOT "./files"] "@(ROOT)/{name:*}")', '"./files/{name:*}"', grant=False)
		value('"./files/literal*.txt"', '"./files/literal*.txt"', grant=False)
		value('(if :false (let [ROOT "./files"] @(ROOT)/*) "inactive")', '"inactive"', grant=False)
		value('ROOT = "./files"\nUNUSED = @(ROOT)/*\n(out! "lazy")', 'lazy', lang='km', grant=False)
		reject('(let [ROOT "./files"] @(ROOT)/*)', 'CAP_DENIED')
		for operand in ('42', ':true', '[]', '[x: "./files"]', '"files"', '"file://files"', '(parse-json ' + json.dumps(json.dumps('\x00')) + ')'):
			reject(f'(let [ROOT {operand}] @(ROOT)/{{name:*}})', 'EXPR_INVALID')
		for operand in ('"files"', '[]'):
			reject(f'(let [ROOT {operand}] @(ROOT)/*)', 'EXPR_INVALID')
		reject('./@(42)/{name:*}', 'EXPR_INVALID')
		for source in ('./{@(NAME):*}', './{name:@(GLOB)}', './{name:~@(REGEX)}', '@(ROOT)/{a:*}/{b}', '@(ROOT)/{name:~(a|)}'):
			reject(source, 'PARSE_ERR', parse=True)
		reject('ROOT = "./"\n(include @(ROOT)/included.km)', 'PARSE_ERR', lang='km', parse=True)
		(project / 'included.km').write_text('(out! "literal-include")\n')
		(project / 'parent.km').write_text('(include ./included.km)\n')
		includedResult = run(['do', 'run', '--lang', 'km', 'parent.km'])
		assert includedResult.returncode == 0 and not includedResult.stderr and includedResult.stdout == 'literal-include', includedResult
		checks += 1
		for lang, boundarySource in (
			('kash', 'printf ./{name:*}\n'),
			('rule', './out/{name}.o : ./src/{name}.c\n\tprintf unchanged\n')):
			(project / 'boundary').write_text(boundarySource)
			boundary = run(['do', 'parse', '--json', '--lang', lang, 'boundary'])
			assert boundary.returncode == 0 and not boundary.stderr, boundary
			boundaryAst = json.loads(boundary.stdout)['ast']
			assert not any(node.get('kind') == 'interpolation' for node in walk(boundaryAst)), boundaryAst
			if lang == 'kash':
				assert not any(node.get('kind') == 'pattern' for node in walk(boundaryAst)), boundaryAst
			boundaryFormat = run(['do', 'fmt', '--lang', lang, 'boundary'])
			assert boundaryFormat.returncode == 0 and not boundaryFormat.stderr and '{name' in boundaryFormat.stdout, boundaryFormat
			checks += 1
		# Inspect the public schema rather than assuming a private AST layout.
		source = '@(ROOT)/{one:*}.@(EXT)/{two:~[a-z]+}'
		(project / 'source.expr').write_text(source)
		parsed = run(['do', 'parse', '--json', '--lang', 'expr', 'source.expr'])
		assert parsed.returncode == 0 and not parsed.stderr, parsed
		ast = json.loads(parsed.stdout)['ast']
		patterns = [node for node in walk(ast) if node.get('kind') == 'pattern']
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
