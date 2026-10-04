"""Native source reload and input invalidation during an active recipe."""
import os
from pathlib import Path
import select
import signal
import subprocess
import sys
import tempfile
import time

runner = sys.argv[1:]
cases = 0

def wait_for(process, predicate, error, label):
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        assert process.poll() is None, (label, process.returncode, error.read_text())
        if predicate():
            return
        time.sleep(.025)
    raise AssertionError((label, error.read_text()))

def read(path):
    return path.read_text() if path.exists() else ''

def stop(process):
    process.send_signal(signal.SIGINT)
    try:
        assert process.wait(timeout=5) == 130, process.returncode
    except BaseException:
        process.kill()
        process.wait()
        raise

with tempfile.TemporaryDirectory(prefix='kame-watch-') as directory:
    project = Path(directory)
    source = project / 'Makefile.kmk'
    included = project / 'values.kmk'
    source.write_text('include values.kmk\nchosen :\n\tprintf %s @(MARK) >> source-log\n')
    included.write_text('MARK = "alpha"\n')
    error = project / 'stderr'
    with (project / 'stdout').open('w') as stdout, error.open('w') as stderr:
        process = subprocess.Popen([*runner, '-C', directory, '--watch', 'chosen'], stdout=stdout, stderr=stderr)
        try:
            log = project / 'source-log'
            wait_for(process, lambda: read(log) == 'alpha', error, 'initial source build')
            stamp = included.stat()
            included.write_text('MARK = "bravo"\n')
            os.utime(included, ns=(stamp.st_atime_ns, stamp.st_mtime_ns))
            wait_for(process, lambda: read(log) == 'alphabravo', error, 'preserved-mtime included source reload')
            cases += 1
            included.write_text('MARK = (\n')
            wait_for(process, lambda: 'PARSE_ERR' in error.read_text(), error, 'malformed include diagnostic')
            included.write_text('MARK = "gamma"\n')
            wait_for(process, lambda: read(log) == 'alphabravogamma', error, 'recovery after source repair')
            cases += 1
            source.write_text('include values.kmk\nchosen : added\n\tprintf %s @(MARK) >> source-log\nadded :\n\tprintf dependency >> source-log\n')
            wait_for(process, lambda: read(log) == 'alphabravogammadependencygamma', error, 'changed source graph')
            cases += 1
        finally:
            stop(process)
    assert 'AddressSanitizer' not in error.read_text() and 'runtime error:' not in error.read_text(), error.read_text()

with tempfile.TemporaryDirectory(prefix='kame-watch-busy-') as directory:
    project = Path(directory)
    (project / 'input').write_text('before')
    (project / 'Makefile.kmk').write_text('./output : ./input\n\tcp @< ./snapshot; touch ready; while [ ! -e release ]; do sleep .01; done; cp ./snapshot @>; printf x >> runs\n')
    error = project / 'stderr'
    with (project / 'stdout').open('w') as stdout, error.open('w') as stderr:
        process = subprocess.Popen([*runner, '-C', directory, '--watch', './output'], stdout=stdout, stderr=stderr)
        try:
            wait_for(process, lambda: (project / 'ready').exists(), error, 'recipe read handshake')
            (project / 'input').write_text('after')
            # Give the 200 ms scanner an opportunity while the recipe is blocked.
            time.sleep(.35)
            (project / 'release').touch()
            wait_for(process, lambda: read(project / 'output') == 'after' and read(project / 'runs') == 'xx', error, 'invalidation after newer output publication')
            cases += 1
        finally:
            stop(process)
    assert 'AddressSanitizer' not in error.read_text() and 'runtime error:' not in error.read_text(), error.read_text()

