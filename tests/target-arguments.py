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
    (project / 'Makefile.km').write_text(
        'task prepare {region=west} {zone=global} :\n'
        '\tprintf %s $region >> dependency-log\n'
        'task deploy {region=west} {zone=global} : "prepare region=${region} zone=${zone}"\n'
        '\tprintf %s $region >> deploy-log\n'
        'task required {environment} :\n'
        '\tprintf %s $environment >> required-log\n'
        'task literal {value=env/KAME_SPEC_055_DEFAULT}\n'
        '\t(write! ./literal-log value)\n'
        'task other :\n\ttrue\n'
        'task cached memoized {region=west} {zone=global}\n'
        '\tprintf "%s:%s\\n" $region $zone >> cache-log\n'
        'task scoped {selector=west} : "./artifact/${selector}-file.txt"\n'
        '\t(write! ./selector-log selector)\n'
        'task forward {environment}\n'
        '\t(out! environment)\n'
        'rule ./artifact/{selector}.txt = selector\n'
    )

    def run(*args):
        return subprocess.run([*runner, *args], cwd=project, text=True, capture_output=True, timeout=10)

    parsed = run('do', 'parse', '--json', '--lang', 'km', 'Makefile.km')
    assert parsed.returncode == 0, parsed.stderr
    rule = json.loads(parsed.stdout)['ast']['items'][0]['rule']
    assert [(item['name'], item['optional'], item.get('default')) for item in rule['arguments']] == [
        ('region', True, 'west'), ('zone', True, 'global')
    ], rule

    missing_required = run('do', 'plan', 'required')
    assert missing_required.returncode == 1 and 'TGT_ARGUMENT' in missing_required.stderr, missing_required.stderr
    required_plan = run('do', 'plan', '--json', 'required', 'environment=production')
    assert required_plan.returncode == 0, required_plan.stderr
    assert json.loads(required_plan.stdout)['producers'][0]['arguments'] == {'environment': 'production'}

    literal_plan = run('do', 'plan', '--json', 'literal')
    assert literal_plan.returncode == 0, literal_plan.stderr
    assert json.loads(literal_plan.stdout)['producers'][0]['arguments'] == {'value': 'env/KAME_SPEC_055_DEFAULT'}
    assert not (project / 'literal-log').exists(), 'planning evaluated the literal task'
    literal_build = run('literal')
    assert literal_build.returncode == 0, literal_build.stderr
    assert (project / 'literal-log').read_text() == 'env/KAME_SPEC_055_DEFAULT'

    planned = run('do', 'plan', '--json', 'deploy', 'region=east', 'zone=eu')
    assert planned.returncode == 0, planned.stderr
    plan = json.loads(planned.stdout)
    assert plan['producers'][0]['arguments'] == {'region': 'east', 'zone': 'eu'}, plan
    assert any(resource['display'] == 'prepare region=east zone=eu' for resource in plan['resources']), plan

    default_plan = run('do', 'plan', '--json', 'deploy')
    assert default_plan.returncode == 0, default_plan.stderr
    assert json.loads(default_plan.stdout)['producers'][0]['arguments'] == {'region': 'west', 'zone': 'global'}
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

    literal_target = run('deploy', 'region=east', '--', './artifact/literal=token.txt')
    assert literal_target.returncode == 0, literal_target.stderr
    assert (project / 'artifact' / 'literal=token.txt').read_text() == 'literal=token'

    forwarded = run('do', 'run', 'Makefile.km', 'forward', 'environment=production',
                    '--lang', 'kash', '-c', '(out! "|" args.0 "|" args.1)', '--',
                    'environment=literal', '--dry-run')
    assert forwarded.returncode == 0, forwarded.stderr
    assert forwarded.stdout == 'production|environment=literal|--dry-run', forwarded.stdout

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

    for operands in [
        ('required',),
        ('memoized', 'other=bad'),
        ('memoized', 'region=a', 'region=b'),
        ('memoized region',),
    ]:
        rejected = run('--json', *operands)
        assert rejected.returncode == 1, (operands, rejected.stdout, rejected.stderr)
        events = [json.loads(line) for line in rejected.stdout.splitlines()]
        assert any(event.get('diagnostic', {}).get('code') == 'TGT_ARGUMENT' for event in events), events
        assert all(event['type'] not in {'process-started', 'effect'} for event in events), events
        assert not (project / 'required-log').exists()
        assert not (project / 'cache-log').exists()

    for operands, expected in [
        (('region=east', 'zone=eu'), 'east:eu\n'),
        (('zone=eu', 'region=east'), 'east:eu\n'),
        (('zone=ap', 'region=east'), 'east:eu\neast:ap\n'),
    ]:
        built = run('memoized', *operands)
        assert built.returncode == 0, built.stderr
        assert (project / 'cache-log').read_text() == expected, built.stderr

    scoped_plan = run('do', 'plan', '--json', 'scoped', 'selector=east')
    assert scoped_plan.returncode == 0, scoped_plan.stderr
    producers = {entry['target']: entry for entry in json.loads(scoped_plan.stdout)['producers']}
    scoped = producers['scoped selector=east']
    assert scoped['arguments'] == {'selector': 'east'}
    assert scoped['captures'] == []
    dependency = producers['./artifact/east-file.txt']
    assert dependency['captures'] == [{'name': 'selector', 'value': 'east-file'}], dependency
    assert 'arguments' not in dependency, dependency
    scoped_build = run('scoped', 'selector=east')
    assert scoped_build.returncode == 0, scoped_build.stderr
    assert (project / 'selector-log').read_text() == 'east'
    assert (project / 'artifact/east-file.txt').read_text() == 'east-file'

