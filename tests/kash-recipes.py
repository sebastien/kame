"""Kash recipe and function ownership checks shared by native and WASM hosts."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

root, backend, binary = sys.argv[1:4]
part = sys.argv[4] if len(sys.argv) > 4 else 'all'
if part not in ('all', 'values', 'lifecycle'):
    raise SystemExit('expected all, values or lifecycle')
root = Path(root)
command = [binary] if backend == 'native' else ['node', str(root / ('src/js/kame.js' if os.environ.get('KAME_WASM') else 'dist/kame.js'))]
cases = 0
with tempfile.TemporaryDirectory() as temporary:
    project = Path(temporary)
    environment = dict(os.environ, MODE='ambient')
    def invoke(arguments, expected=None, code=None):
        global cases
        result = subprocess.run(command + arguments, cwd=project, env=environment,
                                text=True, capture_output=True, timeout=15)
        if code:
            assert result.returncode == 1 and code in result.stdout + result.stderr, (arguments, result)
        else:
            assert result.returncode == 0, (arguments, result)
            if expected is not None:
                assert result.stdout == expected, (arguments, result)
        cases += 1
        return result
    def recipe(text, args=None, expected=None, code=None):
        (project / 'Makefile.km').write_text(text)
        return invoke(args or ['chosen'], expected, code)
    def script(text, expected=None, code=None):
        return invoke(['do', 'run', '--allow-run', '--allow-read', '--allow-write', '-l', 'kash', '-c', text], expected, code)

    def check_deadline():
        recipe('task chosen\n\tsleep .08\n\tsleep .08\n', ['--timeout', '120', 'chosen'], code='RECIPE_TIMEOUT')
    if part == 'lifecycle':
        check_deadline()
        print(f'{backend}: {cases} Kash deadline cases passed')
        raise SystemExit(0)

    # Recipes use the direct Kash grammar by default.
    recipe('def command ()\n\tprintf generated\ntask chosen\n\t(command ())\n', expected='generated')
    recipe('name = "build value"\ntask chosen\n\tprintf %s "$name"\n', expected='build value')
    recipe('task chosen\n\tprintf %s $(printf captured)\n', expected='captured')
    recipe('task chosen\n\tprintf %s $(printf value)\n\tprintf %s $(printf value)\n', expected='valuevalue')
    recipe('task chosen\n\tprintf first\n\tprintf second | cat\n', expected='firstsecond')
    recipe('task chosen\n\tprintf forbidden\n\tif true\n', code='PARSE_ERR')
    recipe('task chosen\n\tprintf forbidden\n\tif true\n', ['--dry-run', 'chosen'], code='PARSE_ERR')
    assert not (project / 'forbidden').exists()

    # File rules prepare directories, wait for async commands, and verify outputs.
    recipe('rule ./nested/result\n\tprintf artifact > $outputs.0\n', ['./nested/result'])
    assert (project / 'nested/result').read_text() == 'artifact'
    recipe('rule ./async-result\n\tsh -c "sleep .03; printf async > async-result" &\n', ['./async-result'])
    assert (project / 'async-result').read_text() == 'async'
    if part == 'all':
        check_deadline()
    recipe('task chosen\n\tsh -c "if test -f retried; then printf success; else touch retried; exit 1; fi"\n', ['--retry', '1', 'chosen'], expected='success')

    (project / 'fresh-input').write_text('input')
    file_recipe = 'rule ./fresh-output : ./fresh-input\n\tsh -c "printf x >> file-runs; printf result > fresh-output"\n'
    recipe(file_recipe, ['./fresh-output'])
    recipe(file_recipe, ['./fresh-output'])
    assert (project / 'file-runs').read_text() == 'x'
    recipe('SHELL = "/bin/sh"\n' + file_recipe, ['./fresh-output'])
    assert (project / 'file-runs').read_text() == 'xx'
    unbound = 'rule ./unbound\n\tsh -c "printf x >> unbound-runs; printf result > unbound"\n'
    recipe(unbound, ['./unbound'])
    recipe(unbound, ['./unbound'])
    assert (project / 'unbound-runs').read_text() == 'xx'

    # Environments inherit through prerequisites; shell overrides are local.
    recipe('task chosen : child ; {env: {MODE: "parent"}}\n\tprintenv MODE\ntask child ; {shell: "/bin/sh"}\n\tprintf %s "$MODE"\n', expected='parentparent\n')
    recipe('task chosen : child\n\tprintf parent\ntask child\n\tprintf child\n', expected='childparent')
    recipe('SHELL = $(touch forbidden)\ntask chosen\n\ttrue\n', code='PHASE_INVALID')
    assert not (project / 'forbidden').exists()
    recipe('task chosen ; {env: {MODE: "rule"}}\n\tprintenv MODE\n\tMODE: command printenv MODE\n', expected='rule\ncommand\n')

    # Declarations are inert, and calls retain their lexical scope and arguments.
    script('def unused ()\n\ttouch forbidden\nprintf constructed', expected='constructed')
    assert not (project / 'forbidden').exists()
    script('let name = "lexical"\ndef show ()\n\tprintf %s $name\nif true\n\tlet name = "caller"\n\t(show ())\nprintf done', expected='lexicaldone')
    script('def show words...\n\tprintf %s (join "-" words)\nshow one two\nprintf done', expected='one-twodone')
    script('def show ()\n\tprintf x\n\tsleep .01\n\tprintf y\nshow\nshow\nprintf done', expected='xyxydone')
    script('def show ()\n\tlet value = 42\n\tprintf %s $value\nshow\nshow\nprintf done', expected='4242done')
    script('def show ()\n\tprintf x &\n\tprintf y\nshow\nprintf done')
    script('def stop ()\n\tfalse\n\ttouch forbidden\nstop', code='RECIPE_FAIL')
    script('def bad ()\n\tprintf x\n\tif true', code='PARSE_ERR')
    assert not (project / 'forbidden').exists()
    script('let value = 42\n(value ())', code='EXPR_INVALID')
    script('def show ()\n\tprintf %s $missing\nshow', code='REF_MISSING')
    script('def show ()\n\treturn "named"\nlet result = $?(show)\nout! $result.value (fmt result.status)', expected='named0')
    script('def make ()\n\tlet name = "retained"\n\tdef show ()\n\t\treturn name\n\treturn show\nlet show = make ()\nlet result = $?(show)\nout! $result.value (fmt result.status)', expected='retained0')

    recipe('task chosen\n\t/usr/bin/printf permitted\n', expected='permitted')
    invoke(['do', 'run', '--allow-run=/usr/bin/printf', str(project / 'Makefile.km'), 'chosen'], expected='permitted')

    script('let name = "outer"\ndef make ()\n\tlet unused = "temporary"\n\tdef show ()\n\t\treturn name\n\treturn show\nlet show = make ()\nlet result = $?(show)\nout! $result.value (fmt result.status)', expected='outer0')

    # Rule stream attribution and caches cover the aggregate process output.
    text = 'task cached chosen\n\tprintf first\n\tprintf second\n\tsh -c "printf x >> executions"\n'
    recipe(text, expected='firstsecond')
    recipe(text, expected='firstsecond')
    assert (project / 'executions').read_text() == 'x'
    text = 'SHELL = "/bin/sh"\n' + text
    recipe(text, expected='firstsecond')
    assert (project / 'executions').read_text() == 'xx'
    result = recipe('task chosen\n\tsh -c "printf hello"\n', ['--json', 'chosen'])
    events = [json.loads(line) for line in result.stdout.splitlines()]
    streams = [event for event in events if event['type'] == 'stdout']
    assert ''.join(event['data'] for event in streams) == 'hello'
    assert all(event.get('target') == 'chosen' for event in streams), events
    started = [event for event in events if event['type'] == 'process-started']
    exited = [event for event in events if event['type'] == 'process-exited']
    assert len(started) == len(exited) == 1, events
    assert started[0]['request'] == exited[0]['request'], events
print(f'{backend}: {cases} Kash recipe and lexical function cases passed')