with tempfile.TemporaryDirectory(prefix='kame-watch-glob-') as directory:
    project = Path(directory)
    (project / 'inputs').mkdir()
    (project / 'inputs/a.txt').write_text('a')
    (project / 'Makefile.kmk').write_text('./joined : ./inputs/*.txt\n\tcat @<* > @>\n')
    error = project / 'stderr'
    with (project / 'stdout').open('w') as stdout, error.open('w') as stderr:
        process = subprocess.Popen([*runner, '-C', directory, '--watch', './joined'], stdout=stdout, stderr=stderr)
        try:
            wait_for(process, lambda: read(project / 'joined') == 'a', error, 'initial glob build')
            (project / 'inputs/b.txt').write_text('b')
            wait_for(process, lambda: read(project / 'joined') == 'ab', error, 'glob membership invalidation')
            (project / 'inputs/b.txt').unlink()
            wait_for(process, lambda: read(project / 'joined') == 'a', error, 'removed glob member invalidation')
            cases += 1
        finally:
            stop(process)
    assert 'AddressSanitizer' not in error.read_text() and 'runtime error:' not in error.read_text(), error.read_text()

with tempfile.TemporaryDirectory(prefix='kame-watch-missing-source-') as directory:
    project = Path(directory)
    error = project / 'stderr'
    with (project / 'stdout').open('w') as stdout, error.open('w') as stderr:
        process = subprocess.Popen([*runner, '-C', directory, '--watch', 'chosen'], stdout=stdout, stderr=stderr)
        try:
            wait_for(process, lambda: 'BUILD_NO_SOURCE' in error.read_text(), error, 'missing source diagnostic')
            (project / 'Makefile.kmk').write_text('chosen :\n\tprintf appeared > source-created\n')
            wait_for(process, lambda: read(project / 'source-created') == 'appeared', error, 'creation of discovered source')
            cases += 1
        finally:
            stop(process)
    assert 'AddressSanitizer' not in error.read_text() and 'runtime error:' not in error.read_text(), error.read_text()

with tempfile.TemporaryDirectory(prefix='kame-watch-optional-include-') as directory:
    project = Path(directory)
    (project / 'Makefile.kmk').write_text('include? ./optional.kmk\nchosen : configured\n\tprintf chosen >> watch-log\n')
    error = project / 'stderr'
    with (project / 'stdout').open('w') as stdout, error.open('w') as stderr:
        process = subprocess.Popen([*runner, '-C', directory, '--watch', 'chosen'], stdout=stdout, stderr=stderr)
        try:
            wait_for(process, lambda: 'TGT_NO_RULE' in error.read_text(), error, 'missing optional include dependency')
            (project / 'optional.kmk').write_text('configured :\n\tprintf configured >> watch-log\n')
            wait_for(process, lambda: read(project / 'watch-log') == 'configuredchosen', error, 'optional include creation and root recovery')
            time.sleep(.5)
            assert read(project / 'watch-log') == 'configuredchosen', 'idle watcher repeated settled tasks'
            cases += 1
        finally:
            stop(process)
    assert 'AddressSanitizer' not in error.read_text() and 'runtime error:' not in error.read_text(), error.read_text()

with tempfile.TemporaryDirectory(prefix='kame-watch-obsolete-input-') as directory:
    project = Path(directory)
    old_input = project / 'old'
    new_input = project / 'new'
    output = project / 'joined'
    runs = project / 'runs'
    old_input.write_text('old')
    source = project / 'Makefile.kmk'
    source.write_text('./joined : ./old\n\tcat @< > @>; printf x >> runs\n')
    error = project / 'stderr'
    with (project / 'stdout').open('w') as stdout, error.open('w') as stderr:
        process = subprocess.Popen([*runner, '-C', directory, '--watch', './joined'], stdout=stdout, stderr=stderr)
        try:
            wait_for(process, lambda: read(output) == 'old' and read(runs) == 'x', error, 'initial observed input')
            source.write_text('./joined : ./new\n\tcat @< > @>; printf x >> runs\n')
            new_input.write_text('new')
            wait_for(process, lambda: read(output) == 'new' and read(runs) == 'xx', error, 'replacement input graph')
            old_input.write_text('obsolete')
            time.sleep(.5)
            assert read(output) == 'new' and read(runs) == 'xx', 'obsolete input subscription triggered work'
            new_input.write_text('current')
            wait_for(process, lambda: read(output) == 'current' and read(runs) == 'xxx', error, 'current input subscription remains active')
            cases += 1
        finally:
            stop(process)
    assert 'AddressSanitizer' not in error.read_text() and 'runtime error:' not in error.read_text(), error.read_text()

