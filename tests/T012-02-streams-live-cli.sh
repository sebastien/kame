#!/usr/bin/env bash
# Spec: docs/spec/012-streams.md — CLI chunks are visible before completion
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T012-02 live CLI streams"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/project"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'KMK'
default :
	printf ready-marker; printf error-marker >&2; while [ ! -f ./release ]; do sleep .01; done
KMK
for backend in native wasm; do
 for mode in human json; do
  test-step "$backend $mode publishes chunks while the recipe waits"
  if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
  if [ "$mode" = json ]; then runner+=(--json); fi
  if python3 - "$project" "${runner[@]}" <<'PYLIVE'
from pathlib import Path
import selectors, signal, subprocess, sys, time
folder = Path(sys.argv[1])
release = folder / 'release'
if release.exists(): release.unlink()
process = subprocess.Popen([*sys.argv[2:], '-C', str(folder), 'default'], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
selector = selectors.DefaultSelector()
selector.register(process.stdout, selectors.EVENT_READ, 'stdout')
selector.register(process.stderr, selectors.EVENT_READ, 'stderr')
received = {'stdout': b'', 'stderr': b''}
try:
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        for key, _ in selector.select(.1):
            chunk = key.fileobj.read1(65536)
            if chunk: received[key.data] += chunk
            else: selector.unregister(key.fileobj)
        merged = received['stdout'] + received['stderr']
        if b'ready-marker' in merged and b'error-marker' in merged: break
        assert process.poll() is None, (process.returncode, received)
    assert b'ready-marker' in merged and b'error-marker' in merged, received
    assert process.poll() is None and not release.exists(), 'output appeared only after completion'
    release.write_text('go')
    remaining_out, remaining_err = process.communicate(timeout=8)
    received['stdout'] += remaining_out
    received['stderr'] += remaining_err
    assert process.returncode == 0, received
    if '--json' in sys.argv:
        import json
        assert not received['stderr'], received
        for line in received['stdout'].splitlines(): json.loads(line)
    else:
        assert b'ready-marker' in received['stdout'] and b'error-marker' in received['stderr'], received
finally:
    selector.close()
    if process.poll() is None:
        release.write_text('cleanup')
        process.send_signal(signal.SIGTERM)
        try: process.communicate(timeout=4)
        except subprocess.TimeoutExpired: process.kill(); process.communicate()
PYLIVE
  then test-ok "$backend $mode output precedes process completion"; else test-fail "$backend $mode live output"; fi
 done
done
test-step "native watch flushes declarative output while remaining alive"
if python3 - "$project" "$CLI_BIN" <<'PYWATCH'
from pathlib import Path
import selectors, signal, subprocess, sys
folder = Path(sys.argv[1]) / 'watch'
folder.mkdir()
(folder / 'Makefile.kmk').write_text('default :\n\t@(out "watch-marker")\n')
process = subprocess.Popen([sys.argv[2], '-C', str(folder), '--watch', 'default'], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
selector = selectors.DefaultSelector()
selector.register(process.stdout, selectors.EVENT_READ)
try:
    assert selector.select(8), 'watch output was buffered'
    assert process.stdout.read1(65536) == b'watch-marker'
    assert process.poll() is None
finally:
    selector.close()
    process.send_signal(signal.SIGTERM)
    try: process.communicate(timeout=4)
    except subprocess.TimeoutExpired: process.kill(); process.communicate()
PYWATCH
then test-ok "watch output is visible before termination"; else test-fail "watch buffered output"; fi
test-end
