"""Exercise reuse-reason decisions, deduplication and privacy through CLI hosts."""
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
    def run(text, arguments, environment=None):
        global cases
        (project / 'Makefile.kmk').write_text(text)
        result = subprocess.run(command + arguments, cwd=project,
            env=dict(os.environ, MODE='ambient', **(environment or {})),
            text=True, capture_output=True, timeout=15)
        assert result.returncode == 0, (text, result)
        cases += 1
        return result
    def reasons(result):
        assert result.stderr == '', result
        events = [json.loads(line) for line in result.stdout.splitlines()]
        selected = [event for event in events if event['type'] == 'target-reason']
        identities = [(e['node'], e['generation'], e['decision'], e['reason'],
                       json.dumps(e.get('dependency'), sort_keys=True), e.get('aspect')) for e in selected]
        assert len(identities) == len(set(identities)), selected
        assert all(e['decision'] in ('reevaluate', 'execute') and e['target'] and e['resource'] for e in selected)
        assert all(not {'before', 'after', 'environment', 'fingerprint'} & e.keys() for e in selected)
        return selected

    (project / 'input').write_text('unchanged')
    # Guarded reuse must skip derived-value evaluation, not merely publication.
    (project / 'extra.json').write_text('"A"')
    guarded = 'SHELL = kash\nPAYLOAD = (parse-json (text (read "./extra.json")))\n./guarded : ./input\n\t@(yield PAYLOAD)\n'
    first = reasons(run(guarded, ['--json', './guarded']))
    # Native file-context reads expose an opaque failure; forwarding establishes absence.
    assert any(e['reason'] in ('record-missing', 'proof-unverifiable') for e in first), first
    stamp = (project / 'guarded').stat().st_mtime_ns
    warm = run(guarded, ['--json', './guarded'])
    assert not reasons(warm)
    events = [json.loads(line) for line in warm.stdout.splitlines()]
    assert not any(e['type'] == 'process-started' for e in events)
    assert not any(e['type'] == 'dependency' and e['dependency']['resource']['kind'] == 'definition' for e in events)
    assert (project / 'guarded').stat().st_mtime_ns == stamp
    # An unrelated source edit falls back once, without rewriting equal bytes.
    guarded += 'UNUSED = "unrelated"\n'
    changed = reasons(run(guarded, ['--json', './guarded']))
    assert any(e['reason'] == 'guard-changed' and e['decision'] == 'reevaluate' for e in changed), changed
    assert not any(e['decision'] == 'execute' for e in changed), changed
    assert (project / 'guarded').stat().st_mtime_ns == stamp
    warm = run(guarded, ['--json', './guarded'])
    events = [json.loads(line) for line in warm.stdout.splitlines()]
    assert not any(e['type'] == 'dependency' and e['dependency']['resource']['kind'] == 'definition' for e in events)
    # Content identity must detect edits with identical size and timestamps.
    metadata = (project / 'extra.json').stat()
    (project / 'extra.json').write_text('"B"')
    os.utime(project / 'extra.json', ns=(metadata.st_atime_ns, metadata.st_mtime_ns))
    changed = reasons(run(guarded, ['--json', './guarded']))
    assert any(e['reason'] == 'input-changed' for e in changed), changed
    assert (project / 'guarded').read_text() == 'B'
    (project / 'guarded').write_text('tampered')
    changed = reasons(run(guarded, ['--json', './guarded']))
    assert any(e['reason'] == 'output-changed' for e in changed), changed
    assert (project / 'guarded').read_text() == 'B'
    (project / 'guarded').unlink()
    changed = reasons(run(guarded, ['--json', './guarded']))
    assert any(e['reason'] == 'output-missing' for e in changed), changed
    assert (project / 'guarded').read_text() == 'B'

    # Effective configuration changes invalidate the guard without leaking values.
    configured = 'SHELL = kash\nPAYLOAD ?= "default"\n./configured : ./input\n\t@(yield PAYLOAD)\n'
    config_args = ['--json', '--define', 'PAYLOAD=before-config', './configured']
    run(configured, config_args)
    assert not reasons(run(configured, config_args))
    config_args = ['--json', '--define', 'PAYLOAD=after-config', './configured']
    changed = run(configured, config_args)
    selected = reasons(changed)
    assert any(e['reason'] == 'guard-changed' and e['decision'] == 'reevaluate' for e in selected), selected
    assert any(e['decision'] == 'execute' for e in selected), selected
    assert (project / 'configured').read_text() == 'after-config'
    # Explicit recipe text may contain the configured value; explanations must not.
    assert 'before-config' not in json.dumps(selected)
    assert 'after-config' not in json.dumps(selected)
    assert not reasons(run(configured, config_args))

    # A child snapshot is aggregate evidence, not evidence about particular names.
    explain = 'SHELL = /bin/sh\n./explained : ./input\n\tcp input explained\n'
    first = reasons(run(explain, ['--json', './explained']))
    assert any(e['reason'] in ('record-missing', 'proof-unverifiable') for e in first), first
    stamp = (project / 'explained').stat().st_mtime_ns
    assert not reasons(run(explain, ['--json', './explained']))
    assert (project / 'explained').stat().st_mtime_ns == stamp
    secret = 'do-not-display-this-value'
    changed = run(explain, ['--json', './explained'], environment={'REASON_TEST': secret})
    selected = reasons(changed)
    assert any(e['reason'] == 'process-environment-changed' and 'dependency' not in e for e in selected), selected
    assert secret not in changed.stdout + changed.stderr
    assert not reasons(run(explain, ['--json', './explained'], environment={'REASON_TEST': secret}))
    forced = reasons(run(explain, ['--json', '--force', './explained']))
    assert any(e['reason'] == 'forced' and e['decision'] == 'execute' for e in forced), forced
    human = run(explain, ['--output', 'text', '--force', './explained'])
    assert human.stdout == '' and '[./explained] execute: forced execution' in human.stderr, human
    always = 'SHELL = kash\nalways ./explained : ./input\n\t@(yield "always")\n'
    selected = reasons(run(always, ['--json', './explained']))
    assert any(e['reason'] == 'always' for e in selected), selected
    named = 'SHELL = kash\n./named-env : ./input\n\t@(yield (env "REASON_NAMED"))\n'
    named_args = ['do', 'run', '--json', '--allow-read', '--allow-write', '--allow-env=REASON_NAMED', 'Makefile.kmk', '--entry', './named-env']
    run(named, named_args, environment={'REASON_NAMED': 'before-secret'})
    changed = run(named, named_args, environment={'REASON_NAMED': 'after-secret'})
    selected = reasons(changed)
    assert any(e['reason'] == 'environment-changed' and e['dependency']['resource']['name'] == 'REASON_NAMED' for e in selected), selected
    assert 'before-secret' not in json.dumps(selected) and 'after-secret' not in json.dumps(selected)
    # Corrupt records must explain a bad proof, not claim an input changed.
    records = [record for record in (project / '.kame/cache').rglob('*') if record.is_file()]
    assert records
    for record in records:
        record.write_bytes(b'corrupt')
    selected = reasons(run(explain, ['--json', './explained']))
    assert any(e['reason'] == 'record-invalid' for e in selected), selected
    assert not any(e['reason'] == 'input-changed' for e in selected), selected
    assert not reasons(run(explain, ['--json', './explained']))
    # Cached-task lookup must retain the same missing/invalid distinction.
    cached_reason = 'task reason-cached :\n\t@(out "cached reason")\n'
    selected = reasons(run(cached_reason, ['--json', 'reason-cached']))
    assert [e['reason'] for e in selected] == ['record-missing'], selected
    assert not reasons(run(cached_reason, ['--json', 'reason-cached']))
    cache_bucket = 'tasks' if backend == 'native' else 'host'
    records = [record for record in (project / '.kame/cache' / cache_bucket).iterdir() if record.is_file()]
    assert records
    for record in records:
        record.write_bytes(b'corrupt')
    selected = reasons(run(cached_reason, ['--json', 'reason-cached']))
    assert [e['reason'] for e in selected] == ['record-invalid'], selected
    assert not reasons(run(cached_reason, ['--json', 'reason-cached']))
print(f'{backend}: {cases} reuse-reason cases passed')
# EOF
