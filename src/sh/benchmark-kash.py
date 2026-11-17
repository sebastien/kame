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
import tempfile

WORKLOADS = {
    # name: (kash source, bash source)
    'startup-empty': ('', ':'),
    'builtins-loop-1000': (
        'for i in (split "\\n" (strip $(seq 1000)))\n\tif test -n $i\n\t\techo line $i\n',
        'for i in $(seq 1000); do if test -n "$i"; then echo line $i; fi; done\n'),
    'spawn-300': (
        'for i in (split "\\n" (strip $(seq 300)))\n\tcat /dev/null\n',
        'for i in $(seq 300); do cat /dev/null; done\n'),
    'pipeline-100': (
        'for i in (split "\\n" (strip $(seq 100)))\n\tprintf "a\\nb\\n" | sort | uniq\n',
        'for i in $(seq 100); do printf "a\\nb\\n" | sort | uniq; done\n'),
    'stdout-256MiB': ('head -c 268435456 /dev/zero\n', 'head -c 268435456 /dev/zero\n'),
    'list-reference-1000': (
        'big = (split "\\n" (strip $(seq 20000)))\nfor i in (split "\\n" (strip $(seq 1000)))\n\t(nop (count big))\n',
        'big=($(seq 20000)); for i in $(seq 1000); do n=${#big[@]}; done\n'),
    'map-filter-100k': (
        'xs = (split "\\n" (strip $(seq 100000)))\nys = (map ([x] (str x "!")) xs)\necho (count (filter ([x] (ends? "0!" x)) ys))\n',
        'n=0; for x in $(seq 100000); do y="$x!"; case $y in *0!) n=$((n+1));; esac; done; echo $n\n'),
    'large-tool-64MiB': ('./large-tool\n', './large-tool\n'),
}


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
    kash = str(args.kash.resolve())
    with tempfile.TemporaryDirectory(prefix='kash-bench-') as tmp:
        root = Path(tmp)
        # The persistent digest store lives under the user cache directory;
        # keep it private so the first sample measures a cold hash.
        env = dict(os.environ, XDG_CACHE_HOME=str(root / 'cache'))
        tool = root / 'large-tool'
        with tool.open('wb') as f:
            f.write(b'#!/bin/sh\nexit 0\n')
            f.write(b'#' * (64 << 20) + b'\n')
        tool.chmod(0o755)
        results = {}
        for name in args.workloads:
            kash_source, bash_source = WORKLOADS[name]
            (root / f'{name}.kash').write_text(kash_source)
            (root / f'{name}.sh').write_text(bash_source)
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
