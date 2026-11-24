#!/usr/bin/env python3
"""Compare Kash with bash on shell-style workloads, by per-process CPU time.

CPU time (user + system of the shell and its children, from wait4) is used
instead of wall time so results stay meaningful on a loaded machine. Commands
are interleaved per sample so drift affects every workload alike.
"""
import argparse
import os
from pathlib import Path
import statistics
import subprocess
import tempfile

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
    'map-filter-100k': (
        'let xs = (split "\\n" (strip $(seq 100000)))\nlet ys = (map (x => str x "!") xs)\necho (count (filter (x => ends? "0!" x) ys))\n',
        'mapfile -t xs < <(seq 100000); for x in "${xs[@]}"; do ys+=("$x!"); done; '
        'for y in "${ys[@]}"; do case "$y" in *0!) zs+=("$y");; esac; done; echo "${#zs[@]}"\n',
        b'10000\n'),
    'large-tool-64MiB': ('./large-tool\n', './large-tool\n', b''),
}


def validate(argv, cwd, env, expected):
    with subprocess.Popen(argv, cwd=cwd, env=env, stdout=subprocess.PIPE) as process:
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


def cpu_ms(argv, cwd, env):
    pid = os.fork()
    if pid == 0:
        devnull = os.open(os.devnull, os.O_WRONLY)
        os.dup2(devnull, 1)
        os.dup2(devnull, 2)
        os.chdir(cwd)
        try:
            os.execvpe(argv[0], argv, env)
        finally:
            os._exit(127)
    _, status, usage = os.wait4(pid, 0)
    if os.waitstatus_to_exitcode(status) != 0:
        raise SystemExit(f'failed: {" ".join(argv)}')
    return (usage.ru_utime + usage.ru_stime) * 1000


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--samples', type=int, default=5)
    parser.add_argument('--kash', type=Path, default=Path(os.environ.get('KASH_BIN', 'dist/kash')))
    parser.add_argument('--bash', default='bash')
    parser.add_argument('workloads', nargs='*', default=list(WORKLOADS))
    args = parser.parse_args()
    if args.samples < 1:
        parser.error('--samples must be positive')
    kash = str(args.kash.resolve())
    with tempfile.TemporaryDirectory(prefix='kash-bench-') as tmp:
        root = Path(tmp)
        # Do not let previous benchmark runs seed the tool digest store.
        env = dict(os.environ, XDG_CACHE_HOME=str(root / 'cache'))
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
            validate([args.bash, f'{name}.sh'], root, env, expected)
            results[name] = ([], [])
        for _ in range(args.samples):
            for name in args.workloads:
                results[name][0].append(cpu_ms([kash, f'{name}.kash'], root, env))
                results[name][1].append(cpu_ms([args.bash, f'{name}.sh'], root, env))
        print(f'{"workload":24} {"kash ms":>10} {"bash ms":>10} {"ratio":>7}')
        for name, (kash_ms, bash_ms) in results.items():
            k, b = statistics.median(kash_ms), statistics.median(bash_ms)
            print(f'{name:24} {k:10.2f} {b:10.2f} {k / b if b else float("inf"):7.2f}')


if __name__ == '__main__':
    main()
