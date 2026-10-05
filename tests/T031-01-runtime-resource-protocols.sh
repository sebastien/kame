#!/usr/bin/env bash
# Spec: docs/spec/031-resource-protocols.md — resource URI CLI behavior
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T031-01 resource URI protocols"
test-step "build native and WASM runners"
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null

printf 'file-uri' >"$TMPDIR/file-resource.txt"
file_uri="file://$TMPDIR/file-resource.txt"
file_root="file://$TMPDIR/"

for host in native wasm; do
	if [ "$host" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
	"${runner[@]}" do run --lang expr --allow-write=mem://workspace/ --allow-read=mem://workspace/ \
		-c '(write (resource "mem://workspace/out.txt") "memory-uri")' \
		-c '(read (resource "mem://workspace/out.txt"))' >"$TMPDIR/$host.memory.out" 2>"$TMPDIR/$host.memory.err"
	if [ "$(cat "$TMPDIR/$host.memory.out")" = 'memory-uri' ]; then
		test-ok "$host reads a memory URI after publishing it"
	else
		test-fail "$host memory URI round trip: $(cat "$TMPDIR/$host.memory.err") $(cat "$TMPDIR/$host.memory.out")"
	fi
	"${runner[@]}" do run --lang expr --allow-write=mem://glob/ --allow-read=mem://glob/ \
		-c '(write (resource "mem://glob/a.txt") "a")' \
		-c '(write (resource "mem://glob/sub/b.txt") "b")' \
		-c '(wildcard (resource "mem://glob/**/*.txt"))' >"$TMPDIR/$host.glob.out" 2>"$TMPDIR/$host.glob.err"
	if [ "$(cat "$TMPDIR/$host.glob.out")" = '["mem://glob/a.txt" "mem://glob/sub/b.txt"]' ]; then
		test-ok "$host lists sorted recursive memory URI matches"
	else
		test-fail "$host memory URI glob: $(cat "$TMPDIR/$host.glob.err") $(cat "$TMPDIR/$host.glob.out")"
	fi

	"${runner[@]}" do run --lang expr "--allow-read=$file_root" -c "(read (resource \"$file_uri\"))" \
		>"$TMPDIR/$host.file.out" 2>"$TMPDIR/$host.file.err"
	if [ "$(cat "$TMPDIR/$host.file.out")" = 'file-uri' ]; then
		test-ok "$host maps a file URI through filesystem read grants"
	else
		test-fail "$host file URI read: $(cat "$TMPDIR/$host.file.err") $(cat "$TMPDIR/$host.file.out")"
	fi

	status=0
	"${runner[@]}" do run --lang expr --allow-write=mem://workspace/ \
		-c '(write (resource "mem://other/out.txt") "denied")' \
		>"$TMPDIR/$host.denied.out" 2>"$TMPDIR/$host.denied.err" || status=$?
	if [ "$status" -ne 0 ] && grep -Fq 'CAP_DENIED' "$TMPDIR/$host.denied.err"; then
		test-ok "$host keeps memory namespace grants isolated"
	else
		test-fail "$host memory namespace grant: $(cat "$TMPDIR/$host.denied.err")"
	fi
done

python3 - "$CLI_BIN" "$CLI_ROOT/dist/kame.js" <<'PY'
import pathlib
import subprocess
import sys
import tempfile
import time

native, wasm = sys.argv[1:]
for runner in ([native], ['node', wasm]):
    with tempfile.TemporaryDirectory(prefix='kame-resource-watch-') as directory:
        work = pathlib.Path(directory)
        source = work / 'input.txt'
        source.write_text('first')
        uri = 'file://' + str(source)
        (work / 'Makefile.kmk').write_text(
            f'./output : @((list (resource "{uri}")))\n'
            f'\t@(yield (read (resource "{uri}")))\n')
        process = subprocess.Popen(
            runner + ['-C', directory, '--watch', './output'],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        output = work / 'output'
        try:
            for expected in ('first', 'second'):
                if expected == 'second':
                    source.write_text(expected)
                deadline = time.monotonic() + 8
                while time.monotonic() < deadline:
                    if output.exists() and output.read_text() == expected:
                        break
                    if process.poll() is not None:
                        stdout, stderr = process.communicate()
                        raise AssertionError(
                            f'{runner[0]} watch exited {process.returncode}: {stderr} {stdout}')
                    time.sleep(.05)
                else:
                    raise AssertionError(f'{runner[0]} watch did not publish {expected!r}')
        finally:
            process.terminate()
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
PY
test-ok "native and WASM watch reload file URI dependencies"

test-end
