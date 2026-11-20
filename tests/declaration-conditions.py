"""Exercise conditional source composition through both public CLI hosts."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time

root, backend, binary = sys.argv[1:]
root = Path(root)
command = [binary] if backend == 'native' else ['node', os.environ.get('KAME_TEST_WASM_WRAPPER', str(root / 'dist/kame.js'))]
environment = dict(os.environ)
environment.pop('KAME_MODE', None)
cases = 0

with tempfile.TemporaryDirectory() as temporary:
    project = Path(temporary)

    def run(arguments, expected=None, code=None, settings=None, input_text=None):
        global cases
        settings = dict(environment, **(settings or {}))
        result = subprocess.run(command + arguments, cwd=project, env=settings,
            input=input_text, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=20)
        if code is None:
            assert result.returncode == 0, (arguments, result.stdout, result.stderr)
            if expected is not None:
                assert result.stdout == expected, (arguments, result.stdout, result.stderr)
        else:
            assert result.returncode == 1, (arguments, result.returncode, result.stderr)
            assert code in result.stdout + result.stderr, (arguments, result.stdout, result.stderr)
        cases += 1
        return result

    def wait_for_cycle(watcher, watch_log, watch_errors, number):
        deadline = time.monotonic() + 25
        while time.monotonic() < deadline:
            rows = [json.loads(line) for line in watch_log.read_text().split('\n')[:-1]]
            if any(row.get('type') == 'watch-idle' and row['cycle'] == number for row in rows):
                return [row for row in rows if row.get('cycle') == number]
            assert watcher.poll() is None, (rows, watch_errors.read_text())
            time.sleep(0.02)
        raise AssertionError((number, watch_log.read_text(), watch_errors.read_text()))

    def stop_watch(watcher):
        if watcher.poll() is None:
            watcher.send_signal(signal.SIGINT)
        try:
            watcher.wait(timeout=8)
        except subprocess.TimeoutExpired:
            watcher.kill()
            watcher.wait()

    (project / 'config.km').write_text('def selected wanted = MODE == wanted\n')
    (project / 'debug.km').write_text('label = "debug"\ntask chosen\n\t(out! label)\ntask debug-only\n')
    (project / 'release.km').write_text('label = "release"\ntask chosen\n\t(out! label)\ntask release-only\n')
    build = ('MODE ?= "debug"\ninclude ./config.km\n'
        'if selected "debug"\n\tinclude ./debug.km\nelse\n\tinclude ./release.km\n'
        'if false\n\tinclude ./missing.km\n'
        'if false\n\tinclude ./Makefile.km\n'
        'if false\n\tif read "never read"\n\t\tinclude ./another-missing.km\n')
    (project / 'Makefile.km').write_text(build)
    run(['chosen'], 'debug')
    run(['--define', 'MODE=first', '--define', 'MODE=release', 'chosen'], 'release')
    run(['chosen'], 'release', settings={'KAME_MODE': 'release'})
    run(['--define', 'MODE=debug', 'chosen'], 'debug', settings={'KAME_MODE': 'release'})
    run(['--define', 'MODE=', 'chosen'], 'release')
    run(['--define', 'unknown=value', 'chosen'], code='DEF_INVALID')
    # Inspection sees only the selected rule set and never runs its recipes.
    plan = run(['do', 'plan', '--json', 'chosen'])
    assert json.loads(plan.stdout)['producers'][0]['target'] == 'chosen'
    run(['do', 'plan', 'debug-only'])
    run(['do', 'plan', 'release-only'], code='TGT_NO_RULE')
    ast = json.loads(run(['do', 'parse', '--json', '--lang', 'km', 'Makefile.km']).stdout)
    kinds = [item['kind'] for item in ast['ast']['items']]
    assert kinds.count('composition') == 4 and kinds.count('include') == 1
    assert not set(kinds) & {'when', 'otherwise', 'end-when'}
    formatted = run(['do', 'fmt', '--lang', 'km', 'Makefile.km']).stdout
    (project / 'formatted.km').write_text(formatted)
    assert run(['do', 'fmt', '--lang', 'km', 'formatted.km']).stdout == formatted
    run(['-f', 'formatted.km', 'chosen'], 'debug')
    # A selected include supplies configuration to a later predicate.
    (project / 'config-value.km').write_text('enabled = (bool 1)\n')
    (project / 'included.km').write_text('task chosen\n\tprintf included\n')
    (project / 'from-include.km').write_text('include ./config-value.km\nif enabled\n\tinclude ./included.km\n')
    run(['-f', 'from-include.km', 'chosen'], 'included')
    # Paths in conditions use evaluation cwd, not the included source directory.
    (project / 'work').mkdir()
    (project / 'sdk').mkdir()
    (project / 'sdk' / 'selected.km').write_text('task chosen\n\t(out! (abspath ./))\n')
    for predicate in ['(abspath ./)', 'root', '(helper ())']:
        (project / 'sdk' / 'paths.km').write_text(
            'expected ?= ""\nroot = abspath ./\ndef helper () = abspath ./child/..\n'
            f'if (str {predicate}) == expected\n\tinclude ./selected.km\n')
        for directory in ['.', 'work']:
            expected = str(project / directory)
            run(['-C', directory, '-f', str(project / 'sdk' / 'paths.km'),
                '--define', f'expected={expected}', 'chosen'], expected)
            run(['do', 'run', '-C', directory, '-f', str(project / 'sdk' / 'paths.km'),
                '--define', f'expected={expected}', 'chosen'], expected)
    # Inline conditions have the same cwd even though they cannot load includes.
    run(['do', 'run', '-C', 'work', '--print-result', '-l', 'km', '-c',
        f'if (str (abspath ./)) != {json.dumps(str(project / "work"))}\n\tinclude ./missing.km\n42\n'], '42')
    # A lazy helper observes membership in the evaluation directory. Only the
    # selected include resolves beside the authoring file.
    discovery = project / 'work' / 'discovery'
    discovery.mkdir()
    seed = discovery / 'active.seed'
    source = project / 'sdk' / 'discovery.km'
    source.write_text('def has-seed () = count (files ./discovery/*.seed) > 0\n'
                      'if has-seed ()\n\tinclude ./seeded.km\nelse\n\tinclude ./unseeded.km\n')
    (project / 'sdk' / 'seeded.km').write_text('task observed\n\tout! seeded\n')
    (project / 'sdk' / 'unseeded.km').write_text('task observed\n\tout! unseeded\n')
    select_source = ['-C', 'work', '-f', str(source), '--allow-read=.']
    run([*select_source, 'observed'], 'unseeded')
    seed.touch()
    run([*select_source, 'observed'], 'seeded')
    run(['do', 'run', *select_source, 'observed'], 'seeded')
    run([*select_source[:-1], '--allow-run', 'observed'], code='CAP_DENIED')
    (project / 'work' / 'Makefile.km').write_text('include ../sdk/discovery.km\n')
    run(['-C', 'work', 'observed'], 'seeded')
    (project / 'sdk' / 'seeded.km').write_text('task observed\n\tout! forbidden\n'
                                             'rule ./discovery/generated.seed = "output"\n')
    overlap = run([*select_source, 'observed'], code='DISCOVERY_OVERLAP')
    assert 'discovery.km:1:' in overlap.stderr and 'seeded.km:3:' in overlap.stderr, overlap.stderr
    assert overlap.stdout == '' and not (discovery / 'generated.seed').exists(), overlap.stdout
    (project / 'sdk' / 'seeded.km').unlink()

    watch_log = project / 'discovery-watch.jsonl'
    watch_errors = project / 'discovery-watch.err'
    with watch_log.open('w') as events, watch_errors.open('w') as errors:
        watcher = subprocess.Popen([*command, '--watch', '--json', *select_source, 'observed'],
                                   cwd=project, env=environment, stdout=events, stderr=errors)
        try:
            failed = wait_for_cycle(watcher, watch_log, watch_errors, 1)
            assert any(row.get('status') == 'failure' for row in failed), failed
            seed.unlink()
            recovered = wait_for_cycle(watcher, watch_log, watch_errors, 2)
            assert any(row.get('status') == 'success' for row in recovered), recovered
            cases += 1
        finally:
            stop_watch(watcher)

    kinds = project / 'discovery-kinds'
    (kinds / 'input' / 'pkg').mkdir(parents=True)
    for member in ['a.c', 'b.h', 'literal*.h', 'literal-other.h', 'pkg/nested.c']:
        (kinds / 'input' / member).touch()
    (kinds / 'input' / 'link').symlink_to('missing')
    (kinds / 'Makefile.km').write_text(r'''if files ./input/{id:*}.c ./input/*.h ./input/*.c == [./input/a.c, ./input/b.h, ./input/literal\*.h, ./input/literal-other.h]
    task files-ok
if dirs ./input/* == [./input/pkg]
    task dirs-ok
if wildcard :+dir :+symlinks ./input/* == [./input/link, ./input/pkg]
    task mask-ok
if wildcard :-symlinks ./input/* == [./input/a.c, ./input/b.h, ./input/literal\*.h, ./input/literal-other.h, ./input/pkg]
    task excluded-ok
if files [./input/literal\*.h, ./input/link] == [./input/literal\*.h]
    task literal-ok
if wildcard ./input/* == [./input/a.c, ./input/b.h, ./input/link, ./input/literal\*.h, ./input/literal-other.h, ./input/pkg]
    task all-ok
''')
    results = run(['do', 'list', '--json', '-C', str(kinds)])
    assert json.loads(results.stdout) == ['all-ok', 'dirs-ok', 'excluded-ok', 'files-ok', 'literal-ok', 'mask-ok']
    run(['do', 'list', '-C', str(kinds), '--allow-run'], code='CAP_DENIED')
    (kinds / 'overlap.km').write_text('if count (wildcard :+dir ./input/{stem:*}.o) >= 0\n'
                                      '\tgenerate (rule ./input/generated.o = "forbidden")\n')
    overlap = run(['do', 'plan', '-C', str(kinds), '-f', 'overlap.km', './input/generated.o'], code='DISCOVERY_OVERLAP')
    assert './input/{stem:*}.o' in overlap.stderr and './input/generated.o' in overlap.stderr, overlap.stderr
    assert not (kinds / 'input' / 'generated.o').exists()

    # A kind change preserves the unfiltered path set. The directory snapshot
    # must still invalidate source selection when that name changes kind.
    member = kinds / 'input' / 'same'
    member.touch()
    (kinds / 'file.km').write_text('task selected\n\t(write! ./selected "file")\n')
    (kinds / 'directory.km').write_text('task selected\n\t(write! ./selected "directory")\n')
    (kinds / 'Makefile.km').write_text('if in? ./input/same (dirs ./input/*)\n'
                                      '\tinclude ./directory.km\nelse\n\tinclude ./file.km\n')
    with watch_log.open('w') as events, watch_errors.open('w') as errors:
        watcher = subprocess.Popen([*command, '--watch', '--json', '-C', str(kinds), 'selected'],
                                   cwd=project, env=environment, stdout=events, stderr=errors)
        try:
            wait_for_cycle(watcher, watch_log, watch_errors, 1)
            assert (kinds / 'selected').read_text() == 'file'
            member.unlink()
            member.mkdir()
            wait_for_cycle(watcher, watch_log, watch_errors, 2)
            assert (kinds / 'selected').read_text() == 'directory'
            cases += 1
        finally:
            stop_watch(watcher)
    # Includes share a contribution binding within an iteration. Earlier
    # recipes see the final list, while conditions use the accepted prefix.
    (project / 'first-contribution.km').write_text('local-checks ?= ["base"]\n'
        'local-checks += [flavor]\nrule ./checks-${flavor}.json = json local-checks\n'
        'parent-checks += [flavor]\nrule ./inherited-${flavor}.json = json parent-checks\n')
    (project / 'next-contribution.km').write_text('local-checks += [suffix]\n')
    (project / 'local-contributions.km').write_text('parent-checks = ["root"]\nfor flavor in ["debug", "release"]\n'
        '\tinclude ./first-contribution.km\n'
        '\tif count local-checks == 2\n\t\ttask saw-prefix-${flavor}\n'
        '\tlet suffix = str flavor "-later"\n'
        '\tinclude ./next-contribution.km\n\tinclude ./next-contribution.km\n')
    local_checks = ['-f', 'local-contributions.km', '--allow-write=.']
    outputs = [project / 'checks-debug.json', project / 'checks-release.json']
    run([*local_checks, './checks-debug.json', './checks-release.json', './inherited-debug.json',
         './inherited-release.json', 'saw-prefix-debug', 'saw-prefix-release'])
    for output, flavor in zip(outputs, ['debug', 'release']):
        assert json.loads(output.read_text()) == ['base', flavor, f'{flavor}-later', f'{flavor}-later']
        assert json.loads((project / f'inherited-{flavor}.json').read_text()) == ['root', flavor]
    assert json.loads(run(['do', 'run', '-f', 'local-contributions.km', 'parent-checks']).stdout) == ['root']
    run([*local_checks, '--set', 'local-checks=["override"]', './checks-debug.json', './checks-release.json'])
    assert [json.loads(output.read_text()) for output in outputs] == [['override'], ['override']]
    inspected = run(['do', 'plan', '--json', *local_checks, '--set', 'local-checks=["override"]', './checks-debug.json'])
    assert json.loads(inspected.stdout)['producers'][0]['configuration']['local-checks'] == ['override']
    (project / 'bad-contribution.km').write_text('local-checks += [local-checks]\n')
    (project / 'local-cycle.km').write_text('for flavor in ["cycle"]\n'
        '\tinclude ./first-contribution.km\n\tinclude ./bad-contribution.km\n')
    run(['-f', 'local-cycle.km', '--allow-write=.', './checks-cycle.json'], code='DEP_CYCLE')
    assert not (project / 'checks-cycle.json').exists()
    (project / 'bad-contribution.km').write_text('local-checks += 42\n')
    bad_contribution = run(['-f', 'local-cycle.km', '--allow-write=.', './checks-cycle.json'], code='EXPR_INVALID')
    assert 'bad-contribution.km:1:' in bad_contribution.stderr, bad_contribution.stderr
    # Required active includes, active cycles and bad branch syntax still fail.
    (project / 'missing-active.km').write_text('if true\n\tinclude ./absent.km\ntask chosen\n\ttouch should-not-run\n')
    run(['-f', 'missing-active.km', 'chosen'], code='FS_ERR')
    assert not (project / 'should-not-run').exists()
    (project / 'cycle.km').write_text('if true\n\tinclude ./cycle.km\n')
    run(['-f', 'cycle.km'], code='DEP_CYCLE')
    invalid = ['if\n', 'else\n\tinclude ./absent.km\n', 'elif true\n\tinclude ./absent.km\n',
        'if true\ninclude ./absent.km\n',
        'if true\n\tlet x = 1\nelse\n\tlet x = 2\nelse\n\tlet x = 3\n',
        'if false\n\tbad = (\n',
        'include\n', 'include ./a.km ./b.km\n', 'include ""\n',
        'include ./a*.km\n', 'include "a\\x00b"\n',
        'include path\n', 'include (str "./" "a.km")\n',
        'include "${path}"\n', 'include ./${path}\n',
        'if false\n\tinclude path\n']
    for text in invalid:
        (project / 'invalid.km').write_text(text)
        run(['-f', 'invalid.km'], code='PARSE_ERR')
    # Predicates have no host authority; registration failures precede effects.
    for predicate, code in [('(sh! "touch forbidden")', 'CAP_DENIED'), ('(read "forbidden")', 'CAP_DENIED'), ('(out! "forbidden")', 'PHASE_INVALID')]:
        (project / 'unsafe.km').write_text(f'if {predicate}\n\tinclude ./included.km\ntask chosen\n\ttouch should-not-run\n')
        result = run(['-f', 'unsafe.km', 'chosen'], code=code)
        assert 'unsafe.km:1:' in result.stderr, result.stderr
        assert not (project / 'forbidden').exists() and not (project / 'should-not-run').exists()
    # Selected declarations enter the shared scope; inactive duplicates do not.
    (project / 'answer.km').write_text('answer = (first (list 42))\n')
    (project / 'zero.km').write_text('answer = (first (list 0))\n')
    value = 'MODE = "debug"\nif MODE == "debug"\n\tinclude ./answer.km\nelse\n\tinclude ./zero.km\n'
    (project / 'value.km').write_text(value)
    run(['do', 'run', '-f', 'value.km', 'answer'], '42')
    inline_value = 'MODE = "debug"\nanswer = if MODE == "debug" then (first [42]) else (first [0])\nanswer\n'
    run(['do', 'run', '--print-result', '-l', 'km', '-c', inline_value], '42')
    run(['do', 'run', '--print-result', '-l', 'km', '-f', '-'], '42', input_text=inline_value)
    (project / 'first.km').write_text('MODE = "debug"\n')
    (project / 'selected.km').write_text('answer = 42\n')
    (project / 'unselected.km').write_text('answer = 0\n')
    second = 'if MODE == "debug"\n\tinclude ./selected.km\nelse\n\tinclude ./unselected.km\n'
    (project / 'second.km').write_text(second)
    run(['do', 'run', '-f', 'first.km', '-f', 'second.km', 'answer'], '42')
    run(['do', 'run', '--print-result', '-f', 'first.km', '-l', 'km', '-c', '(if MODE == "debug" then 42 else 0)\n'], '42')
    inline_build = 'task chosen\n\t(out! (if true then "inline" else "wrong"))\n'
    run(['do', 'run', '-l', 'km', '-c', inline_build, 'chosen'], 'inline')
    # Reached relative includes require a file-backed source.
    run(['do', 'run', '-l', 'km', '-c', 'if true\n\tinclude ./missing.km\n'], code='RES_INVALID')
    run(['do', 'run', '--print-result', '-l', 'km', '-c', 'if false\n\tinclude ./missing.km\n42\n'], '42')
    (project / 'bad-predicate.km').write_text('# é keeps byte offsets honest\nif missing == 1\n\tinclude ./included.km\n')
    result = run(['--json', '-f', 'bad-predicate.km', 'chosen'], code='REF_MISSING')
    diagnostic = [json.loads(line)['diagnostic'] for line in result.stdout.splitlines() if json.loads(line).get('type') == 'diagnostic'][0]
    assert diagnostic['source'].endswith('bad-predicate.km')
    assert diagnostic['span']['start'] == len('# é keeps byte offsets honest\nif '.encode())

    # Failing operands keep their own origins, not the outer selecting condition.
    definitions = '# é source origins\nAVAILABLE = pattern "{~[}"\nMODULES = AVAILABLE\ndef selected () = MODULES\n'
    (project / 'trace-defs.km').write_text(definitions)
    selecting = 'include ./trace-defs.km\nif (selected ())\n\tinclude ./absent.km\n'
    (project / 'trace-main.km').write_text(selecting)
    result = run(['--json', 'do', 'run', '-f', 'trace-main.km'], code='PAT_INVALID')
    diagnostic = next(json.loads(line)['diagnostic'] for line in result.stdout.splitlines() if json.loads(line).get('type') == 'diagnostic')
    assert diagnostic['source'].endswith('trace-defs.km'), diagnostic
    assert definitions.encode()[diagnostic['span']['start']:diagnostic['span']['end']] == b'pattern "{~[}"', diagnostic
    assert [frame['kind'] for frame in diagnostic['frames']] == ['operation', 'definition', 'definition', 'call', 'condition', 'include'], diagnostic
    assert [frame['label'] for frame in diagnostic['frames'][:4]] == ['pattern', 'AVAILABLE', 'MODULES', 'selected'], diagnostic
    assert diagnostic['frames'][3]['span']['start'] == selecting.index('selected'), diagnostic
    for display in ['human', 'plain']:
        result = run(['--color', 'never', '--diagnostic-format', display, 'do', 'run', '-f', 'trace-main.km'], code='PAT_INVALID')
        rail = '│' if display == 'human' else '|'
        for excerpt in ['trace-defs.km:2:13', 'trace-defs.km:3:1', 'trace-main.km:2:5', f'2 {rail} AVAILABLE = pattern "{{~[}}"', '#0  operation pattern', '#4  condition source selection', 'Trace (innermost first)']:
            assert excerpt in result.stderr, (excerpt, result.stderr)
        assert '\x1b' not in result.stderr
        assert result.stderr.count('AVAILABLE = pattern') == 1, result.stderr
        assert result.stderr.count('if (selected ())') == 1, result.stderr
        assert len(result.stderr.splitlines()) < 18, result.stderr
    result = run(['--color', 'always', '--diagnostic-format', 'human', 'do', 'run', '-f', 'trace-main.km'], code='PAT_INVALID')
    for decoration in ['╭─', '├─', '╰─', '╯', '│', '\x1b[36m', '\x1b[33m', '\x1b[35m', '\x1b[1;31m']:
        assert decoration in result.stderr, (decoration, result.stderr)
    # Earlier explicit inputs retain their own origins too, without an include.
    (project / 'trace-second.km').write_text('# later input\nif (selected ())\n\tinclude ./absent.km\n')
    result = run(['--json', 'do', 'run', '-f', 'trace-defs.km', '-f', 'trace-second.km'], code='PAT_INVALID')
    diagnostic = next(json.loads(line)['diagnostic'] for line in result.stdout.splitlines() if json.loads(line).get('type') == 'diagnostic')
    assert diagnostic['source'].endswith('trace-defs.km'), diagnostic
    assert diagnostic['frames'][-1]['source'].endswith('trace-second.km'), diagnostic
    assert not any(frame['kind'] == 'include' for frame in diagnostic['frames']), diagnostic
    # Both hosts render the same complete, uncoloured source-backed trace.
    result = run(['--color', 'never', '--diagnostic-format', 'plain', 'do', 'run', '-l', 'km', '-c', 'if (first 1)\n\tinclude ./absent.km'], code='EXPR_INVALID')
    assert result.stderr == '''error[EXPR_INVALID]: `first` argument 1 expects list or string; got int
  --> <command:1>:1:11
 1 | if (first 1)
   |           ^

Trace (innermost first)
  #0  operation first at <command:1>:1:5
  #1  condition source selection at <command:1>:1:4
''', result.stderr

    point = run(['--color', 'never', '--diagnostic-format', 'human', 'do', 'run', '-l', 'expr', '-c', '(first 1)'], code='EXPR_INVALID')
    assert '│        ╵' in point.stderr and '^' not in point.stderr, point.stderr

    # Long excerpts keep the failure in one clipped viewport, without broken UTF-8.
    for padding in ['x' * 120, '界' * 30 + 'e\u0301' * 10]:
        expression = f'((unused => first 1) "{padding}")'
        result = run(['--color', 'never', '--diagnostic-format', 'plain', 'do', 'run', '-l', 'expr', '-c', expression], code='EXPR_INVALID')
        excerpt = next(line for line in result.stderr.splitlines() if line.startswith(' 1 | '))
        assert '...' in excerpt and 'first 1' in excerpt, result.stderr
        assert '\ufffd' not in result.stderr and len(excerpt) <= 80, result.stderr

    # Ordinary truthiness, multi-pair selection, nested bodies and nil/no-else.
    for predicate, expected in [('false', '0'), ('nil', '0'), ('true', '42'),
            ('0', '42'), ('42', '42'), ('""', '42'), ('"text"', '42')]:
        (project / 'truth.km').write_text(f'if {predicate}\n\tinclude ./answer.km\nelse\n\tinclude ./zero.km\n')
        run(['do', 'run', '-f', 'truth.km', 'answer'], expected)
    (project / 'pairs.km').write_text('if false\n\tinclude ./absent.km\nelif 0\n\tif true\n\t\tinclude ./answer.km\nelse\n\tinclude ./zero.km\n')
    run(['do', 'run', '-f', 'pairs.km', 'answer'], '42')
    (project / 'nil.km').write_text('if false\n\tinclude ./absent.km\nif true\n\tlet unused = nil\nelse\n\tinclude ./absent.km\nanswer = 42\n')
    run(['do', 'run', '-f', 'nil.km', 'answer'], '42')

    # Only earlier declarations feed composition; ordinary forward refs remain lazy.
    (project / 'earlier.km').write_text('enabled = true\ndef selected () = enabled\nunused = read "forbidden"\nif (selected ())\n\tinclude ./answer.km\n')
    run(['do', 'run', '-f', 'earlier.km', 'answer'], '42')
    (project / 'forward.km').write_text('if enabled\n\tinclude ./answer.km\nenabled = true\n')
    run(['do', 'run', '-f', 'forward.km'], code='REF_MISSING')
    (project / 'predicate-cycle.km').write_text('a = b\nb = a\nif a\n\tinclude ./answer.km\n')
    run(['do', 'run', '-f', 'predicate-cycle.km'], code='DEP_CYCLE')
    sentinel = 'task sentinel\n\t(out! "must-not-run")\n'
    (project / 'duplicate.km').write_text(sentinel + 'include ./answer.km\ninclude ./answer.km\n')
    result = run(['do', 'run', '-f', 'duplicate.km', 'sentinel'], code='DEF_INVALID')
    assert not result.stdout

    # Reserved call heads cannot become runtime calls, even in unused definitions
    # and inactive branches. Every authored placement is validated before effects.
    misplaced = ['unused = (include ./absent.km)\n',
        'def unused () = (include? ./absent.km)\n',
        'unused = if false then (str (include ./absent.km)) else 42\n',
        'if (include ./absent.km)\n\tlet value = nil\n',
        'unused = [(include ./absent.km)]\n', 'unused = {x: (include? ./absent.km)}\n',
        'unused = "${include ./absent.km}"\n',
        'unused = if true then (include ./answer.km) else 42\n']
    for text in misplaced:
        (project / 'misplaced.km').write_text(sentinel + text)
        result = run(['do', 'run', '-f', 'misplaced.km', 'sentinel'], code='PARSE_ERR')
        assert not result.stdout
    run(['do', 'run', '-l', 'expr', '-c', '(include ./absent.km)'], code='PARSE_ERR')
    run(['do', 'run', '-l', 'expr', '-c', '(include? ./absent.km)'], code='PARSE_ERR')
    for head in ['include', 'include?']:
        run(['do', 'run', '-l', 'km', '-c', f'def {head} path = 42\n({head} ./absent.km)\n'], code='PARSE_ERR')

print(f'{backend}: {cases} public conditional declaration cases passed')
