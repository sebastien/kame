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
    (project / 'debug.kmk').write_text('label = "debug"\nchosen :\n\t@(out! label)\ndebug-only :\n')
    (project / 'release.kmk').write_text('label = "release"\nchosen :\n\t@(out! label)\nrelease-only :\n')
    build = ('mode ?= "debug"\n(include ./config.kmk)\n'
        '(if (selected "debug") (include ./debug.kmk) (include ./release.kmk))\n'
        '(if :false (include ./missing.kmk) :nil)\n'
        '(if :false (include ./Makefile.kmk) :nil)\n'
        '(if :false (if (read "never read") (include ./another-missing.kmk)) :nil)\n')
    (project / 'Makefile.kmk').write_text(build)
    run(['chosen'], 'debug')
    run(['--define', 'mode=first', '--define', 'mode=release', 'chosen'], 'release')
    run(['chosen'], 'release', settings={'KAME_mode': 'release'})
    run(['--define', 'mode=debug', 'chosen'], 'debug', settings={'KAME_mode': 'release'})
    run(['--define', 'mode=', 'chosen'], 'release')
    run(['--define', 'unknown=value', 'chosen'], code='DEF_INVALID')
    # Inspection sees only the selected rule set and never runs its recipes.
    plan = run(['do', 'plan', '--json', 'chosen'])
    assert json.loads(plan.stdout)['producers'][0]['target'] == 'chosen'
    run(['do', 'plan', 'debug-only'])
    run(['do', 'plan', 'release-only'], code='TGT_NO_RULE')
    ast = json.loads(run(['do', 'parse', '--json', '--lang', 'script', 'Makefile.kmk']).stdout)
    kinds = [item['kind'] for item in ast['ast']['items']]
    assert kinds.count('composition') == 4 and kinds.count('include') == 1
    assert not set(kinds) & {'when', 'otherwise', 'end-when'}
    formatted = run(['do', 'fmt', '--lang', 'script', 'Makefile.kmk']).stdout
    (project / 'formatted.kmk').write_text(formatted)
    assert run(['do', 'fmt', '--lang', 'script', 'formatted.kmk']).stdout == formatted
    run(['-f', 'formatted.kmk', 'chosen'], 'debug')
    # A selected include supplies configuration to a later predicate.
    (project / 'config-value.kmk').write_text('enabled = (bool 1)\n')
    (project / 'included.kmk').write_text('chosen :\n\tprintf included\n')
    (project / 'from-include.kmk').write_text('(include ./config-value.kmk)\n(if enabled (include ./included.kmk))\n')
    run(['-f', 'from-include.kmk', 'chosen'], 'included')
    # Required active includes, active cycles and bad branch syntax still fail.
    (project / 'missing-active.kmk').write_text('(if :true (include ./absent.kmk))\nchosen :\n\ttouch should-not-run\n')
    run(['-f', 'missing-active.kmk', 'chosen'], code='FS_ERR')
    assert not (project / 'should-not-run').exists()
    (project / 'cycle.kmk').write_text('(if :true (include ./cycle.kmk))\n')
    run(['-f', 'cycle.kmk'], code='DEP_CYCLE')
    invalid = ['when\nend\n', 'otherwise\n', 'end\n', 'when :true\n',
        'when :true\notherwise\notherwise\nend\n', 'when :false\nbad = (\nend\n',
        'include ./absent.kmk\n', 'include? ./absent.kmk\n',
        '(include)\n', '(include ./a.kmk ./b.kmk)\n', '(include "")\n',
        '(include ./a*.kmk)\n', '(include "a\\x00b")\n',
        '(include path)\n', '(include (str "./" "a.kmk"))\n',
        '(include "@(path)")\n', '(include ./@(path))\n',
        '(if :false (include path) :nil)\n']
    for text in invalid:
        (project / 'invalid.kmk').write_text(text)
        run(['-f', 'invalid.kmk'], code='PARSE_ERR')
    # Predicates have no host authority; registration failures precede effects.
    for predicate, code in [('(shell! "touch forbidden")', 'CAP_DENIED'), ('(read "forbidden")', 'CAP_DENIED'), ('(out! "forbidden")', 'PHASE_INVALID')]:
        (project / 'unsafe.kmk').write_text(f'(if {predicate} (include ./included.kmk))\nchosen :\n\ttouch should-not-run\n')
        result = run(['-f', 'unsafe.kmk', 'chosen'], code=code)
        assert 'unsafe.kmk:1:' in result.stderr, result.stderr
        assert not (project / 'forbidden').exists() and not (project / 'should-not-run').exists()
    # Selected declarations enter the shared scope; inactive duplicates do not.
    (project / 'answer.km').write_text('answer = (first (list 42))\n')
    (project / 'zero.km').write_text('answer = (first (list 0))\n')
    value = 'mode = "debug"\n(if (eq mode "debug") (include ./answer.km) (include ./zero.km))\nanswer\n'
    (project / 'value.km').write_text(value)
    run(['do', 'run', '--print-result', '-f', 'value.km'], '42')
    inline_value = 'mode = "debug"\nanswer = (if (eq mode "debug") (first (list 42)) (first (list 0)))\nanswer\n'
    run(['do', 'run', '--print-result', '-l', 'km', '-c', inline_value], '42')
    run(['do', 'run', '--print-result', '-l', 'km', '-f', '-'], '42', input_text=inline_value)
    (project / 'first.km').write_text('mode = "debug"\n')
    (project / 'selected.km').write_text('42\n')
    (project / 'unselected.km').write_text('0\n')
    second = ':true\n(if (eq mode "debug") (include ./selected.km) (include ./unselected.km))\n'
    (project / 'second.km').write_text(second)
    run(['do', 'run', '--print-result', '-f', 'first.km', '-f', 'second.km'], '42')
    run(['do', 'run', '--print-result', '-f', 'first.km', '-l', 'km', '-c', ':true\n(if (eq mode "debug") 42 0)\n'], '42')
    inline_build = 'chosen :\n\t@(out! (if :true "inline" "wrong"))\n'
    run(['do', 'run', '-l', 'kmk', '-c', inline_build, 'chosen'], 'inline')
    # Includes require a file-backed source, even when conditionally reached.
    run(['do', 'run', '-l', 'km', '-c', '(if :true (include ./missing.km))\n'], code='FEATURE_UNSUP')
    run(['do', 'run', '--print-result', '-l', 'km', '-c', '(if :false (include ./missing.km))\n42\n'], '42')
    (project / 'bad-predicate.kmk').write_text('# é keeps byte offsets honest\n(if (eq missing 1) (include ./included.kmk))\n')
    result = run(['--json', '-f', 'bad-predicate.kmk', 'chosen'], code='REF_MISSING')
    diagnostic = [json.loads(line)['diagnostic'] for line in result.stdout.splitlines() if json.loads(line).get('type') == 'diagnostic'][0]
    assert diagnostic['source'].endswith('bad-predicate.kmk')
    assert diagnostic['span']['start'] == len('# é keeps byte offsets honest\n(if '.encode())

    # Ordinary truthiness, multi-pair selection, nested bodies and nil/no-else.
    for predicate, expected in [(':false', '0'), (':nil', '0'), (':true', '42'),
            ('0', '42'), ('42', '42'), ('""', '42'), ('"text"', '42')]:
        (project / 'truth.km').write_text(f'(if {predicate} (include ./answer.km) (include ./zero.km))\nanswer\n')
        run(['do', 'run', '--print-result', '-f', 'truth.km'], expected)
    (project / 'pairs.km').write_text('(if :false (include ./absent.km) 0 (if :true (include ./answer.km) :nil) (include ./zero.km))\nanswer\n')
    run(['do', 'run', '--print-result', '-f', 'pairs.km'], '42')
    (project / 'nil.km').write_text('(if :false (include ./absent.km))\n(if :true :nil (include ./absent.km))\n42\n')
    run(['do', 'run', '--print-result', '-f', 'nil.km'], '42')

    # Only earlier declarations feed composition; ordinary forward refs remain lazy.
    (project / 'earlier.km').write_text('enabled = :true\n(selected) = enabled\nunused = (read "forbidden")\n(if (selected) (include ./answer.km))\nanswer\n')
    run(['do', 'run', '--print-result', '-f', 'earlier.km'], '42')
    (project / 'forward.km').write_text('(if enabled (include ./answer.km))\nenabled = :true\nanswer\n')
    run(['do', 'run', '-f', 'forward.km'], code='REF_MISSING')
    (project / 'predicate-cycle.km').write_text('a = b\nb = a\n(if a (include ./answer.km))\n')
    run(['do', 'run', '-f', 'predicate-cycle.km'], code='DEP_CYCLE')
    (project / 'duplicate.km').write_text('(out! "must-not-run")\n(include ./answer.km)\n(include ./answer.km)\n')
    result = run(['do', 'run', '-f', 'duplicate.km'], code='DEF_INVALID')
    assert not result.stdout

    # Reserved call heads cannot become runtime calls, even in unused definitions
    # and inactive branches. Every authored placement is validated before effects.
    misplaced = ['unused = (include ./absent.km)\n',
        'unused = ([] (include? ./absent.km))\n',
        '(if :false (str (include ./absent.km)) 42)\n',
        '(if (include ./absent.km) :nil)\n',
        '[(include ./absent.km)]\n', '[x: (include? ./absent.km)]\n',
        '"@(include ./absent.km)"\n',
        '(if :true (include ./answer.km) 42)\n']
    for text in misplaced:
        (project / 'misplaced.km').write_text('(out! "must-not-run")\n' + text)
        result = run(['do', 'run', '-f', 'misplaced.km'], code='EXPR_INVALID')
        assert not result.stdout
    run(['do', 'run', '-l', 'expr', '-c', '(include ./absent.km)'], code='EXPR_INVALID')
    run(['do', 'run', '-l', 'expr', '-c', '(include? ./absent.km)'], code='EXPR_INVALID')
    for head in ['include', 'include?']:
        run(['do', 'run', '-l', 'km', '-c', f'({head} path) = 42\n({head} ./absent.km)\n'], code='FEATURE_UNSUP')

print(f'{backend}: {cases} public conditional declaration cases passed')
