"""Exercise selected shell policies and structured recipes through CLI hosts."""
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
    def run(text, arguments=None, expected=None, code=None):
        global cases
        (project / 'Makefile.kmk').write_text(text)
        result = subprocess.run(command + (arguments or ['chosen']), cwd=project,
            env=dict(os.environ, MODE='ambient'), text=True, capture_output=True, timeout=15)
        if code:
            assert result.returncode == 1 and code in result.stdout + result.stderr, (text, result)
        else:
            assert result.returncode == 0, (text, result)
            if expected is not None:
                assert result.stdout == expected, (text, result)
        cases += 1
        return result
    run('chosen : ; [shell: kash]\n\t/usr/bin/printf hello\n', expected='hello')
    cached = 'task chosen : ; [shell: kash]\n\t/bin/sh -c "printf x >> cache-runs; printf cached; printf warning >&2"\n'
    first = run(cached, ['--json', 'chosen'])
    second = run(cached, ['--json', 'chosen'])
    for result in (first, second):
        events = [json.loads(line) for line in result.stdout.splitlines()]
        assert ''.join(e['data'] for e in events if e['type'] == 'stdout') == 'cached', result
        assert ''.join(e['data'] for e in events if e['type'] == 'stderr') == 'warning', result
    assert (project / 'cache-runs').read_text() == 'x'
    async_cached = 'task chosen : ; [shell: kash]\n\t/bin/sh -c "printf x >> async-runs; printf async; printf async-warning >&2" &\n'
    first = run(async_cached, ['--json', 'chosen'])
    second = run(async_cached, ['--json', 'chosen'])
    for result in (first, second):
        events = [json.loads(line) for line in result.stdout.splitlines()]
        assert ''.join(e['data'] for e in events if e['type'] == 'stdout') == 'async', result
        assert ''.join(e['data'] for e in events if e['type'] == 'stderr') == 'async-warning', result
    assert (project / 'async-runs').read_text() == 'x'
    (project / 'retry-stage.sh').write_text('printf "%s" "$MODE" >> retry-runs\nif [ ! -e retry-marker ]; then touch retry-marker; exit 1; fi\nprintf recovered\n')
    retry = 'chosen : ; [shell: kash env: [MODE: "scoped"]]\n\t/bin/sh ./retry-stage.sh\n'
    run(retry, ['--retry', '1', 'chosen'], expected='recovered')
    assert (project / 'retry-runs').read_text() == 'scopedscoped'
    run('chosen : ; [shell: kash]\n\t/bin/sleep .08\n\t/bin/sleep .08\n\t/usr/bin/touch forbidden\n', ['--timeout', '120', 'chosen'], code='RECIPE_TIMEOUT')
    assert not (project / 'forbidden').exists()
    run('chosen : ; [shell: kash]\n\t/bin/sleep 30 &\n\t/usr/bin/false\n', code='RECIPE_FAIL')
    run('chosen : ; [shell: "/bin/sh"]\n\tprintf shell\n', expected='shell')
    run('chosen : ; [shell: ["/bin/sh" "-c"]]\n\tprintf argv\n', expected='argv')
    run('SHELL = (first (list kash))\nchosen :\n\t/usr/bin/printf global\n', expected='global')
    run('SHELL = "/missing-shell"\nchosen : ; [shell: "/bin/sh"]\n\tprintf override\n', expected='override')
    run('mode = "configured"\nchosen : ; [env: [MODE: mode]]\n\tprintf %s "$MODE"\n', expected='configured')
    run('chosen : ; [shell: kash env: [MODE: "scoped"]]\n\t/usr/bin/printenv MODE\n', expected='scoped\n')
    run('chosen : ; [shell: kash]\n\t/usr/bin/false\n\t/usr/bin/touch forbidden\n', code='RECIPE_FAIL')
    assert not (project / 'forbidden').exists()
    run('chosen : ; [shell: kash]\n\tif /usr/bin/true\n\t  /usr/bin/printf selected\n', expected='selected')
    for setting in ('42', '""', '[]', '["/bin/sh" 42]'):
        run(f'chosen : ; [shell: {setting}]\n\ttouch forbidden\n', code='EXPR_INVALID')
        assert not (project / 'forbidden').exists()
    run('chosen : ; [env: 42]\n\ttouch forbidden\n', code='EXPR_INVALID')
    run('chosen : ; [env: [MODE: 42]]\n\ttouch forbidden\n', code='EXPR_INVALID')
    run('chosen : ; [shell: (out "forbidden")]\n\ttouch forbidden\n', code='PHASE_INVALID')
    run('chosen : ; [shell: (shell "touch forbidden")]\n\ttouch forbidden\n', code='PHASE_INVALID')
    assert not (project / 'forbidden').exists()
    run('./out : ; [shell: kash]\n\t/usr/bin/printf artifact > ./out\n', ['./out'])
    assert (project / 'out').read_text() == 'artifact'
    (project / 'out').unlink()
    run('./out : ; [shell: kash]\n\t/usr/bin/true\n', ['./out'], code='OUTPUT_MISSING')
    run('chosen : ; [shell: kash]\n\t/usr/bin/touch forbidden\n', ['--dry-run', 'chosen'])
    assert not (project / 'forbidden').exists()
    run('chosen : ; [shell: kash]\n\t/usr/bin/touch forbidden\n', ['do', 'plan', 'chosen'])
    assert not (project / 'forbidden').exists()
    result = run('chosen : ; [shell: kash]\n\t/usr/bin/printf hello\n', ['--json', 'chosen'])
    events = [json.loads(line) for line in result.stdout.splitlines()]
    assert ''.join(event['data'] for event in events if event['type'] == 'stdout') == 'hello'
print(f'{backend}: {cases} selected recipe policy cases passed')
