"""Exercise selected shell policies and structured recipes through CLI hosts."""
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
    def run(text, arguments=None, expected=None, code=None, environment=None):
        global cases
        (project / 'Makefile.km').write_text(text)
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
    def check_lifecycle():
        run('task chosen\n\t/bin/sleep .08\n\t/bin/sleep .08\n\t/usr/bin/touch forbidden\n', ['--timeout', '120', 'chosen'], code='RECIPE_TIMEOUT')
        assert not (project / 'forbidden').exists()
        run('task chosen\n\t/bin/sleep 30 &\n\t/usr/bin/false\n', code='RECIPE_FAIL')
    if part == 'lifecycle':
        check_lifecycle()
        print(f'{backend}: {cases} recipe lifecycle cases passed')
        raise SystemExit(0)
    run('task chosen\n\t/usr/bin/printf hello\n', expected='hello')
    cached = 'task cached chosen\n\t/bin/sh -c "printf x >> cache-runs; printf cached; printf warning >&2"\n'
    first = run(cached, ['--json', 'chosen'])
    second = run(cached, ['--json', 'chosen'])
    for result in (first, second):
        events = [json.loads(line) for line in result.stdout.splitlines()]
        assert ''.join(e['data'] for e in events if e['type'] == 'stdout') == 'cached', result
        assert ''.join(e['data'] for e in events if e['type'] == 'stderr') == 'warning', result
    assert (project / 'cache-runs').read_text() == 'x'
    async_cached = 'task cached chosen\n\t/bin/sh -c "printf x >> async-runs; printf async; printf async-warning >&2" &\n'
    first = run(async_cached, ['--json', 'chosen'])
    second = run(async_cached, ['--json', 'chosen'])
    for result in (first, second):
        events = [json.loads(line) for line in result.stdout.splitlines()]
        assert ''.join(e['data'] for e in events if e['type'] == 'stdout') == 'async', result
        assert ''.join(e['data'] for e in events if e['type'] == 'stderr') == 'async-warning', result
    assert (project / 'async-runs').read_text() == 'x'
    (project / 'retry-stage.sh').write_text('printf "%s" "$MODE" >> retry-runs\nif [ ! -e retry-marker ]; then touch retry-marker; exit 1; fi\nprintf recovered\n')
    retry = 'task chosen ; {env: {MODE: "scoped"}}\n\t/bin/sh ./retry-stage.sh\n'
    run(retry, ['--retry', '1', 'chosen'], expected='recovered')
    assert (project / 'retry-runs').read_text() == 'scopedscoped'
    if part == 'all':
        check_lifecycle()
    run('task chosen ; {shell: "/bin/sh"}\n\tprintf shell\n', expected='shell')
    run('task chosen ; {shell: ["/bin/sh", "-c"]}\n\tprintf argv\n', expected='argv')
    run('SHELL = (first ["/bin/sh"])\ntask chosen\n\t/usr/bin/printf global\n', expected='global')
    # Interpreter constructors are settings, not unhashable artifact inputs.
    (project / 'input').write_text('unchanged')
    for shell in ('', 'SHELL = (first ["/bin/sh"])\n'):
        text = f'{shell}rule ./reused : ./input\n\t/bin/sh -c "printf x >> file-runs; cp input reused"\n'
        (project / 'file-runs').write_text('')
        run(text, ['./reused'])
        runs = (project / 'file-runs').read_text()
        stamp = (project / 'reused').stat().st_mtime_ns
        run(text, ['./reused'])
        assert (project / 'file-runs').read_text() == runs
        assert (project / 'reused').stat().st_mtime_ns == stamp
        (project / 'input').write_text(shell or 'default Kash')
        run(text, ['./reused'])
        assert (project / 'reused').read_text() == (shell or 'default Kash')
        run(text, ['./reused'])
        assert (project / 'file-runs').read_text() == runs + 'x'
    for value in ('A', 'B'):
        helper = f'def payload () = "{value}"\nrule ./helper : ./input\n\treturn (payload ())\n'
        run(helper, ['./helper'])
        assert (project / 'helper').read_text() == value
        stamp = (project / 'helper').stat().st_mtime_ns
        run(helper, ['./helper'])
        assert (project / 'helper').stat().st_mtime_ns == stamp
        # Calls inside Kash execute later than recipe template rendering.
        execution = f'def payload () = "{value}"\nPAYLOAD = (payload ())\nrule ./executed : ./input\n\t/usr/bin/printf %s $PAYLOAD > ./executed\n'
        run(execution, ['./executed'])
        assert (project / 'executed').read_text() == value
    # Membership is itself a dependency, including appearance and disappearance.
    (project / 'members').mkdir()
    (project / 'members/a').write_text('A')
    membership = 'MEMBERS = (files ./members/*)\nrule ./membership : ./input\n\treturn (join "" (map (file => text (read file)) MEMBERS))\n'
    for expected in ('A', 'AB', 'B'):
        run(membership, ['./membership'])
        assert (project / 'membership').read_text() == expected
        if expected == 'A':
            (project / 'members/b').write_text('B')
        elif expected == 'AB':
            (project / 'members/a').unlink()
    # A clock/process-backed definition has no revalidatable leaf manifest.
    volatile = 'PAYLOAD = $(sh -c "printf x >> volatile-runs; printf payload")\nrule ./volatile : ./input\n\treturn PAYLOAD\n'
    run(volatile, ['./volatile'])
    run(volatile, ['./volatile'])
    assert (project / 'volatile-runs').read_text() == 'xx'
    (project / 'seed').write_text('first')
    generated = 'rule ./generated : ./seed\n\tcp $inputs.0 $outputs.0\nrule ./consumer : ./input\n\treturn (text (read "./generated"))\n'
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
    staged = 'def announce label value\n\tout! $label\n\treturn value\nrule ./staged : ./input\n\treturn (str (announce "once" "A") (text (read ./late)))\n'
    run(staged, ['./staged'], expected='once')
    assert (project / 'staged').read_text() == 'AB'
    run(staged, ['--force', './staged'], expected='once')
    nested = 'def announce label value\n\tout! $label\n\treturn value\nrule ./nested : ./input\n\treturn (str (announce "a" "A") (str (announce "b" "B") (text (read ./late))))\n'
    run(nested, ['./nested'], expected='ab')
    assert (project / 'nested').read_text() == 'ABB'
    (project / 'first').write_text('A')
    (project / 'second').write_text('C')
    callbacks = 'rule ./callbacks : ./input\n\treturn (join "|" (map (file => str (text (read file)) (text (read ./late))) [./first, ./second]))\n'
    run(callbacks, ['./callbacks'])
    assert (project / 'callbacks').read_text() == 'AB|CB'
    mutable = 'rule ./mutable : ./input\n\tlet value = "A"\n\tif true\n\t\tset value = "C"\n\treturn (str value (text (read ./late)))\n'
    run(mutable, ['./mutable'])
    assert (project / 'mutable').read_text() == 'CB'
    run('SHELL = "/missing-shell"\ntask chosen ; {shell: "/bin/sh"}\n\tprintf override\n', expected='override')
    run('mode = "configured"\ntask chosen ; {env: {MODE: mode}}\n\tprintenv MODE\n', expected='configured\n')
    run('task chosen ; {env: {MODE: "scoped"}}\n\t/usr/bin/printenv MODE\n', expected='scoped\n')
    run('task chosen\n\t/usr/bin/false\n\t/usr/bin/touch forbidden\n', code='RECIPE_FAIL')
    assert not (project / 'forbidden').exists()
    run('task chosen\n\tif $?(/usr/bin/true).ok\n\t  /usr/bin/printf selected\n', expected='selected')
    for setting in ('42', '""', '[]', '["/bin/sh", 42]'):
        run(f'task chosen ; {{shell: {setting}}}\n\ttouch forbidden\n', code='EXPR_INVALID')
        assert not (project / 'forbidden').exists()
    run('task chosen ; {env: 42}\n\ttouch forbidden\n', code='EXPR_INVALID')
    run('task chosen ; {env: {MODE: 42}}\n\ttouch forbidden\n', code='EXPR_INVALID')
    run('task chosen ; {shell: (out! "forbidden")}\n\ttouch forbidden\n', code='PHASE_INVALID')
    run('task chosen ; {shell: $(touch forbidden)}\n\ttouch forbidden\n', code='PHASE_INVALID')
    assert not (project / 'forbidden').exists()
    run('rule ./out\n\t/usr/bin/printf artifact > ./out\n', ['./out'])
    assert (project / 'out').read_text() == 'artifact'
    (project / 'out').unlink()
    run('rule ./out\n\t/usr/bin/true\n', ['./out'], code='OUTPUT_MISSING')
    run('task chosen\n\t/usr/bin/touch forbidden\n', ['--dry-run', 'chosen'])
    assert not (project / 'forbidden').exists()
    run('task chosen\n\t/usr/bin/touch forbidden\n', ['do', 'plan', 'chosen'])
    assert not (project / 'forbidden').exists()
    result = run('task chosen\n\t/usr/bin/printf hello\n', ['--json', 'chosen'])
    events = [json.loads(line) for line in result.stdout.splitlines()]
    assert ''.join(event['data'] for event in events if event['type'] == 'stdout') == 'hello'
print(f'{backend}: {cases} selected recipe policy cases passed')
