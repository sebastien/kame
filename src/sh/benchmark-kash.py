#!/usr/bin/env python3
"""Compare Kash with Bash or an earlier Kash build on checked shell workloads.

CPU includes waited-for children. On Linux, --rss runs a separate pass with
GNU time: peak RSS is the largest single process, not the sum of a pipeline.
"""
import argparse
import os
from pathlib import Path
import statistics
import subprocess
import sys
import tempfile
import time

WORKLOADS = {
    # name: (Kash source, Bash source, expected stdout or count of zero bytes)
    'startup-empty': ('', ':', b''),
    'builtins-loop-1000': (
        'for i in (split "\\n" (strip $(seq 1000)))\n\tif (not (empty? i))\n\t\techo line $i\n',
        'for i in $(seq 1000); do if test -n "$i"; then echo line $i; fi; done\n',
        b''.join(f'line {i}\n'.encode() for i in range(1, 1001))),
    'spawn-300': (
        'for i in (split "\\n" (strip $(seq 300)))\n\tcat /dev/null\n',
        'for i in $(seq 300); do cat /dev/null; done\n', b''),
    'pipeline-100': (
        'for i in (split "\\n" (strip $(seq 100)))\n\tprintf "a\\nb\\n" | sort | uniq\n',
        'for i in $(seq 100); do printf "a\\nb\\n" | sort | uniq; done\n', b'a\nb\n' * 100),
    'stdout-256MiB': ('head -c 268435456 /dev/zero\n', 'head -c 268435456 /dev/zero\n', 256 << 20),
    'list-reference-1000': (
        'let big = (split "\\n" (strip $(seq 20000)))\nfor i in (split "\\n" (strip $(seq 1000)))\n\t(nop (count big))\necho (count big)\n',
        'mapfile -t big < <(seq 20000); for i in $(seq 1000); do n=${#big[@]}; done; echo "$n"\n',
        b'20000\n'),
    'file-records-1000': (
        'for item from ./records :lines\n\techo $item\n',
        'while IFS= read -r item; do echo "$item"; done < records\n',
        b''.join(f'{i:07d}\n'.encode() for i in range(1000))),
    'map-filter-100k': (
        'let xs = (split "\\n" (strip $(seq 100000)))\nlet ys = (map (x => str x "!") xs)\necho (count (filter (x => ends? "0!" x) ys))\n',
        'mapfile -t xs < <(seq 100000); for x in "${xs[@]}"; do ys+=("$x!"); done; '
        'for y in "${ys[@]}"; do case "$y" in *0!) zs+=("$y");; esac; done; echo "${#zs[@]}"\n',
        b'10000\n'),
    'large-tool-64MiB': ('./large-tool\n', './large-tool\n', b''),
}


def validate(argv, cwd, env, expected):
    with subprocess.Popen(argv, cwd=cwd, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE) as process:
        if isinstance(expected, int):
            size, only_zeroes = 0, True
            while chunk := process.stdout.read(64 << 10):
                size += len(chunk)
                only_zeroes = only_zeroes and not chunk.strip(b'\0')
            correct = only_zeroes and size == expected
        else:
            correct = process.stdout.read() == expected
        if process.wait() != 0 or not correct:
            raise SystemExit(f'failed validation: {" ".join(argv)}')


def measure(argv, cwd, env):
    start = time.perf_counter_ns()
    with subprocess.Popen(argv, cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL) as process:
        _, status, usage = os.wait4(process.pid, 0)
        process.returncode = os.waitstatus_to_exitcode(status)
    wall = (time.perf_counter_ns() - start) / 1e6
    if process.returncode != 0:
        raise SystemExit(f'failed: {" ".join(argv)}')
    return (usage.ru_utime + usage.ru_stime) * 1000, wall


def peak_rss(argv, cwd, env):
    # wait4 directly from Python includes the Python parent's pre-exec RSS floor.
    meter = cwd / 'peak-rss'
    subprocess.run(['/usr/bin/time', '-f', '%M', '-o', str(meter), *argv], cwd=cwd, env=env,
                   stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, check=True)
    return int(meter.read_text()) / 1024


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--samples', type=int, default=5)
    parser.add_argument('--kash', type=Path, default=Path(os.environ.get('KASH_BIN', 'dist/kash')))
    against = parser.add_mutually_exclusive_group()
    against.add_argument('--bash', default='bash')
    against.add_argument('--baseline-kash', type=Path, help='compare against this Kash build on identical scripts')
    parser.add_argument('--rss', action='store_true', help='also measure peak MiB (Linux with GNU time)')
    parser.add_argument('workloads', nargs='*', default=list(WORKLOADS))
    args = parser.parse_args()
    if args.samples < 1:
        parser.error('--samples must be positive')
    if args.rss and (sys.platform != 'linux' or not Path('/usr/bin/time').is_file()):
        parser.error('--rss requires Linux with GNU /usr/bin/time')
    # Kash dispatches on argv[0]; dist/kash is a symlink to the kame binary.
    kash = str(args.kash.absolute())
    other, suffix, label = (str(args.baseline_kash.absolute()), 'kash', 'base') if args.baseline_kash else (args.bash, 'sh', 'bash')
    with tempfile.TemporaryDirectory(prefix='kash-bench-') as tmp:
        root = Path(tmp)
        # Do not let previous benchmark runs seed the tool digest store.
        env = dict(os.environ, XDG_CACHE_HOME=str(root / 'cache'))
        if 'file-records-1000' in args.workloads:
            (root / 'records').write_bytes(WORKLOADS['file-records-1000'][2])
        if 'large-tool-64MiB' in args.workloads:
            tool = root / 'large-tool'
            with tool.open('wb') as f:
                f.write(b'#!/bin/sh\nexit 0\n')
                f.write(b'#' * (64 << 20) + b'\n')
            tool.chmod(0o755)
        results = {}
        for name in args.workloads:
            kash_source, bash_source, expected = WORKLOADS[name]
            (root / f'{name}.kash').write_text(kash_source)
            (root / f'{name}.sh').write_text(bash_source)
            validate([kash, f'{name}.kash'], root, env, expected)
            validate([other, f'{name}.{suffix}'], root, env, expected)
            results[name] = ([], [])
        for sample in range(args.samples):
            for name in args.workloads:
                commands = ([kash, f'{name}.kash'], [other, f'{name}.{suffix}'])
                for i in ((0, 1) if sample % 2 == 0 else (1, 0)):
                    values = measure(commands[i], root, env)
                    if args.rss:
                        values += (peak_rss(commands[i], root, env),)
                    results[name][i].append(values)
        print('Median CPU and wall time in ms' + ('; peak RSS in MiB' if args.rss else ''))
        print(f'workload                   kash CPU   {label} CPU   ratio  kash wall  {label} wall'
              + (f'   kash RSS   {label} RSS' if args.rss else ''))
        for name, pair in results.items():
            k, b = ([statistics.median(column) for column in zip(*runs)] for runs in pair)
            ratio = k[0] / b[0] if b[0] else float('inf')
            print(f'{name:24} {k[0]:10.2f} {b[0]:10.2f} {ratio:7.2f} {k[1]:10.2f} {b[1]:10.2f}'
                  + (f' {k[2]:10.2f} {b[2]:10.2f}' if args.rss else ''))


if __name__ == '__main__':
    main()
