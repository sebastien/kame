"""Kash recipe and script ownership checks shared by native and WASM hosts."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

root, backend, binary = sys.argv[1:]
root = Path(root)
command = [binary] if backend == 'native' else ['node', str(root / 'dist/kame.js')]
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
        (project / 'Makefile.kmk').write_text(text)
        return invoke(args or ['chosen'], expected, code)
    def expression(text, expected=None, code=None, args=None):
        return invoke(['do', 'run', '--allow-run', '--allow-read', '--allow-write', '-c', text] + (args or []), expected, code)

    # Templates expand before Kash parsing, including generated statements.
    recipe('SHELL = kash\ncommand = "printf generated"\nchosen :\n\t@(command)\n', expected='generated')
    recipe('SHELL = kash\nname = "build value"\nchosen :\n\tprintf %s "@(name)"\n', expected='build value')
    recipe('SHELL = kash\nchosen :\n\tprintf %s $(printf captured)\n', expected='captured')
    recipe('SHELL = kash\nchosen :\n\tprintf %s $(printf value)\n\tprintf %s $(printf value)\n', expected='valuevalue')
    recipe('SHELL = kash\nchosen :\n\tprintf first\n\tprintf second | cat\n', expected='firstsecond')
    recipe('chosen : ; [shell: kash]\n\tprintf forbidden\n\tif true\n', code='PARSE_ERR')
    recipe('chosen : ; [shell: kash]\n\tprintf forbidden\n\tif true\n', ['--dry-run', 'chosen'], code='PARSE_ERR')
    assert not (project / 'forbidden').exists()

    # File rules prepare directories, wait for async commands, and verify outputs.
    recipe('SHELL = kash\n./nested/result :\n\tprintf artifact > @>\n', ['./nested/result'])
    assert (project / 'nested/result').read_text() == 'artifact'
    recipe('SHELL = kash\n./async-result :\n\tsh -c "sleep .03; printf async > async-result" &\n', ['./async-result'])
    assert (project / 'async-result').read_text() == 'async'
    recipe('SHELL = kash\nchosen :\n\tsleep .08\n\tsleep .08\n', ['--timeout', '120', 'chosen'], code='RECIPE_TIMEOUT')
    recipe('SHELL = kash\nchosen :\n\tsh -c "if test -f retried; then printf success; else touch retried; exit 1; fi"\n', ['--retry', '1', 'chosen'], expected='success')

    (project / 'fresh-input').write_text('input')
    file_recipe = 'SHELL = kash\n./fresh-output : ./fresh-input\n\tsh -c "printf x >> file-runs; printf result > fresh-output"\n'
    recipe(file_recipe, ['./fresh-output'])
    recipe(file_recipe, ['./fresh-output'])
    assert (project / 'file-runs').read_text() == 'x'
    recipe(file_recipe.replace('SHELL = kash', 'SHELL = "/bin/sh"'), ['./fresh-output'])
    assert (project / 'file-runs').read_text() == 'xx'
    unbound = 'SHELL = kash\n./unbound :\n\tsh -c "printf x >> unbound-runs; printf result > unbound"\n'
    recipe(unbound, ['./unbound'])
    recipe(unbound, ['./unbound'])
    assert (project / 'unbound-runs').read_text() == 'xx'

    # Environments inherit through prerequisites; shell overrides are local.
    recipe('SHELL = kash\nchosen : child ; [env: [MODE: "parent"]]\n\tprintenv MODE\nchild : ; [shell: "/bin/sh"]\n\tprintf %s "$MODE"\n', expected='parentparent\n')
    recipe('chosen : child ; [shell: kash]\n\tprintf parent\nchild :\n\tprintf child; :\n', expected='childparent')
    recipe('SHELL = (shell "touch forbidden")\nchosen :\n\ttrue\n', code='PHASE_INVALID')
    assert not (project / 'forbidden').exists()
    recipe('chosen : ; [shell: kash env: [MODE: "rule"]]\n\tprintenv MODE\n\t:MODE command printenv MODE\n', expected='rule\ncommand\n')

    # Construction is pure; calls have lexical scope, arguments and stable progress.
    expression('(let [script (kash "touch forbidden")] (out "constructed"))', expected='constructed"constructed"')
    assert not (project / 'forbidden').exists()
    expression('(let [name "lexical" script (kash "printf %s $name")] (let [name "caller"] (script)) (out "done"))', expected='lexicaldone"done"')
    expression('(let [script (kash """printf %s @(join @* "-")""")] (script "one" "two") (out "done"))', expected='one-twodone"done"')
    expression('(let [script (kash "printf x; sleep .01; printf y")] (script) (script) (out "done"))', expected='xyxydone"done"')
    expression('(let [script (kash "value = 42; printf %s $value")] (script) (script) (out "done"))', expected='4242done"done"')
    expression('(let [script (kash "printf x &; printf y")] (script) (out "done"))')
    expression('(let [script (kash "false; touch forbidden")] (script))', code='RECIPE_FAIL')
    expression('(kash "printf x; if true")', code='PARSE_ERR')
    assert not (project / 'forbidden').exists()
    expression('(kash 42)', code='EXPR_INVALID')
    expression('(let [script (kash "printf %s $missing")] (script))', code='REF_MISSING')
    expression('script = (kash "printf named")\nresult = (script)\n(out result.status)', expected='named0"0"')
    expression('script = (let [name "retained"] (kash "printf %s $name"))\nresult = (script)\n(out result.status)', expected='retained0"0"')

    recipe('chosen : ; [shell: kash]\n\t/usr/bin/printf permitted\n', expected='permitted')
    invoke(['do', 'run', '--allow-run=/usr/bin/printf', str(project / 'Makefile.kmk'), 'chosen'], expected='permitted')

    expression('name = "outer"\nscript = (kash (let [unused "temporary"] "printf %s $name"))\nresult = (script)\n(out result.status)', expected='outer0"0"')

    # Rule stream attribution and caches cover the aggregate process output.
    text = 'SHELL = kash\ntask chosen :\n\tprintf first\n\tprintf second\n\tsh -c "printf x >> executions"\n'
    recipe(text, expected='firstsecond')
    recipe(text, expected='firstsecond')
    assert (project / 'executions').read_text() == 'x'
    text = text.replace('SHELL = kash', 'SHELL = "/bin/sh"')
    recipe(text, expected='firstsecond')
    assert (project / 'executions').read_text() == 'xx'
    result = recipe('SHELL = kash\nchosen :\n\tsh -c "printf hello"\n', ['--json', 'chosen'])
    events = [json.loads(line) for line in result.stdout.splitlines()]
    streams = [event for event in events if event['type'] == 'stdout']
    assert ''.join(event['data'] for event in streams) == 'hello'
    assert all(event.get('target') == 'chosen' for event in streams), events
    started = [event for event in events if event['type'] == 'process-started']
    exited = [event for event in events if event['type'] == 'process-exited']
    assert len(started) == len(exited) == 1, events
    assert started[0]['request'] == exited[0]['request'], events
print(f'{backend}: {cases} Kash recipe and reusable script cases passed')
