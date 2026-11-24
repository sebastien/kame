#!/usr/bin/env python3
"""Measure checked parser, runtime and file-build CLI workloads."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import statistics
import subprocess
import tempfile
import time


def timings(samples):
    return {'median_ms': round(statistics.median(samples), 2), 'min_ms': round(min(samples), 2),
            'max_ms': round(max(samples), 2)}


def publication(command, env, root, count):
    fixture = Path(__file__).resolve().parents[2] / 'examples/rules/publication'
    outputs = ('public/hello.txt', 'public/world.txt', 'public/index.txt')
    phases = {'clean': set(outputs), 'no-op': set(), 'edit': {'public/hello.txt', 'public/index.txt'}}
    samples = {phase: [] for phase in phases}
    for _ in range(count):
        with tempfile.TemporaryDirectory(dir=root, prefix='publication-') as tmp:
            project = Path(tmp).resolve()
            (project / 'notes').mkdir()
            for name in ('Makefile.km', 'banner.txt', 'notes/hello.txt', 'notes/world.txt'):
                shutil.copyfile(fixture / name, project / name)
            hello, world = project / 'notes/hello.txt', project / 'notes/world.txt'
            banner = (project / 'banner.txt').read_bytes()
            stamps = {}
            for phase, writes in phases.items():
                if phase == 'edit':
                    original = hello.read_bytes()
                    edited = original.replace(b'kame', b'kash')
                    assert len(original) == len(edited) and original != edited
                    stamp = hello.stat()
                    hello.write_bytes(edited)
                    os.utime(hello, ns=(stamp.st_atime_ns, stamp.st_mtime_ns))
                started = time.perf_counter()
                process = subprocess.run(command + ['--json', 'default'], cwd=project, env=env,
                                         stdin=subprocess.DEVNULL, capture_output=True, timeout=60)
                elapsed = (time.perf_counter() - started) * 1000
                if process.returncode:
                    raise RuntimeError(f'publication {phase} failed: {(process.stderr or process.stdout)[-1800:]!r}')
                pages = [banner + path.read_bytes().upper() for path in (hello, world)]
                events = [json.loads(line) for line in process.stdout.splitlines()]
                executed = {e['resource']['name'] for e in events if e['type'] == 'target-reason'
                            and e['decision'] == 'execute' and e['resource']['kind'] == 'file'}
                if phase == 'no-op' and any(e['type'] == 'process-started' for e in events):
                    raise RuntimeError('publication no-op launched a process')
                if executed != {str(project / name) for name in writes}:
                    raise RuntimeError(f'publication {phase}: unexpected rebuild set {executed}')
                for name, expected in zip(outputs, (*pages, b''.join(pages))):
                    output = project / name
                    info = output.stat()
                    stamp = (info.st_ino, info.st_mtime_ns, info.st_ctime_ns)
                    if output.read_bytes() != expected or (name not in writes and stamp != stamps[name]):
                        raise RuntimeError(f'publication {phase}: unexpected output or rewrite of {name}')
                    stamps[name] = stamp
                samples[phase].append(elapsed)
    return samples


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--samples', type=int, default=5)
    parser.add_argument('--native', type=Path, default=Path(os.environ.get('CLI_BIN', 'build/kame.debug')))
    parser.add_argument('--wrapper', type=Path, default=Path('dist/kame.js'))
    args = parser.parse_args()
    if args.samples < 1:
        parser.error('--samples must be positive')
    native, wrapper = args.native.resolve(), args.wrapper.resolve()
    wasm = Path(os.environ.get('KAME_WASM') or wrapper.with_name('kame.wasm')).resolve()
    wasm_env = dict(os.environ, KAME_WASM=str(wasm))
    result = {'platform': platform.platform(), 'node': subprocess.check_output(['node', '--version'], text=True).strip(),
              'samples': args.samples, 'artifacts': {}, 'results': {}}
    for name, path in [('native', native), ('wrapper', wrapper), ('wasm', wasm)]:
        result['artifacts'][name] = {'path': str(path), 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}
    with tempfile.TemporaryDirectory(prefix='kame-perf-') as tmp:
        root = Path(tmp)
        workloads = {}

        expression = root / 'rules-512.km'
        expression.write_text(''.join(f'v{i} = {i}\n' for i in range(512)) + 'task default\n\tout! ok\n')
        workloads['parse-512-definitions'] = (['do', 'parse', '--json', '--lang', 'km', str(expression)], lambda p: b'v511' in p.stdout)

        wide = root / 'scope-1000.km'
        wide.write_text(''.join(f'v{i} = {i}\n' for i in range(1000)))
        workloads['scope-wide-1000'] = (['-f', str(wide), 'v999'], lambda p: p.stdout == b'999')
        deep = '(item => ' * 16 + 'item' + ') 0' * 16
        workloads['scope-deep-16'] = (['do', 'run', '--lang', 'expr', '-c', deep], lambda p: p.stdout == b'0')

        graph = root / 'graph-512.km'
        graph.write_text(''.join(f'task target{i}\n\tout! ok\n' for i in range(512)))
        workloads['graph-lookup-512'] = (['do', 'plan', '--json', '-f', str(graph), 'target511'], lambda p: [producer['target'] for producer in json.loads(p.stdout)['producers']] == ['target511'])

        for count in (512, 2048):
            rules = root / f'file-rules-{count}.km'
            rules.write_text(''.join(f'rule ./r{i} = "ok"\n' for i in range(count)))
            target = f'./r{count - 1}'
            workloads[f'plan-literal-file-{count}'] = (
                ['do', 'plan', '--json', '-f', str(rules), target],
                lambda p, target=target: [producer['target'] for producer in json.loads(p.stdout)['producers']] == [target])

        tree = root / 'tree'
        for i in range(128):
            sibling = tree / f'sibling-{i:03}'
            sibling.mkdir(parents=True)
            (sibling / ('hit.txt' if i == 127 else 'noise.txt')).write_text('x')
        pattern = str(tree / '*' / 'hit.txt')
        invocation = ['do', 'run', '--lang', 'expr', '--allow-read', '-c', f'count (files {pattern})']
        workloads['glob-selective-128-siblings'] = (invocation, lambda p: p.stdout == b'1')

        for host, command, env in [('native', [str(native)], None), ('wasm', ['node', str(wrapper)], wasm_env)]:
            for name, (invocation, validate) in workloads.items():
                samples = []
                for _ in range(args.samples):
                    started = time.perf_counter()
                    process = subprocess.run(command + invocation, env=env, capture_output=True, timeout=60)
                    if process.returncode != 0:
                        detail = process.stderr.decode(errors='replace').strip().splitlines()
                        message = detail[-1] if detail else f'exit status {process.returncode}'
                        raise RuntimeError(f'{host} {name} failed: {message}')
                    samples.append((time.perf_counter() - started) * 1000)
                    if not validate(process):
                        raise RuntimeError(f'unexpected output for {name} on {host}: {process.stdout[:200]!r}')
                result['results'][f'{host}-{name}'] = timings(samples)
            for phase, samples in publication(command, env, root, args.samples).items():
                result['results'][f'{host}-publication-{phase}'] = timings(samples)
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()

# EOF