with tempfile.TemporaryDirectory(prefix='kame-watch-shared-roots-') as directory:
    project = Path(directory)
    (project / 'Makefile.kmk').write_text('shared :\n\tprintf s >> shared-log\nalpha : shared\n\tprintf a > alpha-out\nbeta : shared\n\tprintf b > beta-out\n')
    error = project / 'stderr'
    with (project / 'stdout').open('w') as stdout, error.open('w') as stderr:
        process = subprocess.Popen([*runner, '-C', directory, '--watch', 'alpha', 'beta'], stdout=stdout, stderr=stderr)
        try:
            wait_for(process, lambda: read(project / 'alpha-out') == 'a' and read(project / 'beta-out') == 'b', error, 'shared prerequisite roots')
            assert read(project / 'shared-log') == 's', 'shared prerequisite ran more than once'
            time.sleep(.5)
            assert read(project / 'shared-log') == 's', 'idle shared roots repeated prerequisite work'
            cases += 1
        finally:
            stop(process)
    assert 'AddressSanitizer' not in error.read_text() and 'runtime error:' not in error.read_text(), error.read_text()

with tempfile.TemporaryDirectory(prefix='kame-watch-scoped-env-') as directory:
    project = Path(directory)
    (project / 'input').write_text('first')
    source = project / 'Makefile.kmk'
    source.write_text('chosen : ./input ; [env: [WATCH_SCOPED: "scoped"]]\n\tprintf %s "$WATCH_SCOPED" >> watch-log\n')
    error = project / 'stderr'
    with (project / 'stdout').open('w') as stdout, error.open('w') as stderr:
        process = subprocess.Popen([*runner, '-C', directory, '--env', 'WATCH_SCOPED=invocation', '--watch', 'chosen'], stdout=stdout, stderr=stderr)
        try:
            log = project / 'watch-log'
            wait_for(process, lambda: read(log) == 'scoped', error, 'initial scoped environment')
            (project / 'input').write_text('second')
            wait_for(process, lambda: read(log) == 'scopedscoped', error, 'scoped environment after input invalidation')
            source.write_text('chosen : ./input ; [env: [WATCH_SCOPED: "scoped"]]\n\tprintf %s "$WATCH_SCOPED" >> watch-log; : # source reload\n')
            wait_for(process, lambda: read(log) == 'scopedscopedscoped', error, 'scoped environment after source reload')
            (project / 'input').write_text('third')
            wait_for(process, lambda: read(log) == 'scopedscopedscopedscoped', error, 'scoped environment after reloaded graph invalidation')
            cases += 1
        finally:
            stop(process)
    assert 'AddressSanitizer' not in error.read_text() and 'runtime error:' not in error.read_text(), error.read_text()

with tempfile.TemporaryDirectory(prefix='kame-watch-active-cancel-') as directory:
    project = Path(directory)
    (project / 'Makefile.kmk').write_text('chosen :\n\tsleep 60 & echo $! > child-pid; touch ready; wait\n')
    error = project / 'stderr'
    with (project / 'stdout').open('w') as stdout, error.open('w') as stderr:
        process = subprocess.Popen([*runner, '-C', directory, '--watch', 'chosen'], stdout=stdout, stderr=stderr)
        try:
            wait_for(process, lambda: (project / 'ready').exists() and (project / 'child-pid').exists(), error, 'active watcher child start')
            child_pid = int((project / 'child-pid').read_text())
            stop(process)
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                try:
                    os.kill(child_pid, 0)
                except ProcessLookupError:
                    break
                time.sleep(.025)
            else:
                raise AssertionError(f'watch cancellation left child process {child_pid} alive')
            cases += 1
        finally:
            if process.poll() is None:
                stop(process)
    assert 'AddressSanitizer' not in error.read_text() and 'runtime error:' not in error.read_text(), error.read_text()

