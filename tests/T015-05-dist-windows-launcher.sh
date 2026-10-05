#!/usr/bin/env bash
# Spec: docs/spec/015-distribution.md — native Windows launcher bundle
# Spec: docs/spec/013-tests.md — T015-05-dist-windows-launcher
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/lib-bootstrap.sh"

test-start "T015-05 Windows launcher bundle"
cd "$CLI_ROOT"

test-step "Node launch preserves argv and child status"
(cd src/go/kame && go test ./cmd/kame-launcher)
test-ok "native launcher preserves special arguments and exit status"

test-step "the launcher resolves the colocated CLI entrypoint"
launcher="$TMPDIR/native-kame-launcher"
(cd src/go/kame && go build -trimpath -o "$launcher" ./cmd/kame-launcher)
cat >"$TMPDIR/kame.js" <<'JS'
process.stdout.write(JSON.stringify(process.argv.slice(2)));
process.exit(23);
JS
set +e
"$launcher" "two words" '&|;$(touch nope)' '' '雪' >"$TMPDIR/launcher-out"
launcher_status=$?
set -e
if [ "$launcher_status" = 23 ] && python3 - "$TMPDIR/launcher-out" <<'PY'
import json
import pathlib
import sys
assert json.loads(pathlib.Path(sys.argv[1]).read_text()) == ["two words", "&|;$(touch nope)", "", "雪"]
PY
then
	test-ok "launcher uses its adjacent script and preserves its child status and argv"
else
	test-fail "launcher handoff status=$launcher_status output=$(cat "$TMPDIR/launcher-out")"
fi

test-step "cross compile and package the Windows executable with runtime assets"
make dist-windows >/dev/null
if python3 - dist/kame-windows-x64.zip <<'PY'
import pathlib
import sys
import zipfile

archive = pathlib.Path(sys.argv[1])
with zipfile.ZipFile(archive) as bundle:
    assert set(bundle.namelist()) == {"kame.exe", "kame.js", "kame.wasm"}
    assert bundle.read("kame.exe").startswith(b"MZ")
    assert len(bundle.read("kame.js")) > 0
    assert len(bundle.read("kame.wasm")) > 8
PY
then
	test-ok "Windows x64 bundle contains a PE launcher and adjacent Node/WASM assets"
else
	test-fail "Windows launcher bundle is incomplete"
fi

test-end
