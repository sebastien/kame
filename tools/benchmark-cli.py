#!/usr/bin/env python3
"""Measure checked parser/runtime CLI workloads, including host startup."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import statistics
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--samples', type=int, default=5)
    parser.add_argument('--native', type=Path, default=Path(os.environ.get('CLI_BIN', 'build/kame.debug')))
    parser.add_argument('--wrapper', type=Path, default=Path('dist/kame.js'))
    args = parser.parse_args()
    if args.samples < 1:
        parser.error('--samples must be positive')
    native, wrapper = args.native.resolve(), args.wrapper.resolve()
    result = {'platform': platform.platform(), 'node': subprocess.check_output(['node', '--version'], text=True).strip(),
              'samples': args.samples, 'artifacts': {}, 'results': {}}
    for name, path in [('native', native), ('wrapper', wrapper), ('wasm', wrapper.with_name('kame.wasm'))]:
        result['artifacts'][name] = {'path': str(path), 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}
    with tempfile.TemporaryDirectory(prefix='kame-perf-') as tmp:
        root = Path(tmp)
        workloads = {}

        expression = root / 'rules-512.kmk'
        expression.write_text(''.join(f'v{i} = {i}\n' for i in range(512)) + 'task default :\n\t@(out "ok")\n')
        workloads['parse-512-definitions'] = (['do', 'parse', '--json', '--lang', 'script', str(expression)], lambda p: b'v511' in p.stdout)

        wide = ' '.join(f'v{i} {i}' for i in range(1000))
        workloads['scope-wide-1000'] = (['do', 'run', '--lang', 'expr', '-c', f'(let [{wide}] v999)'], lambda p: p.stdout == b'999')
        deep = '(let [v0 0] ' * 16 + 'v0' + ')' * 16
        workloads['scope-deep-16'] = (['do', 'run', '--lang', 'expr', '-c', deep], lambda p: p.stdout == b'0')

        graph = root / 'graph-512.kmk'
        graph.write_text(''.join(f'task target{i} :\n\t@(out "ok")\n' for i in range(512)))
        workloads['graph-lookup-512'] = (['do', 'plan', '--json', '-f', str(graph), 'target511'], lambda p: json.loads(p.stdout)['target'] == 'target511')

        tree = root / 'tree'
        for i in range(128):
            sibling = tree / f'sibling-{i:03}'
            sibling.mkdir(parents=True)
            (sibling / ('hit.txt' if i == 127 else 'noise.txt')).write_text('x')
        pattern = str(tree / '*' / 'hit.txt')
        invocation = ['do', 'run', '--lang', 'expr', '--allow-read', '-c', f'(count (wildcard {pattern}))']
        workloads['glob-selective-128-siblings'] = (invocation, lambda p: p.stdout == b'1')

        for host, command in [('native', [str(native)]), ('wasm', ['node', str(wrapper)])]:
            for name, (invocation, validate) in workloads.items():
                samples = []
                for _ in range(args.samples):
                    started = time.perf_counter()
                    process = subprocess.run(command + invocation, capture_output=True, timeout=60)
                    if process.returncode != 0:
                        detail = process.stderr.decode(errors='replace').strip().splitlines()
                        message = detail[-1] if detail else f'exit status {process.returncode}'
                        raise RuntimeError(f'{host} {name} failed: {message}')
                    samples.append((time.perf_counter() - started) * 1000)
                    if not validate(process):
                        raise RuntimeError(f'unexpected output for {name} on {host}: {process.stdout[:200]!r}')
                result['results'][f'{host}-{name}'] = {key: round(value, 2) for key, value in
                    [('median_ms', statistics.median(samples)), ('min_ms', min(samples)), ('max_ms', max(samples))]}
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