with tempfile.TemporaryDirectory(prefix='kame-watch-output-pressure-') as directory:
    project = Path(directory)
    (project / 'emit.py').write_text("import os,pathlib\npathlib.Path('ready').touch()\nfor _ in range(128): os.write(1,b'Z'*65536)\npathlib.Path('done').touch()\n")
    (project / 'Makefile.kmk').write_text('chosen :\n\tpython3 emit.py\n')
    error = project / 'stderr'
    with error.open('w') as stderr:
        process = subprocess.Popen([*runner, '-C', directory, '--watch', 'chosen'], stdout=subprocess.PIPE, stderr=stderr)
        try:
            wait_for(process, lambda: (project / 'ready').exists(), error, 'watch producer readiness')
            time.sleep(.5)
            assert not (project / 'done').exists(), 'watch producer outran an unread public pipe'
            total = 0
            deadline = time.monotonic() + 15
            expected = 8 * 1024 * 1024
            while total < expected:
                remaining = deadline - time.monotonic()
                assert remaining > 0 and select.select([process.stdout], [], [], remaining)[0], 'watch output drain stalled'
                chunk = os.read(process.stdout.fileno(), 65536)
                assert chunk, 'watch output closed before all bytes were drained'
                total += chunk.count(b'Z')
            assert total == expected, f'watch output byte count = {total}'
            wait_for(process, lambda: (project / 'done').exists(), error, 'watch producer resumes after drain')
            cases += 1
        finally:
            if process.poll() is None:
                stop(process)
            process.stdout.close()
    assert 'AddressSanitizer' not in error.read_text() and 'runtime error:' not in error.read_text(), error.read_text()

with tempfile.TemporaryDirectory(prefix='kame-watch-root-repair-') as directory:
    project = Path(directory)
    (project / 'Makefile.kmk').write_text('MARK = (text (read ./input))\nchosen :\n\tprintf %s @(MARK) >> watch-log\n')
    error = project / 'stderr'
    with (project / 'stdout').open('w') as stdout, error.open('w') as stderr:
        process = subprocess.Popen([*runner, '-C', directory, '--watch', 'chosen'], stdout=stdout, stderr=stderr)
        try:
            wait_for(process, lambda: 'FS_ERR' in error.read_text(), error, 'failed root with missing input')
            (project / 'input').write_text('recovered')
            wait_for(process, lambda: read(project / 'watch-log') == 'recovered', error, 'root recovery after input repair')
            cases += 1
        finally:
            stop(process)
    assert 'AddressSanitizer' not in error.read_text() and 'runtime error:' not in error.read_text(), error.read_text()

with tempfile.TemporaryDirectory(prefix='kame-watch-grants-') as directory:
    project = Path(directory)
    source = project / 'Makefile.kmk'
    secret = project / 'secret'
    other = project / 'other'
    secret.write_text('first')
    source.write_text('MARK = (text (read ./secret))\nchosen : ./secret\n\tprintf %s @(MARK) >> watch-log\n')
    error = project / 'stderr'
    with (project / 'stdout').open('w') as stdout, error.open('w') as stderr:
        process = subprocess.Popen([*runner, '-C', directory, '--watch', 'chosen'], stdout=stdout, stderr=stderr)
        try:
            log = project / 'watch-log'
            wait_for(process, lambda: read(log) == 'first', error, 'granted initial read')
            secret.write_text('second')
            wait_for(process, lambda: read(log) == 'firstsecond', error, 'granted input invalidation')
            source.write_text('MARK = (text (read ../secret))\nchosen : ./secret\n\tprintf %s @(MARK) >> watch-log\n')
            wait_for(process, lambda: 'CAP_DENIED' in error.read_text(), error, 'denied read after source reload')
            assert read(log) == 'firstsecond', 'denied read ran the recipe'
            other.write_text('third')
            source.write_text('MARK = (text (read ./other))\nchosen : ./secret\n\tprintf %s @(MARK) >> watch-log\n')
            wait_for(process, lambda: read(log) == 'firstsecondthird', error, 'grant preserved after source reload')
            cases += 1
        finally:
            stop(process)
    assert 'AddressSanitizer' not in error.read_text() and 'runtime error:' not in error.read_text(), error.read_text()

print(f'{cases} watch scenarios passed')
