import json
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

runner = sys.argv[1:]
if not runner:
    raise SystemExit('pass a native or WASM CLI command')

with tempfile.TemporaryDirectory(prefix='kame-target-arguments-') as directory:
    project = Path(directory)
    (project / 'Makefile.kmk').write_text(
        'prepare {region=west} {zone=global} :\n'
        '\tprintf @(region) >> dependency-log\n'
        'deploy {region=west} {zone=global} : "prepare region=@(region) zone=@(zone)"\n'
        '\tprintf @(region) >> deploy-log\n'
        'required {environment} :\n'
        '\tprintf @(environment) >> required-log\n'
        'other :\n\ttrue\n'
    )

    def run(*args):
        return subprocess.run([*runner, *args], cwd=project, text=True, capture_output=True)

    parsed = run('do', 'parse', '--lang', 'script', 'Makefile.kmk')
    assert parsed.returncode == 0, parsed.stderr
    rule = json.loads(parsed.stdout)['ast']['items'][0]['rule']
    assert [(item['name'], item['optional'], item.get('default')) for item in rule['arguments']] == [
        ('region', True, 'west'), ('zone', True, 'global')
    ], rule

    missing_required = run('do', 'plan', 'required')
    assert missing_required.returncode == 1 and 'TGT_ARGUMENT' in missing_required.stderr, missing_required.stderr
    required_plan = run('do', 'plan', 'required', 'environment=production')
    assert required_plan.returncode == 0, required_plan.stderr
    assert json.loads(required_plan.stdout)['arguments'] == {'environment': 'production'}

    planned = run('do', 'plan', 'deploy', 'region=east', 'zone=eu')
    assert planned.returncode == 0, planned.stderr
    plan = json.loads(planned.stdout)
    assert plan['arguments'] == {'region': 'east', 'zone': 'eu'}, plan
    assert plan['inputs'] == ['prepare region=east zone=eu'], plan

    default_plan = run('do', 'plan', 'deploy')
    assert default_plan.returncode == 0, default_plan.stderr
    assert json.loads(default_plan.stdout)['arguments'] == {'region': 'west', 'zone': 'global'}
    assert not (project / 'dependency-log').exists(), 'planning ran a dependency recipe'
    dry_run = run('--dry-run', 'deploy', 'region=north', 'zone=ap')
    assert dry_run.returncode == 0, dry_run.stderr
    assert not (project / 'dependency-log').exists(), 'dry-run executed a dependency recipe'

    built = run('deploy', 'region=east', 'zone=eu', 'other')
    assert built.returncode == 0, built.stderr
    built = run('deploy')
    assert built.returncode == 0, built.stderr
    assert (project / 'dependency-log').read_text() == 'eastwest'
    assert (project / 'deploy-log').read_text() == 'eastwest'

    for operands in [('other=bad',), ('region=a', 'region=b')]:
        rejected = run('do', 'plan', 'deploy', *operands)
        assert rejected.returncode == 1, (operands, rejected.stdout, rejected.stderr)
        assert 'TGT_ARGUMENT' in rejected.stderr, rejected.stderr
    malformed = run('do', 'plan', 'deploy region')
    assert malformed.returncode == 1 and 'TGT_ARGUMENT' in malformed.stderr, malformed.stderr
    json_error = run('do', 'plan', '--json', 'deploy', 'other=bad')
    assert json_error.returncode == 1, json_error.stdout
    diagnostic = json.loads(json_error.stdout)
    assert diagnostic['type'] == 'diagnostic', diagnostic
    assert diagnostic['diagnostic']['code'] == 'TGT_ARGUMENT', diagnostic
    assert 'other' in diagnostic['diagnostic']['message'], diagnostic

with tempfile.TemporaryDirectory(prefix='kame-target-argument-watch-') as directory:
    project = Path(directory)
    (project / 'input').write_text('before')
    (project / 'Makefile.kmk').write_text(
        'deploy {region=west} : ./input\n'
        '\tprintf @(region): >> watch-log; cat ./input >> watch-log\n'
    )
    process = subprocess.Popen([*runner, '--watch', 'deploy', 'region=east'], cwd=project, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    try:
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline and (not (project / 'watch-log').exists() or (project / 'watch-log').read_text() != 'east:before'):
            if process.poll() is not None:
                raise AssertionError(f'watch exited early: {process.stderr.read()}')
            time.sleep(0.02)
        assert (project / 'watch-log').exists() and (project / 'watch-log').read_text() == 'east:before', 'watch did not bind the target argument initially'
        (project / 'input').write_text('after')
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline and (project / 'watch-log').read_text() != 'east:beforeeast:after':
            if process.poll() is not None:
                raise AssertionError(f'watch exited before invalidation: {process.stderr.read()}')
            time.sleep(0.02)
        assert (project / 'watch-log').read_text() == 'east:beforeeast:after', 'watch lost the target argument on invalidation'
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGINT)
        process.communicate(timeout=3)

print('target argument planning, execution, defaults and validation passed')
