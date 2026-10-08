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
    def run(text, arguments=None, expected=None, code=None, environment=None):
        global cases
        (project / 'Makefile.kmk').write_text(text)
        result = subprocess.run(command + (arguments or ['chosen']), cwd=project,
            env=dict(os.environ, MODE='ambient', **(environment or {})), text=True, capture_output=True, timeout=15)
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
    # Interpreter constructors are settings, not unhashable artifact inputs.
    (project / 'input').write_text('unchanged')
    for setting in ('kash', '(first (list kash))'):
        text = f'SHELL = {setting}\n./reused : ./input\n\t/bin/sh -c "printf x >> file-runs; cp input reused"\n'
        (project / 'file-runs').write_text('')
        run(text, ['./reused'])
        runs = (project / 'file-runs').read_text()
        stamp = (project / 'reused').stat().st_mtime_ns
        run(text, ['./reused'])
        assert (project / 'file-runs').read_text() == runs
        assert (project / 'reused').stat().st_mtime_ns == stamp
        (project / 'input').write_text(setting)
        run(text, ['./reused'])
        assert (project / 'reused').read_text() == setting
        run(text, ['./reused'])
        assert (project / 'file-runs').read_text() == runs + 'x'
    for value in ('A', 'B'):
        helper = f'SHELL = kash\n(payload) = "{value}"\n./helper : ./input\n\t@(yield (payload))\n'
        run(helper, ['./helper'])
        assert (project / 'helper').read_text() == value
        stamp = (project / 'helper').stat().st_mtime_ns
        run(helper, ['./helper'])
        assert (project / 'helper').stat().st_mtime_ns == stamp
        # Calls inside Kash execute later than recipe template rendering.
        execution = f'SHELL = kash\n(payload) = "{value}"\nPAYLOAD = (payload)\n./executed : ./input\n\t/usr/bin/printf %s $PAYLOAD > ./executed\n'
        run(execution, ['./executed'])
        assert (project / 'executed').read_text() == value
    # Membership is itself a dependency, including appearance and disappearance.
    (project / 'members').mkdir()
    (project / 'members/a').write_text('A')
    membership = 'SHELL = kash\nMEMBERS = (wildcard ./members/*)\n./membership : ./input\n\t@(yield (join (map ([file] (text (read file))) MEMBERS) ""))\n'
    for expected in ('A', 'AB', 'B'):
        run(membership, ['./membership'])
        assert (project / 'membership').read_text() == expected
        if expected == 'A':
            (project / 'members/b').write_text('B')
        elif expected == 'AB':
            (project / 'members/a').unlink()
    # A clock/process-backed definition has no revalidatable leaf manifest.
    volatile = 'SHELL = kash\nPAYLOAD = (shell "printf x >> volatile-runs; printf payload")\n./volatile : ./input\n\t@(yield PAYLOAD.stdout)\n'
    run(volatile, ['./volatile'])
    run(volatile, ['./volatile'])
    assert (project / 'volatile-runs').read_text() == 'xx'
    (project / 'seed').write_text('first')
    generated = './generated : ./seed\n\tcp @< @>\n./consumer : ./input\n\t@(yield (text (read "./generated")))\n'
    run(generated, ['./consumer'])
    (project / 'seed').write_text('second')
    run(generated, ['./consumer'])
    assert (project / 'generated').read_text() == 'second'
    assert (project / 'consumer').read_text() == 'second'
    stamp = (project / 'consumer').stat().st_mtime_ns
    run(generated, ['./consumer'])
    assert (project / 'consumer').stat().st_mtime_ns == stamp
    # Completed operands retain their staged effects and reconstructed scopes.
    (project / 'late').write_text('B')
    staged = 'SHELL = kash\n./staged : ./input\n\t@(yield (join (list (let [] (out "once") "A") (text (read "./late"))) ""))\n'
    run(staged, ['./staged'], expected='once')
    assert (project / 'staged').read_text() == 'AB'
    run(staged, ['--force', './staged'], expected='once')
    nested = 'SHELL = kash\n./nested : ./input\n\t@(yield (cat (let [] (out "a") "A") (cat (let [] (out "b") "B") (text (read "./late")))))\n'
    run(nested, ['./nested'], expected='ab')
    assert (project / 'nested').read_text() == 'ABB'
    (project / 'first').write_text('A')
    (project / 'second').write_text('C')
    callbacks = 'SHELL = kash\n./callbacks : ./input\n\t@(yield (join (map ([file] (cat (text (read file)) (text (read "./late")))) [./first ./second]) "|"))\n'
    run(callbacks, ['./callbacks'])
    assert (project / 'callbacks').read_text() == 'AB|CB'
    mutable = 'SHELL = kash\n./mutable : ./input\n\t@(yield (let [value "A"] (def value "C") (cat value (text (read "./late")))))\n'
    run(mutable, ['./mutable'])
    assert (project / 'mutable').read_text() == 'CB'
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