with tempfile.TemporaryDirectory(prefix='kame-target-argument-watch-') as directory:
    project = Path(directory)
    (project / 'input').write_text('before')
    (project / 'Makefile.km').write_text(
        'label ?= "authored"\n'
        'task deploy {region=west} : ./input ; {shell: "sh"}\n'
        '\tprintf \'%s:%s:%s:\' \'@{region}\' \'@{label}\' "$HOST" >> watch-log; cat ./input >> watch-log\n'
    )
    process = subprocess.Popen([*runner, '--watch', 'deploy', 'region=east', 'label=override', 'env/HOST=local'], cwd=project, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    try:
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline and (not (project / 'watch-log').exists() or (project / 'watch-log').read_text() != 'east:override:local:before'):
            if process.poll() is not None:
                raise AssertionError(f'watch exited early: {process.stderr.read()}')
            time.sleep(0.02)
        assert (project / 'watch-log').exists() and (project / 'watch-log').read_text() == 'east:override:local:before', 'watch did not bind the parameter and environment overrides initially'
        (project / 'input').write_text('after')
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline and (project / 'watch-log').read_text() != 'east:override:local:beforeeast:override:local:after':
            if process.poll() is not None:
                raise AssertionError(f'watch exited before invalidation: {process.stderr.read()}')
            time.sleep(0.02)
        assert (project / 'watch-log').read_text() == 'east:override:local:beforeeast:override:local:after', 'watch lost parameter or environment overrides on invalidation'
        source = project / 'Makefile.km'
        source.write_text(source.read_text().replace("'%s:%s:%s:'", "'reload:%s:%s:%s:'"))
        expected = 'east:override:local:beforeeast:override:local:afterreload:east:override:local:after'
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline and (project / 'watch-log').read_text() != expected:
            if process.poll() is not None:
                raise AssertionError(f'watch exited during source reload: {process.stderr.read()}')
            time.sleep(0.02)
        assert (project / 'watch-log').read_text() == expected, 'watch lost parameter or environment overrides on source reload'
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGINT)
        try:
            process.communicate(timeout=3)
        except subprocess.TimeoutExpired:
            process.kill()
            process.communicate(timeout=3)
            raise

print('target argument planning, execution, defaults and validation passed')
