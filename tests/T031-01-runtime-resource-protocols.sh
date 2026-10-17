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
	mkdir -p "$TMPDIR/$host-work"
	# Isolate the process cwd as well as -C: bad URI parent traversal used to
	# create empty scheme-named directories outside the selected build directory.
	if [ "$host" = native ]; then runner=(env -C "$TMPDIR/$host-work" "$CLI_BIN"); else runner=(env -C "$TMPDIR/$host-work" node "$CLI_ROOT/dist/kame.js"); fi
	if (cd "$TMPDIR/$host-work" && "${runner[@]}" do run --lang expr --allow-write=mem://workspace/ --allow-read=mem://workspace/ \
		-c '(write! (resource "mem://workspace/out.txt") "memory-uri")' \
		-c '(read (resource "mem://workspace/out.txt"))' >"$TMPDIR/$host.memory.out" 2>"$TMPDIR/$host.memory.err"); then
		:
	else
		test-fail "$host memory URI round trip: $(cat "$TMPDIR/$host.memory.err") $(cat "$TMPDIR/$host.memory.out")"
	fi
	if [ "$(cat "$TMPDIR/$host.memory.out")" = 'memory-uri' ] && [ ! -e "$TMPDIR/$host-work/mem:" ]; then
		test-ok "$host reads a memory URI after publishing it"
	else
		test-fail "$host memory URI must not create a local mem: path"
	fi
	"${runner[@]}" do run --lang expr --allow-write=mem://workspace/ --allow-read=mem://workspace/ \
		-c '(write! (resource "mem://workspace/entry%252F%23%3f") "escaped-memory")' \
		-c '(read (resource "mem://workspace/entry%252F%23%3F"))' >"$TMPDIR/$host.escaped-memory.out" 2>"$TMPDIR/$host.escaped-memory.err"
	if [ "$(cat "$TMPDIR/$host.escaped-memory.out")" = 'escaped-memory' ]; then
		test-ok "$host preserves percent, hash, and question characters in memory URI identity"
	else
		test-fail "$host escaped memory URI round trip: $(cat "$TMPDIR/$host.escaped-memory.err")"
	fi
	"${runner[@]}" do run --lang expr --allow-write=mem://glob/ --allow-read=mem://glob/ \
		-c '(write! (resource "mem://glob/a.txt") "a")' \
		-c '(write! (resource "mem://glob/sub/b.txt") "b")' \
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
	escaped_file_uri="file://$TMPDIR/$host-entry%252F%23%3F"
	"${runner[@]}" do run --lang expr "--allow-write=$file_root" "--allow-read=$file_root" \
		-c "(write! (resource \"$escaped_file_uri\") \"escaped-file\")" \
		-c "(read (resource \"$escaped_file_uri\"))" >"$TMPDIR/$host.escaped-file.out" 2>"$TMPDIR/$host.escaped-file.err"
	if [ "$(cat "$TMPDIR/$host.escaped-file.out")" = 'escaped-file' ] && [ "$(cat "$TMPDIR/$host-entry%2F#?")" = 'escaped-file' ]; then
		test-ok "$host preserves percent, hash, and question characters in file URI paths"
	else
		test-fail "$host escaped file URI round trip: $(cat "$TMPDIR/$host.escaped-file.err")"
	fi
	selected="$TMPDIR/$host-work/selected"
	mkdir -p "$selected"
	nested_uri="file://$selected/new%252F%23%3F/deep/output.txt"
	"${runner[@]}" do run -C "$selected" --lang expr "--allow-write=$selected" "--allow-read=$selected" \
		-c "(write! (resource \"$nested_uri\") \"nested-file\")" \
		-c "(read (resource \"$nested_uri\"))" >"$TMPDIR/$host.nested-file.out" 2>"$TMPDIR/$host.nested-file.err"
	if [ "$(cat "$TMPDIR/$host.nested-file.out")" = nested-file ] && [ "$(cat "$selected/new%2F#?/deep/output.txt")" = nested-file ]; then
		test-ok "$host creates decoded file URI parents under the selected grant root"
	else
		test-fail "$host nested file URI publication: $(cat "$TMPDIR/$host.nested-file.err")"
	fi
	status=0
	"${runner[@]}" do run -C "$selected" --lang expr "--allow-write=$selected" \
		-c "(write! (resource \"file://$TMPDIR/$host-work/forbidden/output.txt\") \"denied\")" \
		>"$TMPDIR/$host.denied-file.out" 2>"$TMPDIR/$host.denied-file.err" || status=$?
	if [ "$status" = 1 ] && grep -q CAP_DENIED "$TMPDIR/$host.denied-file.err" && [ ! -e "$TMPDIR/$host-work/forbidden" ]; then
		test-ok "$host denied file URI creates no parent directories"
	else
		test-fail "$host denied file URI publication"
	fi
	for scheme in s3 https; do
		status=0
		"${runner[@]}" do run --lang expr --allow-write \
			-c "(write! (resource \"$scheme://workspace/output.txt\") \"unsupported\")" \
			>"$TMPDIR/$host.unsupported.out" 2>"$TMPDIR/$host.unsupported.err" || status=$?
		if [ "$status" = 1 ] && grep -q RES_INVALID "$TMPDIR/$host.unsupported.err" && [ ! -e "$TMPDIR/$host-work/$scheme:" ]; then
			test-ok "$host rejects $scheme resources without filesystem fallback"
		else
			test-fail "$host unsupported resource scheme fell through to a filesystem path"
		fi
	done
	file_glob="file://$TMPDIR/$host-glob/**/*.txt"
	mkdir -p "$TMPDIR/$host-glob/a%#?"
	printf 'escaped-glob' >"$TMPDIR/$host-glob/a%#?/entry%#?.txt"
	"${runner[@]}" do run --lang expr "--allow-read=$file_root" \
		-c "(map ([name] (read (resource name))) (wildcard (resource \"$file_glob\")))" \
		>"$TMPDIR/$host.escaped-glob.out" 2>"$TMPDIR/$host.escaped-glob.err"
	# read returns bytes, displayed without string quotes inside the list.
	if [ "$(cat "$TMPDIR/$host.escaped-glob.out")" = '[escaped-glob]' ]; then
		test-ok "$host recursively globs and reads escaped file URI names"
	else
		test-fail "$host escaped recursive glob: $(cat "$TMPDIR/$host.escaped-glob.err")"
	fi

	status=0
	"${runner[@]}" do run --lang expr --allow-write=mem://workspace/ \
		-c '(write! (resource "mem://other/out.txt") "denied")' \
		>"$TMPDIR/$host.denied.out" 2>"$TMPDIR/$host.denied.err" || status=$?
	if [ "$status" -ne 0 ] && grep -Fq 'CAP_DENIED' "$TMPDIR/$host.denied.err"; then
		test-ok "$host keeps memory namespace grants isolated"
	else
		test-fail "$host memory namespace grant: $(cat "$TMPDIR/$host.denied.err")"
	fi
	if [ ! -e "$TMPDIR/$host-work/file:" ] && [ ! -e "$TMPDIR/$host-work/mem:" ]; then
		test-ok "$host URI operations create no local scheme-named directories"
	else
		test-fail "$host URI operations leaked a local file: or mem: directory"
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
            f'./output : @(list (resource "{uri}"))\n'
            f'\t@(yield! (read (resource "{uri}")))\n')
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
