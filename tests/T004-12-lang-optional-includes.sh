#!/usr/bin/env bash
# Spec: docs/spec/004-language.md — optional configuration includes
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"
test-start "T004-12 optional includes"
test-step "build artifacts"
cli_require_tools
cli_build
cd "$CLI_ROOT"
make dist-wasm >/dev/null
project="$TMPDIR/project"
mkdir -p "$project"
cat >"$project/Makefile.kmk" <<'KMK'
include? ./missing.kmk
include? ./present.kmk
default :
	@(out GREETING)
KMK
printf 'GREETING = "hello"\n' >"$project/present.kmk"
printf 'include? ./absent.km\n"okay"\n' >"$project/value.km"
for backend in native wasm; do
 test-step "$backend skips absent files and loads present optional includes"
 if [ "$backend" = native ]; then runner=("$CLI_BIN"); else runner=(node "$CLI_ROOT/dist/kame.js"); fi
 "${runner[@]}" -C "$project" default >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = hello ]; then test-ok "$backend optional includes expand in order"; else test-fail "$backend optional includes"; fi
 "${runner[@]}" do parse --lang script "$project/Makefile.kmk" >"$project/ast" 2>"$project/error"
 if grep -q '"optional":true' "$project/ast"; then test-ok "$backend AST preserves optional flag"; else test-fail "$backend optional AST"; fi
 "${runner[@]}" do fmt --lang script "$project/Makefile.kmk" >"$project/fmt" 2>"$project/error"
 if cmp -s "$project/Makefile.kmk" "$project/fmt"; then test-ok "$backend optional include formatting"; else test-fail "$backend optional format"; fi
 "${runner[@]}" -C "$project" ./value.km >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = '"okay"' ]; then test-ok "$backend value source optional include"; else test-fail "$backend optional value fragment"; fi
 printf 'later :\n\t@(out "loaded")\n' >"$project/missing.kmk"
 "${runner[@]}" -C "$project" later >"$project/value" 2>"$project/error"
 if [ "$(cat "$project/value")" = loaded ]; then test-ok "$backend newly present optional include registers declarations"; else test-fail "$backend newly present include"; fi
 printf '(\n' >"$project/missing.kmk"
 status=0
 "${runner[@]}" -C "$project" default >"$project/value" 2>"$project/error" || status=$?
 if [ "$status" = 1 ] && [ ! -s "$project/value" ] && grep -q PARSE_ERR "$project/error"; then test-ok "$backend malformed optional source fails before effects"; else test-fail "$backend malformed optional source"; fi
 printf 'include? ./Makefile.kmk\n' >"$project/missing.kmk"
 status=0
 "${runner[@]}" -C "$project" default >"$project/value" 2>"$project/error" || status=$?
 if [ "$status" = 1 ] && grep -q DEP_CYCLE "$project/error"; then test-ok "$backend optional include cycles fail"; else test-fail "$backend optional cycle"; fi
 rm "$project/missing.kmk"
 printf 'include? ./present-directory\ndefault :\n\ttrue\n' >"$project/invalid.kmk"
 mkdir -p "$project/present-directory"
 status=0
 "${runner[@]}" -C "$project" -f invalid.kmk default >"$project/value" 2>"$project/error" || status=$?
 if [ "$status" = 1 ] && grep -q FS_ERR "$project/error"; then test-ok "$backend optional include preserves read errors"; else test-fail "$backend optional read error"; fi
done
test-step "native watch observes creation of a missing optional source"
if python3 - "$CLI_BIN" "$project" <<'PYWATCH'
from pathlib import Path
import subprocess, sys, time, signal
binary, folder = sys.argv[1:]
root = Path(folder) / 'watch'
root.mkdir()
(root / 'Makefile.kmk').write_text('include? ./config.kmk\ndefault :\n\tprintf "run\\n" >> ./runs\n')
with (root / 'stdout').open('wb') as out, (root / 'stderr').open('wb') as err:
    process = subprocess.Popen([binary, '-C', str(root), '--watch', 'default'], stdout=out, stderr=err)
    def count():
        return (root / 'runs').read_text().count('run\n') if (root / 'runs').exists() else 0
    try:
        deadline = time.monotonic() + 8
        while count() < 1 and time.monotonic() < deadline: time.sleep(.05)
        assert count() == 1, (count(), (root / 'stderr').read_text())
        (root / 'config.kmk').write_text('EXTRA = "new"\n')
        deadline = time.monotonic() + 8
        while count() < 2 and time.monotonic() < deadline: time.sleep(.05)
        assert count() >= 2, (count(), (root / 'stderr').read_text())
    finally:
        process.send_signal(signal.SIGTERM)
        try: process.wait(timeout=4)
        except subprocess.TimeoutExpired: process.kill(); process.wait()
PYWATCH
then test-ok "native watch registers a newly created optional include"; else test-fail "optional include watch tracking"; fi
test-end
