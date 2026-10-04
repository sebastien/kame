"""Exercise conditional source composition through both public CLI hosts."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

root, backend, binary = sys.argv[1:]
root = Path(root)
command = [binary] if backend == 'native' else ['node', str(root / 'dist/kame.js')]
environment = dict(os.environ)
environment.pop('KAME_mode', None)
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

    (project / 'config.kmk').write_text('(selected wanted) = (eq mode wanted)\n')
    (project / 'debug.kmk').write_text('label = "debug"\nchosen :\n\t@(out label)\ndebug-only :\n')
    (project / 'release.kmk').write_text('label = "release"\nchosen :\n\t@(out label)\nrelease-only :\n')
    build = ('mode ?= "debug"\ninclude config.kmk\n'
        'when (selected "debug")\ninclude debug.kmk\notherwise\ninclude release.kmk\nend\n'
        'when :false\ninclude missing.kmk\ninclude Makefile.kmk\n'
        'when (read "never read")\ninclude another-missing.kmk\nend\nend\n')
    (project / 'Makefile.kmk').write_text(build)
    run(['chosen'], 'debug')
    run(['--define', 'mode=first', '--define', 'mode=release', 'chosen'], 'release')
    run(['chosen'], 'release', settings={'KAME_mode': 'release'})
    run(['--define', 'mode=debug', 'chosen'], 'debug', settings={'KAME_mode': 'release'})
    run(['--define', 'mode=', 'chosen'], 'release')
    run(['--define', 'unknown=value', 'chosen'], code='DEF_INVALID')
    # Inspection sees only the selected rule set and never runs its recipes.
    plan = run(['do', 'plan', '--json', 'chosen'])
    assert json.loads(plan.stdout)['target'] == 'chosen'
    run(['do', 'plan', 'debug-only'])
    run(['do', 'plan', 'release-only'], code='TGT_NO_RULE')
    ast = json.loads(run(['do', 'parse', '--lang', 'script', 'Makefile.kmk']).stdout)
    kinds = [item['kind'] for item in ast['ast']['items']]
    assert kinds.count('when') == 3 and 'otherwise' in kinds and kinds.count('end-when') == 3
    formatted = run(['do', 'fmt', '--lang', 'script', 'Makefile.kmk']).stdout
    (project / 'formatted.kmk').write_text(formatted)
    assert run(['do', 'fmt', '--lang', 'script', 'formatted.kmk']).stdout == formatted
    run(['-f', 'formatted.kmk', 'chosen'], 'debug')
    # A selected include supplies configuration to a later predicate.
    (project / 'config-value.kmk').write_text('enabled = (bool 1)\n')
    (project / 'from-include.kmk').write_text('include config-value.kmk\nwhen enabled\nchosen :\n\tprintf included\nend\n')
    run(['-f', 'from-include.kmk', 'chosen'], 'included')
    # Required active includes, active cycles and bad branch syntax still fail.
    (project / 'missing-active.kmk').write_text('when :true\ninclude absent.kmk\nend\nchosen :\n\ttouch should-not-run\n')
    run(['-f', 'missing-active.kmk', 'chosen'], code='FS_ERR')
    assert not (project / 'should-not-run').exists()
    (project / 'cycle.kmk').write_text('when :true\ninclude cycle.kmk\nend\n')
    run(['-f', 'cycle.kmk'], code='DEP_CYCLE')
    invalid = ['when\nend\n', 'otherwise\n', 'end\n', 'when :true\n', 'when :true\notherwise\notherwise\nend\n', 'when :false\nbad = (\nend\n']
    for text in invalid:
        (project / 'invalid.kmk').write_text(text)
        run(['-f', 'invalid.kmk'], code='PARSE_ERR')
    # Predicates have no host authority; registration failures precede effects.
    for predicate, code in [('42', 'EXPR_INVALID'), ('(shell "touch forbidden")', 'CAP_DENIED'), ('(read "forbidden")', 'CAP_DENIED'), ('(out "forbidden")', 'PHASE_INVALID')]:
        (project / 'unsafe.kmk').write_text(f'when {predicate}\nchosen :\n\ttouch should-not-run\nend\n')
        result = run(['-f', 'unsafe.kmk', 'chosen'], code=code)
        assert 'unsafe.kmk:1:' in result.stderr, result.stderr
        assert not (project / 'forbidden').exists() and not (project / 'should-not-run').exists()
    # Selected declarations enter the shared scope; inactive duplicates do not.
    value = 'mode = "debug"\nwhen (eq mode "debug")\nanswer = (first (list 42))\notherwise\nanswer = (first (list 0))\nend\nanswer\n'
    (project / 'value.km').write_text(value)
    run(['do', 'run', '-f', 'value.km'], '42')
    run(['do', 'run', '-l', 'km', '-c', value], '42')
    run(['do', 'run', '-l', 'km', '-f', '-'], '42', input_text=value)
    (project / 'first.km').write_text('mode = "debug"\n')
    second = ':true\nwhen (eq mode "debug")\n42\notherwise\n0\nend\n'
    (project / 'second.km').write_text(second)
    run(['do', 'run', '-f', 'first.km', '-f', 'second.km'], '42')
    run(['do', 'run', '-f', 'first.km', '-l', 'km', '-c', second], '42')
    inline_build = 'when :true\nchosen :\n\tprintf inline\notherwise\nchosen :\n\tprintf wrong\nend\n'
    run(['do', 'run', '-l', 'kmk', '-c', inline_build, 'chosen'], 'inline')
    # Includes require a file-backed source, even when conditionally reached.
    run(['do', 'run', '-l', 'km', '-c', 'when :true\ninclude missing.km\nend\n'], code='FEATURE_UNSUP')
    run(['do', 'run', '-l', 'km', '-c', 'when :false\ninclude missing.km\nend\n42\n'], '42')
    (project / 'bad-predicate.kmk').write_text('# é keeps byte offsets honest\nwhen (eq missing 1)\nchosen :\n\ttrue\nend\n')
    result = run(['--json', '-f', 'bad-predicate.kmk', 'chosen'], code='REF_MISSING')
    diagnostic = [json.loads(line)['diagnostic'] for line in result.stdout.splitlines() if json.loads(line).get('type') == 'diagnostic'][0]
    assert diagnostic['source'].endswith('bad-predicate.kmk')
    assert diagnostic['span']['start'] == len('# é keeps byte offsets honest\nwhen '.encode())

print(f'{backend}: {cases} public conditional declaration cases passed')
