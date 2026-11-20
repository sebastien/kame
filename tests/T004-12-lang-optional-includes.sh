#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — optional configuration includes
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T004-12 optional includes"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
if [ -z "${KAME_TEST_WASM_WRAPPER:-}" ]; then make dist-wasm >/dev/null; fi
wasm_wrapper=${KAME_TEST_WASM_WRAPPER:-"$CLI_ROOT/dist/kame.js"}
project="$TMPDIR/project"
mkdir -p "$project"
cat >"$project/Makefile.km" <<'KM'
include? ./missing.km
include? ./present.km
task default
	(out! GREETING)
KM
printf 'GREETING = "hello"\n' >"$project/present.km"
printf 'include? ./absent.km\ndefault = "okay"\n' >"$project/value.km"
for backend in native wasm; do
 test-step "$backend skips absent files and loads present optional includes"
 if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$wasm_wrapper"); fi
 "${runner[@]}" -C "$project" default >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = hello ]; then test-ok "$backend optional includes expand in order"; else test-fail "$backend optional includes"; fi
  "${runner[@]}" do parse --json --lang km "$project/Makefile.km" >"$project/ast" 2>"$project/error"
 if grep -q '"optional":true' "$project/ast"; then test-ok "$backend AST preserves optional flag"; else test-fail "$backend optional AST"; fi
 "${runner[@]}" do fmt --lang km "$project/Makefile.km" >"$project/fmt" 2>"$project/error"
 if cmp -s "$project/Makefile.km" "$project/fmt"; then test-ok "$backend optional include formatting"; else test-fail "$backend optional format"; fi
  "${runner[@]}" --print-result -C "$project" ./value.km >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = '"okay"' ]; then test-ok "$backend value source optional include"; else test-fail "$backend optional value fragment"; fi
 printf 'task later\n\t(out! "loaded")\n' >"$project/missing.km"
 "${runner[@]}" -C "$project" later >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = loaded ]; then test-ok "$backend newly present optional include registers declarations"; else test-fail "$backend newly present include"; fi
 printf '(\n' >"$project/missing.km"
 status=0
 "${runner[@]}" -C "$project" default >"$project/value" 2>"$project/error" || status=$?
 if [ "$status" = 1 ] && [ ! -s "$project/value" ] && grep -q PARSE_ERR "$project/error"; then test-ok "$backend malformed optional source fails before effects"; else test-fail "$backend malformed optional source"; fi
  printf 'include? ./Makefile.km\n' >"$project/missing.km"
 status=0
 "${runner[@]}" -C "$project" default >"$project/value" 2>"$project/error" || status=$?
 if [ "$status" = 1 ] && grep -q DEP_CYCLE "$project/error"; then test-ok "$backend optional include cycles fail"; else test-fail "$backend optional cycle"; fi
 rm "$project/missing.km"
  printf 'include? ./present-directory\ntask default\n\ttrue\n' >"$project/invalid.km"
 mkdir -p "$project/present-directory"
 status=0
 "${runner[@]}" -C "$project" -f invalid.km default >"$project/value" 2>"$project/error" || status=$?
 if [ "$status" = 1 ] && grep -q FS_ERR "$project/error"; then test-ok "$backend optional include preserves read errors"; else test-fail "$backend optional read error"; fi
done
test-step "watch observes creation of a missing optional source"
if python3 - "$CLI_BIN" "$project" "$wasm_wrapper" <<'PYWATCH'
from pathlib import Path
import signal
import subprocess
import sys
import time

binary, folder, wrapper = sys.argv[1:]
for backend, command in [('native', [binary]), ('WASM', ['node', wrapper])]:
    root = Path(folder) / ('watch-' + backend)
    root.mkdir()
    (root / 'Makefile.km').write_text('include? ./config.km\ntask default\n\tprintf "run\\n" >> ./runs\n')
    with (root / 'stdout').open('wb') as out, (root / 'stderr').open('wb') as err:
        process = subprocess.Popen([*command, '-C', str(root), '--watch', 'default'], stdout=out, stderr=err)
        def wait_for_runs(wanted):
            deadline = time.monotonic() + 8
            while time.monotonic() < deadline and process.poll() is None:
                runs = root / 'runs'
                if runs.exists() and len(runs.read_text().splitlines()) >= wanted:
                    return
                time.sleep(.05)
            raise AssertionError((backend, wanted, (root / 'stderr').read_text()))
        try:
            wait_for_runs(1)
            (root / 'config.km').write_text('EXTRA = "new"\n')
            wait_for_runs(2)
        finally:
            if process.poll() is None:
                process.send_signal(signal.SIGTERM)
                try: process.wait(timeout=4)
                except subprocess.TimeoutExpired: process.kill(); process.wait()
PYWATCH
then test-ok "native and WASM watch register a newly created optional include"; else test-fail "optional include watch tracking"; fi
test-end
