#!/usr/bin/env python3
"""Measure checked CLI workloads, including host startup; print artifact hashes."""
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
        for host, command in [('native', [str(native)]), ('wasm', ['node', str(wrapper)])]:
            for size in [0, 100, 1000]:
                source = Path(tmp) / f'rules-{size}.kmk'
                source.write_text(''.join(f'v{i} = {i}\n' for i in range(size)) + 'task default :\n\t@(out "ok")\n')
                invocation = ['do', 'plan', '-f', str(source), 'default'] if size else ['do', 'run', '--lang', 'expr', '-c', '(count [1 2 3])']
                samples = []
                for _ in range(args.samples):
                    started = time.perf_counter()
                    process = subprocess.run(command + invocation, capture_output=True, timeout=30, check=True)
                    samples.append((time.perf_counter() - started) * 1000)
                    if size:
                        if json.loads(process.stdout)['target'] != 'default':
                            raise RuntimeError('unexpected plan output')
                    elif process.stdout != b'3':
                        raise RuntimeError('unexpected expression output')
                result['results'][f'{host}-{size}'] = {key: round(value, 2) for key, value in
                    [('median_ms', statistics.median(samples)), ('min_ms', min(samples)), ('max_ms', max(samples))]}
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
